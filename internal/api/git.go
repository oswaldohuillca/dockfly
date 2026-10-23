package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/dockfly/dockfly/internal/builder"
	"github.com/dockfly/dockfly/internal/gitprovider"
	"github.com/dockfly/dockfly/internal/store"
)

var (
	errGitInvalidProvider    = newError("git_invalid_provider", "Invalid provider: use github or gitlab")
	errGitTokenRequired      = newError("git_token_required", "The token is required")
	errGitTokenCheckFailed   = newError("git_token_check_failed", "Couldn't verify the token: {detail}")
	errGitConnectionInUse    = newError("git_connection_in_use", "These sites use it; disconnect them first: {sites}")
	errGitConnectionNotFound = newError("git_connection_not_found", "Account not found")
	errPreviewDomainInvalid  = newError("preview_domain_invalid", "The previews domain must look like previews.mydomain.com (without *.)")
	errPublicURLInvalid      = newError("public_url_invalid", "The public URL must look like https://dockfly.mydomain.com")
	errGitRootDirDotDot      = newError("git_root_dir_dotdot", "The root directory can't contain \"..\"")
	errGitOutputDirDotDot    = newError("git_output_dir_dotdot", "The output directory can't contain \"..\"")
	errGitConnectionRequired = newError("git_connection_required", "Choose a GitHub or GitLab account")
	errGitRepoRequired       = newError("git_repo_required", "Choose a repository")
	errGitBranchRequired     = newError("git_branch_required", "Enter the branch to deploy")
	errGitInvalidBuildImage  = newError("git_invalid_build_image", "Invalid image: {image}")
	errGitInvalidEnvName     = newError("git_invalid_env_name", "Invalid variable name: {name}")
	errGitConnectionMissing  = newError("git_connection_missing", "The selected account doesn't exist")
	errGitBranchUnreachable  = newError("git_branch_unreachable", "Couldn't access {repo} ({branch}): {detail}")
	errSiteNotLinked         = newError("site_not_linked", "The site isn't linked to any repository")
	errBuildInvalidID        = newError("build_invalid_id", "Invalid build ID")
	errBuildNotFound         = newError("build_not_found", "Build not found")
	errWebhookBodyTooLarge   = newError("webhook_body_too_large", "Body too large")
	errSiteWebhooksInactive  = newError("site_webhooks_inactive", "This site has no active webhooks")
	errWebhookBadSignature   = newError("webhook_bad_signature", "Invalid signature")
	errWebhookBadPayload     = newError("webhook_bad_payload", "Invalid payload")
	errBuildCacheClearFailed = newError("build_cache_clear_failed", "Couldn't clear the cache (is a build running?): {detail}")
)

// --- Cuentas de GitHub / GitLab ---

func (s *Server) handleListConnections(w http.ResponseWriter, r *http.Request) {
	conns, err := s.Store.ListGitConnections(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, conns)
}

func (s *Server) handleCreateConnection(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Provider string `json:"provider"`
		BaseURL  string `json:"base_url"`
		Token    string `json:"token"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Provider != gitprovider.GitHub && in.Provider != gitprovider.GitLab {
		writeError(w, http.StatusBadRequest, errGitInvalidProvider)
		return
	}
	in.Token = strings.TrimSpace(in.Token)
	if in.Token == "" {
		writeError(w, http.StatusBadRequest, errGitTokenRequired)
		return
	}
	base, err := gitprovider.NormalizeBaseURL(in.Provider, in.BaseURL)
	if err != nil {
		writeErrorFrom(w, http.StatusBadRequest, err, errUnexpected)
		return
	}
	// Validamos el token antes de guardarlo.
	p, _ := gitprovider.New(in.Provider, base, in.Token)
	user, err := p.CurrentUser(r.Context())
	if err != nil {
		writeError(w, http.StatusBadRequest, errGitTokenCheckFailed.With("detail", err.Error()))
		return
	}
	conn := &store.GitConnection{Provider: in.Provider, BaseURL: base, Username: user, TokenEnc: s.Secrets.Seal([]byte(in.Token))}
	if err := s.Store.CreateGitConnection(r.Context(), conn); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, conn)
}

func (s *Server) handleDeleteConnection(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	// No se puede borrar una cuenta en uso: sus sitios quedarían con webhooks
	// huérfanos y sin poder construir.
	sites, err := s.Store.ListSites(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	var using []string
	for _, site := range sites {
		if site.GitConnectionID != nil && *site.GitConnectionID == id {
			using = append(using, site.Name)
		}
	}
	if len(using) > 0 {
		writeError(w, http.StatusConflict, errGitConnectionInUse.With("sites", strings.Join(using, ", ")))
		return
	}
	s.Git.Forget(id)
	err = s.Store.DeleteGitConnection(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, errGitConnectionNotFound)
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// connectionProvider carga una cuenta y crea su cliente.
func (s *Server) connectionProvider(ctx context.Context, id int64) (*store.GitConnection, gitprovider.Provider, error) {
	conn, err := s.Store.GetGitConnection(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	p, err := s.Git.Provider(ctx, conn)
	return conn, p, err
}

func (s *Server) loadConnection(w http.ResponseWriter, r *http.Request) (gitprovider.Provider, bool) {
	id, ok := pathID(w, r)
	if !ok {
		return nil, false
	}
	_, p, err := s.connectionProvider(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, errGitConnectionNotFound)
		return nil, false
	} else if err != nil {
		writeErrorFrom(w, http.StatusBadGateway, err, errUnexpected)
		return nil, false
	}
	return p, true
}

func (s *Server) handleListRepos(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadConnection(w, r)
	if !ok {
		return
	}
	repos, err := p.ListRepos(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		writeErrorFrom(w, http.StatusBadGateway, err, errUnexpected)
		return
	}
	if repos == nil {
		repos = []gitprovider.Repo{}
	}
	writeJSON(w, http.StatusOK, repos)
}

func (s *Server) handleListBranches(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadConnection(w, r)
	if !ok {
		return
	}
	branches, err := p.ListBranches(r.Context(), r.URL.Query().Get("repo"))
	if err != nil {
		writeErrorFrom(w, http.StatusBadGateway, err, errUnexpected)
		return
	}
	writeJSON(w, http.StatusOK, branches)
}

// --- Ajustes ---

type settingsView struct {
	PublicURL         string `json:"public_url"`
	PreviewDomain     string `json:"preview_domain"`
	PreviewAccessList *int64 `json:"preview_access_list_id"`
	// PreviewSSL indica si hay un certificado comodín para las previews.
	PreviewSSL bool `json:"preview_ssl"`
}

func (s *Server) settings(ctx context.Context) (settingsView, error) {
	var v settingsView
	var err error
	if v.PublicURL, err = s.Store.GetSetting(ctx, store.SettingPublicURL); err != nil {
		return v, err
	}
	if v.PreviewDomain, err = s.previewDomain(ctx); err != nil {
		return v, err
	}
	if raw, _ := s.Store.GetSetting(ctx, store.SettingPreviewAccessList); raw != "" {
		if id, err := strconv.ParseInt(raw, 10, 64); err == nil {
			v.PreviewAccessList = &id
		}
	}
	if v.PreviewDomain != "" {
		plan, err := s.loadCertPlan(ctx)
		if err != nil {
			return v, err
		}
		v.PreviewSSL = plan.resolver([]string{"*." + v.PreviewDomain}) != ""
	}
	return v, nil
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	v, err := s.settings(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// handlePutSettings guarda solo los ajustes que vienen en la petición.
func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PublicURL     *string `json:"public_url"`
		PreviewDomain *string `json:"preview_domain"`
		// 0 = sin lista de acceso.
		PreviewAccessList *int64 `json:"preview_access_list_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	ctx := r.Context()
	var warnings []string
	if in.PreviewDomain != nil || in.PreviewAccessList != nil {
		if !s.putPreviewSettings(w, r, in.PreviewDomain, in.PreviewAccessList) {
			return
		}
		if err := s.Apply(ctx); err != nil {
			s.internalError(w, fmt.Errorf("aplicando configuración: %w", err))
			return
		}
	}
	if in.PublicURL != nil {
		publicURL, ok := s.putPublicURL(w, r, *in.PublicURL)
		if !ok {
			return
		}
		warnings = s.syncGitHubWebhooks(r, publicURL)
	}
	v, err := s.settings(ctx)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": v, "warnings": warnings})
}

func (s *Server) putPreviewSettings(w http.ResponseWriter, r *http.Request, domain *string, aclID *int64) bool {
	ctx := r.Context()
	if domain != nil {
		d := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(*domain)), ".")
		if d != "" {
			list, err := normalizeDomains([]string{d})
			if err != nil || len(list) != 1 || strings.HasPrefix(list[0], "*.") || !strings.Contains(list[0], ".") {
				writeError(w, http.StatusBadRequest, errPreviewDomainInvalid)
				return false
			}
			d = list[0]
		}
		if err := s.Store.SetSetting(ctx, store.SettingPreviewDomain, d); err != nil {
			s.internalError(w, err)
			return false
		}
	}
	if aclID != nil {
		raw := ""
		if *aclID != 0 {
			if !s.checkAccessList(w, r, aclID) {
				return false
			}
			raw = strconv.FormatInt(*aclID, 10)
		}
		if err := s.Store.SetSetting(ctx, store.SettingPreviewAccessList, raw); err != nil {
			s.internalError(w, err)
			return false
		}
	}
	return true
}

func (s *Server) putPublicURL(w http.ResponseWriter, r *http.Request, raw string) (string, bool) {
	publicURL := strings.TrimRight(strings.TrimSpace(raw), "/")
	if publicURL != "" {
		u, err := url.Parse(publicURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || (u.Path != "" && u.Path != "/") {
			writeError(w, http.StatusBadRequest, errPublicURLInvalid)
			return "", false
		}
	}
	if err := s.Store.SetSetting(r.Context(), store.SettingPublicURL, publicURL); err != nil {
		s.internalError(w, err)
		return "", false
	}
	return publicURL, true
}

// --- Repositorio de un sitio ---

// appWebhook marca los sitios que reciben los pushes por el webhook de la
// GitHub App (en vez de uno propio en el repositorio).
const appWebhook = "github-app"

var (
	imageRe   = regexp.MustCompile(`^[a-z0-9]+([._/:@-][a-zA-Z0-9]+)*$`)
	envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

type siteGitInput struct {
	ConnectionID   int64             `json:"connection_id"`
	Repo           string            `json:"repo"`
	Branch         string            `json:"branch"`
	RootDir        string            `json:"root_dir"`
	InstallCommand string            `json:"install_command"`
	BuildCommand   string            `json:"build_command"`
	OutputDir      string            `json:"output_dir"`
	BuildImage     string            `json:"build_image"`
	Env            map[string]string `json:"env"`
}

// relDir normaliza un directorio relativo al repositorio. "" y "." son la raíz.
func relDir(field, dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	clean := strings.TrimPrefix(path.Clean("/"+dir), "/")
	if strings.Contains(dir, "..") {
		return "", fmt.Errorf("%s no puede contener «..»", field)
	}
	return clean, nil
}

func (in *siteGitInput) validate() error {
	in.Repo = strings.Trim(strings.TrimSpace(in.Repo), "/")
	in.Branch = strings.TrimSpace(in.Branch)
	in.BuildImage = strings.TrimSpace(in.BuildImage)
	switch {
	case in.ConnectionID == 0:
		return errGitConnectionRequired
	case !strings.Contains(in.Repo, "/"):
		return errGitRepoRequired
	case in.Branch == "":
		return errGitBranchRequired
	case in.BuildImage != "" && !imageRe.MatchString(in.BuildImage):
		return errGitInvalidBuildImage.With("image", in.BuildImage)
	}
	var err error
	// relDir solo falla si el directorio contiene "..".
	if in.RootDir, err = relDir("el directorio raíz", in.RootDir); err != nil {
		return errGitRootDirDotDot
	}
	if in.OutputDir, err = relDir("el directorio de salida", in.OutputDir); err != nil {
		return errGitOutputDirDotDot
	}
	for k := range in.Env {
		if !envNameRe.MatchString(k) {
			return errGitInvalidEnvName.With("name", k)
		}
	}
	return nil
}

// handlePutSiteGit enlaza un sitio con un repositorio (o actualiza su
// configuración de build) y registra el webhook de push si es posible.
func (s *Server) handlePutSiteGit(w http.ResponseWriter, r *http.Request) {
	site, ok := s.loadSite(w, r)
	if !ok {
		return
	}
	var in siteGitInput
	if !decode(w, r, &in) {
		return
	}
	if err := in.validate(); err != nil {
		writeErrorFrom(w, http.StatusBadRequest, err, errUnexpected)
		return
	}
	ctx := r.Context()
	if old := site.SiteGit; (old.Repo != in.Repo || old.GitConnectionID == nil || *old.GitConnectionID != in.ConnectionID) &&
		!canLinkRepo(r) {
		writeError(w, http.StatusForbidden, errRepoLinkAdmin)
		return
	}
	conn, prov, err := s.connectionProvider(ctx, in.ConnectionID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusBadRequest, errGitConnectionMissing)
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	// Comprobamos que la rama existe con el token de la cuenta.
	if _, err := prov.LatestCommit(ctx, in.Repo, in.Branch); err != nil {
		writeError(w, http.StatusBadRequest, errGitBranchUnreachable.With("repo", in.Repo, "branch", in.Branch, "detail", err.Error()))
		return
	}

	git := site.SiteGit
	sameRepo := git.Repo == in.Repo && git.GitConnectionID != nil && *git.GitConnectionID == conn.ID
	if !sameRepo {
		// Otro repositorio: el webhook anterior ya no sirve.
		s.deleteWebhook(ctx, site)
		git.WebhookID, git.WebhookSecret = "", ""
	}
	git.GitConnectionID = &conn.ID
	git.Repo, git.Branch = in.Repo, in.Branch
	git.RootDir, git.OutputDir = in.RootDir, in.OutputDir
	git.InstallCommand, git.BuildCommand = strings.TrimSpace(in.InstallCommand), strings.TrimSpace(in.BuildCommand)
	git.BuildImage = in.BuildImage
	if in.Env != nil {
		raw, _ := json.Marshal(in.Env)
		git.BuildEnvEnc = s.Secrets.Seal(raw)
	}

	var webhookErr string
	if conn.AuthType == store.AuthGitHubApp {
		// La GitHub App tiene un único webhook para todos sus repositorios.
		git.WebhookID, git.WebhookSecret = "", ""
		app, err := s.Store.GetGitHubApp(ctx, *conn.GitHubAppID)
		if err == nil && app.WebhookActive {
			git.WebhookID = appWebhook
		} else {
			webhookErr = "configura la URL pública de Dockfly en Integraciones para desplegar en cada push"
		}
	} else if git.WebhookID == "" || git.WebhookID == appWebhook {
		publicURL, _ := s.Store.GetSetting(ctx, store.SettingPublicURL)
		if publicURL == "" {
			webhookErr = "configura la URL pública de Dockfly en Integraciones para desplegar en cada push"
		} else {
			secret := randomHex(32)
			hookURL := fmt.Sprintf("%s/api/hooks/%s/%d", publicURL, conn.Provider, site.ID)
			id, err := prov.CreateWebhook(ctx, in.Repo, hookURL, secret)
			if err != nil {
				webhookErr = "no se pudo crear el webhook: " + err.Error()
			} else {
				git.WebhookID, git.WebhookSecret = id, secret
			}
		}
	}

	if err := s.Store.UpdateSiteGit(ctx, site.ID, &git); err != nil {
		s.internalError(w, err)
		return
	}
	site.SiteGit = git
	writeJSON(w, http.StatusOK, map[string]any{"site": site, "webhook_error": webhookErr})
}

func (s *Server) handleDeleteSiteGit(w http.ResponseWriter, r *http.Request) {
	site, ok := s.loadSite(w, r)
	if !ok {
		return
	}
	s.deleteWebhook(r.Context(), site)
	if err := s.Store.UpdateSiteGit(r.Context(), site.ID, &store.SiteGit{}); err != nil {
		s.internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGetSiteEnv(w http.ResponseWriter, r *http.Request) {
	site, ok := s.loadSite(w, r)
	if !ok {
		return
	}
	env, err := builder.Env(s.Secrets, site)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, env)
}

// deleteWebhook intenta borrar el webhook del repositorio. Si falla (token
// revocado, repo borrado…) solo se registra: no debe impedir desenlazar.
func (s *Server) deleteWebhook(ctx context.Context, site *store.Site) {
	if site.WebhookID == "" || site.WebhookID == appWebhook || site.GitConnectionID == nil {
		return
	}
	_, p, err := s.connectionProvider(ctx, *site.GitConnectionID)
	if err == nil {
		err = p.DeleteWebhook(ctx, site.Repo, site.WebhookID)
	}
	if err != nil {
		s.Log.Warn("no se pudo borrar el webhook", "site", site.ID, "repo", site.Repo, "error", err)
	}
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// --- Builds ---

func (s *Server) handleListBuilds(w http.ResponseWriter, r *http.Request) {
	site, ok := s.loadSite(w, r)
	if !ok {
		return
	}
	builds, err := s.Store.ListBuilds(r.Context(), site.ID, 20)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, builds)
}

// handleTriggerBuild construye el último commit de la rama del sitio.
func (s *Server) handleTriggerBuild(w http.ResponseWriter, r *http.Request) {
	site, ok := s.loadSite(w, r)
	if !ok {
		return
	}
	if site.Repo == "" {
		writeError(w, http.StatusBadRequest, errSiteNotLinked)
		return
	}
	b, err := s.Builds.Enqueue(r.Context(), site.ID, store.TriggerManual, site.Branch, "", gitprovider.Commit{})
	if err != nil {
		writeErrorFrom(w, http.StatusServiceUnavailable, err, errUnexpected)
		return
	}
	writeJSON(w, http.StatusAccepted, b)
}

func (s *Server) loadBuild(w http.ResponseWriter, r *http.Request) (*store.Build, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "buildID"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, errBuildInvalidID)
		return nil, false
	}
	b, err := s.Store.GetBuild(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, errBuildNotFound)
		return nil, false
	} else if err != nil {
		s.internalError(w, err)
		return nil, false
	}
	return b, true
}

func (s *Server) handleGetBuild(w http.ResponseWriter, r *http.Request) {
	if b, ok := s.loadBuild(w, r); ok {
		writeJSON(w, http.StatusOK, b)
	}
}

func (s *Server) handleCancelBuild(w http.ResponseWriter, r *http.Request) {
	b, ok := s.loadBuild(w, r)
	if !ok {
		return
	}
	if err := s.Builds.Cancel(r.Context(), b.ID); err != nil {
		writeErrorFrom(w, http.StatusConflict, err, errUnexpected)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleBuildLogs transmite el log por Server-Sent Events: primero lo que ya
// hay y después lo que se va escribiendo, hasta que el build termina.
// Eventos: "log" (texto), "status" (el build cambió) y "end" (terminó).
func (s *Server) handleBuildLogs(w http.ResponseWriter, r *http.Request) {
	b, ok := s.loadBuild(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	rc := http.NewResponseController(w)

	send := func(event string, data any) bool {
		payload, _ := json.Marshal(data)
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload); err != nil {
			return false
		}
		return rc.Flush() == nil
	}

	// El cliente puede tener un estado viejo (p. ej. el de la lista): le
	// mandamos el actual nada más conectar.
	if !send("status", b) {
		return
	}

	var f *os.File
	defer func() {
		if f != nil {
			f.Close()
		}
	}()
	buf := make([]byte, 32<<10)
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()

	for {
		// El build en cola todavía no tiene log.
		if f == nil {
			f, _ = os.Open(s.Builds.LogPath(b.ID))
		}
		if f != nil {
			for {
				n, err := f.Read(buf)
				if n > 0 && !send("log", string(buf[:n])) {
					return
				}
				if err == io.EOF || n == 0 {
					break
				}
				if err != nil {
					return
				}
			}
		}
		// Comprobamos el estado después de leer: si ya terminó, todo lo que
		// escribió está en el archivo y lo acabamos de enviar.
		if b.Finished() {
			send("end", b)
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		}
		cur, err := s.Store.GetBuild(r.Context(), b.ID)
		if err != nil {
			return
		}
		// Avisamos de los cambios de estado (en cola → construyendo) y del
		// commit, que en un build manual se conoce al empezar.
		if cur.Status != b.Status || cur.CommitSHA != b.CommitSHA {
			if !send("status", cur) {
				return
			}
		}
		b = cur
	}
}

// --- Webhooks ---

// handleWebhook recibe los pushes de GitHub y GitLab. Es la única ruta de
// escritura sin sesión: la autentica la firma del propio webhook.
func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	provider := chi.URLParam(r, "provider")
	site, ok := s.loadSite(w, r)
	if !ok {
		return
	}
	body, err := readWebhookBody(w, r)
	if err != nil {
		return
	}
	if site.WebhookSecret == "" || site.GitConnectionID == nil {
		writeError(w, http.StatusNotFound, errSiteWebhooksInactive)
		return
	}
	conn, err := s.Store.GetGitConnection(r.Context(), *site.GitConnectionID)
	if err != nil || conn.Provider != provider {
		writeError(w, http.StatusNotFound, errSiteWebhooksInactive)
		return
	}

	var (
		push   gitprovider.Push
		isPush bool
	)
	switch provider {
	case gitprovider.GitHub:
		push, isPush, err = gitprovider.ParseGitHubWebhook(r.Header, body, site.WebhookSecret)
	case gitprovider.GitLab:
		push, isPush, err = gitprovider.ParseGitLabWebhook(r.Header, body, site.WebhookSecret)
	}
	if errors.Is(err, gitprovider.ErrBadSignature) {
		writeError(w, http.StatusUnauthorized, errWebhookBadSignature)
		return
	} else if err != nil {
		writeError(w, http.StatusBadRequest, errWebhookBadPayload)
		return
	}
	// GitHub y GitLab reintentan si no reciben 2xx: los eventos que no nos
	// interesan se confirman igualmente.
	if !isPush {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignorado"})
		return
	}
	id, status, err := s.handleSitePush(r.Context(), site, push)
	if err != nil {
		writeErrorFrom(w, http.StatusServiceUnavailable, err, errUnexpected)
		return
	}
	if id == 0 {
		writeJSON(w, http.StatusOK, map[string]string{"status": status})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"status": status, "build_id": id})
}

// handleClearBuildCache vacía la caché de dependencias del sitio (útil si
// una caché corrupta provoca errores extraños en el build).
func (s *Server) handleClearBuildCache(w http.ResponseWriter, r *http.Request) {
	site, ok := s.loadSite(w, r)
	if !ok {
		return
	}
	if err := s.Builds.ClearCache(r.Context(), site.ID); err != nil {
		writeError(w, http.StatusConflict, errBuildCacheClearFailed.With("detail", err.Error()))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

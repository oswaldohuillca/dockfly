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
	errGitConnectionInUse    = newError("git_connection_in_use", "These sites and apps use it; disconnect them first: {sites}")
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
	errDeployHookNotFound    = newError("deploy_hook_not_found", "This deploy hook doesn't exist or was regenerated")
	errSiteNotLinked         = newError("site_not_linked", "The site isn't linked to any repository")
	errBuildInvalidID        = newError("build_invalid_id", "Invalid build ID")
	errBuildNotFound         = newError("build_not_found", "Build not found")
	errWebhookBodyTooLarge   = newError("webhook_body_too_large", "Body too large")
	errWebhookBadSignature   = newError("webhook_bad_signature", "Invalid signature")
	errWebhookBadPayload     = newError("webhook_bad_payload", "Invalid payload")
	errBuildCacheClearFailed = newError("build_cache_clear_failed", "Couldn't clear the cache (is a build running?): {detail}")
)

// --- GitHub / GitLab accounts ---

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
	// Validate the token before storing it.
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
	if !s.refuseIfInUse(w, r, func(c *store.GitConnection) bool { return c.ID == id }) {
		return
	}
	s.Git.Forget(id)
	err := s.Store.DeleteGitConnection(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, errGitConnectionNotFound)
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// connectionProvider loads an account and builds its client.
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

// --- Settings ---

type settingsView struct {
	PublicURL         string `json:"public_url"`
	PreviewDomain     string `json:"preview_domain"`
	PreviewAccessList *int64 `json:"preview_access_list_id"`
	// PreviewSSL reports whether there is a wildcard certificate for previews.
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

// handlePutSettings saves only the settings present in the request.
func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PublicURL     *string `json:"public_url"`
		PreviewDomain *string `json:"preview_domain"`
		// 0 = no access list.
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
			s.internalError(w, fmt.Errorf("applying configuration: %w", err))
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

// --- A site's repository ---

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

// relDir normalizes a directory relative to the repository. "" and "." are the
// root.
func relDir(field, dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	clean := strings.TrimPrefix(path.Clean("/"+dir), "/")
	if strings.Contains(dir, "..") {
		return "", fmt.Errorf("%s can't contain \"..\"", field)
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
	// relDir only fails if the directory contains "..".
	if in.RootDir, err = relDir("the root directory", in.RootDir); err != nil {
		return errGitRootDirDotDot
	}
	if in.OutputDir, err = relDir("the output directory", in.OutputDir); err != nil {
		return errGitOutputDirDotDot
	}
	for k := range in.Env {
		if !envNameRe.MatchString(k) {
			return errGitInvalidEnvName.With("name", k)
		}
	}
	return nil
}

// handlePutSiteGit links a site to a repository (or updates its build
// configuration) and registers the push webhook when possible.
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
	// Check that the branch exists, using the account's token.
	if _, err := prov.LatestCommit(ctx, in.Repo, in.Branch); err != nil {
		writeError(w, http.StatusBadRequest, errGitBranchUnreachable.With("repo", in.Repo, "branch", in.Branch, "detail", err.Error()))
		return
	}

	git := site.SiteGit
	sameRepo := git.Repo == in.Repo && git.GitConnectionID != nil && *git.GitConnectionID == conn.ID
	if !sameRepo {
		// A different repository: the old webhook is no longer any use.
		s.removeWebhook(ctx, siteHook(site))
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

	hook, warn := s.ensureWebhook(ctx, hookTarget{
		connectionID: &conn.ID, repo: in.Repo,
		hook: hookState{git.WebhookID, git.WebhookSecret}, path: itoa(site.ID),
	})
	git.WebhookID, git.WebhookSecret = hook.ID, hook.Secret

	if err := s.Store.UpdateSiteGit(ctx, site.ID, &git); err != nil {
		s.internalError(w, err)
		return
	}
	site.SiteGit = git
	writeJSON(w, http.StatusOK, map[string]any{"site": site, "webhook_warning": toProblem(warn)})
}

func (s *Server) handleDeleteSiteGit(w http.ResponseWriter, r *http.Request) {
	site, ok := s.loadSite(w, r)
	if !ok {
		return
	}
	s.removeWebhook(r.Context(), siteHook(site))
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

// handleTriggerBuild builds the latest commit on the site's branch.
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

// handleBuildLogs streams the log over Server-Sent Events: first whatever is
// already there, then whatever gets written, until the build finishes.
// Events: "log" (text), "status" (the build changed) and "end" (finished).
func (s *Server) handleBuildLogs(w http.ResponseWriter, r *http.Request) {
	b, ok := s.loadBuild(w, r)
	if !ok {
		return
	}
	send := newSSE(w).send

	// The client may hold a stale state (the one from the list, say), so send
	// it the current one as soon as it connects.
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
		// A queued build has no log yet.
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
		// Check the status after reading: if it already finished, everything
		// it wrote is in the file and we just sent it.
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
		// Report status changes (queued → building) and the commit, which on a
		// manual build is only known once it starts.
		if cur.Status != b.Status || cur.CommitSHA != b.CommitSHA {
			if !send("status", cur) {
				return
			}
		}
		b = cur
	}
}

// --- Webhooks ---

// denyWebhook answers the same way to every webhook that can't be verified:
// unknown id, no webhooks, another provider or a wrong signature. That way a
// caller without the secret can't find out which ids exist.
func denyWebhook(w http.ResponseWriter) {
	writeError(w, http.StatusUnauthorized, errWebhookBadSignature)
}

// handleWebhook receives pushes from GitHub and GitLab. It is the only write
// route without a session: the webhook's own signature authenticates it.
func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	provider := chi.URLParam(r, "provider")
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	// The body is read before looking up the site: a 413 must not depend on
	// whether it exists either.
	body, err := readWebhookBody(w, r)
	if err != nil {
		return
	}
	site, err := s.Store.GetSite(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && (site.WebhookSecret == "" || site.GitConnectionID == nil)) {
		denyWebhook(w)
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	conn, err := s.Store.GetGitConnection(r.Context(), *site.GitConnectionID)
	if err != nil || conn.Provider != provider {
		denyWebhook(w)
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
		denyWebhook(w)
		return
	} else if err != nil {
		writeError(w, http.StatusBadRequest, errWebhookBadPayload)
		return
	}
	// GitHub and GitLab retry unless they get a 2xx, so acknowledge the events
	// we don't care about as well.
	if !isPush {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
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

// --- Deploy hook ---
//
// A secret URL per site that triggers a build of its branch: to rebuild when a
// CMS's content changes, from a cron job… That is all it can do, and only for
// that site; if it leaks, regenerate it.

func (s *Server) handleGetDeployHook(w http.ResponseWriter, r *http.Request) {
	site, ok := s.loadSite(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": site.DeployHook})
}

// handleCreateDeployHook creates the hook or, if it already exists,
// regenerates it (the old URL stops working).
func (s *Server) handleCreateDeployHook(w http.ResponseWriter, r *http.Request) {
	s.setDeployHook(w, r, randomHex(24))
}

func (s *Server) handleDeleteDeployHook(w http.ResponseWriter, r *http.Request) {
	s.setDeployHook(w, r, "")
}

func (s *Server) setDeployHook(w http.ResponseWriter, r *http.Request, token string) {
	site, ok := s.loadSite(w, r)
	if !ok {
		return
	}
	if err := s.Store.SetSiteDeployHook(r.Context(), site.ID, token); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

// handleDeployHook triggers a build of the site that owns the secret. It does
// not read the body: every CMS sends its own. Several calls in a row don't
// pile up (Enqueue drops the builds still waiting in the queue).
func (s *Server) handleDeployHook(w http.ResponseWriter, r *http.Request) {
	site, err := s.Store.GetSiteByDeployHook(r.Context(), chi.URLParam(r, "token"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, errDeployHookNotFound)
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	if site.Repo == "" {
		writeError(w, http.StatusBadRequest, errSiteNotLinked)
		return
	}
	b, err := s.Builds.Enqueue(r.Context(), site.ID, store.TriggerHook, site.Branch, "", gitprovider.Commit{})
	if err != nil {
		writeErrorFrom(w, http.StatusServiceUnavailable, err, errUnexpected)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "queued", "build_id": b.ID})
}

// handleClearBuildCache empties the site's dependency cache (handy when a
// corrupt cache causes odd build errors).
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

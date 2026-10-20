package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dockfly/dockfly/internal/gitprovider"
	"github.com/dockfly/dockfly/internal/store"
)

// --- Estados OAuth ---
//
// Los retornos desde GitHub/GitLab no llevan la cookie de sesión (es
// SameSite=Strict y la navegación viene de otro sitio), así que se
// autentican con el parámetro state: aleatorio, de un solo uso, con
// caducidad y creado por un usuario con sesión.

const stateTTL = 15 * time.Minute

type oauthState struct {
	kind        string // "github_manifest" | "github_install" | "gitlab"
	panelURL    string
	webURL      string
	apiURL      string
	appID       int64 // github_apps.id o gitlab_apps.id
	redirectURI string
	created     time.Time
}

type stateStore struct {
	mu     sync.Mutex
	states map[string]oauthState
}

func (s *stateStore) issue(st oauthState) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.states == nil {
		s.states = map[string]oauthState{}
	}
	for k, v := range s.states {
		if time.Since(v.created) > stateTTL {
			delete(s.states, k)
		}
	}
	st.created = time.Now()
	key := randomHex(24)
	s.states[key] = st
	return key
}

// take consume el estado: un mismo state no puede usarse dos veces.
func (s *stateStore) take(key, kind string) (oauthState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.states[key]
	delete(s.states, key)
	if !ok || st.kind != kind || time.Since(st.created) > stateTTL {
		return oauthState{}, false
	}
	return st, true
}

// panelURL valida la dirección con la que el navegador abre el panel: los
// retornos de GitHub/GitLab volverán a ella.
func panelURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("dirección del panel inválida")
	}
	return u.Scheme + "://" + u.Host, nil
}

// --- Página de retorno ---

var callbackPage = template.Must(template.New("cb").Parse(`<!doctype html>
<html lang="es"><meta charset="utf-8"><title>Dockfly</title>
<style>body{font-family:system-ui,sans-serif;display:grid;place-items:center;min-height:100vh;margin:0;color:#333}
.box{text-align:center;max-width:28rem;padding:1rem}.ok{color:#16a34a}.err{color:#dc2626}</style>
<div class="box"><h2 class="{{if .OK}}ok{{else}}err{{end}}">{{if .OK}}✔ Conectado{{else}}No se pudo conectar{{end}}</h2>
<p>{{.Message}}</p><p><a href="/#/integrations">Volver a Dockfly</a></p></div>
<script>
// Esta página se abre en la ventana emergente: avisa al panel y se cierra.
if (window.opener) {
  window.opener.postMessage({type: "dockfly-oauth", ok: {{.OK}}, message: {{.Message}}}, location.origin);
  window.close();
}
</script></html>`))

func writeCallback(w http.ResponseWriter, ok bool, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
	}
	_ = callbackPage.Execute(w, map[string]any{"OK": ok, "Message": msg})
}

// --- GitHub App ---

// handleGitHubManifestStart prepara la GitHub App de esta instalación. El
// navegador envía el manifiesto a GitHub, que crea la app con un clic.
func (s *Server) handleGitHubManifestStart(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PanelURL      string `json:"panel_url"`
		Organization  string `json:"organization"`
		EnterpriseURL string `json:"enterprise_url"`
	}
	if !decode(w, r, &in) {
		return
	}
	panel, err := panelURL(in.PanelURL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	web, apiURL, err := gitprovider.GitHubWebURLs(in.EnterpriseURL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	publicURL, _ := s.Store.GetSetting(r.Context(), store.SettingPublicURL)
	hookURL := ""
	if publicURL != "" {
		hookURL = publicURL + "/api/hooks/github-app"
	}
	// El nombre de una GitHub App es único en todo GitHub: un sufijo
	// aleatorio evita choques entre instalaciones de Dockfly.
	manifest := gitprovider.NewManifest("Dockfly "+randomHex(3), panel, hookURL)
	raw, _ := json.Marshal(manifest)
	state := s.oauthStates.issue(oauthState{kind: "github_manifest", panelURL: panel, webURL: web, apiURL: apiURL})
	writeJSON(w, http.StatusOK, map[string]any{
		"action":   gitprovider.ManifestFormURL(web, strings.TrimSpace(in.Organization), state),
		"manifest": string(raw),
		"webhook":  hookURL != "",
	})
}

// handleGitHubManifestCallback recibe a la vuelta de GitHub el código con el
// que se obtienen las credenciales de la app recién creada, y lleva al
// usuario a instalarla en su cuenta u organización.
func (s *Server) handleGitHubManifestCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	st, ok := s.oauthStates.take(q.Get("state"), "github_manifest")
	if !ok {
		writeCallback(w, false, "El enlace caducó o ya se usó. Vuelve a pulsar «Conectar con GitHub».")
		return
	}
	creds, err := gitprovider.ConvertManifest(r.Context(), st.apiURL, q.Get("code"))
	if err != nil {
		writeCallback(w, false, "GitHub no devolvió la app: "+err.Error())
		return
	}
	app := &store.GitHubApp{
		WebURL: st.webURL, APIURL: st.apiURL, AppID: creds.ID, Slug: creds.Slug, Name: creds.Name,
		Owner: creds.Owner.Login, HTMLURL: creds.HTMLURL, ClientID: creds.ClientID,
		ClientSecretEnc: s.Secrets.Seal([]byte(creds.ClientSecret)),
		PrivateKeyEnc:   s.Secrets.Seal([]byte(creds.PEM)),
	}
	if creds.WebhookSecret != "" {
		app.WebhookSecretEnc = s.Secrets.Seal([]byte(creds.WebhookSecret))
		app.WebhookActive = true
	}
	if err := s.Store.CreateGitHubApp(r.Context(), app); err != nil {
		s.Log.Error("guardando la GitHub App", "error", err)
		writeCallback(w, false, "No se pudo guardar la app.")
		return
	}
	next := s.oauthStates.issue(oauthState{kind: "github_install", panelURL: st.panelURL, appID: app.ID})
	http.Redirect(w, r, gitprovider.InstallURL(app.WebURL, app.Slug, next), http.StatusFound)
}

// handleGitHubSetup es adonde vuelve GitHub tras instalar la app (o cambiar
// sus repositorios): registra la cuenta u organización como conexión.
func (s *Server) handleGitHubSetup(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("setup_action") == "request" {
		writeCallback(w, true, "Se pidió la instalación a un administrador de la organización. Cuando la apruebe, aparecerá aquí.")
		return
	}
	instID, err := strconv.ParseInt(q.Get("installation_id"), 10, 64)
	if err != nil || instID < 1 {
		writeCallback(w, false, "GitHub no indicó la instalación.")
		return
	}
	ctx := r.Context()

	// Con un state válido sabemos qué app es. Sin él (el usuario cambió los
	// repositorios desde GitHub) la buscamos: solo la app dueña de la
	// instalación puede consultarla, así que no se puede falsificar.
	var apps []*store.GitHubApp
	if st, ok := s.oauthStates.take(q.Get("state"), "github_install"); ok {
		if app, err := s.Store.GetGitHubApp(ctx, st.appID); err == nil {
			apps = []*store.GitHubApp{app}
		}
	} else {
		apps, _ = s.Store.ListGitHubApps(ctx)
	}
	for _, app := range apps {
		ghApp, err := s.Git.App(app)
		if err != nil {
			continue
		}
		inst, err := ghApp.Installation(ctx, instID)
		if err != nil {
			continue
		}
		conn := &store.GitConnection{
			Provider: gitprovider.GitHub, AuthType: store.AuthGitHubApp, BaseURL: app.APIURL,
			Username: inst.Account, AccountType: inst.AccountType, SettingsURL: inst.HTMLURL,
			GitHubAppID: &app.ID, InstallationID: &inst.ID,
		}
		if err := s.Store.UpsertInstallation(ctx, conn); err != nil {
			s.Log.Error("guardando la instalación", "error", err)
			writeCallback(w, false, "No se pudo guardar la conexión.")
			return
		}
		s.Git.Forget(conn.ID) // los repos visibles pudieron cambiar
		writeCallback(w, true, "GitHub conectado: "+inst.Account)
		return
	}
	writeCallback(w, false, "Esta instalación no pertenece a ninguna GitHub App de este Dockfly.")
}

// handleGitHubInstallURL devuelve el enlace para instalar la app en otra
// cuenta u organización.
func (s *Server) handleGitHubInstallURL(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		PanelURL string `json:"panel_url"`
	}
	if !decode(w, r, &in) {
		return
	}
	panel, err := panelURL(in.PanelURL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	app, err := s.Store.GetGitHubApp(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "app no encontrada")
		return
	}
	state := s.oauthStates.issue(oauthState{kind: "github_install", panelURL: panel, appID: app.ID})
	writeJSON(w, http.StatusOK, map[string]string{"url": gitprovider.InstallURL(app.WebURL, app.Slug, state)})
}

func (s *Server) handleDeleteGitHubApp(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if !s.refuseIfInUse(w, r, func(c *store.GitConnection) bool { return c.GitHubAppID != nil && *c.GitHubAppID == id }) {
		return
	}
	if err := s.Store.DeleteGitHubApp(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "app no encontrada")
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// refuseIfInUse responde 409 si algún sitio usa una de las conexiones que
// cumplen match.
func (s *Server) refuseIfInUse(w http.ResponseWriter, r *http.Request, match func(*store.GitConnection) bool) bool {
	conns, err := s.Store.ListGitConnections(r.Context())
	if err != nil {
		s.internalError(w, err)
		return false
	}
	var ids []int64
	for _, c := range conns {
		if match(c) {
			ids = append(ids, c.ID)
		}
	}
	sites, err := s.Store.SitesUsingConnections(r.Context(), ids)
	if err != nil {
		s.internalError(w, err)
		return false
	}
	if len(sites) > 0 {
		names := make([]string, len(sites))
		for i, st := range sites {
			names[i] = st.Name
		}
		writeError(w, http.StatusConflict, "la usan estos sitios; desconéctalos primero: "+strings.Join(names, ", "))
		return false
	}
	return true
}

func (s *Server) handleListGitApps(w http.ResponseWriter, r *http.Request) {
	gh, err := s.Store.ListGitHubApps(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	gl, err := s.Store.ListGitLabApps(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"github": gh, "gitlab": gl})
}

// syncGitHubWebhooks apunta el webhook de cada GitHub App a la URL pública
// actual. Se llama al cambiar esa URL.
func (s *Server) syncGitHubWebhooks(r *http.Request, publicURL string) []string {
	if publicURL == "" {
		return nil
	}
	apps, err := s.Store.ListGitHubApps(r.Context())
	if err != nil {
		return []string{err.Error()}
	}
	var warnings []string
	for _, app := range apps {
		ghApp, err := s.Git.App(app)
		if err == nil {
			secret := randomHex(32)
			if err = ghApp.UpdateWebhook(r.Context(), publicURL+"/api/hooks/github-app", secret); err == nil {
				app.WebhookSecretEnc, app.WebhookActive = s.Secrets.Seal([]byte(secret)), true
				err = s.Store.UpdateGitHubAppWebhook(r.Context(), app)
			}
		}
		if err != nil {
			warnings = append(warnings, fmt.Sprintf(
				"no se pudo actualizar el webhook de la app %s: actívalo en %s con la URL %s/api/hooks/github-app (%v)",
				app.Name, app.HTMLURL, publicURL, err))
		}
	}
	return warnings
}

// handleGitHubAppWebhook recibe los eventos de todas las instalaciones de
// una GitHub App: un único webhook para todos sus repositorios.
func (s *Server) handleGitHubAppWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := readWebhookBody(w, r)
	if err != nil {
		return
	}
	ctx := r.Context()
	appID, _ := strconv.ParseInt(r.Header.Get("X-GitHub-Hook-Installation-Target-ID"), 10, 64)
	app, err := s.Store.GitHubAppByAppID(ctx, appID)
	if err != nil || len(app.WebhookSecretEnc) == 0 {
		writeError(w, http.StatusNotFound, "app desconocida")
		return
	}
	secret, err := s.Secrets.Open(app.WebhookSecretEnc)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if err := gitprovider.VerifyGitHubSignature(r.Header, body, string(secret)); err != nil {
		writeError(w, http.StatusUnauthorized, "firma inválida")
		return
	}

	// Si desinstalan la app de una cuenta, sus conexiones dejan de servir.
	if instID, ok := gitprovider.ParseGitHubInstallationDeleted(r.Header, body); ok {
		conns, _ := s.Store.ConnectionsByInstallation(ctx, app.ID, instID)
		for _, c := range conns {
			_ = s.Store.DeleteGitConnection(ctx, c.ID)
			s.Git.Forget(c.ID)
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "instalación eliminada"})
		return
	}

	push, isPush, err := gitprovider.ParseGitHubPush(r.Header, body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "payload inválido")
		return
	}
	if !isPush {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignorado"})
		return
	}
	conns, err := s.Store.ConnectionsByInstallation(ctx, app.ID, push.InstallationID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	ids := make([]int64, len(conns))
	for i, c := range conns {
		ids[i] = c.ID
	}
	sites, err := s.Store.SitesUsingConnections(ctx, ids)
	if err != nil {
		s.internalError(w, err)
		return
	}
	var builds []int64
	for _, site := range sites {
		if site.Repo != push.Repo || site.Branch != push.Branch {
			continue
		}
		b, err := s.Builds.Enqueue(ctx, site.ID, store.TriggerPush, push.Branch, push.Commit)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		builds = append(builds, b.ID)
	}
	if len(builds) == 0 {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignorado: ningún sitio despliega " + push.Repo + "@" + push.Branch})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "en cola", "build_ids": builds})
}

func readWebhookBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 25<<20))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "cuerpo demasiado grande")
	}
	return body, err
}

// --- GitLab OAuth ---

func (s *Server) handlePutGitLabApp(w http.ResponseWriter, r *http.Request) {
	var in struct {
		WebURL       string `json:"web_url"`
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if !decode(w, r, &in) {
		return
	}
	web, err := gitprovider.GitLabWebURL(in.WebURL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	in.ClientID, in.ClientSecret = strings.TrimSpace(in.ClientID), strings.TrimSpace(in.ClientSecret)
	if in.ClientID == "" || in.ClientSecret == "" {
		writeError(w, http.StatusBadRequest, "el Application ID y el Secret son obligatorios")
		return
	}
	app := &store.GitLabApp{WebURL: web, ClientID: in.ClientID, ClientSecretEnc: s.Secrets.Seal([]byte(in.ClientSecret))}
	if err := s.Store.SaveGitLabApp(r.Context(), app); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, app)
}

func (s *Server) handleDeleteGitLabApp(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if !s.refuseIfInUse(w, r, func(c *store.GitConnection) bool { return c.GitLabAppID != nil && *c.GitLabAppID == id }) {
		return
	}
	if err := s.Store.DeleteGitLabApp(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "app no encontrada")
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleGitLabAuthorize devuelve la URL de la ventana de autorización.
func (s *Server) handleGitLabAuthorize(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AppID    int64  `json:"app_id"`
		PanelURL string `json:"panel_url"`
	}
	if !decode(w, r, &in) {
		return
	}
	panel, err := panelURL(in.PanelURL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	app, err := s.Store.GetGitLabApp(r.Context(), in.AppID)
	if err != nil {
		writeError(w, http.StatusNotFound, "configura primero la aplicación de GitLab")
		return
	}
	redirectURI := panel + "/api/git/gitlab/callback"
	state := s.oauthStates.issue(oauthState{kind: "gitlab", panelURL: panel, appID: app.ID, redirectURI: redirectURI})
	writeJSON(w, http.StatusOK, map[string]string{"url": gitprovider.GitLabAuthorizeURL(app.WebURL, app.ClientID, redirectURI, state)})
}

func (s *Server) handleGitLabCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	st, ok := s.oauthStates.take(q.Get("state"), "gitlab")
	if !ok {
		writeCallback(w, false, "El enlace caducó o ya se usó. Vuelve a pulsar «Conectar con GitLab».")
		return
	}
	if e := q.Get("error"); e != "" {
		msg := "Cancelaste la autorización."
		if e != "access_denied" {
			msg = "GitLab devolvió un error: " + q.Get("error_description")
		}
		writeCallback(w, false, msg)
		return
	}
	ctx := r.Context()
	app, err := s.Store.GetGitLabApp(ctx, st.appID)
	if err != nil {
		writeCallback(w, false, "La aplicación de GitLab ya no existe.")
		return
	}
	secret, err := s.Secrets.Open(app.ClientSecretEnc)
	if err != nil {
		s.internalError(w, err)
		return
	}
	tok, err := gitprovider.GitLabExchangeCode(ctx, app.WebURL, app.ClientID, string(secret), q.Get("code"), st.redirectURI)
	if err != nil {
		writeCallback(w, false, err.Error())
		return
	}
	apiURL := gitprovider.GitLabAPIURL(app.WebURL)
	p, _ := gitprovider.NewWithOptions(gitprovider.GitLab, apiURL, tok.AccessToken, gitprovider.Options{OAuth: true})
	user, err := p.CurrentUser(ctx)
	if err != nil {
		writeCallback(w, false, "No se pudo leer tu usuario de GitLab: "+err.Error())
		return
	}
	conn := &store.GitConnection{
		Provider: gitprovider.GitLab, AuthType: store.AuthGitLabOAuth, BaseURL: apiURL, Username: user,
		SettingsURL:     app.WebURL + "/-/user_settings/applications",
		TokenEnc:        s.Secrets.Seal([]byte(tok.AccessToken)),
		RefreshTokenEnc: s.Secrets.Seal([]byte(tok.RefreshToken)),
		RedirectURI:     st.redirectURI,
		GitLabAppID:     &app.ID,
	}
	if !tok.ExpiresAt.IsZero() {
		conn.TokenExpiresAt = &tok.ExpiresAt
	}
	if err := s.Store.SaveGitLabConnection(ctx, conn); err != nil {
		s.internalError(w, err)
		return
	}
	writeCallback(w, true, "GitLab conectado: "+user)
}

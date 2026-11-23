package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dockfly/dockfly/internal/gitprovider"
	"github.com/dockfly/dockfly/internal/store"
)

var (
	errInvalidPanelURL        = newError("invalid_panel_url", "Invalid panel address")
	errGitAppNotFound         = newError("git_app_not_found", "App not found")
	errGitHubAppUnknown       = newError("github_app_unknown", "Unknown app")
	errGitLabAppCredsRequired = newError("gitlab_app_credentials_required", "The Application ID and Secret are required")
	errGitLabAppNotConfigured = newError("gitlab_app_not_configured", "Set up the GitLab application first")
)

// --- OAuth states ---
//
// The returns from GitHub/GitLab don't carry the session cookie (it is
// SameSite=Strict and the navigation comes from another site), so they are
// authenticated with the state parameter: random, single-use, with an expiry
// and created by a user with a session.

const stateTTL = 15 * time.Minute

type oauthState struct {
	kind        string // "github_manifest" | "github_install" | "gitlab"
	panelURL    string
	webURL      string
	apiURL      string
	appID       int64 // github_apps.id or gitlab_apps.id
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

// take consumes the state: the same state can't be used twice.
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

// panelURL validates the address the browser opens the panel with: the
// returns from GitHub/GitLab will come back to it.
func panelURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || (u.Path != "" && u.Path != "/") {
		return "", errInvalidPanelURL
	}
	return u.Scheme + "://" + u.Host, nil
}

// --- Return page ---

var callbackPage = template.Must(template.New("cb").Parse(`<!doctype html>
<html lang="en"><meta charset="utf-8"><title>Dockfly</title>
<style>body{font-family:system-ui,sans-serif;display:grid;place-items:center;min-height:100vh;margin:0;color:#333}
.box{text-align:center;max-width:28rem;padding:1rem}.ok{color:#16a34a}.err{color:#dc2626}</style>
<div class="box"><h2 class="{{if .OK}}ok{{else}}err{{end}}">{{if .OK}}✔ Connected{{else}}Couldn't connect{{end}}</h2>
<p>{{.Message}}</p><p><a href="/integrations">Back to Dockfly</a></p></div>
<script>
// This page opens in the popup window: it notifies the panel and closes.
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

// handleGitHubManifestStart prepares this installation's GitHub App. The
// browser posts the manifest to GitHub, which creates the app in one click.
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
		writeErrorFrom(w, http.StatusBadRequest, err, errUnexpected)
		return
	}
	web, apiURL, err := gitprovider.GitHubWebURLs(in.EnterpriseURL)
	if err != nil {
		writeErrorFrom(w, http.StatusBadRequest, err, errUnexpected)
		return
	}
	publicURL, _ := s.Store.GetSetting(r.Context(), store.SettingPublicURL)
	hookURL := ""
	if publicURL != "" {
		hookURL = publicURL + "/api/hooks/github-app"
	}
	// A GitHub App's name is unique across all of GitHub, so a random suffix
	// avoids clashes between Dockfly installations.
	manifest := gitprovider.NewManifest("Dockfly "+randomHex(3), panel, hookURL)
	raw, _ := json.Marshal(manifest)
	state := s.oauthStates.issue(oauthState{kind: "github_manifest", panelURL: panel, webURL: web, apiURL: apiURL})
	writeJSON(w, http.StatusOK, map[string]any{
		"action":   gitprovider.ManifestFormURL(web, strings.TrimSpace(in.Organization), state),
		"manifest": string(raw),
		"webhook":  hookURL != "",
	})
}

// handleGitHubManifestCallback receives, on the way back from GitHub, the code
// that the just-created app's credentials are obtained with, and then sends the
// user off to install it on their account or organization.
func (s *Server) handleGitHubManifestCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	st, ok := s.oauthStates.take(q.Get("state"), "github_manifest")
	if !ok {
		writeCallback(w, false, "The link expired or was already used. Click “Connect with GitHub” again.")
		return
	}
	creds, err := gitprovider.ConvertManifest(r.Context(), st.apiURL, q.Get("code"))
	if err != nil {
		writeCallback(w, false, "GitHub didn't return the app: "+err.Error())
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
		s.Log.Error("saving the GitHub App", "error", err)
		writeCallback(w, false, "Couldn't save the app.")
		return
	}
	next := s.oauthStates.issue(oauthState{kind: "github_install", panelURL: st.panelURL, appID: app.ID})
	http.Redirect(w, r, gitprovider.InstallURL(app.WebURL, app.Slug, next), http.StatusFound)
}

// handleGitHubSetup is where GitHub comes back to after the app is installed
// (or its repositories change): it registers the account or organization as a
// connection.
func (s *Server) handleGitHubSetup(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("setup_action") == "request" {
		writeCallback(w, true, "The installation was requested from an organization administrator. It will show up here once they approve it.")
		return
	}
	instID, err := strconv.ParseInt(q.Get("installation_id"), 10, 64)
	if err != nil || instID < 1 {
		writeCallback(w, false, "GitHub didn't say which installation it was.")
		return
	}
	ctx := r.Context()

	// With a valid state we know which app it is. Without one (the user
	// changed the repositories from GitHub) we look for it: only the app that
	// owns the installation can query it, so this can't be forged.
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
			s.Log.Error("saving the installation", "error", err)
			writeCallback(w, false, "Couldn't save the connection.")
			return
		}
		s.Git.Forget(conn.ID) // the visible repos may have changed
		writeCallback(w, true, "GitHub connected: "+inst.Account)
		return
	}
	writeCallback(w, false, "This installation doesn't belong to any GitHub App of this Dockfly.")
}

// handleGitHubInstallURL returns the link to install the app on another
// account or organization.
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
		writeErrorFrom(w, http.StatusBadRequest, err, errUnexpected)
		return
	}
	app, err := s.Store.GetGitHubApp(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, errGitAppNotFound)
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
		writeError(w, http.StatusNotFound, errGitAppNotFound)
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// refuseIfInUse answers 409 if any site uses one of the connections that
// match.
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
	// Their sites and apps would be left unable to build and with orphan webhooks.
	sites, err := s.Store.SitesUsingConnections(r.Context(), ids)
	if err != nil {
		s.internalError(w, err)
		return false
	}
	apps, err := s.Store.ListApps(r.Context())
	if err != nil {
		s.internalError(w, err)
		return false
	}
	var names []string
	for _, st := range sites {
		names = append(names, st.Name)
	}
	for _, a := range apps {
		if a.GitConnectionID != nil && slices.Contains(ids, *a.GitConnectionID) {
			names = append(names, a.Name)
		}
	}
	if len(names) > 0 {
		writeError(w, http.StatusConflict, errGitConnectionInUse.With("sites", strings.Join(names, ", ")))
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

// syncGitHubWebhooks points every GitHub App's webhook at the current public
// URL. It is called when that URL changes.
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
				"couldn't update the webhook of app %s: enable it at %s with the URL %s/api/hooks/github-app (%v)",
				app.Name, app.HTMLURL, publicURL, err))
		}
	}
	return warnings
}

// handleGitHubAppWebhook receives the events from every installation of a
// GitHub App: a single webhook for all of its repositories.
func (s *Server) handleGitHubAppWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := readWebhookBody(w, r)
	if err != nil {
		return
	}
	ctx := r.Context()
	appID, _ := strconv.ParseInt(r.Header.Get("X-GitHub-Hook-Installation-Target-ID"), 10, 64)
	app, err := s.Store.GitHubAppByAppID(ctx, appID)
	if err != nil || len(app.WebhookSecretEnc) == 0 {
		writeError(w, http.StatusNotFound, errGitHubAppUnknown)
		return
	}
	secret, err := s.Secrets.Open(app.WebhookSecretEnc)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if err := gitprovider.VerifyGitHubSignature(r.Header, body, string(secret)); err != nil {
		writeError(w, http.StatusUnauthorized, errWebhookBadSignature)
		return
	}

	// If the app is uninstalled from an account, its connections are useless.
	if instID, ok := gitprovider.ParseGitHubInstallationDeleted(r.Header, body); ok {
		conns, _ := s.Store.ConnectionsByInstallation(ctx, app.ID, instID)
		for _, c := range conns {
			_ = s.Store.DeleteGitConnection(ctx, c.ID)
			s.Git.Forget(c.ID)
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "installation deleted"})
		return
	}

	push, isPush, err := gitprovider.ParseGitHubPush(r.Header, body)
	if err != nil {
		writeError(w, http.StatusBadRequest, errWebhookBadPayload)
		return
	}
	if !isPush {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
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
	appList, err := s.Store.ListApps(ctx)
	if err != nil {
		s.internalError(w, err)
		return
	}
	for _, a := range appList {
		if push.Deleted || a.Source == store.AppSourceImage || a.GitConnectionID == nil || !slices.Contains(ids, *a.GitConnectionID) ||
			a.Repo != push.Repo || a.Branch != push.Branch {
			continue
		}
		d, err := s.Apps.Enqueue(ctx, a.ID, store.TriggerPush, push.Commit, "")
		if err != nil {
			writeErrorFrom(w, http.StatusServiceUnavailable, err, errUnexpected)
			return
		}
		builds = append(builds, d.ID)
	}
	handled := false
	for _, site := range sites {
		if site.Repo != push.Repo {
			continue
		}
		id, _, err := s.handleSitePush(ctx, site, push)
		if err != nil {
			writeErrorFrom(w, http.StatusServiceUnavailable, err, errUnexpected)
			return
		}
		if id != 0 {
			builds = append(builds, id)
		} else if push.Deleted {
			handled = true
		}
	}
	if handled && len(builds) == 0 {
		writeJSON(w, http.StatusOK, map[string]string{"status": "branch deleted"})
		return
	}
	if len(builds) == 0 {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored: no site deploys " + push.Repo + "@" + push.Branch})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "queued", "build_ids": builds})
}

// maxWebhookBytes caps a webhook's body (a GitHub/GitLab push is a few KB).
// It used to accept 25 MB unauthenticated.
const maxWebhookBytes = 5 << 20

// readWebhookBody reads a webhook's body with a size cap and a read deadline,
// so a slow unauthenticated request can't hold on to the connection or the
// memory.
func readWebhookBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	if rc := http.NewResponseController(w); rc != nil {
		_ = rc.SetReadDeadline(time.Now().Add(15 * time.Second))
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBytes))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, errWebhookBodyTooLarge)
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
		writeErrorFrom(w, http.StatusBadRequest, err, errUnexpected)
		return
	}
	in.ClientID, in.ClientSecret = strings.TrimSpace(in.ClientID), strings.TrimSpace(in.ClientSecret)
	if in.ClientID == "" || in.ClientSecret == "" {
		writeError(w, http.StatusBadRequest, errGitLabAppCredsRequired)
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
		writeError(w, http.StatusNotFound, errGitAppNotFound)
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleGitLabAuthorize returns the URL of the authorization window.
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
		writeErrorFrom(w, http.StatusBadRequest, err, errUnexpected)
		return
	}
	app, err := s.Store.GetGitLabApp(r.Context(), in.AppID)
	if err != nil {
		writeError(w, http.StatusNotFound, errGitLabAppNotConfigured)
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
		writeCallback(w, false, "The link expired or was already used. Click “Connect with GitLab” again.")
		return
	}
	if e := q.Get("error"); e != "" {
		msg := "You canceled the authorization."
		if e != "access_denied" {
			msg = "GitLab returned an error: " + q.Get("error_description")
		}
		writeCallback(w, false, msg)
		return
	}
	ctx := r.Context()
	app, err := s.Store.GetGitLabApp(ctx, st.appID)
	if err != nil {
		writeCallback(w, false, "The GitLab application no longer exists.")
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
		writeCallback(w, false, "Couldn't read your GitLab user: "+err.Error())
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
	writeCallback(w, true, "GitLab connected: "+user)
}

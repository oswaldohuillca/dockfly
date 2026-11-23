// Package api exposes the panel's REST API and serves the frontend.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/dockfly/dockfly/internal/apps"
	"github.com/dockfly/dockfly/internal/auth"
	"github.com/dockfly/dockfly/internal/builder"
	"github.com/dockfly/dockfly/internal/dbbackup"
	"github.com/dockfly/dockfly/internal/docker"
	"github.com/dockfly/dockfly/internal/gitauth"
	"github.com/dockfly/dockfly/internal/notify"
	"github.com/dockfly/dockfly/internal/pages"
	"github.com/dockfly/dockfly/internal/routing"
	"github.com/dockfly/dockfly/internal/secrets"
	"github.com/dockfly/dockfly/internal/selfupdate"
	"github.com/dockfly/dockfly/internal/store"
	"github.com/dockfly/dockfly/internal/traefik"
	"github.com/dockfly/dockfly/internal/tunnel"
)

const sessionCookieDev = "dockfly_session"

// sessionCookieName uses the __Host- prefix in production (which requires
// Secure, Path=/ and no Domain). In development (no Traefik, plain HTTP on
// localhost) it uses the bare name so the cookie works without HTTPS.
func (s *Server) sessionCookieName() string {
	if s.Super == nil {
		return sessionCookieDev
	}
	return "__Host-" + sessionCookieDev
}

func (s *Server) cookieSecure(r *http.Request) bool {
	return s.Super != nil || r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

type ctxKey struct{}

type Server struct {
	Store    *store.Store
	Signer   *auth.Signer
	Docker   *docker.Client // nil when Docker isn't available
	Traefik  traefik.Options
	Super    *traefik.Supervisor // nil when Traefik is disabled (development)
	Certs    *traefik.CertTracker
	Notify   *notify.Dispatcher // nil in some tests
	Frontend fs.FS
	Version  string
	Log      *slog.Logger
	// TrustedProxies are the CIDR ranges whose X-Forwarded-For is accepted to
	// work out the client IP. Empty = the panel is exposed directly.
	TrustedProxies []string

	// Static sites.
	Pages        *pages.Server  // serves the published files
	PagesStorage *pages.Storage // deployments on disk
	StaticURL    string         // the static server's internal URL, for Traefik
	MaxUploadMB  int64

	// Builds from GitHub/GitLab.
	Builds  *builder.Manager
	Secrets *secrets.Box
	Git     *gitauth.Resolver
	Apps    *apps.Manager
	// DBBackups takes the backups of the database apps.
	DBBackups *dbbackup.Manager
	// Hub connects to the agents on the remote servers.
	Hub *tunnel.Hub
	// Image is the Dockfly image (used in the agents' command).
	Image   string
	remotes remotes

	oauthStates stateStore

	DataDir    string
	ListenAddr string // the panel's address (so a stream can't reuse its port)
	// Restart stops Dockfly so that Docker starts it again (used by the backup
	// restore, which is applied on startup).
	Restart func()
	// Updater updates Dockfly from the panel.
	Updater *selfupdate.Updater

	// SetupToken is the setup code, valid while there are no users.
	SetupToken string

	applyMu   sync.Mutex
	mfa       failures[int64] // wrong 2FA codes per user
	csrf      *http.CrossOriginProtection
	csrfOnce  sync.Once
	loginFail failures[string] // wrong passwords per email
}

// Apply publishes the current state: it regenerates Traefik's configuration
// and the static server's domain table.
//
// It ignores ctx cancellation: if the client disconnects right after saving a
// change, the database is already updated and Traefik has to hear about it
// anyway.
func (s *Server) Apply(ctx context.Context) error {
	ctx = context.WithoutCancel(ctx)
	s.applyMu.Lock()
	defer s.applyMu.Unlock()

	snap, err := s.snapshot(ctx)
	if err != nil {
		return err
	}
	plan := routing.Build(snap)
	if s.Hub != nil {
		defer s.pushServers(plan.Remote, plan.Dynamic.Middlewares)
	}
	// The static server first and Traefik second: that way, by the time
	// Traefik starts routing a new domain, the server knows what to serve.
	s.Pages.SetRoutes(plan.Static)
	return traefik.WriteDynamic(s.Traefik, plan.Dynamic)
}

// snapshot gathers everything that affects routing (see routing.Build).
func (s *Server) snapshot(ctx context.Context) (routing.Snapshot, error) {
	var snap routing.Snapshot
	var err error
	if snap.Hosts, err = s.Store.ListProxyHosts(ctx); err != nil {
		return snap, err
	}
	if snap.Sites, err = s.Store.ListSites(ctx); err != nil {
		return snap, err
	}
	if snap.Apps, err = s.Store.ListApps(ctx); err != nil {
		return snap, err
	}
	if snap.Redirects, err = s.Store.ListRedirects(ctx); err != nil {
		return snap, err
	}
	if snap.Streams, err = s.Store.ListStreams(ctx); err != nil {
		return snap, err
	}
	if snap.AccessLists, err = s.Store.ListAccessLists(ctx); err != nil {
		return snap, err
	}
	plan, err := s.loadCertPlan(ctx)
	if err != nil {
		return snap, err
	}
	snap.Resolver = plan.resolver
	for _, c := range plan.custom {
		base := filepath.Join(s.certDir(), strconv.FormatInt(c.ID, 10))
		snap.Certificates = append(snap.Certificates, traefik.CertFile{Cert: base + ".crt", Key: base + ".key"})
	}
	snap.StaticURL = s.StaticURL
	snap.SiteDir = s.PagesStorage.DeploymentDir
	if snap.Previews, err = s.previews(ctx, snap.Sites); err != nil {
		return snap, err
	}
	return snap, nil
}

// Errors from signup and login (and the rest of account management).
var (
	errNameRequired     = newError("name_required", "Name is required")
	errInvalidEmail     = newError("invalid_email", "Invalid email")
	errPasswordTooShort = newError("password_too_short", "The password must be at least {min} characters long")
	errAlreadySetUp     = newError("already_set_up", "This installation is already set up")
	errBadCredentials   = newError("bad_credentials", "Wrong email or password")
	errSetupToken       = newError("setup_token_invalid", "Wrong setup code: find it in Dockfly's logs (docker logs dockfly) or in /data/setup-token")
)

func currentUser(r *http.Request) *store.User {
	u, _ := r.Context().Value(ctxKey{}).(*store.User)
	return u
}

// --- Session ---

func (s *Server) setSession(w http.ResponseWriter, r *http.Request, u *store.User) {
	token, exp := s.Signer.Issue(u.ID, u.SessionVersion)
	http.SetCookie(w, &http.Cookie{
		Name:     s.sessionCookieName(),
		Value:    token,
		Path:     "/",
		Expires:  exp,
		HttpOnly: true,
		Secure:   s.cookieSecure(r),
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	n, err := s.Store.CountUsers(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"needs_setup": n == 0, "setup_code_required": n == 0 && s.SetupToken != ""})
}

type credentials struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
	// SetupToken is the setup code (only used by the initial signup).
	SetupToken string `json:"setup_token,omitempty"`
}

// handleSetup creates the first administrator. It only works while there
// are no users.
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if !decode(w, r, &in) {
		return
	}
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	in.Name = strings.TrimSpace(in.Name)
	switch {
	case in.Name == "":
		writeError(w, http.StatusBadRequest, errNameRequired)
		return
	case !strings.Contains(in.Email, "@"):
		writeError(w, http.StatusBadRequest, errInvalidEmail)
		return
	case len(in.Password) < 8:
		writeError(w, http.StatusBadRequest, errPasswordTooShort.With("min", minPassword))
		return
	}

	// applyMu serializes setup too, so that two simultaneous requests can't
	// create two administrators.
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	n, err := s.Store.CountUsers(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	if n > 0 {
		writeError(w, http.StatusConflict, errAlreadySetUp)
		return
	}
	if !s.checkSetupToken(in.SetupToken) {
		writeError(w, http.StatusForbidden, errSetupToken)
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		s.internalError(w, err)
		return
	}
	u := &store.User{Email: in.Email, Name: in.Name, PasswordHash: hash, Role: store.RoleAdmin}
	if err := s.Store.CreateUser(r.Context(), u); err != nil {
		s.internalError(w, err)
		return
	}
	s.clearSetupToken()
	s.setSession(w, r, u)
	writeJSON(w, http.StatusCreated, u)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if !decode(w, r, &in) {
		return
	}
	if s.loginFail.blocked(loginKey(in.Email), loginMaxFailures) {
		writeError(w, http.StatusTooManyRequests, errTooManyAttempts)
		return
	}
	u, err := s.Store.UserByEmail(r.Context(), strings.ToLower(strings.TrimSpace(in.Email)))
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.internalError(w, err)
		return
	}
	// If the user doesn't exist (or hasn't accepted the invitation yet) we
	// compare against a dummy hash anyway: the response then takes just as
	// long and doesn't reveal which emails are registered or pending.
	hash := auth.DummyHash()
	if u != nil && u.PasswordHash != "" {
		hash = u.PasswordHash
	}
	if !auth.CheckPassword(hash, in.Password) || u == nil || u.PasswordHash == "" {
		s.loginFail.fail(loginKey(in.Email))
		writeError(w, http.StatusUnauthorized, errBadCredentials)
		return
	}
	s.loginFail.reset(loginKey(in.Email))
	if u.TOTPEnabled {
		writeJSON(w, http.StatusOK, map[string]any{
			"two_factor": true, "ticket": s.Signer.IssueTicket(mfaTicketPurpose, u.ID, mfaTicketTTL),
		})
		return
	}
	s.setSession(w, r, u)
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	// Bumping the session version invalidates the cookie on the server (a
	// stolen cookie stops working). It closes every session of that user.
	if u, ok := s.sessionUser(r); ok {
		_ = s.Store.BumpSessionVersion(r.Context(), u.ID)
	}
	http.SetCookie(w, &http.Cookie{Name: s.sessionCookieName(), Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.cookieSecure(r), SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, currentUser(r))
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{
		"version":          s.Version,
		"docker_available": s.Docker != nil && s.Docker.Available(r.Context()),
	}
	if s.Super != nil {
		out["traefik"] = s.Super.Status()
	} else {
		out["traefik"] = map[string]any{"running": false, "last_error": "Traefik disabled (development mode)"}
	}
	writeJSON(w, http.StatusOK, out)
}

// --- Helpers ---

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, errInvalidJSON.With("detail", err.Error()))
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) internalError(w http.ResponseWriter, err error) {
	s.Log.Error("internal error", "error", err)
	writeError(w, http.StatusInternalServerError, errInternal)
}

// spaHandler serves the frontend's files and returns index.html for any
// unknown route (client-side routing).
func spaHandler(fsys fs.FS) http.Handler {
	files := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if _, err := fs.Stat(fsys, p); err == nil {
				if strings.HasPrefix(p, "assets/") {
					// Vite generates hashed names: they can be cached forever.
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, fsys, "index.html")
	})
}

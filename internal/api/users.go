package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/dockfly/dockfly/internal/auth"
	"github.com/dockfly/dockfly/internal/store"
)

// Errors for users, permissions and invitation links.
var (
	errPanelOnly         = newError("panel_only", "This can only be done from the panel, not with an API token")
	errRoleForbidden     = newError("role_forbidden", "Your role doesn't allow this; ask an administrator")
	errInviteLinkInvalid = newError("invite_link_invalid", "The link is invalid or has expired; ask an administrator for a new one")
	errUnknownRole       = newError("unknown_role", "Unknown role: use admin, deployer or viewer")
	errUserEmailTaken    = newError("user_email_taken", "A user with that email already exists")
	errUserNotFound      = newError("user_not_found", "User not found")
	errLastAdmin         = newError("last_admin", "This is the only administrator: make someone else an administrator first")
	errDeleteSelf        = newError("delete_own_account", "You can't delete your own account")
	errRepoLinkAdmin     = newError("repo_link_admin_only", "Only an administrator can link a new repository")
	errSiteRoutingAdmin  = newError("site_routing_admin_only", "Only an administrator can change custom locations or the access list")
	errAppAccessAdmin    = newError("app_access_admin_only", "Only an administrator can change the access list")
)

func isAdmin(r *http.Request) bool {
	return store.RoleRank(effectiveRole(currentUser(r), currentToken(r))) >= store.RoleRank(store.RoleAdmin)
}

// canLinkRepo: linking a new repository (to a site or an app) is the admin's
// business. The Git accounts are theirs and see every one of their private
// repos: a deployer could publish any of them. A deployer can still change
// the branch or the build of the ones already linked.
func canLinkRepo(r *http.Request) bool { return isAdmin(r) }

// --- Permissions ---

// requiredRole decides which role a request needs. It all lives here, in a
// single place, so it can be reviewed (and tested) at a glance:
//   - viewer: reading, except secrets (environment variables) and backups.
//   - deployer: on top of that, everything about sites, apps, builds and
//     deployments.
//   - admin: everything else (proxy hosts, certificates, integrations,
//     notifications, settings, users…).
func requiredRole(method, path string) string {
	path = strings.TrimSuffix(path, "/")
	// Your own account (2FA, password, tokens) belongs to each user.
	if strings.HasPrefix(path, "/api/auth/") || path == "/api/tokens" || strings.HasPrefix(path, "/api/tokens/") {
		return store.RoleViewer
	}
	read := method == http.MethodGet || method == http.MethodHead
	// The environment variables, the deploy hook and the database backups
	// carry secrets.
	secret := strings.HasSuffix(path, "/env") || strings.HasSuffix(path, "/deploy-hook") || strings.HasSuffix(path, "/download") ||
		path == "/api/backup" || strings.HasPrefix(path, "/api/users")
	// The repositories of a Git account may be private; listing them is
	// restricted to admin, just like linking them (canLinkRepo).
	if read && strings.HasPrefix(path, "/api/git/connections/") &&
		(strings.HasSuffix(path, "/repos") || strings.HasSuffix(path, "/branches")) {
		return store.RoleAdmin
	}
	deployScope := false
	for _, p := range []string{"/api/sites", "/api/builds", "/api/apps", "/api/app-deployments"} {
		if path == p || strings.HasPrefix(path, p+"/") {
			deployScope = true
		}
	}
	switch {
	case path == "/api/backup" || strings.HasPrefix(path, "/api/users"):
		return store.RoleAdmin
	case read && !secret:
		return store.RoleViewer
	case deployScope:
		return store.RoleDeployer
	}
	return store.RoleAdmin
}

// authorize runs after requireAuth.
func (s *Server) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, t := currentUser(r), currentToken(r)
		// The account (password, 2FA), your own tokens and user management can
		// only be done from the panel: a leaked token can't create more tokens,
		// nor a lasting admin user (with an invite/reset link) that would
		// outlive revoking the token.
		if t != nil && (strings.HasPrefix(r.URL.Path, "/api/auth/") || strings.HasPrefix(r.URL.Path, "/api/tokens") ||
			strings.HasPrefix(r.URL.Path, "/api/users")) {
			writeError(w, http.StatusForbidden, errPanelOnly)
			return
		}
		if store.RoleRank(effectiveRole(u, t)) < store.RoleRank(requiredRole(r.Method, r.URL.Path)) {
			writeError(w, http.StatusForbidden, errRoleForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- Invitation and password-change links ---

const (
	inviteTTL   = 7 * 24 * time.Hour
	resetTTL    = 24 * time.Hour
	invitePath  = "/invite/"
	minPassword = 8
)

// linkPurpose ties the link to the current password: as soon as it is used
// (and the password changes) it stops working. Single use, nothing stored.
func linkPurpose(u *store.User) string {
	sum := sha256.Sum256([]byte("dockfly-link:" + u.PasswordHash))
	return "pw-" + hex.EncodeToString(sum[:8])
}

func (s *Server) passwordLink(ctx context.Context, r *http.Request, u *store.User) (url string, expires time.Time) {
	ttl := resetTTL
	if u.PasswordHash == "" {
		ttl = inviteTTL
	}
	token := s.Signer.IssueTicket(linkPurpose(u), u.ID, ttl)
	base, _ := s.Store.GetSetting(ctx, store.SettingPublicURL)
	if base == "" {
		scheme := "http"
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	return base + invitePath + token, time.Now().Add(ttl)
}

// linkUser validates an invitation or password-change link.
func (s *Server) linkUser(ctx context.Context, token string) (*store.User, error) {
	// "purpose.id.expiry.signature": the purpose depends on the user, so the
	// user is looked up first and the signature verified afterwards.
	parts := strings.Split(token, ".")
	if len(parts) != 4 {
		return nil, auth.ErrInvalidSession
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return nil, auth.ErrInvalidSession
	}
	u, err := s.Store.UserByID(ctx, id)
	if err != nil {
		return nil, auth.ErrInvalidSession
	}
	if _, err := s.Signer.VerifyTicket(linkPurpose(u), token); err != nil {
		return nil, err
	}
	return u, nil
}

func (s *Server) handleGetInvite(w http.ResponseWriter, r *http.Request) {
	u, err := s.linkUser(r.Context(), chi.URLParam(r, "token"))
	if err != nil {
		writeError(w, http.StatusNotFound, errInviteLinkInvalid)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": u.Name, "email": u.Email, "new": u.PasswordHash == ""})
}

// handleAcceptInvite sets the password and opens the session.
func (s *Server) handleAcceptInvite(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	u, err := s.linkUser(r.Context(), chi.URLParam(r, "token"))
	if err != nil {
		writeError(w, http.StatusNotFound, errInviteLinkInvalid)
		return
	}
	if len(in.Password) < minPassword {
		writeError(w, http.StatusBadRequest, errPasswordTooShort.With("min", minPassword))
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if u.SessionVersion, err = s.Store.SetPassword(r.Context(), u.ID, hash); err != nil {
		s.internalError(w, err)
		return
	}
	u.PasswordHash = hash
	if u.TOTPEnabled {
		// Resetting the password does not get around the second factor.
		writeJSON(w, http.StatusOK, map[string]any{
			"two_factor": true, "ticket": s.Signer.IssueTicket(mfaTicketPurpose, u.ID, mfaTicketTTL),
		})
		return
	}
	s.setSession(w, r, u)
	writeJSON(w, http.StatusOK, u)
}

// handleChangePassword changes your own password and closes the other
// sessions.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if !decode(w, r, &in) {
		return
	}
	u, ok := s.requirePassword(w, r, in.Current)
	if !ok {
		return
	}
	if len(in.New) < minPassword {
		writeError(w, http.StatusBadRequest, errPasswordTooShort.With("min", minPassword))
		return
	}
	hash, err := auth.HashPassword(in.New)
	if err != nil {
		s.internalError(w, err)
		return
	}
	if u.SessionVersion, err = s.Store.SetPassword(r.Context(), u.ID, hash); err != nil {
		s.internalError(w, err)
		return
	}
	s.setSession(w, r, u)
	w.WriteHeader(http.StatusNoContent)
}

// --- Users (administrators only) ---

type userView struct {
	*store.User
	// Pending: invited, still without a password.
	Pending bool `json:"pending"`
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.Store.ListUsers(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	out := make([]userView, len(users))
	for i, u := range users {
		out[i] = userView{User: u, Pending: u.PasswordHash == ""}
	}
	writeJSON(w, http.StatusOK, out)
}

type userInput struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

func (in *userInput) validate(withEmail bool) error {
	in.Name = strings.TrimSpace(in.Name)
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	switch {
	case in.Name == "":
		return errNameRequired
	case withEmail && !strings.Contains(in.Email, "@"):
		return errInvalidEmail
	case store.RoleRank(in.Role) == 0:
		return errUnknownRole
	}
	return nil
}

// handleInviteUser creates a user without a password and returns their
// invitation link, which the administrator shares however they like.
func (s *Server) handleInviteUser(w http.ResponseWriter, r *http.Request) {
	var in userInput
	if !decode(w, r, &in) {
		return
	}
	if err := in.validate(true); err != nil {
		writeErrorFrom(w, http.StatusBadRequest, err, errUnexpected)
		return
	}
	u := &store.User{Name: in.Name, Email: in.Email, Role: in.Role}
	if err := s.Store.CreateUser(r.Context(), u); isUniqueViolation(err) {
		writeError(w, http.StatusConflict, errUserEmailTaken)
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	link, exp := s.passwordLink(r.Context(), r, u)
	writeJSON(w, http.StatusCreated, map[string]any{"user": userView{User: u, Pending: true}, "link": link, "expires_at": exp})
}

func (s *Server) loadUser(w http.ResponseWriter, r *http.Request) (*store.User, bool) {
	id, ok := pathID(w, r)
	if !ok {
		return nil, false
	}
	u, err := s.Store.UserByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, errUserNotFound)
		return nil, false
	} else if err != nil {
		s.internalError(w, err)
		return nil, false
	}
	return u, true
}

// lastAdmin reports whether u is the only active administrator: they can
// neither be demoted nor deleted, or nobody could administer Dockfly.
func (s *Server) lastAdmin(ctx context.Context, u *store.User) (bool, error) {
	if u.Role != store.RoleAdmin || u.PasswordHash == "" {
		return false, nil
	}
	n, err := s.Store.CountAdmins(ctx)
	return n <= 1, err
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	u, ok := s.loadUser(w, r)
	if !ok {
		return
	}
	var in userInput
	if !decode(w, r, &in) {
		return
	}
	if err := in.validate(false); err != nil {
		writeErrorFrom(w, http.StatusBadRequest, err, errUnexpected)
		return
	}
	if in.Role != store.RoleAdmin {
		if last, err := s.lastAdmin(r.Context(), u); err != nil {
			s.internalError(w, err)
			return
		} else if last {
			writeError(w, http.StatusConflict, errLastAdmin)
			return
		}
	}
	u.Name, u.Role = in.Name, in.Role
	if err := s.Store.UpdateUserProfile(r.Context(), u); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, userView{User: u, Pending: u.PasswordHash == ""})
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	u, ok := s.loadUser(w, r)
	if !ok {
		return
	}
	if u.ID == currentUser(r).ID {
		writeError(w, http.StatusConflict, errDeleteSelf)
		return
	}
	if last, err := s.lastAdmin(r.Context(), u); err != nil {
		s.internalError(w, err)
		return
	} else if last {
		writeError(w, http.StatusConflict, errLastAdmin)
		return
	}
	if err := s.Store.DeleteUser(r.Context(), u.ID); err != nil {
		s.internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleUserLink generates another link: an invitation if they haven't
// accepted it yet, or a password reset if they forgot it.
func (s *Server) handleUserLink(w http.ResponseWriter, r *http.Request) {
	u, ok := s.loadUser(w, r)
	if !ok {
		return
	}
	link, exp := s.passwordLink(r.Context(), r, u)
	writeJSON(w, http.StatusOK, map[string]any{"link": link, "expires_at": exp})
}

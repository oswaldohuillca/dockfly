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

// Errores de usuarios, permisos y enlaces de invitación.
var (
	errPanelOnly         = newError("panel_only", "This can only be done from the panel, not with an API token")
	errRoleForbidden     = newError("role_forbidden", "Your role doesn't allow this; ask an administrator")
	errInviteLinkInvalid = newError("invite_link_invalid", "The link is invalid or has expired; ask an administrator for a new one")
	errUnknownRole       = newError("unknown_role", "Unknown role: use admin, deployer or viewer")
	errUserEmailTaken    = newError("user_email_taken", "A user with that email already exists")
	errUserNotFound      = newError("user_not_found", "User not found")
	errLastAdmin         = newError("last_admin", "This is the only administrator: make someone else an administrator first")
	errDeleteSelf        = newError("delete_own_account", "You can't delete your own account")
)

// --- Permisos ---

// requiredRole decide qué rol hace falta para una petición. Está todo aquí,
// en un solo sitio, para poder revisarlo (y probarlo) de un vistazo:
//   - viewer: leer, salvo secretos (variables de entorno) y copias de seguridad.
//   - deployer: además, todo lo de sitios, apps, builds y despliegues.
//   - admin: todo lo demás (proxy hosts, certificados, integraciones,
//     notificaciones, ajustes, usuarios…).
func requiredRole(method, path string) string {
	path = strings.TrimSuffix(path, "/")
	// La propia cuenta (2FA, contraseña, tokens) es de cada usuario.
	if strings.HasPrefix(path, "/api/auth/") || path == "/api/tokens" || strings.HasPrefix(path, "/api/tokens/") {
		return store.RoleViewer
	}
	read := method == http.MethodGet || method == http.MethodHead
	// Las variables de entorno y las copias de las bases de datos llevan secretos.
	secret := strings.HasSuffix(path, "/env") || strings.HasSuffix(path, "/download") ||
		path == "/api/backup" || strings.HasPrefix(path, "/api/users")
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

// authorize va después de requireAuth.
func (s *Server) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, t := currentUser(r), currentToken(r)
		// La cuenta (contraseña, 2FA) y los propios tokens solo se gestionan
		// desde el panel: un token filtrado no puede crear otros.
		if t != nil && (strings.HasPrefix(r.URL.Path, "/api/auth/") || strings.HasPrefix(r.URL.Path, "/api/tokens")) {
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

// --- Enlaces de invitación y de cambio de contraseña ---

const (
	inviteTTL   = 7 * 24 * time.Hour
	resetTTL    = 24 * time.Hour
	invitePath  = "/invite/"
	minPassword = 8
)

// linkPurpose ata el enlace a la contraseña actual: en cuanto se usa (y la
// contraseña cambia) deja de valer. Es de un solo uso sin guardar nada.
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

// linkUser valida un enlace de invitación o de cambio de contraseña.
func (s *Server) linkUser(ctx context.Context, token string) (*store.User, error) {
	// "propósito.id.expiración.firma": el propósito depende del usuario, así
	// que primero se busca y después se verifica la firma.
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

// handleAcceptInvite fija la contraseña y abre la sesión.
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
		// Restablecer la contraseña no se salta el segundo factor.
		writeJSON(w, http.StatusOK, map[string]any{
			"two_factor": true, "ticket": s.Signer.IssueTicket(mfaTicketPurpose, u.ID, mfaTicketTTL),
		})
		return
	}
	s.setSession(w, r, u)
	writeJSON(w, http.StatusOK, u)
}

// handleChangePassword cambia la contraseña propia y cierra las demás sesiones.
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

// --- Usuarios (solo administradores) ---

type userView struct {
	*store.User
	// Pending: invitado, todavía sin contraseña.
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

// handleInviteUser crea un usuario sin contraseña y devuelve su enlace de
// invitación, que el administrador comparte como prefiera.
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

// lastAdmin indica si u es el único administrador activo: no se le puede
// quitar el rol ni borrar, o nadie podría administrar Dockfly.
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

// handleUserLink genera otro enlace: de invitación si aún no la aceptó, o
// para restablecer la contraseña si la olvidó.
func (s *Server) handleUserLink(w http.ResponseWriter, r *http.Request) {
	u, ok := s.loadUser(w, r)
	if !ok {
		return
	}
	link, exp := s.passwordLink(r.Context(), r, u)
	writeJSON(w, http.StatusOK, map[string]any{"link": link, "expires_at": exp})
}

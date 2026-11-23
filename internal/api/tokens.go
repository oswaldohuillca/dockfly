package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/dockfly/dockfly/internal/store"
)

// API tokens: "Authorization: Bearer dfy_…". They act on behalf of their
// user with, at most, the role they were given when they were created.

const tokenPrefix = "dfy_"

// API token errors.
var (
	errTokenNameRequired = newError("token_name_required", "Give it a name (e.g. \"GitHub Actions\")")
	errTokenRoleTooHigh  = newError("token_role_too_high", "A token can't have more permissions than your user")
	errTokenExpiry       = newError("token_expiry_out_of_range", "Expiry must be between 0 (never) and {max} days")
	errTokenNotFound     = newError("token_not_found", "Token not found")
)

type tokenCtxKey struct{}

// currentToken returns the token the request authenticated with (nil when
// the panel's session was used).
func currentToken(r *http.Request) *store.APIToken {
	t, _ := r.Context().Value(tokenCtxKey{}).(*store.APIToken)
	return t
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newToken() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return tokenPrefix + hex.EncodeToString(b)
}

// bearerUser authenticates a request with an API token.
func (s *Server) bearerUser(r *http.Request) (*store.User, *store.APIToken, bool) {
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || !strings.HasPrefix(raw, tokenPrefix) {
		return nil, nil, false
	}
	t, err := s.Store.APITokenByHash(r.Context(), hashToken(strings.TrimSpace(raw)))
	if err != nil || t.Expired(time.Now()) {
		return nil, nil, false
	}
	u, err := s.Store.UserByID(r.Context(), t.UserID)
	if err != nil || u.PasswordHash == "" {
		return nil, nil, false
	}
	s.touchToken(r.Context(), t)
	return u, t, true
}

// touchToken records the last use at most once a minute, so that a script
// doesn't write to the database on every single request.
var touched sync.Map // id → time.Time

func (s *Server) touchToken(ctx context.Context, t *store.APIToken) {
	if last, ok := touched.Load(t.ID); ok && time.Since(last.(time.Time)) < time.Minute {
		return
	}
	touched.Store(t.ID, time.Now())
	_ = s.Store.TouchAPIToken(context.WithoutCancel(ctx), t.ID)
}

// effectiveRole is the role the request is served with: the user's or, when
// a token is used, the lower of the token's and the user's.
func effectiveRole(u *store.User, t *store.APIToken) string {
	if t != nil && store.RoleRank(t.Role) < store.RoleRank(u.Role) {
		return t.Role
	}
	return u.Role
}

func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListAPITokens(r.Context(), currentUser(r).ID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
		Role string `json:"role"`
		// Days until it expires; 0 = never.
		ExpiresDays int `json:"expires_days"`
	}
	if !decode(w, r, &in) {
		return
	}
	u := currentUser(r)
	in.Name = strings.TrimSpace(in.Name)
	switch {
	case in.Name == "":
		writeError(w, http.StatusBadRequest, errTokenNameRequired)
		return
	case store.RoleRank(in.Role) == 0:
		writeError(w, http.StatusBadRequest, errUnknownRole)
		return
	case store.RoleRank(in.Role) > store.RoleRank(u.Role):
		writeError(w, http.StatusForbidden, errTokenRoleTooHigh)
		return
	case in.ExpiresDays < 0 || in.ExpiresDays > 3650:
		writeError(w, http.StatusBadRequest, errTokenExpiry.With("max", 3650))
		return
	}
	token := newToken()
	t := &store.APIToken{UserID: u.ID, Name: in.Name, Role: in.Role, TokenHash: hashToken(token), Prefix: token[:len(tokenPrefix)+6]}
	if in.ExpiresDays > 0 {
		exp := time.Now().UTC().Add(time.Duration(in.ExpiresDays) * 24 * time.Hour).Truncate(time.Second)
		t.ExpiresAt = &exp
	}
	if err := s.Store.CreateAPIToken(r.Context(), t); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"token": token, "api_token": t})
}

func (s *Server) handleDeleteToken(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	err := s.Store.DeleteAPIToken(r.Context(), currentUser(r).ID, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, errTokenNotFound)
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	touched.Delete(id)
	w.WriteHeader(http.StatusNoContent)
}

// Package api expone la API REST del panel y sirve el frontend.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/dockfly/dockfly/internal/auth"
	"github.com/dockfly/dockfly/internal/docker"
	"github.com/dockfly/dockfly/internal/store"
	"github.com/dockfly/dockfly/internal/traefik"
)

const sessionCookie = "dockfly_session"

type ctxKey struct{}

type Server struct {
	Store    *store.Store
	Signer   *auth.Signer
	Docker   *docker.Client // nil si Docker no está disponible
	Traefik  traefik.Options
	Super    *traefik.Supervisor // nil si Traefik está desactivado (desarrollo)
	Frontend fs.FS
	Version  string
	Log      *slog.Logger
	// TrustedProxies son los rangos (CIDR) de los que se acepta X-Forwarded-For
	// para obtener la IP del cliente. Vacío = el panel se expone directamente.
	TrustedProxies []string

	applyMu sync.Mutex
}

// Apply regenera la configuración dinámica de Traefik con el estado actual.
//
// Ignora la cancelación de ctx: si el cliente se desconecta justo después de
// guardar un cambio, la base de datos ya está actualizada y Traefik debe
// enterarse igualmente.
func (s *Server) Apply(ctx context.Context) error {
	ctx = context.WithoutCancel(ctx)
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	hosts, err := s.Store.ListProxyHosts(ctx)
	if err != nil {
		return err
	}
	return traefik.WriteDynamic(s.Traefik, hosts)
}

func currentUser(r *http.Request) *store.User {
	u, _ := r.Context().Value(ctxKey{}).(*store.User)
	return u
}

// --- Sesión ---

func (s *Server) setSession(w http.ResponseWriter, r *http.Request, userID int64) {
	token, exp := s.Signer.Issue(userID)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  exp,
		HttpOnly: true,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	n, err := s.Store.CountUsers(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"needs_setup": n == 0})
}

type credentials struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

// handleSetup crea el primer administrador. Solo funciona mientras no haya usuarios.
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if !decode(w, r, &in) {
		return
	}
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	in.Name = strings.TrimSpace(in.Name)
	switch {
	case in.Name == "":
		writeError(w, http.StatusBadRequest, "el nombre es obligatorio")
		return
	case !strings.Contains(in.Email, "@"):
		writeError(w, http.StatusBadRequest, "email inválido")
		return
	case len(in.Password) < 8:
		writeError(w, http.StatusBadRequest, "la contraseña debe tener al menos 8 caracteres")
		return
	}

	// applyMu serializa también el setup para que dos peticiones simultáneas
	// no creen dos administradores.
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	n, err := s.Store.CountUsers(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	if n > 0 {
		writeError(w, http.StatusConflict, "la instalación ya está configurada")
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		s.internalError(w, err)
		return
	}
	u := &store.User{Email: in.Email, Name: in.Name, PasswordHash: hash}
	if err := s.Store.CreateUser(r.Context(), u); err != nil {
		s.internalError(w, err)
		return
	}
	s.setSession(w, r, u.ID)
	writeJSON(w, http.StatusCreated, u)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if !decode(w, r, &in) {
		return
	}
	u, err := s.Store.UserByEmail(r.Context(), strings.ToLower(strings.TrimSpace(in.Email)))
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.internalError(w, err)
		return
	}
	// Si el usuario no existe comparamos igualmente contra un hash ficticio:
	// así la respuesta tarda lo mismo y no revela qué emails están registrados.
	hash := auth.DummyHash()
	if u != nil {
		hash = u.PasswordHash
	}
	if !auth.CheckPassword(hash, in.Password) || u == nil {
		writeError(w, http.StatusUnauthorized, "email o contraseña incorrectos")
		return
	}
	s.setSession(w, r, u.ID)
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
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
		out["traefik"] = map[string]any{"running": false, "last_error": "Traefik desactivado (modo desarrollo)"}
	}
	writeJSON(w, http.StatusOK, out)
}

// --- Utilidades ---

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "JSON inválido: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *Server) internalError(w http.ResponseWriter, err error) {
	s.Log.Error("error interno", "error", err)
	writeError(w, http.StatusInternalServerError, "error interno")
}

// spaHandler sirve los archivos del frontend y devuelve index.html para
// cualquier ruta desconocida (enrutado del lado del cliente).
func spaHandler(fsys fs.FS) http.Handler {
	files := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if _, err := fs.Stat(fsys, p); err == nil {
				if strings.HasPrefix(p, "assets/") {
					// Vite genera nombres con hash: se pueden cachear para siempre.
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

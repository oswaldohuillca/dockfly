package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
)

// Límite de intentos de login y setup por IP, contra ataques de fuerza bruta.
const (
	authAttempts = 10
	authWindow   = time.Minute
)

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(s.clientIP())
	r.Use(s.requestLogger)
	r.Use(s.recoverer)

	r.Route("/api", func(r chi.Router) {
		r.Use(middleware.NoCache)
		r.Use(jsonOnly)
		r.NotFound(func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusNotFound, "ruta no encontrada")
		})
		r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusMethodNotAllowed, "método no permitido")
		})

		// Rutas públicas.
		authLimit := httprate.LimitBy(authAttempts, authWindow, keyByClientIP,
			httprate.WithLimitHandler(func(w http.ResponseWriter, r *http.Request) {
				writeError(w, http.StatusTooManyRequests, "demasiados intentos, espera un minuto")
			}))
		r.Get("/setup", s.handleSetupStatus)
		r.With(authLimit).Post("/setup", s.handleSetup)
		r.With(authLimit).Post("/auth/login", s.handleLogin)
		r.Post("/auth/logout", s.handleLogout)

		// Rutas que requieren sesión.
		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)

			r.Get("/auth/me", s.handleMe)
			r.Get("/status", s.handleStatus)

			r.Route("/proxy-hosts", func(r chi.Router) {
				r.Get("/", s.handleListProxyHosts)
				r.Post("/", s.handleCreateProxyHost)
				r.Put("/{id}", s.handleUpdateProxyHost)
				r.Delete("/{id}", s.handleDeleteProxyHost)
			})

			r.Get("/docker/containers", s.handleListContainers)
		})
	})

	r.Handle("/*", spaHandler(s.Frontend))
	return r
}

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
		// Webhooks de GitHub/GitLab: sin sesión, autenticados por su firma.
		r.Post("/hooks/{provider}/{id}", s.handleWebhook)

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

			r.Route("/sites", func(r chi.Router) {
				r.Get("/", s.handleListSites)
				r.Post("/", s.handleCreateSite)
				r.Route("/{id}", func(r chi.Router) {
					r.Get("/", s.handleGetSite)
					r.Put("/", s.handleUpdateSite)
					r.Delete("/", s.handleDeleteSite)
					r.Post("/deployments", s.handleUploadDeployment)
					r.Post("/deployments/{deploymentID}/activate", s.handleActivateDeployment)
					r.Delete("/deployments/{deploymentID}", s.handleDeleteDeployment)

					r.Put("/git", s.handlePutSiteGit)
					r.Delete("/git", s.handleDeleteSiteGit)
					r.Get("/git/env", s.handleGetSiteEnv)
					r.Get("/builds", s.handleListBuilds)
					r.Post("/builds", s.handleTriggerBuild)
				})
			})

			r.Route("/builds/{buildID}", func(r chi.Router) {
				r.Get("/", s.handleGetBuild)
				r.Get("/logs", s.handleBuildLogs)
				r.Post("/cancel", s.handleCancelBuild)
			})

			r.Route("/git/connections", func(r chi.Router) {
				r.Get("/", s.handleListConnections)
				r.Post("/", s.handleCreateConnection)
				r.Delete("/{id}", s.handleDeleteConnection)
				r.Get("/{id}/repos", s.handleListRepos)
				r.Get("/{id}/branches", s.handleListBranches)
			})

			r.Get("/settings", s.handleGetSettings)
			r.Put("/settings", s.handlePutSettings)

			r.Get("/docker/containers", s.handleListContainers)
		})
	})

	r.Handle("/*", spaHandler(s.Frontend))
	return r
}

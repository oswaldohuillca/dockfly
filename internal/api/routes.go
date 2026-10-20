package api

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"

	"github.com/dockfly/dockfly/internal/store"
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
		// Sin sesión solo en una instalación nueva (lo comprueba el handler).
		r.Post("/backup/restore", s.handleRestoreBackup)
		// Webhooks de GitHub/GitLab: sin sesión, autenticados por su firma.
		r.Post("/hooks/github-app", s.handleGitHubAppWebhook)
		r.Post("/hooks/{provider}/apps/{id}", s.handleAppWebhook)
		r.Post("/hooks/{provider}/{id}", s.handleWebhook)
		// Retornos de GitHub/GitLab: los autentica el parámetro state.
		r.Get("/git/github/manifest", s.handleGitHubManifestCallback)
		r.Get("/git/github/setup", s.handleGitHubSetup)
		r.Get("/git/gitlab/callback", s.handleGitLabCallback)

		// Rutas que requieren sesión.
		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)

			r.Get("/auth/me", s.handleMe)
			r.Get("/status", s.handleStatus)
			r.Get("/certificates", s.handleCertificates)

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
					r.Delete("/cache", s.handleClearBuildCache)
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

			r.Get("/git/apps", s.handleListGitApps)
			r.Post("/git/github/manifest", s.handleGitHubManifestStart)
			r.Post("/git/github/apps/{id}/install-url", s.handleGitHubInstallURL)
			r.Delete("/git/github/apps/{id}", s.handleDeleteGitHubApp)
			r.Put("/git/gitlab/apps", s.handlePutGitLabApp)
			r.Delete("/git/gitlab/apps/{id}", s.handleDeleteGitLabApp)
			r.Post("/git/gitlab/authorize", s.handleGitLabAuthorize)

			r.Get("/backup", s.handleDownloadBackup)

			r.Get("/dns-providers/catalog", s.handleDNSCatalog)
			r.Route("/dns-providers", func(r chi.Router) {
				r.Get("/", s.handleListDNSProviders)
				r.Put("/", s.handleSaveDNSProvider)
				r.Delete("/{id}", s.handleDeleteDNSProvider)
			})
			r.Route("/custom-certificates", func(r chi.Router) {
				r.Get("/", s.handleListCustomCerts)
				r.Post("/", s.handleUploadCustomCert)
				r.Delete("/{id}", s.handleDeleteCustomCert)
			})

			r.Route("/streams", func(r chi.Router) {
				r.Get("/", s.handleListStreams)
				r.Post("/", s.handleCreateStream)
				r.Put("/{id}", s.handleUpdateStream)
				r.Delete("/{id}", s.handleDeleteStream)
			})

			r.Route("/redirects", func(r chi.Router) {
				r.Get("/", s.handleListRedirects)
				r.Post("/", s.handleCreateRedirect)
				r.Put("/{id}", s.handleUpdateRedirect)
				r.Delete("/{id}", s.handleDeleteRedirect)
			})

			r.Route("/access-lists", func(r chi.Router) {
				r.Get("/", s.handleListAccessLists)
				r.Post("/", s.handleCreateAccessList)
				r.Put("/{id}", s.handleUpdateAccessList)
				r.Delete("/{id}", s.handleDeleteAccessList)
			})

			r.Route("/notifications", func(r chi.Router) {
				r.Get("/", s.handleListChannels)
				r.Post("/", s.handleCreateChannel)
				r.Put("/{id}", s.handleUpdateChannel)
				r.Delete("/{id}", s.handleDeleteChannel)
				r.Post("/{id}/test", s.handleTestChannel)
			})

			r.Get("/settings", s.handleGetSettings)
			r.Put("/settings", s.handlePutSettings)

			r.Route("/apps", func(r chi.Router) {
				r.Get("/", s.handleListApps)
				r.Post("/", s.handleCreateApp)
				r.Route("/{id}", func(r chi.Router) {
					r.Get("/", s.handleGetApp)
					r.Put("/", s.handleUpdateApp)
					r.Delete("/", s.handleDeleteApp)
					r.Get("/env", s.handleGetAppEnv)
					r.Get("/logs", s.handleAppLogs)
					r.Post("/deploy", s.handleDeployApp)
					r.Post("/stop", s.appAction(func(ctx context.Context, a *store.App) error { return s.Apps.Stop(ctx, a) }))
					r.Post("/start", s.appAction(func(ctx context.Context, a *store.App) error { return s.Apps.Start(ctx, a) }))
					r.Post("/restart", s.appAction(func(ctx context.Context, a *store.App) error { return s.Apps.Restart(ctx, a) }))
					r.Post("/deployments/{deploymentID}/redeploy", s.handleRedeploy)
				})
			})
			r.Get("/app-deployments/{deploymentID}/logs", s.handleAppDeploymentLogs)
			r.Post("/app-deployments/{deploymentID}/cancel", s.handleCancelAppDeployment)

			r.Get("/docker/containers", s.handleListContainers)
		})
	})

	r.Handle("/*", spaHandler(s.Frontend))
	return r
}

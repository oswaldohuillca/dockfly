// Package api expone la API REST del panel y sirve el frontend.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/dockfly/dockfly/internal/apps"
	"github.com/dockfly/dockfly/internal/auth"
	"github.com/dockfly/dockfly/internal/builder"
	"github.com/dockfly/dockfly/internal/docker"
	"github.com/dockfly/dockfly/internal/gitauth"
	"github.com/dockfly/dockfly/internal/notify"
	"github.com/dockfly/dockfly/internal/pages"
	"github.com/dockfly/dockfly/internal/secrets"
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
	Certs    *traefik.CertTracker
	Notify   *notify.Dispatcher // nil en algunos tests
	Frontend fs.FS
	Version  string
	Log      *slog.Logger
	// TrustedProxies son los rangos (CIDR) de los que se acepta X-Forwarded-For
	// para obtener la IP del cliente. Vacío = el panel se expone directamente.
	TrustedProxies []string

	// Sitios estáticos.
	Pages        *pages.Server  // sirve los archivos publicados
	PagesStorage *pages.Storage // despliegues en disco
	StaticURL    string         // URL interna del servidor estático, para Traefik
	MaxUploadMB  int64

	// Builds desde GitHub/GitLab.
	Builds  *builder.Manager
	Secrets *secrets.Box
	Git     *gitauth.Resolver
	Apps    *apps.Manager

	oauthStates stateStore

	DataDir    string
	ListenAddr string // dirección del panel (para no reutilizar su puerto en un stream)
	// Restart detiene Dockfly para que Docker lo vuelva a arrancar (lo usa la
	// restauración de copias, que se aplica al arrancar).
	Restart func()

	applyMu sync.Mutex
	mfa     mfaFailures
}

// Apply publica el estado actual: regenera la configuración de Traefik y la
// tabla de dominios del servidor estático.
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
	sites, err := s.Store.ListSites(ctx)
	if err != nil {
		return err
	}

	extra := map[string]any{}
	acl, err := s.accessMiddlewares(ctx, extra)
	if err != nil {
		return err
	}
	plan, err := s.loadCertPlan(ctx)
	if err != nil {
		return err
	}
	withACL := func(rt traefik.Route, id *int64) traefik.Route {
		if id != nil {
			rt.Middlewares = acl[*id]
		}
		return rt
	}

	var routes []traefik.Route
	for _, h := range hosts {
		if rt, ok := traefik.ProxyHostRoute(h); ok {
			routes = append(routes, withACL(rt, h.AccessListID))
		}
	}
	// Apps desplegadas, en marcha y con puerto y dominios: Traefik envía el
	// tráfico a su contenedor actual (cambia en cada despliegue).
	appList, err := s.Store.ListApps(ctx)
	if err != nil {
		return err
	}
	for _, a := range appList {
		if a.Stopped || a.Port == 0 || len(a.Domains) == 0 || a.Current == nil || a.Current.Container == "" {
			continue
		}
		routes = append(routes, withACL(traefik.Route{
			Name: "app-" + strconv.FormatInt(a.ID, 10), Domains: a.Domains,
			URL:          fmt.Sprintf("http://%s:%d", a.Current.Container, a.Port),
			PreserveHost: true, SSL: a.SSL, ForceHTTPS: a.ForceHTTPS, HSTS: a.HSTS,
		}, a.AccessListID))
	}

	redirects, err := s.Store.ListRedirects(ctx)
	if err != nil {
		return err
	}
	for _, rd := range redirects {
		if !rd.Enabled {
			continue
		}
		routes = append(routes, traefik.Route{
			Name: redirectRouteName(rd.ID), Domains: rd.Domains, SSL: rd.SSL,
			Redirect: &traefik.Redirect{Target: rd.Target, Permanent: rd.Permanent, PreservePath: rd.PreservePath},
		})
	}

	table := map[string]pages.Route{}
	for _, site := range sites {
		if rt, ok := traefik.SiteRoute(site, s.StaticURL); ok {
			routes = append(routes, withACL(rt, site.AccessListID))
		}
		var pr pages.Route
		if id := site.ActiveDeploymentID; id != nil {
			pr = pages.Route{Dir: s.PagesStorage.DeploymentDir(site.ID, *id), DeploymentID: *id, SPA: site.SPA}
		}
		for _, d := range site.Domains {
			table[d] = pr
		}
	}
	if rt, ok, err := s.previewRoutes(ctx, sites, table, acl); err != nil {
		return err
	} else if ok {
		routes = append(routes, rt)
	}

	// Primero el servidor estático y luego Traefik: así, cuando Traefik
	// empiece a enrutar un dominio nuevo, el servidor ya sabe qué servir.
	s.Pages.SetRoutes(table)
	streamList, err := s.Store.ListStreams(ctx)
	if err != nil {
		return err
	}
	var streams []traefik.Stream
	for _, st := range streamList {
		if st.Enabled {
			streams = append(streams, traefik.Stream{
				Name:    "st-" + strconv.FormatInt(st.ID, 10),
				Port:    traefik.StreamPort{Protocol: st.Protocol, Port: st.ListenPort},
				Address: net.JoinHostPort(st.ForwardHost, strconv.Itoa(st.ForwardPort)),
			})
		}
	}
	// Cada ruta con SSL usa el certificado que le corresponde: propio, por
	// DNS (zonas configuradas) o por HTTP.
	for i := range routes {
		if routes[i].SSL {
			routes[i].Resolver = plan.resolver(routes[i].Domains)
		}
	}
	var certFiles []traefik.CertFile
	for _, c := range plan.custom {
		base := filepath.Join(s.certDir(), strconv.FormatInt(c.ID, 10))
		certFiles = append(certFiles, traefik.CertFile{Cert: base + ".crt", Key: base + ".key"})
	}
	return traefik.WriteDynamic(s.Traefik, traefik.Dynamic{Routes: routes, Middlewares: extra, Streams: streams, Certificates: certFiles})
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
	if u.TOTPEnabled {
		writeJSON(w, http.StatusOK, map[string]any{
			"two_factor": true, "ticket": s.Signer.IssueTicket(mfaTicketPurpose, u.ID, mfaTicketTTL),
		})
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

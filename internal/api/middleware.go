package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"

	"github.com/dockfly/dockfly/internal/store"
)

// clientIP resuelve la IP real del cliente. Solo se confía en X-Forwarded-For
// si la petición llega desde un proxy declarado: de lo contrario, cualquiera
// podría falsificar la cabecera y saltarse el límite de intentos.
func (s *Server) clientIP() func(http.Handler) http.Handler {
	if len(s.TrustedProxies) > 0 {
		return middleware.ClientIPFromXFF(s.TrustedProxies...)
	}
	return middleware.ClientIPFromRemoteAddr
}

// keyByClientIP agrupa IPv6 por /64 para que un atacante no pueda rotar
// direcciones dentro de su propio rango.
func keyByClientIP(r *http.Request) (string, error) {
	return httprate.CanonicalizeIP(middleware.GetClientIP(r.Context())), nil
}

// requireAuth acepta la cookie de sesión del panel o un token de API.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			u, t, ok := s.bearerUser(r)
			if !ok {
				writeError(w, http.StatusUnauthorized, errBadAPIToken)
				return
			}
			ctx := context.WithValue(r.Context(), ctxKey{}, u)
			next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, tokenCtxKey{}, t)))
			return
		}
		u, ok := s.sessionUser(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, errUnauthenticated)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, u)))
	})
}

// sessionUser devuelve el usuario de la cookie de sesión, si es válida.
func (s *Server) sessionUser(r *http.Request) (*store.User, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil, false
	}
	id, version, err := s.Signer.Verify(c.Value)
	if err != nil {
		return nil, false
	}
	u, err := s.Store.UserByID(r.Context(), id)
	if err != nil || u.SessionVersion != version || u.PasswordHash == "" {
		return nil, false
	}
	return u, true
}

// crossOrigin rechaza las peticiones de navegador que no son del mismo origen
// (incluidas las de otro subdominio del mismo sitio, que SameSite=Strict no
// frena). Deja pasar las que no son de navegador (CLI con token, webhooks,
// agentes), que no llevan Sec-Fetch-Site ni Origin.
func (s *Server) crossOrigin(next http.Handler) http.Handler {
	s.csrfOnce.Do(func() { s.csrf = http.NewCrossOriginProtection() })
	return s.csrf.Handler(next)
}

// securityHeaders protege el panel: evita que se enmarque (clickjacking), no
// deja adivinar el tipo de contenido y limita de dónde carga recursos.
func securityHeaders(next http.Handler) http.Handler {
	const csp = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data:; font-src 'self'; connect-src 'self'; " +
		"frame-ancestors 'none'; base-uri 'none'; object-src 'none'"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", csp)
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// jsonOnly exige Content-Type JSON en peticiones que modifican estado. Junto
// con SameSite=Strict y crossOrigin, bloquea CSRF desde formularios HTML.
// La única excepción son las subidas de despliegues, que llevan una cabecera
// propia que un formulario no puede añadir.
func jsonOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if r.ContentLength != 0 && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") &&
				!isMultipartUpload(r) {
				writeError(w, http.StatusUnsupportedMediaType, errJSONRequired)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// recoverer convierte un panic en un 500 JSON en lugar de cortar la conexión.
func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				s.Log.Error("panic en handler", "panic", v, "path", r.URL.Path,
					"request_id", middleware.GetReqID(r.Context()))
				writeError(w, http.StatusInternalServerError, errInternal)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// requestLogger registra las peticiones a la API con slog. Los archivos
// estáticos del frontend se omiten para no llenar el log.
func (s *Server) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		start := time.Now()
		next.ServeHTTP(ww, r)

		level := s.Log.Info
		switch {
		case ww.Status() >= 500:
			level = s.Log.Error
		case r.Method == http.MethodGet && ww.Status() < 400:
			level = s.Log.Debug // las consultas periódicas del panel no aportan
		}
		level("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.Status(),
			"duration", time.Since(start).Round(time.Microsecond),
			"ip", middleware.GetClientIP(r.Context()),
			"request_id", middleware.GetReqID(r.Context()),
		)
	})
}

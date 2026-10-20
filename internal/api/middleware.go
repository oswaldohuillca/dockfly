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

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := s.sessionUser(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "no autenticado o sesión expirada")
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
	id, err := s.Signer.Verify(c.Value)
	if err != nil {
		return nil, false
	}
	u, err := s.Store.UserByID(r.Context(), id)
	return u, err == nil
}

// jsonOnly exige Content-Type JSON en peticiones que modifican estado. Junto
// con la cookie SameSite=Strict, esto bloquea CSRF desde formularios HTML.
// La única excepción son las subidas de despliegues, que llevan una cabecera
// propia que un formulario no puede añadir.
func jsonOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if r.ContentLength != 0 && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") &&
				!isMultipartUpload(r) {
				writeError(w, http.StatusUnsupportedMediaType, "se requiere application/json")
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
				writeError(w, http.StatusInternalServerError, "error interno")
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

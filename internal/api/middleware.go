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

// clientIP resolves the client's real IP. X-Forwarded-For is only trusted when
// the request comes from a declared proxy: otherwise anyone could forge the
// header and slip past the attempt limit.
func (s *Server) clientIP() func(http.Handler) http.Handler {
	if len(s.TrustedProxies) > 0 {
		return middleware.ClientIPFromXFF(s.TrustedProxies...)
	}
	return middleware.ClientIPFromRemoteAddr
}

// keyByClientIP groups IPv6 addresses by /64 so an attacker cannot rotate
// addresses within their own range.
func keyByClientIP(r *http.Request) (string, error) {
	return httprate.CanonicalizeIP(middleware.GetClientIP(r.Context())), nil
}

// requireAuth accepts either the panel's session cookie or an API token.
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

// sessionUser returns the user from the session cookie, if it is valid.
func (s *Server) sessionUser(r *http.Request) (*store.User, bool) {
	c, err := r.Cookie(s.sessionCookieName())
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

// crossOrigin rejects browser requests that do not come from the same origin
// (including those from another subdomain of the same site, which
// SameSite=Strict does not stop). Non-browser requests (CLI with a token,
// webhooks, agents) carry neither Sec-Fetch-Site nor Origin, so they pass.
func (s *Server) crossOrigin(next http.Handler) http.Handler {
	s.csrfOnce.Do(func() { s.csrf = http.NewCrossOriginProtection() })
	return s.csrf.Handler(next)
}

// securityHeaders protects the panel: it keeps the page from being framed
// (clickjacking), stops content-type sniffing and limits where resources may
// be loaded from.
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

// jsonOnly requires a JSON Content-Type on state-changing requests. Together
// with SameSite=Strict and crossOrigin, it blocks CSRF from HTML forms.
// The only exception is deployment uploads, which carry a header of their own
// that a form cannot add.
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

// recoverer turns a panic into a JSON 500 instead of dropping the connection.
func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				s.Log.Error("panic in handler", "panic", v, "path", r.URL.Path,
					"request_id", middleware.GetReqID(r.Context()))
				writeError(w, http.StatusInternalServerError, errInternal)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// requestLogger logs API requests with slog. The frontend's static files are
// skipped so they do not flood the log.
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
			level = s.Log.Debug // the panel's periodic polling adds nothing
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

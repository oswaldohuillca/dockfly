package pages

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path"
	"strings"
	"sync/atomic"
)

// Route tells which deployment serves a domain.
type Route struct {
	Dir          string // empty if the site has no deployments yet
	DeploymentID int64
	SPA          bool
}

// Server serves the static sites. Traefik forwards it the traffic of each
// site's domains; the site is picked from the Host header.
type Server struct {
	routes atomic.Pointer[map[string]Route]
}

// SetRoutes replaces the domain table atomically: in-flight requests finish
// with the previous table and new ones use the new one, so publishing or
// rolling back never cuts off a request.
func (s *Server) SetRoutes(routes map[string]Route) {
	s.routes.Store(&routes)
}

func (s *Server) lookup(host string) (Route, bool) {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	table := s.routes.Load()
	if table == nil {
		return Route{}, false
	}
	host = strings.ToLower(host)
	if r, ok := (*table)[host]; ok {
		return r, true
	}
	// A wildcard domain (*.example.com) covers a single level, as in Traefik
	// and in the certificates; another site's exact domain comes first.
	if _, parent, ok := strings.Cut(host, "."); ok && strings.Contains(parent, ".") {
		r, ok := (*table)["*."+parent]
		return r, ok
	}
	return Route{}, false
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	route, ok := s.lookup(r.Host)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if route.Dir == "" {
		placeholder(w)
		return
	}
	root, err := os.OpenRoot(route.Dir)
	if err != nil {
		// The deployment may have been deleted right after reading the table.
		http.Error(w, "deployment unavailable", http.StatusServiceUnavailable)
		return
	}
	defer root.Close()

	w.Header().Set("X-Content-Type-Options", "nosniff")
	rules := loadRules(route.Dir)
	urlPath := path.Clean("/" + r.URL.Path)

	// First, whether the path is a real file (without the SPA mode fallback).
	name, status := resolve(root, urlPath, false)
	target := urlPath
	if to, code, ok := rules.Match(urlPath); ok {
		if code != http.StatusOK {
			// Redirects are applied before files.
			if !strings.Contains(to, "?") && r.URL.RawQuery != "" {
				to += "?" + r.URL.RawQuery
			}
			rules.ApplyHeaders(w.Header(), urlPath)
			http.Redirect(w, r, to, code)
			return
		}
		// A rewrite only kicks in if the path is not already a file.
		target = to
	}
	if status != http.StatusOK {
		name, status = resolve(root, target, route.SPA)
	}
	if name == "" {
		rules.ApplyHeaders(w.Header(), urlPath)
		http.NotFound(w, r)
		return
	}
	serveFile(w, r, root, name, status, route.DeploymentID, func(h http.Header) { rules.ApplyHeaders(h, urlPath) })
}

// resolve maps a URL path to the file to serve, with the same convention as
// Cloudflare Pages:
//
//	/            → index.html
//	/about       → about, about.html or about/index.html
//	nonexistent  → index.html (SPA mode), 404.html or an empty 404
func resolve(root *os.Root, urlPath string, spa bool) (name string, status int) {
	p := strings.TrimPrefix(path.Clean("/"+urlPath), "/")
	if hidden(p) {
		return notFoundFile(root, spa)
	}

	var candidates []string
	if p == "" {
		candidates = []string{"index.html"}
	} else {
		candidates = []string{p, p + ".html", p + "/index.html"}
	}
	for _, c := range candidates {
		if isFile(root, c) {
			return c, http.StatusOK
		}
	}
	return notFoundFile(root, spa)
}

func notFoundFile(root *os.Root, spa bool) (string, int) {
	if spa && isFile(root, "index.html") {
		return "index.html", http.StatusOK
	}
	if isFile(root, "404.html") {
		return "404.html", http.StatusNotFound
	}
	return "", http.StatusNotFound
}

// hidden hides the files starting with a dot (.env, .htaccess…), except
// .well-known (domain verifications), and the site's configuration files
// (_redirects, _headers).
func hidden(p string) bool {
	if p == redirectsFile || p == headersFile {
		return true
	}
	for seg := range strings.SplitSeq(p, "/") {
		if strings.HasPrefix(seg, ".") && seg != ".well-known" {
			return true
		}
	}
	return false
}

func isFile(root *os.Root, name string) bool {
	fi, err := root.Stat(name)
	return err == nil && fi.Mode().IsRegular()
}

// serveFile serves name. extra applies the headers from _headers, after
// Dockfly's own so the site can override them (e.g. Cache-Control).
func serveFile(w http.ResponseWriter, r *http.Request, root *os.Root, name string, status int, deploymentID int64, extra func(http.Header)) {
	f, err := root.Open(name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			http.NotFound(w, r)
		} else {
			http.Error(w, "error reading the file", http.StatusInternalServerError)
		}
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		http.Error(w, "error reading the file", http.StatusInternalServerError)
		return
	}

	// As in Pages: the browser may store the files, but it must revalidate
	// them. The ETag includes the deployment, so after publishing or rolling
	// back the browser downloads the right version.
	w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
	w.Header().Set("ETag", fmt.Sprintf(`"%d-%x-%x"`, deploymentID, fi.Size(), fi.ModTime().UnixNano()))
	extra(w.Header())

	if status != http.StatusOK {
		// ServeContent always answers 200; for 404.html we write it by hand.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		if r.Method != http.MethodHead {
			_, _ = f.WriteTo(w)
		}
		return
	}
	http.ServeContent(w, r, name, fi.ModTime(), f)
}

func placeholder(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNotFound)
	fmt.Fprint(w, `<!doctype html><meta charset="utf-8"><title>Site not deployed</title>
<style>body{font-family:system-ui,sans-serif;display:grid;place-items:center;min-height:100vh;margin:0;color:#555}</style>
<div><h1>This site has no deployments yet</h1><p>Upload the files from the Dockfly panel.</p></div>`)
}

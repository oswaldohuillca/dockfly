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

// Route indica qué despliegue sirve un dominio.
type Route struct {
	Dir          string // vacío si el sitio aún no tiene despliegues
	DeploymentID int64
	SPA          bool
}

// Server sirve los sitios estáticos. Traefik le reenvía el tráfico de los
// dominios de cada sitio; el sitio se elige por la cabecera Host.
type Server struct {
	routes atomic.Pointer[map[string]Route]
}

// SetRoutes reemplaza la tabla de dominios de forma atómica: las peticiones
// en curso terminan con la tabla anterior y las nuevas usan la nueva, así
// que publicar o hacer rollback no corta ninguna petición.
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
	r, ok := (*table)[strings.ToLower(host)]
	return r, ok
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
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
		// El despliegue pudo borrarse justo después de leer la tabla.
		http.Error(w, "despliegue no disponible", http.StatusServiceUnavailable)
		return
	}
	defer root.Close()

	w.Header().Set("X-Content-Type-Options", "nosniff")
	name, status := resolve(root, r.URL.Path, route.SPA)
	if name == "" {
		http.NotFound(w, r)
		return
	}
	serveFile(w, r, root, name, status, route.DeploymentID)
}

// resolve traduce una ruta URL al archivo que hay que servir, con la misma
// convención que Cloudflare Pages:
//
//	/            → index.html
//	/about       → about, about.html o about/index.html
//	inexistente  → index.html (modo SPA), 404.html o 404 vacío
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

// hidden oculta los archivos que empiezan por punto (.env, .htaccess…),
// salvo .well-known, que se usa para verificaciones de dominio.
func hidden(p string) bool {
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

func serveFile(w http.ResponseWriter, r *http.Request, root *os.Root, name string, status int, deploymentID int64) {
	f, err := root.Open(name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			http.NotFound(w, r)
		} else {
			http.Error(w, "error leyendo el archivo", http.StatusInternalServerError)
		}
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		http.Error(w, "error leyendo el archivo", http.StatusInternalServerError)
		return
	}

	// Como en Pages: el navegador puede guardar los archivos, pero debe
	// revalidarlos. El ETag incluye el despliegue, así que tras publicar o
	// hacer rollback el navegador descarga la versión correcta.
	w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
	w.Header().Set("ETag", fmt.Sprintf(`"%d-%x-%x"`, deploymentID, fi.Size(), fi.ModTime().UnixNano()))

	if status != http.StatusOK {
		// ServeContent siempre responde 200; para 404.html escribimos a mano.
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
	fmt.Fprint(w, `<!doctype html><meta charset="utf-8"><title>Sitio sin desplegar</title>
<style>body{font-family:system-ui,sans-serif;display:grid;place-items:center;min-height:100vh;margin:0;color:#555}</style>
<div><h1>Este sitio todavía no tiene despliegues</h1><p>Sube los archivos desde el panel de Dockfly.</p></div>`)
}

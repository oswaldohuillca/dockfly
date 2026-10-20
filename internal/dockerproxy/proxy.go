// Package dockerproxy es un proxy de la API de Docker con permisos mínimos.
//
// Lo usa el contenedor auxiliar de Compose: en vez de recibir el socket de
// Docker del host (con el que cualquier docker-compose.yml podría hacerse
// root en el servidor con un contenedor privilegiado), habla con Dockfly
// por una red interna propia de cada ejecución, en la que solo están él y
// Dockfly. El proxy solo deja pasar lo que Compose necesita:
//   - nada de contenedores privilegiados, dispositivos, namespaces o rutas
//     sensibles del host;
//   - cada ejecución solo ve los contenedores y volúmenes de su proyecto de
//     Compose: ni los del propio Dockfly (sus datos y claves) ni los de otras apps.
package dockerproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"
)

// ProjectLabel es la etiqueta con la que Compose marca lo que crea.
const ProjectLabel = "com.docker.compose.project"

type Proxy struct {
	// Dial abre una conexión con el Docker real.
	Dial func(ctx context.Context) (net.Conn, error)
	Log  *slog.Logger
	// Protected son rutas del host que nunca se pueden montar (los datos del
	// propio Dockfly).
	Protected []string

	rp   *httputil.ReverseProxy
	api  *http.Client
	once sync.Once
}

func (p *Proxy) proxy() *httputil.ReverseProxy {
	p.once.Do(func() {
		transport := &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return p.Dial(ctx) },
		}
		p.api = &http.Client{Transport: transport}
		p.rp = &httputil.ReverseProxy{
			Rewrite: func(r *httputil.ProxyRequest) {
				r.SetURL(&url.URL{Scheme: "http", Host: "docker"})
			},
			Transport: transport,
			// Logs, pulls y builds son flujos: se reenvían al momento.
			FlushInterval: -1,
		}
	})
	return p.rp
}

// Handler atiende al auxiliar de un proyecto de Compose. Quien llega a él
// ya está autorizado (escucha en la red interna de esa ejecución): solo
// queda aplicar la política.
func (p *Proxy) Handler(project string) http.Handler {
	rp := p.proxy()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := p.check(r, project); err != nil {
			p.Log.Warn("proxy de Docker: petición rechazada", "proyecto", project, "método", r.Method, "ruta", r.URL.Path, "motivo", err)
			deny(w, http.StatusForbidden, "Dockfly: "+err.Error())
			return
		}
		rp.ServeHTTP(w, r)
	})
}

// deny responde como lo haría Docker, para que la CLI muestre el mensaje.
func deny(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": msg})
}

var versionPrefix = regexp.MustCompile(`^/v[0-9]+\.[0-9]+`)

// cleanRoute quita la versión de la API ("/v1.47") y normaliza la ruta.
func cleanRoute(p string) string {
	return path.Clean("/" + versionPrefix.ReplaceAllString(p, ""))
}

// allowed son las partes de la API que usa Compose. Quedan fuera swarm,
// servicios, secretos, plugins, el sistema…
var allowed = []string{
	"/_ping", "/version", "/info", "/events",
	"/containers", "/images", "/networks", "/volumes", "/exec",
	"/build", "/session", "/grpc", "/distribution", "/auth",
}

// containerOps son las rutas /containers/… que no se refieren a un
// contenedor concreto.
var containerOps = map[string]bool{"create": true, "json": true}

// check aplica la política a una petición del proyecto. Para crear
// contenedores lee (y restaura) el cuerpo.
func (p *Proxy) check(r *http.Request, project string) error {
	// La ruta que se comprueba debe ser exactamente la que verá Docker.
	if r.URL.RawPath != "" || path.Clean(r.URL.Path) != r.URL.Path {
		return fmt.Errorf("ruta no canónica: %s", r.URL.EscapedPath())
	}
	route := cleanRoute(r.URL.Path)
	if err := checkRoute(r, route); err != nil {
		return err
	}
	if strings.HasSuffix(route, "/prune") {
		return fmt.Errorf("%s no está permitido: borraría recursos de otras apps", route)
	}
	read := r.Method == http.MethodGet || r.Method == http.MethodHead
	// Lo de cada proyecto: Compose nombra sus recursos "<proyecto>_…".
	owned := func(name string) bool { return strings.HasPrefix(name, project+"_") }
	switch rest, _ := strings.CutPrefix(route, "/"); {
	// Las operaciones sobre un contenedor solo valen para los del proyecto.
	case strings.HasPrefix(rest, "containers/"):
		id, _, _ := strings.Cut(strings.TrimPrefix(rest, "containers/"), "/")
		if !containerOps[id] {
			if err := p.ownContainer(r.Context(), id, project); err != nil {
				return err
			}
		}
		if r.Method == http.MethodPost && route == "/containers/create" {
			body, err := readBody(r)
			if err != nil {
				return err
			}
			return checkCreate(body, project, p.Protected)
		}
	case strings.HasPrefix(rest, "volumes/") && !read || route == "/volumes/create":
		name := strings.TrimPrefix(route, "/volumes/")
		if route == "/volumes/create" {
			body, err := readBody(r)
			if err != nil {
				return err
			}
			var v struct{ Name string }
			_ = json.Unmarshal(body, &v)
			name = v.Name
		}
		if name != "" && !owned(name) {
			return fmt.Errorf("el volumen %s no es de este proyecto", name)
		}
	case strings.HasPrefix(rest, "networks/") && !read:
		id, action, _ := strings.Cut(strings.TrimPrefix(rest, "networks/"), "/")
		switch {
		case id == "create":
			body, err := readBody(r)
			if err != nil {
				return err
			}
			var n struct{ Name string }
			_ = json.Unmarshal(body, &n)
			if !owned(n.Name) {
				return fmt.Errorf("la red %s no es de este proyecto", n.Name)
			}
		case action == "connect" || action == "disconnect":
			// Solo se conectan (o desconectan) contenedores del proyecto.
			body, err := readBody(r)
			if err != nil {
				return err
			}
			var c struct{ Container string }
			_ = json.Unmarshal(body, &c)
			if err := p.ownContainer(r.Context(), c.Container, project); err != nil {
				return err
			}
		case action == "" && r.Method == http.MethodDelete:
			// Compose las borra por ID: se pregunta a Docker de quién es.
			name, labels, err := p.networkInfo(r.Context(), id)
			if err != nil {
				return err
			}
			if labels != nil && labels[ProjectLabel] != project && !owned(name) {
				return fmt.Errorf("la red %s no es de este proyecto", name)
			}
		default:
			return fmt.Errorf("%s %s no está permitido", r.Method, route)
		}
	}
	return nil
}

// readBody lee el cuerpo (y lo deja listo para reenviarlo).
func readBody(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	return body, nil
}

// networkInfo devuelve el nombre y las etiquetas de una red (labels nil si
// no existe: Docker responderá él mismo con su 404).
func (p *Proxy) networkInfo(ctx context.Context, id string) (string, map[string]string, error) {
	var info struct {
		Name   string
		Labels map[string]string
	}
	found, err := p.inspect(ctx, "/networks/"+url.PathEscape(id), &info)
	if err != nil || !found {
		return "", nil, err
	}
	if info.Labels == nil {
		info.Labels = map[string]string{}
	}
	return info.Name, info.Labels, nil
}

// inspect hace un GET al Docker real. found=false si responde 404.
func (p *Proxy) inspect(ctx context.Context, path string, out any) (found bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker"+path, nil)
	if err != nil {
		return false, err
	}
	resp, err := p.api.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("Docker respondió %d al consultar %s", resp.StatusCode, path)
	}
	return true, json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
}

// ownContainer comprueba que un contenedor sea del proyecto.
func (p *Proxy) ownContainer(ctx context.Context, id, project string) error {
	labels, err := p.containerLabels(ctx, id)
	if err != nil {
		return err
	}
	if labels != nil && labels[ProjectLabel] != project {
		return fmt.Errorf("el contenedor %s no es de este proyecto", id)
	}
	return nil
}

// containerLabels pregunta al Docker real de quién es un contenedor (nil si
// no existe: Docker responderá él mismo con su 404).
func (p *Proxy) containerLabels(ctx context.Context, id string) (map[string]string, error) {
	var info struct {
		Config struct {
			Labels map[string]string
		}
	}
	found, err := p.inspect(ctx, "/containers/"+url.PathEscape(id)+"/json", &info)
	if err != nil || !found {
		return nil, err
	}
	if info.Config.Labels == nil {
		info.Config.Labels = map[string]string{}
	}
	return info.Config.Labels, nil
}

// checkRoute deja pasar solo las partes de la API que usa Compose.
func checkRoute(r *http.Request, p string) error {
	ok := false
	for _, a := range allowed {
		if p == a || strings.HasPrefix(p, a+"/") {
			ok = true
			break
		}
	}
	if !ok {
		return fmt.Errorf("%s no está permitido", p)
	}
	if r.Method == http.MethodPost && p == "/build" && strings.EqualFold(r.URL.Query().Get("networkmode"), "host") {
		return fmt.Errorf("los builds con la red del host no están permitidos")
	}
	return nil
}

type createRequest struct {
	Labels     map[string]string
	HostConfig struct {
		Privileged     bool
		CapAdd         []string
		Devices        []json.RawMessage
		DeviceRequests []json.RawMessage
		PidMode        string
		IpcMode        string
		UTSMode        string
		UsernsMode     string
		NetworkMode    string
		CgroupnsMode   string
		SecurityOpt    []string
		Binds          []string
		VolumesFrom    []string
		Mounts         []struct {
			Type   string
			Source string
		}
	}
}

// sensitive son rutas del host que dan el control del servidor.
var sensitive = []string{
	"/", "/etc", "/root", "/home", "/boot", "/dev", "/proc", "/sys", "/run", "/var/run",
	"/var/lib/docker", "/var/lib/containerd", "/usr", "/bin", "/sbin", "/lib", "/lib64",
}

func sensitivePath(src string) bool {
	src = path.Clean(src)
	if strings.HasSuffix(src, ".sock") {
		return true // sockets de Docker, containerd…
	}
	for _, s := range sensitive {
		if src == s || (s != "/" && strings.HasPrefix(src, s+"/")) {
			return true
		}
	}
	return false
}

func checkCreate(body []byte, project string, protected []string) error {
	var req createRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return fmt.Errorf("cuerpo inválido: %w", err)
	}
	h := req.HostConfig
	switch {
	case req.Labels[ProjectLabel] != project:
		return fmt.Errorf("solo se pueden crear contenedores del proyecto %s", project)
	case len(h.VolumesFrom) > 0:
		return fmt.Errorf("volumes_from no está permitido")
	case h.Privileged:
		return fmt.Errorf("los contenedores privilegiados (privileged: true) no están permitidos")
	case len(h.CapAdd) > 0:
		return fmt.Errorf("añadir capabilities (cap_add: %s) no está permitido", strings.Join(h.CapAdd, ", "))
	case len(h.Devices) > 0 || len(h.DeviceRequests) > 0:
		return fmt.Errorf("acceder a dispositivos del host (devices, gpus) no está permitido")
	case h.PidMode == "host", h.IpcMode == "host", h.UTSMode == "host", h.UsernsMode == "host", h.CgroupnsMode == "host":
		return fmt.Errorf("compartir los espacios de nombres del host (pid/ipc/uts/userns: host) no está permitido")
	case h.NetworkMode == "host":
		return fmt.Errorf("la red del host (network_mode: host) no está permitida: Traefik enruta el tráfico por la red de Dockfly")
	}
	for _, o := range h.SecurityOpt {
		if strings.Contains(o, "unconfined") || strings.HasPrefix(o, "no-new-privileges:false") {
			return fmt.Errorf("desactivar el aislamiento (security_opt: %s) no está permitido", o)
		}
	}
	for _, b := range h.Binds {
		src, _, _ := strings.Cut(b, ":")
		if err := checkMount(src, strings.HasPrefix(src, "/"), project, protected); err != nil {
			return err
		}
	}
	for _, m := range h.Mounts {
		switch m.Type {
		case "bind":
			if err := checkMount(m.Source, true, project, protected); err != nil {
				return err
			}
		case "volume":
			if err := checkMount(m.Source, false, project, protected); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkMount revisa el origen de un montaje: una ruta del host (bind) o un
// volumen con nombre (vacío = volumen anónimo, siempre nuevo).
func checkMount(src string, bind bool, project string, protected []string) error {
	if !bind {
		// Compose llama a los volúmenes del proyecto "<proyecto>_<nombre>".
		if src != "" && !strings.HasPrefix(src, project+"_") {
			return fmt.Errorf("el volumen %s no es de este proyecto: solo se pueden montar los volúmenes declarados en el docker-compose.yml", src)
		}
		return nil
	}
	src = path.Clean(src)
	if sensitivePath(src) {
		return fmt.Errorf("montar %s del host no está permitido", src)
	}
	// Ni los datos de Dockfly ni una carpeta que los contenga.
	for _, p := range protected {
		if within(src, p) || within(p, src) {
			return fmt.Errorf("montar %s no está permitido: contiene datos de Dockfly", src)
		}
	}
	return nil
}

// within indica si p es dir o está dentro de dir.
func within(p, dir string) bool {
	return p == dir || strings.HasPrefix(p, strings.TrimSuffix(dir, "/")+"/")
}

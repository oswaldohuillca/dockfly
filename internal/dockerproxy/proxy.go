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
	// Network es la red compartida de Dockfly, a la que se pueden unir los
	// servicios para recibir tráfico.
	Network string

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
			refs, err := checkCreate(body, project, p.Network, p.Protected)
			if err != nil {
				return err
			}
			// container:<x> solo con contenedores del proyecto: el nombre no
			// basta (la app "web" y la app "web-db" comparten prefijo).
			for _, ref := range refs {
				if err := p.ownContainer(r.Context(), ref, project); err != nil {
					return err
				}
			}
			return nil
		}
		// Un exec privilegiado da todas las capabilities dentro de un
		// contenedor del proyecto (CAP_SYS_MODULE/ADMIN → root en el host),
		// saltándose la política del create. El resto del create de exec no
		// concede privilegios (ni HostConfig).
		if r.Method == http.MethodPost && strings.HasSuffix(route, "/exec") {
			body, err := readBody(r)
			if err != nil {
				return err
			}
			var ex struct{ Privileged bool }
			_ = json.Unmarshal(body, &ex)
			if ex.Privileged {
				return fmt.Errorf("los exec privilegiados (privileged: true) no están permitidos")
			}
		}
	case strings.HasPrefix(rest, "volumes/") && !read || route == "/volumes/create":
		name := strings.TrimPrefix(route, "/volumes/")
		if route == "/volumes/create" {
			body, err := readBody(r)
			if err != nil {
				return err
			}
			var v struct {
				Name       string
				DriverOpts map[string]string
			}
			_ = json.Unmarshal(body, &v)
			name = v.Name
			// Un volumen local con device = bind al host (type=none,o=bind):
			// al montarlo, el create del contenedor solo ve un nombre, así
			// que la ruta se valida aquí, como cualquier bind.
			if err := checkDriverOpts(v.DriverOpts, project, p.Protected); err != nil {
				return err
			}
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
			// …y solo a redes del proyecto o a la de Dockfly, no a las de otra app.
			name, labels, err := p.networkInfo(r.Context(), id)
			if err != nil {
				return err
			}
			if labels != nil && name != p.Network && !owned(name) && labels[ProjectLabel] != project {
				return fmt.Errorf("la red %s no es de este proyecto", name)
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
	Labels           map[string]string
	NetworkingConfig struct {
		EndpointsConfig map[string]json.RawMessage
	}
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
			Type          string
			Source        string
			VolumeOptions struct {
				DriverConfig struct {
					Options map[string]string
				}
			}
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
		// Bloquea la ruta sensible, algo dentro de ella, y también un
		// ANCESTRO suyo: montar /var/lib expondría /var/lib/docker (volúmenes
		// y rootfs de todas las apps = root en el host).
		if src == s || (s != "/" && strings.HasPrefix(src, s+"/")) || strings.HasPrefix(s, src+"/") {
			return true
		}
	}
	return false
}

// hostConfigAllowed son los campos de HostConfig que Compose usa de forma
// legítima. Todo lo demás se rechaza: es una lista blanca para que un campo
// nuevo o peligroso (Sysctls, CgroupParent, Runtime, OomKillDisable,
// MaskedPaths, Tmpfs…) no pase sin revisar al añadirlo Docker.
// safeNonEmpty son los campos de HostConfig que Compose usa de forma
// legítima y que pueden llevar un valor. Cualquier otro campo debe ir vacío:
// así, un campo de escape que Docker añada en el futuro (necesita un valor
// para hacer daño) queda bloqueado sin tener que conocerlo.
var safeNonEmpty = map[string]bool{
	// Montajes, red, puertos y reinicio.
	"Binds": true, "Mounts": true, "NetworkMode": true, "RestartPolicy": true,
	"PortBindings": true, "PublishAllPorts": true, "Links": true, "VolumeDriver": true,
	"Dns": true, "DnsOptions": true, "DnsSearch": true, "ExtraHosts": true,
	"GroupAdd": true, "Isolation": true, "Init": true, "AutoRemove": true,
	"ConsoleSize": true, "Annotations": true, "LogConfig": true, "ContainerIDFile": true,
	// Límites de recursos (DoS controlado, no escape del aislamiento).
	"Memory": true, "MemorySwap": true, "MemoryReservation": true, "MemorySwappiness": true,
	"KernelMemory": true, "KernelMemoryTCP": true, "NanoCpus": true, "CpuShares": true,
	"CpuQuota": true, "CpuPeriod": true, "CpuRealtimePeriod": true, "CpuRealtimeRuntime": true,
	"CpusetCpus": true, "CpusetMems": true, "CpuCount": true, "CpuPercent": true,
	"BlkioWeight": true, "BlkioWeightDevice": true, "BlkioDeviceReadBps": true,
	"BlkioDeviceWriteBps": true, "BlkioDeviceReadIOps": true, "BlkioDeviceWriteIOps": true,
	"IOMaximumIOps": true, "IOMaximumBandwidth": true, "PidsLimit": true, "ShmSize": true,
	"Ulimits": true, "OomScoreAdj": true, "StorageOpt": true, "DiskQuota": true,
	"CapDrop": true, "ReadonlyRootfs": true,
	// Espacios de nombres: pueden llevar valor, que se valida en okNamespace.
	"PidMode": true, "IpcMode": true, "UTSMode": true, "CgroupnsMode": true, "UsernsMode": true,
	// MaskedPaths/ReadonlyPaths pueden llevar valor, que se valida aparte.
	"MaskedPaths": true, "ReadonlyPaths": true,
}

// defaultMaskedPaths son los que Docker enmascara en /proc por defecto.
var defaultMaskedPaths = []string{
	"/proc/asound", "/proc/acpi", "/proc/kcore", "/proc/keys", "/proc/latency_stats",
	"/proc/timer_list", "/proc/timer_stats", "/proc/sched_debug", "/proc/scsi",
	"/sys/firmware", "/sys/devices/virtual/powercap",
}
var defaultReadonlyPaths = []string{"/proc/bus", "/proc/fs", "/proc/irq", "/proc/sys", "/proc/sysrq-trigger"}

// okNamespace acepta un modo de espacio de nombres: vacío, los predefinidos,
// container:<x> (devuelto en ref: el llamador comprueba que sea del
// proyecto) y, para la red, las del proyecto y la de Dockfly. Rechaza host y
// lo desconocido.
func okNamespace(mode, project, network string, ns bool) (ok bool, ref string) {
	switch mode {
	case "", "none", "private", "default", "shareable":
		return true, ""
	}
	if target, ok := strings.CutPrefix(mode, "container:"); ok {
		return target != "", target
	}
	// Las redes del proyecto se llaman "<proyecto>_<nombre>".
	return ns && (strings.HasPrefix(mode, project+"_") || (network != "" && mode == network)), ""
}

// checkCreate aplica la política al crear un contenedor. refs son los
// contenedores con los que comparte espacios de nombres (container:<x>).
func checkCreate(body []byte, project, network string, protected []string) (refs []string, err error) {
	var req createRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("cuerpo inválido: %w", err)
	}
	h := req.HostConfig
	for _, ns := range []struct {
		name, mode string
		net        bool
	}{{"pid", h.PidMode, false}, {"ipc", h.IpcMode, false}, {"network_mode", h.NetworkMode, true},
		{"uts", h.UTSMode, false}, {"userns", h.UsernsMode, false}, {"cgroup", h.CgroupnsMode, false}} {
		ok, ref := okNamespace(ns.mode, project, network, ns.net)
		if !ok {
			return nil, fmt.Errorf("%s: %q no está permitido (solo redes o contenedores del proyecto)", ns.name, ns.mode)
		}
		if ref != "" {
			refs = append(refs, ref)
		}
	}
	for name := range req.NetworkingConfig.EndpointsConfig {
		if !strings.HasPrefix(name, project+"_") && name != network {
			return nil, fmt.Errorf("la red %s no es de este proyecto", name)
		}
	}
	return refs, checkHost(body, req, project, protected)
}

func checkHost(body []byte, req createRequest, project string, protected []string) error {
	// Lista blanca de los campos de HostConfig.
	var top struct {
		HostConfig map[string]json.RawMessage `json:"HostConfig"`
	}
	_ = json.Unmarshal(body, &top)
	if err := checkHostFields(top.HostConfig); err != nil {
		return err
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
	case h.UTSMode == "host", h.UsernsMode == "host", h.CgroupnsMode == "host":
		return fmt.Errorf("compartir los espacios de nombres del host (uts/userns/cgroup: host) no está permitido")
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
		// Un volumen con DriverConfig local y device=/ruta es un bind al host
		// (como /volumes/create, pero creado al vuelo en el create). El Source
		// solo es un nombre, así que la ruta se valida aquí.
		if err := checkDriverOpts(m.VolumeOptions.DriverConfig.Options, project, protected); err != nil {
			return err
		}
	}
	return nil
}

// checkDriverOpts bloquea un volumen "local" cuyo device sea un bind a una
// ruta del host prohibida. El driver local pasa la clave por ToLower, así que
// la comparamos igual.
func checkDriverOpts(opts map[string]string, project string, protected []string) error {
	for k, dev := range opts {
		if strings.ToLower(k) == "device" && strings.HasPrefix(dev, "/") {
			if err := checkMount(dev, true, project, protected); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkMount revisa el origen de un montaje: una ruta del host (bind) o un
// volumen con nombre (vacío = volumen anónimo, siempre nuevo).
// isEmptyJSON indica si un valor JSON es "vacío" (cero, nulo, lista/objeto
// vacíos): un campo peligroso solo hace daño con un valor no vacío.
func isEmptyJSON(v json.RawMessage) bool {
	t := strings.TrimSpace(string(v))
	return t == "" || t == "null" || t == `""` || t == "0" || t == "false" || t == "[]" || t == "{}"
}

// lowerSafe son los campos que pueden llevar valor (safeNonEmpty más los que
// se validan aparte), en minúsculas: Docker decodifica HostConfig sin
// distinguir mayúsculas, así que la lista blanca también.
var lowerSafe = func() map[string]bool {
	m := map[string]bool{"cgroupparent": true, "cgroup": true, "runtime": true, "securityopt": true}
	for k := range safeNonEmpty {
		m[strings.ToLower(k)] = true
	}
	return m
}()

// checkHostFields aplica la política a HostConfig: los campos seguros pueden
// llevar valor; los peligrosos conocidos se validan; cualquier otro campo
// debe ir vacío. Trabaja con las claves en minúsculas para que un campo
// peligroso no se cuele con otra caja (p.ej. "maskedpaths": [] en vez de
// "MaskedPaths", que desenmascararía /proc en el host).
func checkHostFields(h map[string]json.RawMessage) error {
	low := make(map[string]json.RawMessage, len(h))
	for k, v := range h {
		lk := strings.ToLower(k)
		if _, dup := low[lk]; dup {
			return fmt.Errorf("HostConfig.%s está repetido", k)
		}
		low[lk] = v
	}
	empty := func(field string) bool {
		v, ok := low[strings.ToLower(field)]
		return !ok || isEmptyJSON(v)
	}
	// Campos peligrosos que nunca pueden llevar valor.
	if !empty("Privileged") {
		return fmt.Errorf("los contenedores privilegiados (privileged: true) no están permitidos")
	}
	if !empty("CapAdd") {
		return fmt.Errorf("añadir capabilities (cap_add) no está permitido")
	}
	if !empty("Devices") || !empty("DeviceRequests") || !empty("DeviceCgroupRules") {
		return fmt.Errorf("acceder a dispositivos del host (devices, gpus) no está permitido")
	}
	for _, field := range []string{"Capabilities", "Sysctls", "OomKillDisable", "Tmpfs", "VolumesFrom"} {
		if !empty(field) {
			return fmt.Errorf("HostConfig.%s no está permitido", field)
		}
	}
	for _, field := range []string{"CgroupParent", "Cgroup", "Runtime"} {
		if !empty(field) && string(low[strings.ToLower(field)]) != `"runc"` {
			return fmt.Errorf("HostConfig.%s solo puede ir vacío", field)
		}
	}
	// MaskedPaths/ReadonlyPaths: ausentes o null = Docker aplica sus defaults
	// (seguro); una lista vacía o acortada desenmascara /proc, así que solo
	// se admite exactamente la lista por defecto.
	for field, def := range map[string][]string{"maskedpaths": defaultMaskedPaths, "readonlypaths": defaultReadonlyPaths} {
		raw, ok := low[field]
		if !ok || strings.TrimSpace(string(raw)) == "null" {
			continue
		}
		var got []string
		if json.Unmarshal(raw, &got) != nil || !sameSet(got, def) {
			return fmt.Errorf("HostConfig.%s solo puede tener los valores por defecto", field)
		}
	}
	// Cualquier otro campo (desconocido o no listado como seguro) debe ir vacío.
	for field, v := range low {
		if lowerSafe[field] {
			continue
		}
		if !isEmptyJSON(v) {
			return fmt.Errorf("el campo HostConfig.%s no está permitido", field)
		}
	}
	return nil
}

// sameSet compara dos listas sin tener en cuenta el orden.
func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]int{}
	for _, x := range a {
		m[x]++
	}
	for _, x := range b {
		m[x]--
	}
	for _, v := range m {
		if v != 0 {
			return false
		}
	}
	return true
}

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

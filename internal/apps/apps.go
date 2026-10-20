// Package apps despliega y gestiona contenedores de larga duración (APIs,
// bases de datos, servicios…) desde una imagen o desde un Dockerfile.
package apps

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dockfly/dockfly/internal/docker"
	"github.com/dockfly/dockfly/internal/dockerproxy"
	"github.com/dockfly/dockfly/internal/gitprovider"
	"github.com/dockfly/dockfly/internal/secrets"
	"github.com/dockfly/dockfly/internal/store"
)

// Etiquetas de los contenedores de apps.
const (
	LabelApp        = "dockfly.app"
	LabelDeployment = "dockfly.app.deployment"
)

const (
	healthTimeout   = 2 * time.Minute
	stableFor       = 5 * time.Second // sin puerto que comprobar: que no se caiga en este tiempo
	stopTimeout     = 20 * time.Second
	keepImages      = 5 // imágenes construidas que se conservan para volver atrás
	maxContextBytes = 1 << 30
)

type Providers interface {
	Provider(ctx context.Context, conn *store.GitConnection) (gitprovider.Provider, error)
}

type Config struct {
	Store     *store.Store
	Docker    *docker.Client
	Secrets   *secrets.Box
	Providers Providers
	LogDir    string
	Log       *slog.Logger
	Network   string // red de Dockfly: Traefik y las apps se ven en ella
	// ComposeProxy, si no es nil, es el proxy del socket con permisos
	// mínimos con el que habla el auxiliar de Compose. SelfContainer es el
	// contenedor de Dockfly, que se conecta a la red interna de cada ejecución.
	ComposeProxy  *dockerproxy.Proxy
	SelfContainer string
	// HostDockerSocket es la ruta del socket de Docker en el host (no en el
	// contenedor de Dockfly). Sin proxy, la monta el auxiliar de Compose
	// (solo en desarrollo: tiene acceso total a Docker).
	HostDockerSocket string
	// Apply regenera la configuración de Traefik (la API sabe hacerlo).
	Apply func(ctx context.Context) error
	// OnFinish se llama al terminar cada despliegue (notificaciones).
	OnFinish func(ctx context.Context, deploymentID int64)
}

type Manager struct {
	cfg   Config
	queue chan int64

	mu      sync.Mutex
	running map[int64]context.CancelCauseFunc
	appLock map[int64]*sync.Mutex // una operación a la vez por app
}

// SetComposeProxy activa el proxy del socket para el auxiliar de Compose.
// Se llama al arrancar, antes de Run.
func (m *Manager) SetComposeProxy(p *dockerproxy.Proxy, self string) {
	m.cfg.ComposeProxy, m.cfg.SelfContainer = p, self
}

func New(cfg Config) *Manager {
	return &Manager{cfg: cfg, queue: make(chan int64, 256), running: map[int64]context.CancelCauseFunc{}, appLock: map[int64]*sync.Mutex{}}
}

func (m *Manager) lockApp(id int64) func() {
	m.mu.Lock()
	l, ok := m.appLock[id]
	if !ok {
		l = &sync.Mutex{}
		m.appLock[id] = l
	}
	m.mu.Unlock()
	l.Lock()
	return l.Unlock
}

func (m *Manager) LogPath(deploymentID int64) string {
	return filepath.Join(m.cfg.LogDir, "app-"+strconv.FormatInt(deploymentID, 10)+".log")
}

// ContainerName es el nombre del contenedor de un despliegue. Cada uno tiene
// el suyo: durante un despliegue sin corte conviven el viejo y el nuevo.
func ContainerName(app *store.App, deploymentID int64) string {
	return fmt.Sprintf("dockfly-app-%s-%d", app.Name, deploymentID)
}

// VolumeName es el volumen de Docker de un volumen de la app. Se conserva
// entre despliegues: ahí viven los datos.
func VolumeName(app *store.App, v store.AppVolume) string {
	return "dockfly-app-" + app.Name + "-" + v.Name
}

func (m *Manager) Run(ctx context.Context) {
	if err := os.MkdirAll(m.cfg.LogDir, 0o755); err != nil {
		m.cfg.Log.Error("creando directorio de logs", "error", err)
	}
	m.recover(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-m.queue:
			m.run(ctx, id)
		}
	}
}

// recover limpia lo que dejó un reinicio a mitad de un despliegue: los
// contenedores nuevos que no llegaron a activarse y los despliegues en curso.
func (m *Manager) recover(ctx context.Context) {
	st := m.cfg.Store
	running, _ := st.ListAppDeploymentsByStatus(ctx, store.DeployRunning)
	for _, d := range running {
		_ = st.FinishAppDeployment(ctx, d.ID, store.DeployFailed, "interrumpido por un reinicio de Dockfly")
		if d.Container != "" && m.cfg.Docker != nil {
			_ = m.cfg.Docker.RemoveContainer(ctx, d.Container)
		}
	}
	queued, _ := st.ListAppDeploymentsByStatus(ctx, store.DeployQueued)
	for _, d := range queued {
		m.queue <- d.ID
	}
}

// Enqueue pone en cola un despliegue. Con image != "" se reutiliza esa
// imagen sin construir (volver a una versión anterior).
func (m *Manager) Enqueue(ctx context.Context, appID int64, trigger string, commit gitprovider.Commit, image string) (*store.AppDeployment, error) {
	if m.cfg.Docker == nil {
		return nil, errors.New("Docker no está disponible: monta /var/run/docker.sock")
	}
	d := &store.AppDeployment{AppID: appID, Status: store.DeployQueued, Trigger: trigger,
		CommitSHA: commit.SHA, CommitMessage: commit.Message, Image: image}
	if err := m.cfg.Store.CreateAppDeployment(ctx, d); err != nil {
		return nil, err
	}
	_ = m.cfg.Store.SupersedeQueuedApp(ctx, appID, d.ID)
	select {
	case m.queue <- d.ID:
		return d, nil
	default:
		_ = m.cfg.Store.FinishAppDeployment(ctx, d.ID, store.DeployFailed, "la cola de despliegues está llena")
		return nil, errors.New("la cola de despliegues está llena")
	}
}

var errCanceled = errors.New("cancelado")

func (m *Manager) Cancel(ctx context.Context, deploymentID int64) error {
	m.mu.Lock()
	cancel, ok := m.running[deploymentID]
	m.mu.Unlock()
	if ok {
		cancel(errCanceled)
		return nil
	}
	err := m.cfg.Store.FinishAppDeployment(ctx, deploymentID, store.DeployCanceled, "cancelado")
	if errors.Is(err, store.ErrNotFound) {
		return errors.New("el despliegue ya terminó")
	}
	return err
}

func (m *Manager) run(parent context.Context, id int64) {
	st := m.cfg.Store
	if err := st.StartAppDeployment(parent, id); err != nil {
		return // cancelado mientras esperaba
	}
	d, err := st.GetAppDeployment(parent, id)
	if err != nil {
		return
	}
	unlock := m.lockApp(d.AppID)
	defer unlock()

	ctx, cancel := context.WithCancelCause(parent)
	m.mu.Lock()
	m.running[id] = cancel
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.running, id)
		m.mu.Unlock()
		cancel(nil)
	}()

	f, err := os.Create(m.LogPath(id))
	if err != nil {
		_ = st.FinishAppDeployment(parent, id, store.DeployFailed, err.Error())
		return
	}
	defer f.Close()
	log := io.Writer(f)
	start := time.Now()

	err = m.deploy(ctx, d, log)
	finish := context.WithoutCancel(parent)
	switch {
	case err == nil:
		fmt.Fprintf(log, "\n✔ Desplegado en %s\n", time.Since(start).Round(time.Second))
	case errors.Is(context.Cause(ctx), errCanceled):
		fmt.Fprintln(log, "\n✘ cancelado")
		_ = st.FinishAppDeployment(finish, id, store.DeployCanceled, "cancelado")
	default:
		fmt.Fprintf(log, "\n✘ %v\n", err)
		_ = st.FinishAppDeployment(finish, id, store.DeployFailed, err.Error())
	}
	if m.cfg.OnFinish != nil {
		m.cfg.OnFinish(finish, id)
	}
}

func (m *Manager) deploy(ctx context.Context, d *store.AppDeployment, log io.Writer) error {
	st, dc := m.cfg.Store, m.cfg.Docker
	app, err := st.GetApp(ctx, d.AppID)
	if err != nil {
		return err
	}
	if app.Source == store.AppSourceCompose {
		return m.deployCompose(ctx, app, d, log)
	}

	// 1. La imagen: descargarla, construirla o reutilizar la de un despliegue anterior.
	if d.Image == "" {
		if d.Image, err = m.prepareImage(ctx, app, d, log); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(log, "▶ Reutilizando la imagen %s\n", d.Image)
	}
	_ = st.UpdateAppDeployment(ctx, d)

	env, err := Env(m.cfg.Secrets, app)
	if err != nil {
		return fmt.Errorf("leyendo las variables de entorno: %w", err)
	}
	cmd, err := SplitCommand(app.Command)
	if err != nil {
		return err
	}

	var old *store.AppDeployment
	if app.CurrentDeploymentID != nil {
		old, _ = st.GetAppDeployment(ctx, *app.CurrentDeploymentID)
	}
	// 2. Estrategia. Con volúmenes, el viejo se para antes de arrancar el
	// nuevo: dos instancias de una base de datos escribiendo en el mismo
	// volumen lo corromperían.
	recreate := len(app.Volumes) > 0
	if recreate && old != nil && old.Container != "" {
		fmt.Fprintln(log, "▶ La app tiene volúmenes: se detiene la versión actual antes de arrancar la nueva (corte breve)")
		if err := dc.StopContainer(ctx, old.Container, stopTimeout); err != nil && !docker.IsNotFound(err) {
			return fmt.Errorf("deteniendo la versión actual: %w", err)
		}
	}
	// Si algo falla después de parar el viejo, se vuelve a arrancar.
	restoreOld := func() {
		if recreate && old != nil && old.Container != "" && !app.Stopped {
			fmt.Fprintln(log, "▶ Volviendo a arrancar la versión anterior")
			bg, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if err := dc.StartContainer(bg, old.Container); err != nil {
				fmt.Fprintf(log, "  no se pudo: %v\n", err)
			}
		}
	}

	// 3. Contenedor nuevo.
	name := ContainerName(app, d.ID)
	volumes := map[string]string{}
	for _, v := range app.Volumes {
		volumes[VolumeName(app, v)] = v.Path
	}
	envList := make([]string, 0, len(env))
	for k, v := range env {
		envList = append(envList, k+"="+v)
	}
	fmt.Fprintf(log, "▶ Arrancando %s\n", name)
	cid, err := dc.CreateAppContainer(ctx, docker.AppSpec{
		Name: name, Image: d.Image, Cmd: cmd, Env: envList, Network: m.cfg.Network, Aliases: []string{app.Name},
		Volumes: volumes, MemoryMB: int64(app.MemoryMB), CPUs: app.CPUs,
		Labels: map[string]string{LabelApp: strconv.FormatInt(app.ID, 10), LabelDeployment: strconv.FormatInt(d.ID, 10)},
	})
	if err != nil {
		restoreOld()
		return fmt.Errorf("creando el contenedor: %w", err)
	}
	d.Container = name
	_ = st.UpdateAppDeployment(ctx, d)
	fail := func(err error) error {
		m.dumpLogs(name, log)
		bg, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = dc.RemoveContainer(bg, cid)
		restoreOld()
		return err
	}
	if err := dc.StartContainer(ctx, cid); err != nil {
		return fail(fmt.Errorf("arrancando el contenedor: %w", err))
	}

	// 4. Salud.
	if err := m.waitHealthy(ctx, app, cid, log); err != nil {
		return fail(err)
	}

	// 5. Activar: la base de datos primero y luego Traefik, que pasa el
	// tráfico al contenedor nuevo.
	if err := st.GoLive(context.WithoutCancel(ctx), app.ID, d.ID); err != nil {
		return fail(err)
	}
	if err := m.cfg.Apply(context.WithoutCancel(ctx)); err != nil {
		fmt.Fprintf(log, "⚠ no se pudo actualizar Traefik: %v\n", err)
	}

	// 6. Retirar la versión anterior, dejando a Traefik un momento para
	// recargar (sin esto, las peticiones en vuelo podrían fallar).
	if old != nil && old.Container != "" {
		if !recreate {
			time.Sleep(3 * time.Second)
		}
		bg, cancel := context.WithTimeout(context.Background(), time.Minute)
		_ = dc.StopContainer(bg, old.Container, stopTimeout)
		_ = dc.RemoveContainer(bg, old.Container)
		cancel()
		fmt.Fprintf(log, "▶ Versión anterior (%s) retirada\n", old.Container)
	}
	m.pruneImages(app)
	return nil
}

func (m *Manager) prepareImage(ctx context.Context, app *store.App, d *store.AppDeployment, log io.Writer) (string, error) {
	dc := m.cfg.Docker
	if app.Source == store.AppSourceImage {
		image := docker.WithDefaultTag(app.Image)
		fmt.Fprintf(log, "▶ Descargando %s…\n", image)
		// Siempre se descarga: ":latest" puede haber cambiado desde el último despliegue.
		if err := dc.PullImage(ctx, image, log); err != nil {
			if ok, _ := dc.ImageExists(ctx, image); !ok {
				return "", fmt.Errorf("descargando %s: %w", image, err)
			}
			fmt.Fprintf(log, "⚠ no se pudo descargar (%v): se usa la copia local\n", err)
		}
		return image, nil
	}

	rc, err := m.fetchSource(ctx, app, d, log)
	if err != nil {
		return "", err
	}
	defer rc.Close()

	tag := fmt.Sprintf("dockfly/%s:%d", app.Name, d.ID)
	dockerfile := app.Dockerfile
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}
	fmt.Fprintf(log, "▶ Construyendo %s con %s\n", tag, dockerfile)
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(buildContext(rc, pw, app.RootDir)) }()
	err = dc.BuildImage(ctx, tag, dockerfile, pr, 0, log)
	pr.Close()
	if err != nil {
		return "", fmt.Errorf("el build de la imagen falló: %w", err)
	}
	return tag, nil
}

// fetchSource descarga el código del commit a desplegar (el último de la
// rama si el despliegue no trae uno).
func (m *Manager) fetchSource(ctx context.Context, app *store.App, d *store.AppDeployment, log io.Writer) (io.ReadCloser, error) {
	if app.GitConnectionID == nil || app.Repo == "" {
		return nil, errors.New("la app no tiene un repositorio configurado")
	}
	conn, err := m.cfg.Store.GetGitConnection(ctx, *app.GitConnectionID)
	if err != nil {
		return nil, errors.New("la cuenta de Git de la app ya no existe")
	}
	prov, err := m.cfg.Providers.Provider(ctx, conn)
	if err != nil {
		return nil, err
	}
	if d.CommitSHA == "" {
		c, err := prov.LatestCommit(ctx, app.Repo, app.Branch)
		if err != nil {
			return nil, fmt.Errorf("obteniendo el último commit: %w", err)
		}
		d.CommitSHA, d.CommitMessage = c.SHA, c.Message
		_ = m.cfg.Store.UpdateAppDeployment(ctx, d)
	}
	fmt.Fprintf(log, "▶ %s@%s (%s): %s\n", app.Repo, short(d.CommitSHA), app.Branch, d.CommitMessage)
	rc, err := prov.Tarball(ctx, app.Repo, d.CommitSHA)
	if err != nil {
		return nil, fmt.Errorf("descargando el código: %w", err)
	}
	return rc, nil
}

// waitHealthy espera a que el contenedor esté listo para recibir tráfico.
func (m *Manager) waitHealthy(ctx context.Context, app *store.App, id string, log io.Writer) error {
	dc := m.cfg.Docker
	ctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()
	how := "que el contenedor siga en marcha"
	switch {
	case app.Port > 0 && app.HealthPath != "":
		how = fmt.Sprintf("respuesta HTTP en el puerto %d%s", app.Port, app.HealthPath)
	case app.Port > 0:
		how = fmt.Sprintf("que el puerto %d acepte conexiones", app.Port)
	}
	fmt.Fprintf(log, "▶ Esperando a que esté listo (%s)…\n", how)

	var runningSince, firstStart time.Time
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		info, err := dc.InspectContainer(ctx, id)
		if err != nil {
			return err
		}
		if firstStart.IsZero() {
			firstStart = info.StartedAt
		}
		// Con la política unless-stopped, Docker reinicia en bucle un
		// contenedor que se cae al arrancar: entre reinicio y reinicio parece
		// "en marcha". Un reinicio durante la espera es una caída.
		crashed := !info.Running || info.Restarting || !info.StartedAt.Equal(firstStart)
		if crashed {
			reason := fmt.Sprintf("el contenedor terminó con código %d", info.ExitCode)
			if info.OOMKilled {
				reason += " (sin memoria: aumenta el límite de memoria)"
			}
			return errors.New(reason)
		}
		ok := false
		switch {
		case info.Health != "":
			// La imagen define su propio HEALTHCHECK: es lo más fiable.
			if info.Health == "unhealthy" {
				return errors.New("el HEALTHCHECK de la imagen indica que no está sano")
			}
			ok = info.Health == "healthy"
		case app.Port > 0:
			ok = m.probe(ctx, ip(info, m.cfg.Network), app)
		default:
			if info.Running && runningSince.IsZero() {
				runningSince = time.Now()
			}
			ok = !runningSince.IsZero() && time.Since(runningSince) >= stableFor
		}
		if ok {
			fmt.Fprintln(log, "▶ Listo")
			return nil
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("no estuvo listo en %s (comprobación: %s)", healthTimeout, how)
			}
			return ctx.Err()
		case <-tick.C:
		}
	}
}

func ip(info *docker.ContainerInfo, network string) string {
	if v := info.IPs[network]; v != "" {
		return v
	}
	for _, v := range info.IPs {
		if v != "" {
			return v
		}
	}
	return ""
}

var probeClient = &http.Client{
	Timeout: 3 * time.Second,
	// Una redirección (p. ej. a /login) ya demuestra que la app responde.
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func (m *Manager) probe(ctx context.Context, addr string, app *store.App) bool {
	if addr == "" {
		return false
	}
	hostport := net.JoinHostPort(addr, strconv.Itoa(app.Port))
	if app.HealthPath == "" {
		conn, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", hostport)
		if err != nil {
			return false
		}
		conn.Close()
		return true
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+hostport+app.HealthPath, nil)
	if err != nil {
		return false
	}
	resp, err := probeClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 400
}

// dumpLogs copia al log del despliegue las últimas líneas del contenedor:
// suelen explicar por qué no arrancó.
func (m *Manager) dumpLogs(container string, log io.Writer) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	rc, err := m.cfg.Docker.Logs(ctx, container)
	if err != nil {
		return
	}
	defer rc.Close()
	fmt.Fprintln(log, "\n── Últimas líneas del contenedor ──")
	_ = docker.Demux(io.LimitReader(rc, 64<<10), log)
	fmt.Fprintln(log, "───────────────────────────────────")
}

// pruneImages borra las imágenes construidas de despliegues antiguos,
// conservando las de los más recientes para poder volver atrás.
func (m *Manager) pruneImages(app *store.App) {
	if app.Source != store.AppSourceGit {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	deps, err := m.cfg.Store.ListAppDeployments(ctx, app.ID, 100)
	if err != nil {
		return
	}
	kept := 0
	for _, d := range deps {
		if d.Image == "" || !strings.HasPrefix(d.Image, "dockfly/") {
			continue
		}
		if kept < keepImages || d.Status == store.DeployLive {
			kept++
			continue
		}
		_ = m.cfg.Docker.RemoveImage(ctx, d.Image)
	}
}

// Env descifra las variables de entorno de una app.
func Env(box *secrets.Box, app *store.App) (map[string]string, error) {
	env := map[string]string{}
	if len(app.EnvEnc) == 0 {
		return env, nil
	}
	raw, err := box.Open(app.EnvEnc)
	if err != nil {
		return nil, err
	}
	return env, json.Unmarshal(raw, &env)
}

// buildContext convierte el .tar.gz del repositorio en el contexto del
// build: descarta la carpeta raíz y, con rootDir, se queda con esa
// subcarpeta (monorepos).
func buildContext(gz io.Reader, w io.Writer, rootDir string) error {
	zr, err := gzip.NewReader(gz)
	if err != nil {
		return fmt.Errorf("el código descargado no es un .tar.gz: %w", err)
	}
	prefix := strings.Trim(path.Clean("/"+rootDir), "/")
	tr, tw := tar.NewReader(zr), tar.NewWriter(w)
	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeReg, tar.TypeDir, tar.TypeSymlink:
		default:
			continue
		}
		_, rest, _ := strings.Cut(hdr.Name, "/")
		rest = strings.TrimPrefix(path.Clean("/"+rest), "/")
		if prefix != "" {
			if rest == prefix {
				continue
			}
			var ok bool
			if rest, ok = strings.CutPrefix(rest, prefix+"/"); !ok {
				continue
			}
		}
		if rest == "" {
			continue
		}
		if total += hdr.Size; total > maxContextBytes {
			return fmt.Errorf("el código supera %d MiB", maxContextBytes>>20)
		}
		out := *hdr
		out.Name = rest
		if hdr.Typeflag == tar.TypeDir {
			out.Name += "/"
		}
		out.PAXRecords, out.Format = nil, tar.FormatUnknown
		if err := tw.WriteHeader(&out); err != nil {
			return err
		}
		if hdr.Typeflag == tar.TypeReg {
			if _, err := io.Copy(tw, tr); err != nil {
				return err
			}
		}
	}
	return tw.Close()
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

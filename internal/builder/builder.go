// Package builder construye sitios estáticos a partir de un repositorio de
// GitHub o GitLab y publica el resultado como un despliegue.
//
// Cada build corre en un contenedor efímero y aislado: sin privilegios, con
// límites de recursos y sin acceso al token del repositorio (el código lo
// descarga Dockfly y lo copia dentro).
package builder

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/dockfly/dockfly/internal/docker"
	"github.com/dockfly/dockfly/internal/gitprovider"
	"github.com/dockfly/dockfly/internal/pages"
	"github.com/dockfly/dockfly/internal/secrets"
	"github.com/dockfly/dockfly/internal/store"
)

// Valores por defecto de un build, al estilo de un proyecto Vite.
const (
	DefaultImage   = "oven/bun:1"
	DefaultInstall = "bun install"
	DefaultBuild   = "bun run build"
	DefaultOutput  = "dist"
)

// LabelBuild marca los contenedores de build, para limpiarlos si Dockfly se
// reinicia a mitad de uno.
const LabelBuild = "dockfly.build"

const (
	maxSourceBytes = 1 << 30 // 1 GiB de código fuente
	maxLogBytes    = 5 << 20 // 5 MiB de log por build
)

// Providers da un cliente con credenciales vigentes para una conexión
// (lo implementa gitauth.Resolver).
type Providers interface {
	Provider(ctx context.Context, conn *store.GitConnection) (gitprovider.Provider, error)
}

type Config struct {
	Store     *store.Store
	Docker    *docker.Client // nil si Docker no está disponible
	Secrets   *secrets.Box
	Providers Providers
	Pages     *pages.Storage
	LogDir    string
	Log       *slog.Logger
	// Publish activa un despliegue recién creado (lo implementa la API, que
	// sabe regenerar Traefik y el servidor estático).
	Publish func(ctx context.Context, siteID, deploymentID int64) error
	// OnFinish, si no es nil, se llama al terminar cada build (notificaciones).
	OnFinish func(ctx context.Context, buildID int64)

	Concurrency int
	Timeout     time.Duration
	MemoryMB    int64
	CPUs        float64
}

type Manager struct {
	cfg   Config
	queue chan int64

	mu      sync.Mutex
	running map[int64]context.CancelFunc
}

func New(cfg Config) *Manager {
	if cfg.Concurrency < 1 {
		cfg.Concurrency = 1
	}
	return &Manager{cfg: cfg, queue: make(chan int64, 256), running: map[int64]context.CancelFunc{}}
}

func (m *Manager) LogPath(buildID int64) string {
	return filepath.Join(m.cfg.LogDir, strconv.FormatInt(buildID, 10)+".log")
}

// Run arranca los workers y bloquea hasta que ctx se cancela. Antes recupera
// el estado de un posible reinicio: los builds que estaban en marcha se dan
// por fallidos y los que esperaban vuelven a la cola.
func (m *Manager) Run(ctx context.Context) {
	if err := os.MkdirAll(m.cfg.LogDir, 0o755); err != nil {
		m.cfg.Log.Error("creando directorio de logs", "error", err)
	}
	m.recover(ctx)

	var wg sync.WaitGroup
	for range m.cfg.Concurrency {
		wg.Go(func() {
			for {
				select {
				case <-ctx.Done():
					return
				case id := <-m.queue:
					m.run(ctx, id)
				}
			}
		})
	}
	wg.Wait()
}

func (m *Manager) recover(ctx context.Context) {
	running, err := m.cfg.Store.ListBuildsByStatus(ctx, store.BuildRunning)
	if err != nil {
		m.cfg.Log.Error("recuperando builds", "error", err)
		return
	}
	for _, b := range running {
		_ = m.cfg.Store.FinishBuild(ctx, b.ID, store.BuildFailed, "interrumpido por un reinicio de Dockfly", nil)
	}
	if m.cfg.Docker != nil {
		if ids, err := m.cfg.Docker.ContainersWithLabel(ctx, LabelBuild); err == nil {
			for _, id := range ids {
				_ = m.cfg.Docker.RemoveContainer(ctx, id)
			}
		}
	}
	queued, err := m.cfg.Store.ListBuildsByStatus(ctx, store.BuildQueued)
	if err != nil {
		return
	}
	for _, b := range queued {
		m.queue <- b.ID
	}
}

// Enqueue crea un build y lo pone en cola. Los builds que esperaban para el
// mismo sitio se cancelan: solo tiene sentido construir el commit más nuevo.
func (m *Manager) Enqueue(ctx context.Context, siteID int64, trigger, branch string, commit gitprovider.Commit) (*store.Build, error) {
	b := &store.Build{
		SiteID: siteID, Status: store.BuildQueued, Trigger: trigger, Branch: branch,
		CommitSHA: commit.SHA, CommitMessage: commit.Message,
	}
	if err := m.cfg.Store.CreateBuild(ctx, b); err != nil {
		return nil, err
	}
	if _, err := m.cfg.Store.SupersedeQueued(ctx, siteID, b.ID); err != nil {
		m.cfg.Log.Error("cancelando builds anteriores", "site", siteID, "error", err)
	}
	select {
	case m.queue <- b.ID:
		return b, nil
	default:
		_ = m.cfg.Store.FinishBuild(ctx, b.ID, store.BuildFailed, "la cola de builds está llena", nil)
		return nil, errors.New("la cola de builds está llena; inténtalo en unos minutos")
	}
}

// Cancel detiene un build en marcha o lo saca de la cola.
func (m *Manager) Cancel(ctx context.Context, buildID int64) error {
	m.mu.Lock()
	cancel, ok := m.running[buildID]
	m.mu.Unlock()
	if ok {
		cancel()
		return nil
	}
	err := m.cfg.Store.FinishBuild(ctx, buildID, store.BuildCanceled, "cancelado", nil)
	if errors.Is(err, store.ErrNotFound) {
		return errors.New("el build ya terminó")
	}
	return err
}

var errCanceled = errors.New("cancelado")

func (m *Manager) run(parent context.Context, id int64) {
	st := m.cfg.Store
	// Si se canceló mientras esperaba en cola, MarkBuildStarted falla y no
	// hay nada que hacer.
	if err := st.MarkBuildStarted(parent, id); err != nil {
		return
	}
	b, err := st.GetBuild(parent, id)
	if err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(parent, m.cfg.Timeout)
	ctx, cancelBuild := context.WithCancelCause(ctx)
	m.mu.Lock()
	m.running[id] = func() { cancelBuild(errCanceled) }
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.running, id)
		m.mu.Unlock()
		cancelBuild(nil)
		cancel()
	}()

	logFile, err := os.Create(m.LogPath(id))
	if err != nil {
		_ = st.FinishBuild(parent, id, store.BuildFailed, "no se pudo crear el log: "+err.Error(), nil)
		return
	}
	defer logFile.Close()
	logw := &cappedWriter{w: logFile, remaining: maxLogBytes}

	start := time.Now()
	depID, err := m.pipeline(ctx, b, logw)

	// La causa distingue una cancelación del usuario de un timeout.
	status, msg := store.BuildSuccess, ""
	switch {
	case errors.Is(context.Cause(ctx), errCanceled):
		status, msg = store.BuildCanceled, "cancelado"
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		status, msg = store.BuildFailed, fmt.Sprintf("superó el tiempo máximo de %s", m.cfg.Timeout)
	case err != nil:
		status, msg = store.BuildFailed, err.Error()
	}
	if status == store.BuildSuccess {
		fmt.Fprintf(logw, "\n✔ Publicado en %s\n", time.Since(start).Round(time.Second))
	} else {
		fmt.Fprintf(logw, "\n✘ %s\n", msg)
	}
	var dep *int64
	if depID != 0 {
		dep = &depID
	}
	// El build terminó: guardamos el resultado aunque ctx ya esté cancelado.
	if err := st.FinishBuild(context.WithoutCancel(parent), id, status, msg, dep); err != nil &&
		!errors.Is(err, store.ErrNotFound) {
		m.cfg.Log.Error("guardando resultado del build", "build", id, "error", err)
	}
	if m.cfg.OnFinish != nil && status != store.BuildCanceled {
		m.cfg.OnFinish(context.WithoutCancel(parent), id)
	}
}

// Env descifra las variables de entorno del build de un sitio.
func Env(box *secrets.Box, site *store.Site) (map[string]string, error) {
	env := map[string]string{}
	if len(site.BuildEnvEnc) == 0 {
		return env, nil
	}
	raw, err := box.Open(site.BuildEnvEnc)
	if err != nil {
		return nil, err
	}
	return env, json.Unmarshal(raw, &env)
}

func (m *Manager) provider(ctx context.Context, site *store.Site) (gitprovider.Provider, string, error) {
	if site.Repo == "" || site.GitConnectionID == nil {
		return nil, "", errors.New("el sitio no está enlazado a ningún repositorio")
	}
	conn, err := m.cfg.Store.GetGitConnection(ctx, *site.GitConnectionID)
	if err != nil {
		return nil, "", fmt.Errorf("la cuenta de %s vinculada ya no existe", site.Repo)
	}
	p, err := m.cfg.Providers.Provider(ctx, conn)
	return p, conn.Provider, err
}

// pipeline descarga el código, lo construye y crea el despliegue. Devuelve
// el ID del despliegue publicado.
func (m *Manager) pipeline(ctx context.Context, b *store.Build, log io.Writer) (int64, error) {
	st := m.cfg.Store
	site, err := st.GetSite(ctx, b.SiteID)
	if err != nil {
		return 0, err
	}
	prov, providerName, err := m.provider(ctx, site)
	if err != nil {
		return 0, err
	}

	commit := gitprovider.Commit{SHA: b.CommitSHA, Message: b.CommitMessage}
	if commit.SHA == "" {
		fmt.Fprintf(log, "▶ Buscando el último commit de %s…\n", b.Branch)
		if commit, err = prov.LatestCommit(ctx, site.Repo, b.Branch); err != nil {
			return 0, fmt.Errorf("obteniendo el último commit: %w", err)
		}
		_ = st.SetBuildCommit(ctx, b.ID, commit.SHA, commit.Message)
	}
	fmt.Fprintf(log, "▶ %s@%s (%s): %s\n", site.Repo, short(commit.SHA), b.Branch, commit.Message)

	staging, err := m.cfg.Pages.NewStaging(site.ID)
	if err != nil {
		return 0, err
	}
	committed := false
	defer func() {
		if !committed {
			m.cfg.Pages.Discard(staging)
		}
	}()

	if site.InstallCommand == "" && site.BuildCommand == "" {
		// Sin comandos (HTML plano): se publica directamente desde el código,
		// sin contenedor.
		if err := m.publishSource(ctx, prov, site, commit.SHA, staging, log); err != nil {
			return 0, err
		}
	} else if err := m.buildInContainer(ctx, prov, site, b, commit, staging, log); err != nil {
		return 0, err
	}

	files, size, err := staging.Finish()
	if err != nil {
		return 0, err
	}
	fmt.Fprintf(log, "▶ Publicando %d archivos (%s)…\n", files, humanBytes(size))
	dep := &store.Deployment{
		SiteID: site.ID, Source: providerName, FileCount: files, TotalBytes: size,
		CommitSHA: commit.SHA, CommitMessage: commit.Message, Branch: b.Branch,
	}
	if err := st.CreateDeployment(ctx, dep); err != nil {
		return 0, err
	}
	if err := m.cfg.Pages.Commit(staging, site.ID, dep.ID); err != nil {
		_ = st.DeleteDeployment(ctx, site.ID, dep.ID)
		return 0, err
	}
	committed = true
	// Publicar no debe quedarse a medias si se cancela justo ahora.
	if err := m.cfg.Publish(context.WithoutCancel(ctx), site.ID, dep.ID); err != nil {
		return dep.ID, fmt.Errorf("publicando: %w", err)
	}
	return dep.ID, nil
}

func (m *Manager) publishSource(ctx context.Context, prov gitprovider.Provider, site *store.Site, sha string,
	staging *pages.Staging, log io.Writer) error {
	fmt.Fprintln(log, "▶ Sin comandos de build: se publican los archivos del repositorio")
	rc, err := prov.Tarball(ctx, site.Repo, sha)
	if err != nil {
		return fmt.Errorf("descargando el código: %w", err)
	}
	defer rc.Close()
	gz, err := gzip.NewReader(rc)
	if err != nil {
		return fmt.Errorf("el código descargado no es un .tar.gz: %w", err)
	}
	return staging.AddTar(gz, path.Join(site.RootDir, outputDir(site)))
}

func (m *Manager) buildInContainer(ctx context.Context, prov gitprovider.Provider, site *store.Site, b *store.Build,
	commit gitprovider.Commit, staging *pages.Staging, log io.Writer) error {
	dc := m.cfg.Docker
	if dc == nil {
		return errors.New("Docker no está disponible: monta /var/run/docker.sock para poder hacer builds")
	}
	image := site.BuildImage
	if image == "" {
		image = DefaultImage
	}
	if ok, err := dc.ImageExists(ctx, docker.WithDefaultTag(image)); err != nil {
		return err
	} else if !ok {
		fmt.Fprintf(log, "▶ Descargando la imagen %s…\n", image)
		if err := dc.PullImage(ctx, image, log); err != nil {
			return fmt.Errorf("descargando la imagen %s: %w", image, err)
		}
	}

	env, err := Env(m.cfg.Secrets, site)
	if err != nil {
		return fmt.Errorf("leyendo las variables de entorno: %w", err)
	}
	envList := []string{"CI=true", "HOME=/tmp", "NO_COLOR=1",
		"DOCKFLY_COMMIT_SHA=" + commit.SHA, "DOCKFLY_BRANCH=" + b.Branch}
	for k, v := range env {
		envList = append(envList, k+"="+v)
	}

	var volumes map[string]string
	if m.ensureCache(ctx, site.ID, image, log) {
		volumes = map[string]string{CacheVolume(site.ID): cacheDir}
		envList = append(envList, cacheEnv...)
	}

	workdir := path.Join("/workspace", site.RootDir)
	id, err := dc.CreateContainer(ctx, docker.ContainerSpec{
		Name:       fmt.Sprintf("dockfly-build-%d", b.ID),
		Image:      image,
		Cmd:        []string{"sh", "-c", script(site.InstallCommand, site.BuildCommand)},
		Env:        envList,
		User:       fmt.Sprintf("%d:%d", buildUID, buildUID),
		WorkingDir: workdir,
		Labels:     map[string]string{LabelBuild: strconv.FormatInt(b.ID, 10)},
		MemoryMB:   m.cfg.MemoryMB,
		CPUs:       m.cfg.CPUs,
		PidsLimit:  1024,
		Volumes:    volumes,
	})
	if err != nil {
		return fmt.Errorf("creando el contenedor de build: %w", err)
	}
	defer func() {
		// Con un contexto propio: hay que limpiar aunque el build se haya cancelado.
		rmCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := dc.RemoveContainer(rmCtx, id); err != nil {
			m.cfg.Log.Error("borrando contenedor de build", "container", id, "error", err)
		}
	}()

	fmt.Fprintln(log, "▶ Descargando el código…")
	if err := m.copySource(ctx, prov, site.Repo, commit.SHA, id); err != nil {
		return err
	}

	fmt.Fprintf(log, "▶ Ejecutando el build en %s\n", image)
	if err := dc.StartContainer(ctx, id); err != nil {
		return fmt.Errorf("iniciando el build: %w", err)
	}
	logs, err := dc.Logs(ctx, id)
	if err != nil {
		return err
	}
	logsDone := make(chan struct{})
	go func() {
		defer close(logsDone)
		_ = demux(logs, log)
	}()
	code, err := dc.Wait(ctx, id)
	// Si se cancela, cerrar el flujo desbloquea la lectura de logs.
	if err != nil {
		logs.Close()
		<-logsDone
		return err
	}
	<-logsDone
	logs.Close()
	if code != 0 {
		return fmt.Errorf("el build terminó con código %d", code)
	}

	out := path.Join(workdir, outputDir(site))
	rc, err := dc.GetArchive(ctx, id, out)
	if docker.IsNotFound(err) {
		return fmt.Errorf("no se encontró la carpeta de salida %q: revisa el directorio de salida del sitio", outputDir(site))
	}
	if err != nil {
		return err
	}
	defer rc.Close()
	return staging.AddTar(rc, "")
}

// copySource descarga el código y lo transmite al contenedor sin pasar por
// disco: GitHub → repack → API de Docker.
func (m *Manager) copySource(ctx context.Context, prov gitprovider.Provider, repo, sha, containerID string) error {
	rc, err := prov.Tarball(ctx, repo, sha)
	if err != nil {
		return fmt.Errorf("descargando el código: %w", err)
	}
	defer rc.Close()

	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(repack(rc, pw, maxSourceBytes)) }()
	err = m.cfg.Docker.PutArchive(ctx, containerID, "/", pr)
	pr.Close()
	if err != nil {
		return fmt.Errorf("copiando el código al contenedor: %w", err)
	}
	return nil
}

func outputDir(site *store.Site) string {
	if site.OutputDir == "" {
		return "."
	}
	return site.OutputDir
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

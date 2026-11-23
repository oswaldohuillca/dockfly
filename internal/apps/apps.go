// Package apps deploys and manages long-running containers (APIs,
// databases, services…) from an image or from a Dockerfile.
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
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dockfly/dockfly/internal/builder"
	"github.com/dockfly/dockfly/internal/docker"
	"github.com/dockfly/dockfly/internal/dockerproxy"
	"github.com/dockfly/dockfly/internal/gitprovider"
	"github.com/dockfly/dockfly/internal/probe"
	"github.com/dockfly/dockfly/internal/secrets"
	"github.com/dockfly/dockfly/internal/store"
)

// Labels of the app containers.
const (
	LabelApp        = "dockfly.app"
	LabelDeployment = "dockfly.app.deployment"
)

const (
	healthTimeout   = 2 * time.Minute
	stableFor       = 5 * time.Second // with no port to check: it must not crash within this time
	stopTimeout     = 20 * time.Second
	keepImages      = 5 // built images kept around so a rollback is possible
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
	Network   string // Dockfly's network: Traefik and the apps meet there
	// ComposeProxy, if not nil, is the least-privilege socket proxy the
	// Compose helper talks to. SelfContainer is Dockfly's own container,
	// which joins the internal network of every run. Remote gives the Docker
	// of a remote server (through its agent's tunnel) and RemoteNetwork, the
	// Dockfly network on that server.
	Remote        func(serverID int64) *docker.Client
	RemoteNetwork func(serverID int64) string
	// RemoteProbe checks from a remote server whether an app is ready.
	RemoteProbe   func(ctx context.Context, serverID int64, hostport, path string) bool
	ComposeProxy  *dockerproxy.Proxy
	SelfContainer string
	// HostDockerSocket is the path of the Docker socket on the host (not
	// inside Dockfly's container). With no proxy, the Compose helper mounts
	// it (development only: that grants full access to Docker).
	HostDockerSocket string
	// Apply regenerates Traefik's configuration (the API knows how to).
	Apply func(ctx context.Context) error
	// OnFinish is called when each deployment finishes (notifications).
	OnFinish func(ctx context.Context, deploymentID int64)
	// OnJobFailed is called when a scheduled job ends badly.
	OnJobFailed func(ctx context.Context, app *store.App, job *store.AppJob, exitCode int)
}

type Manager struct {
	cfg   Config
	queue chan int64

	mu      sync.Mutex
	running map[int64]context.CancelCauseFunc
	appLock map[int64]*sync.Mutex // one operation at a time per app
	jobs    map[int64]bool        // scheduled jobs currently running
}

// Docker is the Docker where the app runs: the local one or the one of its
// server. nil when no Docker is available.
func (m *Manager) Docker(app *store.App) *docker.Client {
	if app.ServerID != nil && m.cfg.Remote != nil {
		return m.cfg.Remote(*app.ServerID)
	}
	return m.cfg.Docker
}

// network is the Dockfly network the app goes into (its server's one).
func (m *Manager) network(app *store.App) string {
	if app.ServerID != nil && m.cfg.RemoteNetwork != nil {
		return m.cfg.RemoteNetwork(*app.ServerID)
	}
	return m.cfg.Network
}

// SetComposeProxy enables the socket proxy for the Compose helper. It is
// called at startup, before Run.
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

// ContainerName is the container name of a deployment. Each one has its
// own: during a zero-downtime deploy the old and the new coexist.
func ContainerName(app *store.App, deploymentID int64) string {
	return fmt.Sprintf("dockfly-app-%s-%d", app.Name, deploymentID)
}

// VolumeName is the Docker volume behind an app volume. It is kept across
// deployments: that is where the data lives.
func VolumeName(app *store.App, v store.AppVolume) string {
	return "dockfly-app-" + app.Name + "-" + v.Name
}

func (m *Manager) Run(ctx context.Context) {
	if err := os.MkdirAll(m.cfg.LogDir, 0o755); err != nil {
		m.cfg.Log.Error("creating the log directory", "error", err)
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

// recover cleans up what a restart in the middle of a deploy left behind:
// the new containers that never went live and the in-flight deployments.
func (m *Manager) recover(ctx context.Context) {
	st := m.cfg.Store
	running, _ := st.ListAppDeploymentsByStatus(ctx, store.DeployRunning)
	for _, d := range running {
		_ = st.FinishAppDeployment(ctx, d.ID, store.DeployFailed, "interrupted by a Dockfly restart")
		app, err := st.GetApp(ctx, d.AppID)
		if err != nil || m.Docker(app) == nil {
			continue
		}
		if d.Container != "" {
			_ = m.Docker(app).RemoveContainer(ctx, d.Container)
		}
		// With volumes, the deploy stops the previous container before
		// starting the new one: if it was cut right there, the app would be
		// left down. (If it is already running, Docker answers 304 and
		// nothing happens.)
		if cur := app.Current; !app.Stopped && cur != nil && cur.Container != "" && cur.Container != d.Container {
			_ = m.Docker(app).StartContainer(ctx, cur.Container)
		}
	}
	queued, _ := st.ListAppDeploymentsByStatus(ctx, store.DeployQueued)
	for _, d := range queued {
		m.queue <- d.ID
	}
}

// Enqueue queues a deployment. With image != "" that image is reused
// without building (rolling back to an earlier version).
func (m *Manager) Enqueue(ctx context.Context, appID int64, trigger string, commit gitprovider.Commit, image string) (*store.AppDeployment, error) {
	app, err := m.cfg.Store.GetApp(ctx, appID)
	if err != nil {
		return nil, err
	}
	if m.Docker(app) == nil {
		return nil, errors.New("Docker is not available: mount /var/run/docker.sock")
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
		_ = m.cfg.Store.FinishAppDeployment(ctx, d.ID, store.DeployFailed, "the deployment queue is full")
		return nil, errors.New("the deployment queue is full")
	}
}

var errCanceled = errors.New("canceled")

func (m *Manager) Cancel(ctx context.Context, deploymentID int64) error {
	m.mu.Lock()
	cancel, ok := m.running[deploymentID]
	m.mu.Unlock()
	if ok {
		cancel(errCanceled)
		return nil
	}
	err := m.cfg.Store.FinishAppDeployment(ctx, deploymentID, store.DeployCanceled, "canceled")
	if errors.Is(err, store.ErrNotFound) {
		return errors.New("the deployment has already finished")
	}
	return err
}

func (m *Manager) run(parent context.Context, id int64) {
	st := m.cfg.Store
	if err := st.StartAppDeployment(parent, id); err != nil {
		return // canceled while it was waiting
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
	log := io.Writer(builder.NewCappedWriter(f))
	start := time.Now()

	err = m.deploy(ctx, d, log)
	finish := context.WithoutCancel(parent)
	switch {
	case err == nil:
		fmt.Fprintf(log, "\n✔ Deployed in %s\n", time.Since(start).Round(time.Second))
	case errors.Is(context.Cause(ctx), errCanceled):
		fmt.Fprintln(log, "\n✘ canceled")
		_ = st.FinishAppDeployment(finish, id, store.DeployCanceled, "canceled")
	default:
		fmt.Fprintf(log, "\n✘ %v\n", err)
		_ = st.FinishAppDeployment(finish, id, store.DeployFailed, err.Error())
	}
	if m.cfg.OnFinish != nil {
		m.cfg.OnFinish(finish, id)
	}
}

func (m *Manager) deploy(ctx context.Context, d *store.AppDeployment, log io.Writer) error {
	st := m.cfg.Store
	app, err := st.GetApp(ctx, d.AppID)
	if err != nil {
		return err
	}
	dc := m.Docker(app)
	if app.Source == store.AppSourceCompose {
		return m.deployCompose(ctx, app, d, log)
	}

	// 1. The image: pull it, build it or reuse the one of an earlier deploy.
	if d.Image == "" {
		if d.Image, err = m.prepareImage(ctx, app, d, log); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(log, "▶ Reusing the image %s\n", d.Image)
	}
	_ = st.UpdateAppDeployment(ctx, d)

	env, err := Env(m.cfg.Secrets, app)
	if err != nil {
		return fmt.Errorf("reading the environment variables: %w", err)
	}
	cmd, err := SplitCommand(app.Command)
	if err != nil {
		return err
	}

	var old *store.AppDeployment
	if app.CurrentDeploymentID != nil {
		old, _ = st.GetAppDeployment(ctx, *app.CurrentDeploymentID)
	}
	// 2. Strategy. With volumes, the old one is stopped before starting the
	// new one: two instances of a database writing to the same volume would
	// corrupt it.
	recreate := len(app.Volumes) > 0
	if recreate && old != nil && old.Container != "" {
		fmt.Fprintln(log, "▶ The app has volumes: the current version is stopped before starting the new one (brief downtime)")
		if err := dc.StopContainer(ctx, old.Container, stopTimeout); err != nil && !docker.IsNotFound(err) {
			return fmt.Errorf("stopping the current version: %w", err)
		}
	}
	// If anything fails after stopping the old one, start it back up.
	restoreOld := func() {
		if recreate && old != nil && old.Container != "" && !app.Stopped {
			fmt.Fprintln(log, "▶ Starting the previous version back up")
			bg, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if err := dc.StartContainer(bg, old.Container); err != nil {
				fmt.Fprintf(log, "  it did not work: %v\n", err)
			}
		}
	}

	// 3. New container.
	name := ContainerName(app, d.ID)
	volumes := map[string]string{}
	for _, v := range app.Volumes {
		volumes[VolumeName(app, v)] = v.Path
	}
	envList := make([]string, 0, len(env))
	for k, v := range env {
		envList = append(envList, k+"="+v)
	}
	fmt.Fprintf(log, "▶ Starting %s\n", name)
	cid, err := dc.CreateAppContainer(ctx, docker.AppSpec{
		Name: name, Image: d.Image, Cmd: cmd, Env: envList, Network: m.network(app), Aliases: []string{app.Name},
		Volumes: volumes, MemoryMB: int64(app.MemoryMB), CPUs: app.CPUs,
		Labels: map[string]string{LabelApp: strconv.FormatInt(app.ID, 10), LabelDeployment: strconv.FormatInt(d.ID, 10)},
	})
	if err != nil {
		restoreOld()
		return fmt.Errorf("creating the container: %w", err)
	}
	d.Container = name
	_ = st.UpdateAppDeployment(ctx, d)
	fail := func(err error) error {
		m.dumpLogs(app, name, log)
		bg, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = dc.RemoveContainer(bg, cid)
		restoreOld()
		return err
	}
	if err := dc.StartContainer(ctx, cid); err != nil {
		return fail(fmt.Errorf("starting the container: %w", err))
	}

	// 4. Health.
	if err := m.waitHealthy(ctx, app, cid, log); err != nil {
		return fail(err)
	}
	if err := dc.KeepRunning(ctx, cid); err != nil {
		return fail(fmt.Errorf("enabling the automatic restart: %w", err))
	}

	// 5. Go live: the database first and then Traefik, which hands the
	// traffic over to the new container.
	if err := st.GoLive(context.WithoutCancel(ctx), app.ID, d.ID); err != nil {
		return fail(err)
	}
	if err := m.cfg.Apply(context.WithoutCancel(ctx)); err != nil {
		fmt.Fprintf(log, "⚠ Traefik could not be updated: %v\n", err)
	}

	// 6. Retire the previous version, giving Traefik a moment to reload
	// (without this, in-flight requests could fail).
	if old != nil && old.Container != "" {
		if !recreate {
			time.Sleep(3 * time.Second)
		}
		bg, cancel := context.WithTimeout(context.Background(), time.Minute)
		_ = dc.StopContainer(bg, old.Container, stopTimeout)
		_ = dc.RemoveContainer(bg, old.Container)
		cancel()
		fmt.Fprintf(log, "▶ Previous version (%s) retired\n", old.Container)
	}
	m.pruneImages(app)
	return nil
}

func (m *Manager) prepareImage(ctx context.Context, app *store.App, d *store.AppDeployment, log io.Writer) (string, error) {
	dc := m.Docker(app)
	if app.Source == store.AppSourceImage {
		image := docker.WithDefaultTag(app.Image)
		fmt.Fprintf(log, "▶ Pulling %s…\n", image)
		// Always pulled: ":latest" may have changed since the last deploy.
		if err := dc.PullImage(ctx, image, log); err != nil {
			if ok, _ := dc.ImageExists(ctx, image); !ok {
				return "", fmt.Errorf("pulling %s: %w", image, err)
			}
			fmt.Fprintf(log, "⚠ could not pull it (%v): the local copy is used\n", err)
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
	fmt.Fprintf(log, "▶ Building %s with %s\n", tag, dockerfile)
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(buildContext(rc, pw, app.RootDir)) }()
	err = dc.BuildImage(ctx, tag, dockerfile, pr, 0, log)
	pr.Close()
	if err != nil {
		return "", fmt.Errorf("the image build failed: %w", err)
	}
	return tag, nil
}

// fetchSource downloads the code of the commit to deploy (the latest one
// on the branch if the deployment does not carry one).
func (m *Manager) fetchSource(ctx context.Context, app *store.App, d *store.AppDeployment, log io.Writer) (io.ReadCloser, error) {
	if app.GitConnectionID == nil || app.Repo == "" {
		return nil, errors.New("the app has no repository configured")
	}
	conn, err := m.cfg.Store.GetGitConnection(ctx, *app.GitConnectionID)
	if err != nil {
		return nil, errors.New("the app's Git account no longer exists")
	}
	prov, err := m.cfg.Providers.Provider(ctx, conn)
	if err != nil {
		return nil, err
	}
	if d.CommitSHA == "" {
		c, err := prov.LatestCommit(ctx, app.Repo, app.Branch)
		if err != nil {
			return nil, fmt.Errorf("getting the latest commit: %w", err)
		}
		d.CommitSHA, d.CommitMessage = c.SHA, c.Message
		_ = m.cfg.Store.UpdateAppDeployment(ctx, d)
	}
	fmt.Fprintf(log, "▶ %s@%s (%s): %s\n", app.Repo, short(d.CommitSHA), app.Branch, d.CommitMessage)
	rc, err := prov.Tarball(ctx, app.Repo, d.CommitSHA)
	if err != nil {
		return nil, fmt.Errorf("downloading the code: %w", err)
	}
	return rc, nil
}

// waitHealthy waits until the container is ready to take traffic.
func (m *Manager) waitHealthy(ctx context.Context, app *store.App, id string, log io.Writer) error {
	dc := m.Docker(app)
	ctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()
	how := "the container stays up"
	switch {
	case app.Port > 0 && app.HealthPath != "":
		how = fmt.Sprintf("HTTP response on port %d%s", app.Port, app.HealthPath)
	case app.Port > 0:
		how = fmt.Sprintf("port %d accepts connections", app.Port)
	}
	fmt.Fprintf(log, "▶ Waiting until it is ready (%s)…\n", how)

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
		// The new container has no restart policy until it is ready, so a
		// crash leaves it stopped with its exit code. Just in case (an old
		// Docker, outside changes), a restart during the wait also counts as
		// a crash.
		crashed := !info.Running || info.Restarting || !info.StartedAt.Equal(firstStart)
		if crashed {
			reason := fmt.Sprintf("the container exited with code %d", info.ExitCode)
			if info.OOMKilled {
				reason += " (out of memory: raise the memory limit)"
			}
			return errors.New(reason)
		}
		ok := false
		switch {
		case info.Health != "":
			// The image defines its own HEALTHCHECK: that is the most reliable.
			if info.Health == "unhealthy" {
				return errors.New("the image's HEALTHCHECK reports it is not healthy")
			}
			ok = info.Health == "healthy"
		case app.Port > 0:
			ok = m.probe(ctx, ip(info, m.network(app)), app)
		default:
			if info.Running && runningSince.IsZero() {
				runningSince = time.Now()
			}
			ok = !runningSince.IsZero() && time.Since(runningSince) >= stableFor
		}
		if ok {
			fmt.Fprintln(log, "▶ Ready")
			return nil
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("it was not ready within %s (check: %s)", healthTimeout, how)
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

// probe checks whether the app is ready. The ones on a remote server are
// checked from that server: their IP is only reachable there.
func (m *Manager) probe(ctx context.Context, addr string, app *store.App) bool {
	if addr == "" {
		return false
	}
	hostport := net.JoinHostPort(addr, strconv.Itoa(app.Port))
	if app.ServerID != nil && m.cfg.RemoteProbe != nil {
		return m.cfg.RemoteProbe(ctx, *app.ServerID, hostport, app.HealthPath)
	}
	return probe.Ready(ctx, hostport, app.HealthPath)
}

// dumpLogs copies the container's last lines into the deployment log: they
// usually explain why it did not start.
func (m *Manager) dumpLogs(app *store.App, container string, log io.Writer) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	rc, err := m.Docker(app).Logs(ctx, container)
	if err != nil {
		return
	}
	defer rc.Close()
	fmt.Fprintln(log, "\n── Last lines of the container ──")
	_ = docker.Demux(io.LimitReader(rc, 64<<10), log)
	fmt.Fprintln(log, "─────────────────────────────────")
}

// pruneImages deletes the images built by old deployments, keeping those
// of the most recent ones so a rollback is still possible.
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
		_ = m.Docker(app).RemoveImage(ctx, d.Image)
	}
}

// Env decrypts an app's environment variables.
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

// buildContext turns the repository's .tar.gz into the build context: it
// drops the root folder and, with rootDir, keeps only that subfolder
// (monorepos).
func buildContext(gz io.Reader, w io.Writer, rootDir string) error {
	zr, err := gzip.NewReader(gz)
	if err != nil {
		return fmt.Errorf("the downloaded code is not a .tar.gz: %w", err)
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
			return fmt.Errorf("the code is larger than %d MiB", maxContextBytes>>20)
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

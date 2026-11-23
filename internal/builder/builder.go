// Package builder builds static sites from a GitHub or GitLab repository and
// publishes the result as a deployment.
//
// Every build runs in an isolated, throwaway container: unprivileged, with
// resource limits and with no access to the repository token (Dockfly
// downloads the code and copies it in).
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

// Build defaults, in the style of a Vite project.
const (
	DefaultImage   = "oven/bun:1"
	DefaultInstall = "bun install"
	DefaultBuild   = "bun run build"
	DefaultOutput  = "dist"
)

// LabelBuild marks the build containers, so they can be cleaned up if Dockfly
// restarts in the middle of one.
const LabelBuild = "dockfly.build"

const (
	maxSourceBytes = 1 << 30 // 1 GiB of source code
	MaxLogBytes    = 5 << 20 // 5 MiB of log per build or deploy
)

// Providers gives a client with valid credentials for a connection
// (implemented by gitauth.Resolver).
type Providers interface {
	Provider(ctx context.Context, conn *store.GitConnection) (gitprovider.Provider, error)
}

type Config struct {
	Store     *store.Store
	Docker    *docker.Client // nil if Docker is unavailable
	Secrets   *secrets.Box
	Providers Providers
	Pages     *pages.Storage
	LogDir    string
	Log       *slog.Logger
	// Publish activates a freshly created deployment (implemented by the API,
	// which knows how to regenerate Traefik and the static server).
	Publish func(ctx context.Context, siteID, deploymentID int64) error
	// OnFinish, if not nil, is called when each build finishes (notifications).
	OnFinish func(ctx context.Context, buildID int64)
	// StatusURL, if not nil, gives the link for the commit status: the
	// published site if the build succeeded (depID != 0), or the panel if not.
	StatusURL func(ctx context.Context, site *store.Site, b *store.Build, depID int64) string

	// Network and EgressProxy, if not empty, put the builds on an internal
	// network whose only way out is that proxy (see the egress package).
	Network     string
	EgressProxy string

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

// Run starts the workers and blocks until ctx is canceled. First it recovers
// the state left by a possible restart: builds that were running are marked
// as failed and the ones that were waiting go back into the queue.
func (m *Manager) Run(ctx context.Context) {
	if err := os.MkdirAll(m.cfg.LogDir, 0o755); err != nil {
		m.cfg.Log.Error("creating the log directory", "error", err)
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
		m.cfg.Log.Error("recovering builds", "error", err)
		return
	}
	for _, b := range running {
		_ = m.cfg.Store.FinishBuild(ctx, b.ID, store.BuildFailed, "interrupted by a Dockfly restart", nil)
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

// Enqueue creates a build and queues it. Builds already waiting for the same
// site (and the same preview) are canceled: only the newest commit is worth
// building. An empty preview means a production build.
func (m *Manager) Enqueue(ctx context.Context, siteID int64, trigger, branch, preview string, commit gitprovider.Commit) (*store.Build, error) {
	b := &store.Build{
		SiteID: siteID, Status: store.BuildQueued, Trigger: trigger, Branch: branch, Preview: preview,
		CommitSHA: commit.SHA, CommitMessage: commit.Message,
	}
	if err := m.cfg.Store.CreateBuild(ctx, b); err != nil {
		return nil, err
	}
	if _, err := m.cfg.Store.SupersedeQueued(ctx, siteID, b.ID); err != nil {
		m.cfg.Log.Error("canceling earlier builds", "site", siteID, "error", err)
	}
	select {
	case m.queue <- b.ID:
		return b, nil
	default:
		_ = m.cfg.Store.FinishBuild(ctx, b.ID, store.BuildFailed, "the build queue is full", nil)
		return nil, errors.New("the build queue is full; try again in a few minutes")
	}
}

// Cancel stops a running build or takes it out of the queue.
func (m *Manager) Cancel(ctx context.Context, buildID int64) error {
	m.mu.Lock()
	cancel, ok := m.running[buildID]
	m.mu.Unlock()
	if ok {
		cancel()
		return nil
	}
	err := m.cfg.Store.FinishBuild(ctx, buildID, store.BuildCanceled, "canceled", nil)
	if errors.Is(err, store.ErrNotFound) {
		return errors.New("the build already finished")
	}
	return err
}

var errCanceled = errors.New("canceled")

// minBuildFreeBytes is the minimum free disk space needed to start a build.
const minBuildFreeBytes = 2 << 30 // 2 GiB

func (m *Manager) run(parent context.Context, id int64) {
	st := m.cfg.Store
	// If it was canceled while waiting in the queue, MarkBuildStarted fails
	// and there is nothing to do.
	if err := st.MarkBuildStarted(parent, id); err != nil {
		return
	}
	// Do not start if the disk is nearly full: a build can write GiBs into the
	// container layer and the cache, and fill up the host.
	// shortcut: only looks at the filesystem holding LogDir (/data); it does not
	// cover /var/lib/docker on another disk. Move to a per-layer quota
	// (StorageOpt) once Docker's storage driver supports it (overlay2+xfs/pquota,
	// btrfs…).
	if free, err := freeDiskBytes(m.cfg.LogDir); err == nil && free < minBuildFreeBytes {
		_ = st.FinishBuild(parent, id, store.BuildFailed,
			fmt.Sprintf("not enough disk space: %d MiB free, at least %d MiB are needed", free>>20, minBuildFreeBytes>>20), nil)
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
		_ = st.FinishBuild(parent, id, store.BuildFailed, "could not create the log: "+err.Error(), nil)
		return
	}
	defer logFile.Close()
	logw := NewCappedWriter(logFile)

	start := time.Now()
	depID, err := m.pipeline(ctx, b, logw)

	// The cause tells a user cancellation apart from a timeout.
	status, msg := store.BuildSuccess, ""
	switch {
	case errors.Is(context.Cause(ctx), errCanceled):
		status, msg = store.BuildCanceled, "canceled"
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		status, msg = store.BuildFailed, fmt.Sprintf("exceeded the %s time limit", m.cfg.Timeout)
	case err != nil:
		status, msg = store.BuildFailed, err.Error()
	}
	if status == store.BuildSuccess {
		fmt.Fprintf(logw, "\n✔ Published in %s\n", time.Since(start).Round(time.Second))
	} else {
		fmt.Fprintf(logw, "\n✘ %s\n", msg)
	}
	var dep *int64
	if depID != 0 {
		dep = &depID
	}
	// The build is over: save the result even if ctx is already canceled.
	if err := st.FinishBuild(context.WithoutCancel(parent), id, status, msg, dep); err != nil &&
		!errors.Is(err, store.ErrNotFound) {
		m.cfg.Log.Error("saving the build result", "build", id, "error", err)
	}
	if m.cfg.OnFinish != nil && status != store.BuildCanceled {
		m.cfg.OnFinish(context.WithoutCancel(parent), id)
	}
	state, desc := gitprovider.StateSuccess, "Published"
	if b.Preview != "" {
		desc = "Preview published"
	}
	if status != store.BuildSuccess {
		state, desc = gitprovider.StateFailure, msg
		depID = 0
	}
	if fresh, err := st.GetBuild(context.WithoutCancel(parent), id); err == nil {
		m.report(context.WithoutCancel(parent), fresh, state, desc, depID)
	}
}

// report marks the build's commit on GitHub/GitLab. It is informational: if
// it fails (e.g. a GitHub App created before the statuses permission was
// requested), it is only logged.
func (m *Manager) report(ctx context.Context, b *store.Build, state, desc string, depID int64) {
	if b.CommitSHA == "" {
		return
	}
	site, err := m.cfg.Store.GetSite(ctx, b.SiteID)
	if err != nil {
		return
	}
	prov, _, err := m.provider(ctx, site)
	if err != nil {
		return
	}
	st := gitprovider.Status{State: state, Name: "dockfly/" + site.Name, Description: desc}
	if b.Preview != "" {
		st.Name += " (preview)"
	}
	if m.cfg.StatusURL != nil {
		st.URL = m.cfg.StatusURL(ctx, site, b, depID)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := prov.SetStatus(ctx, site.Repo, b.CommitSHA, st); err != nil {
		m.cfg.Log.Warn("could not set the commit status", "site", site.ID, "sha", short(b.CommitSHA), "error", err)
	}
}

// Env decrypts a site's build environment variables.
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
		return nil, "", errors.New("the site is not linked to any repository")
	}
	conn, err := m.cfg.Store.GetGitConnection(ctx, *site.GitConnectionID)
	if err != nil {
		return nil, "", fmt.Errorf("the account linked to %s no longer exists", site.Repo)
	}
	p, err := m.cfg.Providers.Provider(ctx, conn)
	return p, conn.Provider, err
}

// pipeline downloads the code, builds it and creates the deployment. It
// returns the ID of the published deployment.
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
		fmt.Fprintf(log, "▶ Looking up the latest commit on %s…\n", b.Branch)
		if commit, err = prov.LatestCommit(ctx, site.Repo, b.Branch); err != nil {
			return 0, fmt.Errorf("getting the latest commit: %w", err)
		}
		_ = st.SetBuildCommit(ctx, b.ID, commit.SHA, commit.Message)
	}
	fmt.Fprintf(log, "▶ %s@%s (%s): %s\n", site.Repo, short(commit.SHA), b.Branch, commit.Message)
	if b.Preview != "" {
		fmt.Fprintf(log, "▶ Preview of branch %s\n", b.Branch)
	}
	b.CommitSHA = commit.SHA
	m.report(ctx, b, gitprovider.StatePending, "Building…", 0)

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
		// No commands (plain HTML): published straight from the source, with
		// no container.
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
	fmt.Fprintf(log, "▶ Publishing %d files (%s)…\n", files, humanBytes(size))
	dep := &store.Deployment{
		SiteID: site.ID, Source: providerName, FileCount: files, TotalBytes: size,
		CommitSHA: commit.SHA, CommitMessage: commit.Message, Branch: b.Branch, Preview: b.Preview,
	}
	if err := st.CreateDeployment(ctx, dep); err != nil {
		return 0, err
	}
	if err := m.cfg.Pages.Commit(staging, site.ID, dep.ID); err != nil {
		_ = st.DeleteDeployment(ctx, site.ID, dep.ID)
		return 0, err
	}
	committed = true
	// Publishing must not be left half done if it is canceled right now.
	if err := m.cfg.Publish(context.WithoutCancel(ctx), site.ID, dep.ID); err != nil {
		return dep.ID, fmt.Errorf("publishing: %w", err)
	}
	return dep.ID, nil
}

func (m *Manager) publishSource(ctx context.Context, prov gitprovider.Provider, site *store.Site, sha string,
	staging *pages.Staging, log io.Writer) error {
	fmt.Fprintln(log, "▶ No build commands: the repository files are published as they are")
	rc, err := prov.Tarball(ctx, site.Repo, sha)
	if err != nil {
		return fmt.Errorf("downloading the code: %w", err)
	}
	defer rc.Close()
	gz, err := gzip.NewReader(rc)
	if err != nil {
		return fmt.Errorf("the downloaded code is not a .tar.gz: %w", err)
	}
	return staging.AddTar(gz, path.Join(site.RootDir, outputDir(site)))
}

func (m *Manager) buildInContainer(ctx context.Context, prov gitprovider.Provider, site *store.Site, b *store.Build,
	commit gitprovider.Commit, staging *pages.Staging, log io.Writer) error {
	dc := m.cfg.Docker
	if dc == nil {
		return errors.New("Docker is unavailable: mount /var/run/docker.sock to be able to run builds")
	}
	image := site.BuildImage
	if image == "" {
		image = DefaultImage
	}
	if ok, err := dc.ImageExists(ctx, docker.WithDefaultTag(image)); err != nil {
		return err
	} else if !ok {
		fmt.Fprintf(log, "▶ Pulling image %s…\n", image)
		if err := dc.PullImage(ctx, image, log); err != nil {
			return fmt.Errorf("pulling image %s: %w", image, err)
		}
	}

	env, err := Env(m.cfg.Secrets, site)
	if err != nil {
		return fmt.Errorf("reading the environment variables: %w", err)
	}
	envList := []string{"CI=true", "HOME=/tmp", "NO_COLOR=1",
		"DOCKFLY_COMMIT_SHA=" + commit.SHA, "DOCKFLY_BRANCH=" + b.Branch}
	for k, v := range env {
		envList = append(envList, k+"="+v)
	}
	if m.cfg.EgressProxy != "" {
		// Different tools read different ones: set them all.
		for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
			envList = append(envList, k+"="+m.cfg.EgressProxy)
		}
		envList = append(envList, "NO_PROXY=", "no_proxy=")
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
		Network:    m.cfg.Network,
	})
	if err != nil {
		return fmt.Errorf("creating the build container: %w", err)
	}
	defer func() {
		// With its own context: cleanup must happen even if the build was canceled.
		rmCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := dc.RemoveContainer(rmCtx, id); err != nil {
			m.cfg.Log.Error("removing the build container", "container", id, "error", err)
		}
	}()

	fmt.Fprintln(log, "▶ Downloading the code…")
	if err := m.copySource(ctx, prov, site.Repo, commit.SHA, id); err != nil {
		return err
	}

	fmt.Fprintf(log, "▶ Running the build in %s\n", image)
	if err := dc.StartContainer(ctx, id); err != nil {
		return fmt.Errorf("starting the build: %w", err)
	}
	logs, err := dc.Logs(ctx, id)
	if err != nil {
		return err
	}
	logsDone := make(chan struct{})
	go func() {
		defer close(logsDone)
		_ = docker.Demux(logs, log)
	}()
	code, err := dc.Wait(ctx, id)
	// On cancellation, closing the stream unblocks the log reader.
	if err != nil {
		logs.Close()
		<-logsDone
		return err
	}
	<-logsDone
	logs.Close()
	if code != 0 {
		return fmt.Errorf("the build exited with code %d", code)
	}

	out := path.Join(workdir, outputDir(site))
	rc, err := dc.GetArchive(ctx, id, out)
	if docker.IsNotFound(err) {
		return fmt.Errorf("output folder %q not found: check the site's output directory", outputDir(site))
	}
	if err != nil {
		return err
	}
	defer rc.Close()
	return staging.AddTar(rc, "")
}

// copySource downloads the code and streams it into the container without
// going through disk: GitHub → repack → Docker API.
func (m *Manager) copySource(ctx context.Context, prov gitprovider.Provider, repo, sha, containerID string) error {
	rc, err := prov.Tarball(ctx, repo, sha)
	if err != nil {
		return fmt.Errorf("downloading the code: %w", err)
	}
	defer rc.Close()

	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(repack(rc, pw, maxSourceBytes)) }()
	err = m.cfg.Docker.PutArchive(ctx, containerID, "/", pr)
	pr.Close()
	if err != nil {
		return fmt.Errorf("copying the code into the container: %w", err)
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

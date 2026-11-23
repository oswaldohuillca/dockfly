// Package selfupdate updates Dockfly from the panel.
//
// A container is tied to the image it was created with: restarting it after
// pulling the new image is not enough, it has to be recreated. Dockfly cannot
// do that itself (it would cut itself off halfway when it stops), so it
// launches a short-lived helper running the new image ("dockfly self-update")
// that stops the current container, creates an identical one with the new
// image and, if that does not come up healthy, rolls back to the old one.
package selfupdate

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dockfly/dockfly/internal/backup"
	"github.com/dockfly/dockfly/internal/docker"
)

// Repo is the GitHub repository holding the releases.
const Repo = "oswaldohuillca/dockfly"

// helperName is the update's helper container.
const helperName = "dockfly-updater"

// Why updating from the panel is not possible (the panel translates it).
const (
	ReasonNotInDocker = "not_in_docker" // development or a standalone binary
	ReasonDevVersion  = "dev_version"   // edge/dev: nothing to compare against
	ReasonPinnedTag   = "pinned_tag"    // image with a tag that is not a version
	ReasonNoSocket    = "no_socket"     // no way to hand Docker to the helper
)

// Release is the latest published version.
type Release struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	Notes   string `json:"notes"`
}

// Info is what the panel sees.
type Info struct {
	Current   string   `json:"current"`
	Latest    *Release `json:"latest"`
	Available bool     `json:"available"`
	CanUpdate bool     `json:"can_update"`
	Reason    string   `json:"reason,omitempty"`
	// State: "" (nothing in progress), backup, pulling, restarting, failed.
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}

type Updater struct {
	Docker  *docker.Client // nil without Docker
	DB      backup.Snapshotter
	DataDir string
	Version string
	// Socket is DOCKFLY_DOCKER_SOCKET (socket path or tcp://…).
	Socket string
	Log    *slog.Logger
	// API is the base of the GitHub API (the tests override it).
	API string

	mu       sync.Mutex
	latest   *Release
	cachedAt time.Time
	state    string
	err      string
}

func (u *Updater) api() string {
	if u.API != "" {
		return u.API
	}
	return "https://api.github.com"
}

// Latest queries the latest release (cached for an hour).
func (u *Updater) Latest(ctx context.Context) (*Release, error) {
	u.mu.Lock()
	if u.latest != nil && time.Since(u.cachedAt) < time.Hour {
		defer u.mu.Unlock()
		return u.latest, nil
	}
	u.mu.Unlock()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.api()+"/repos/"+Repo+"/releases/latest", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	c := http.Client{Timeout: 10 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub responded %s", resp.Status)
	}
	var rel struct {
		Tag  string `json:"tag_name"`
		URL  string `json:"html_url"`
		Body string `json:"body"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return nil, err
	}
	r := &Release{Version: strings.TrimPrefix(rel.Tag, "v"), URL: rel.URL, Notes: rel.Body}
	u.mu.Lock()
	u.latest, u.cachedAt = r, time.Now()
	u.mu.Unlock()
	return r, nil
}

// semver parses "1.2.3" (or "v1.2.3"); ok=false if it is not a version.
func semver(v string) (out [3]int, ok bool) {
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// newer reports whether latest is a later version than current.
func newer(current, latest string) bool {
	c, ok1 := semver(current)
	l, ok2 := semver(latest)
	if !ok1 || !ok2 {
		return false
	}
	for i := range 3 {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

// targetImage is the image to update to from ref (the current container's):
// with :latest, the same one (it is pulled again); with a version (:0.3.0),
// the new version. Other tags (:edge, a digest) are left alone.
func targetImage(ref, version string) (string, bool) {
	if strings.Contains(ref, "@") {
		return "", false
	}
	repo, tag := ref, "latest"
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		repo, tag = ref[:i], ref[i+1:]
	}
	if tag == "latest" {
		return repo + ":latest", true
	}
	if _, ok := semver(tag); ok {
		return repo + ":" + version, true
	}
	return "", false
}

// plan decides whether updating is possible and to which image.
func (u *Updater) plan(ctx context.Context, latest *Release) (image, reason string, self *docker.ContainerJSON) {
	if _, ok := semver(u.Version); !ok {
		return "", ReasonDevVersion, nil
	}
	host, _ := os.Hostname()
	if u.Docker == nil || host == "" {
		return "", ReasonNotInDocker, nil
	}
	self, err := u.Docker.Inspect(ctx, host)
	if err != nil {
		return "", ReasonNotInDocker, nil
	}
	ref, _ := self.Config["Image"].(string)
	image, ok := targetImage(ref, latest.Version)
	if !ok {
		return "", ReasonPinnedTag, nil
	}
	if !strings.HasPrefix(u.Socket, "tcp://") && u.socketSource(self) == "" {
		return "", ReasonNoSocket, nil
	}
	return image, "", self
}

// socketSource is the host path of the mounted Docker socket.
func (u *Updater) socketSource(self *docker.ContainerJSON) string {
	for _, m := range self.Mounts {
		if m.Destination == u.Socket && m.Type == "bind" {
			return m.Source
		}
	}
	return ""
}

// Info summarizes the state for the panel.
func (u *Updater) Info(ctx context.Context) Info {
	info := Info{Current: u.Version}
	u.mu.Lock()
	info.State, info.Error = u.state, u.err
	u.mu.Unlock()
	latest, err := u.Latest(ctx)
	if err != nil {
		info.Reason = ReasonNotInDocker
		if info.Error == "" {
			info.Error = "could not check the latest version: " + err.Error()
		}
		return info
	}
	info.Latest = latest
	info.Available = newer(u.Version, latest.Version)
	_, info.Reason, _ = u.plan(ctx, latest)
	info.CanUpdate = info.Available && info.Reason == ""
	return info
}

func (u *Updater) setState(state, err string) {
	u.mu.Lock()
	u.state, u.err = state, err
	u.mu.Unlock()
}

// ErrBusy: an update is already in progress.
var ErrBusy = errors.New("an update is already in progress")

// Start backs up /data, pulls the new image and launches the helper, all in
// the background. The panel follows the progress with Info.
func (u *Updater) Start(ctx context.Context) error {
	latest, err := u.Latest(ctx)
	if err != nil {
		return err
	}
	if !newer(u.Version, latest.Version) {
		return fmt.Errorf("you already have the latest version (%s)", u.Version)
	}
	image, reason, self := u.plan(ctx, latest)
	if reason != "" {
		return fmt.Errorf("updating from the panel is not possible (%s)", reason)
	}
	u.mu.Lock()
	if u.state != "" && u.state != "failed" {
		u.mu.Unlock()
		return ErrBusy
	}
	u.state, u.err = "backup", ""
	u.mu.Unlock()

	go func() {
		bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Minute)
		defer cancel()
		if err := u.run(bg, self, image); err != nil {
			u.Log.Error("updating Dockfly", "error", err)
			u.setState("failed", err.Error())
		}
	}()
	return nil
}

func (u *Updater) run(ctx context.Context, self *docker.ContainerJSON, image string) error {
	// Backup of /data in case it has to be rolled back by hand (the new
	// version's migrations do not undo themselves). Only the last one is kept.
	path := filepath.Join(u.DataDir, "pre-update.tar.gz")
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	err = backup.Create(ctx, u.DB, u.DataDir, u.Version, f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("backup: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}

	u.setState("pulling", "")
	if err := u.Docker.PullImage(ctx, image, io.Discard); err != nil {
		return fmt.Errorf("pulling %s: %w", image, err)
	}

	u.setState("restarting", "")
	spec := docker.ContainerSpec{
		Name: helperName, Image: image, Cmd: []string{"self-update", self.ID, image},
		Labels: map[string]string{"dockfly.role": "updater"}, NoHealthcheck: true,
	}
	if strings.HasPrefix(u.Socket, "tcp://") {
		// Behind a socket proxy: the helper reaches it over the same network.
		spec.Env = []string{"DOCKFLY_DOCKER_SOCKET=" + u.Socket}
		for name := range self.NetworkSettings.Networks {
			spec.Network = name
			break
		}
	} else {
		spec.Binds = []string{u.socketSource(self) + ":/var/run/docker.sock"}
		spec.Env = []string{"DOCKFLY_DOCKER_SOCKET=/var/run/docker.sock"}
	}
	if err := u.Docker.RemoveContainerKeepVolumes(ctx, helperName); err != nil {
		return err
	}
	id, err := u.Docker.CreateContainer(ctx, spec)
	if err != nil {
		return fmt.Errorf("creating the helper: %w", err)
	}
	if err := u.Docker.StartContainer(ctx, id); err != nil {
		return fmt.Errorf("starting the helper: %w", err)
	}
	return nil // the helper will stop this container in a few seconds
}

// Cleanup, on startup, removes the helper of a previous update once it
// finishes (the container starting up is precisely the one the helper created
// or restarted, so the helper may still be alive for a few seconds). If that
// update failed and rolled back, it leaves the reason for the panel.
func (u *Updater) Cleanup(ctx context.Context) {
	if u.Docker == nil {
		return
	}
	var info *docker.ContainerJSON
	for range 150 { // up to 5 minutes
		var err error
		if info, err = u.Docker.Inspect(ctx, helperName); err != nil {
			return // there is no helper
		}
		if !info.State.Running {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
	if info.State.Running {
		return
	}
	if info.State.ExitCode != 0 {
		msg := "the update failed and rolled back to the previous version"
		if logs, err := u.Docker.Logs(ctx, helperName); err == nil {
			var out strings.Builder
			_ = docker.Demux(io.LimitReader(logs, 1<<20), &out)
			logs.Close()
			if last := lastLine(out.String()); last != "" {
				msg += ": " + last
			}
		}
		u.setState("failed", msg)
	}
	_ = u.Docker.RemoveContainerKeepVolumes(ctx, helperName)
}

func lastLine(s string) string {
	var last string
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" {
			last = l
		}
	}
	return last
}

// Helper is the helper itself ("dockfly self-update <container> <image>"):
// it stops the Dockfly container, creates an identical one with the new
// image and waits for it to be healthy. If anything fails, it rolls back.
func Helper(ctx context.Context, dc *docker.Client, target, image string, log *slog.Logger) error {
	old, err := dc.Inspect(ctx, target)
	if err != nil {
		return err
	}
	name := strings.TrimPrefix(old.Name, "/")
	// Give the panel time to answer before stopping it.
	time.Sleep(3 * time.Second)
	log.Info("stopping Dockfly", "container", name)
	if err := dc.StopContainer(ctx, old.ID, 30*time.Second); err != nil {
		return fmt.Errorf("stopping %s: %w", name, err)
	}
	if err := dc.RenameContainer(ctx, old.ID, name+"-pre-update"); err != nil {
		_ = dc.StartContainer(ctx, old.ID)
		return fmt.Errorf("renaming %s: %w", name, err)
	}
	var newID string
	rollback := func(cause error) error {
		log.Error("the new version did not start properly: rolling back to the previous one", "error", cause)
		if newID != "" {
			_ = dc.RemoveContainerKeepVolumes(ctx, newID)
		}
		_ = dc.RenameContainer(ctx, old.ID, name)
		if err := dc.StartContainer(ctx, old.ID); err != nil {
			return fmt.Errorf("%v; and the previous version could not be started again: %w", cause, err)
		}
		return cause
	}
	log.Info("creating Dockfly with the new image", "image", image)
	if newID, err = dc.Clone(ctx, old, name, image); err != nil {
		return rollback(fmt.Errorf("creating the new container: %w", err))
	}
	if err := dc.StartContainer(ctx, newID); err != nil {
		return rollback(fmt.Errorf("starting the new container: %w", err))
	}
	if err := dc.WaitHealthy(ctx, newID, 3*time.Minute); err != nil {
		return rollback(fmt.Errorf("the new version failed: %w", err))
	}
	log.Info("Dockfly updated", "image", image)
	return dc.RemoveContainerKeepVolumes(ctx, old.ID)
}

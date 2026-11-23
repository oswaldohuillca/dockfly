package builder

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/dockfly/dockfly/internal/docker"
)

// cacheDir is where each site's dependency cache is mounted.
const cacheDir = "/cache"

// cacheEnv points every package manager's cache at the volume, so that the
// second build does not download all the dependencies again.
var cacheEnv = []string{
	"BUN_INSTALL_CACHE_DIR=" + cacheDir + "/bun",
	"npm_config_cache=" + cacheDir + "/npm",
	"YARN_CACHE_FOLDER=" + cacheDir + "/yarn",
	"npm_config_store_dir=" + cacheDir + "/pnpm", // pnpm
	"XDG_CACHE_HOME=" + cacheDir + "/xdg",
}

// CacheVolume is the Docker volume holding a site's cache.
func CacheVolume(siteID int64) string {
	return "dockfly-build-cache-" + strconv.FormatInt(siteID, 10)
}

// ensureCache creates the cache volume if it does not exist and hands it over
// to the build user: Docker creates new volumes as root, and the build (which
// is unprivileged) would not be able to write in it. Returns false if it
// could not be prepared; the build then goes on without a cache.
func (m *Manager) ensureCache(ctx context.Context, siteID int64, image string, log io.Writer) bool {
	dc := m.cfg.Docker
	name := CacheVolume(siteID)
	exists, err := dc.VolumeExists(ctx, name)
	if err != nil {
		fmt.Fprintf(log, "▶ No dependency cache: %v\n", err)
		return false
	}
	if exists {
		return true
	}
	if err := dc.CreateVolume(ctx, name, map[string]string{"dockfly.cache": strconv.FormatInt(siteID, 10)}); err != nil {
		fmt.Fprintf(log, "▶ No dependency cache: %v\n", err)
		return false
	}
	// Helper container with the same image (already pulled): all it does is a
	// chown, it never runs any repository code.
	id, err := dc.CreateContainer(ctx, docker.ContainerSpec{
		Image: image, User: "0:0", KeepCaps: true,
		Cmd:      []string{"chown", fmt.Sprintf("%d:%d", buildUID, buildUID), cacheDir},
		Volumes:  map[string]string{name: cacheDir},
		Labels:   map[string]string{LabelBuild: "cache"},
		MemoryMB: 64, CPUs: 0.5, PidsLimit: 16,
	})
	if err == nil {
		defer func() {
			rm, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = dc.RemoveContainer(rm, id)
		}()
		if err = dc.StartContainer(ctx, id); err == nil {
			var code int
			if code, err = dc.Wait(ctx, id); err == nil && code != 0 {
				err = fmt.Errorf("chown exited with code %d", code)
			}
		}
	}
	if err != nil {
		_ = dc.RemoveVolume(ctx, name)
		fmt.Fprintf(log, "▶ No dependency cache (could not prepare the volume: %v)\n", err)
		return false
	}
	fmt.Fprintln(log, "▶ Dependency cache created: the next builds will be faster")
	return true
}

// ClearCache deletes a site's dependency cache.
func (m *Manager) ClearCache(ctx context.Context, siteID int64) error {
	if m.cfg.Docker == nil {
		return nil
	}
	return m.cfg.Docker.RemoveVolume(ctx, CacheVolume(siteID))
}

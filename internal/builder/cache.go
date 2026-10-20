package builder

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/dockfly/dockfly/internal/docker"
)

// cacheDir es donde se monta la caché de dependencias de cada sitio.
const cacheDir = "/cache"

// cacheEnv apunta la caché de cada gestor de paquetes al volumen. Así el
// segundo build no vuelve a descargar todas las dependencias.
var cacheEnv = []string{
	"BUN_INSTALL_CACHE_DIR=" + cacheDir + "/bun",
	"npm_config_cache=" + cacheDir + "/npm",
	"YARN_CACHE_FOLDER=" + cacheDir + "/yarn",
	"npm_config_store_dir=" + cacheDir + "/pnpm", // pnpm
	"XDG_CACHE_HOME=" + cacheDir + "/xdg",
}

// CacheVolume es el volumen de Docker con la caché de un sitio.
func CacheVolume(siteID int64) string {
	return "dockfly-build-cache-" + strconv.FormatInt(siteID, 10)
}

// ensureCache crea el volumen de caché si no existe y se lo cede al usuario
// del build: Docker crea los volúmenes nuevos como root, y el build (sin
// privilegios) no podría escribir en él. Devuelve false si no se pudo
// preparar; el build sigue entonces sin caché.
func (m *Manager) ensureCache(ctx context.Context, siteID int64, image string, log io.Writer) bool {
	dc := m.cfg.Docker
	name := CacheVolume(siteID)
	exists, err := dc.VolumeExists(ctx, name)
	if err != nil {
		fmt.Fprintf(log, "▶ Sin caché de dependencias: %v\n", err)
		return false
	}
	if exists {
		return true
	}
	if err := dc.CreateVolume(ctx, name, map[string]string{"dockfly.cache": strconv.FormatInt(siteID, 10)}); err != nil {
		fmt.Fprintf(log, "▶ Sin caché de dependencias: %v\n", err)
		return false
	}
	// Contenedor auxiliar con la misma imagen (ya descargada): solo hace
	// chown, no ejecuta código del repositorio.
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
				err = fmt.Errorf("chown terminó con código %d", code)
			}
		}
	}
	if err != nil {
		_ = dc.RemoveVolume(ctx, name)
		fmt.Fprintf(log, "▶ Sin caché de dependencias (no se pudo preparar el volumen: %v)\n", err)
		return false
	}
	fmt.Fprintln(log, "▶ Caché de dependencias creada: los próximos builds serán más rápidos")
	return true
}

// ClearCache borra la caché de dependencias de un sitio.
func (m *Manager) ClearCache(ctx context.Context, siteID int64) error {
	if m.cfg.Docker == nil {
		return nil
	}
	return m.cfg.Docker.RemoveVolume(ctx, CacheVolume(siteID))
}

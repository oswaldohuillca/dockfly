// Package selfupdate actualiza Dockfly desde el panel.
//
// Un contenedor queda atado a la imagen con la que se creó: reiniciarlo tras
// descargar la nueva no basta, hay que recrearlo. Dockfly no puede hacerlo él
// mismo (al pararse se cortaría a medias), así que lanza un auxiliar de corta
// vida con la imagen nueva ("dockfly self-update") que para el contenedor
// actual, crea uno igual con la imagen nueva y, si no arranca sano, vuelve al
// anterior.
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

// Repo es el repositorio de GitHub de las releases.
const Repo = "oswaldohuillca/dockfly"

// helperName es el contenedor auxiliar de la actualización.
const helperName = "dockfly-updater"

// Por qué no se puede actualizar desde el panel (el panel lo traduce).
const (
	ReasonNotInDocker = "not_in_docker" // desarrollo o binario suelto
	ReasonDevVersion  = "dev_version"   // edge/dev: no hay con qué comparar
	ReasonPinnedTag   = "pinned_tag"    // imagen con una etiqueta que no es una versión
	ReasonNoSocket    = "no_socket"     // no se sabe cómo pasar Docker al auxiliar
)

// Release es la última versión publicada.
type Release struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	Notes   string `json:"notes"`
}

// Info es lo que ve el panel.
type Info struct {
	Current   string   `json:"current"`
	Latest    *Release `json:"latest"`
	Available bool     `json:"available"`
	CanUpdate bool     `json:"can_update"`
	Reason    string   `json:"reason,omitempty"`
	// State: "" (nada en marcha), backup, pulling, restarting, failed.
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}

type Updater struct {
	Docker  *docker.Client // nil sin Docker
	DB      backup.Snapshotter
	DataDir string
	Version string
	// Socket es DOCKFLY_DOCKER_SOCKET (ruta del socket o tcp://…).
	Socket string
	Log    *slog.Logger
	// API es la base de la API de GitHub (los tests la cambian).
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

// Latest consulta la última release (con una hora de caché).
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
		return nil, fmt.Errorf("GitHub respondió %s", resp.Status)
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

// semver lee "1.2.3" (o "v1.2.3"); ok=false si no es una versión.
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

// newer indica si latest es una versión posterior a current.
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

// targetImage es la imagen a la que actualizar desde ref (la del contenedor
// actual): con :latest, la misma (se vuelve a descargar); con una versión
// (:0.3.0), la nueva versión. Otras etiquetas (:edge, un digest) no se tocan.
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

// plan decide si se puede actualizar y a qué imagen.
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

// socketSource es la ruta en el host del socket de Docker montado.
func (u *Updater) socketSource(self *docker.ContainerJSON) string {
	for _, m := range self.Mounts {
		if m.Destination == u.Socket && m.Type == "bind" {
			return m.Source
		}
	}
	return ""
}

// Info resume el estado para el panel.
func (u *Updater) Info(ctx context.Context) Info {
	info := Info{Current: u.Version}
	u.mu.Lock()
	info.State, info.Error = u.state, u.err
	u.mu.Unlock()
	latest, err := u.Latest(ctx)
	if err != nil {
		info.Reason = ReasonNotInDocker
		if info.Error == "" {
			info.Error = "no se pudo consultar la última versión: " + err.Error()
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

// ErrBusy: ya hay una actualización en marcha.
var ErrBusy = errors.New("ya hay una actualización en marcha")

// Start hace una copia de /data, descarga la imagen nueva y lanza el
// auxiliar, en segundo plano. El panel sigue el progreso con Info.
func (u *Updater) Start(ctx context.Context) error {
	latest, err := u.Latest(ctx)
	if err != nil {
		return err
	}
	if !newer(u.Version, latest.Version) {
		return fmt.Errorf("ya tienes la última versión (%s)", u.Version)
	}
	image, reason, self := u.plan(ctx, latest)
	if reason != "" {
		return fmt.Errorf("no se puede actualizar desde el panel (%s)", reason)
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
			u.Log.Error("actualizando Dockfly", "error", err)
			u.setState("failed", err.Error())
		}
	}()
	return nil
}

func (u *Updater) run(ctx context.Context, self *docker.ContainerJSON, image string) error {
	// Copia de /data por si hubiera que volver atrás a mano (las migraciones
	// de la versión nueva no se deshacen solas). Solo se guarda la última.
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
		return fmt.Errorf("copia de seguridad: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}

	u.setState("pulling", "")
	if err := u.Docker.PullImage(ctx, image, io.Discard); err != nil {
		return fmt.Errorf("descargando %s: %w", image, err)
	}

	u.setState("restarting", "")
	spec := docker.ContainerSpec{
		Name: helperName, Image: image, Cmd: []string{"self-update", self.ID, image},
		Labels: map[string]string{"dockfly.role": "updater"}, NoHealthcheck: true,
	}
	if strings.HasPrefix(u.Socket, "tcp://") {
		// Detrás de un proxy del socket: el auxiliar llega por la misma red.
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
		return fmt.Errorf("creando el auxiliar: %w", err)
	}
	if err := u.Docker.StartContainer(ctx, id); err != nil {
		return fmt.Errorf("iniciando el auxiliar: %w", err)
	}
	return nil // el auxiliar parará este contenedor en unos segundos
}

// Cleanup, al arrancar, borra el auxiliar de una actualización anterior
// cuando termina (quien arranca es justo el contenedor que él creó o volvió
// a arrancar, así que puede seguir vivo unos segundos). Si esa actualización
// falló y volvió a la versión anterior, deja el motivo para el panel.
func (u *Updater) Cleanup(ctx context.Context) {
	if u.Docker == nil {
		return
	}
	var info *docker.ContainerJSON
	for range 150 { // hasta 5 minutos
		var err error
		if info, err = u.Docker.Inspect(ctx, helperName); err != nil {
			return // no hay auxiliar
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
		msg := "la actualización falló y se volvió a la versión anterior"
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

// Helper es el auxiliar ("dockfly self-update <contenedor> <imagen>"): para
// el contenedor de Dockfly, crea uno igual con la imagen nueva y espera a
// que esté sano. Si algo falla, vuelve al anterior.
func Helper(ctx context.Context, dc *docker.Client, target, image string, log *slog.Logger) error {
	old, err := dc.Inspect(ctx, target)
	if err != nil {
		return err
	}
	name := strings.TrimPrefix(old.Name, "/")
	// Que el panel tenga tiempo de responder antes de pararlo.
	time.Sleep(3 * time.Second)
	log.Info("parando Dockfly", "contenedor", name)
	if err := dc.StopContainer(ctx, old.ID, 30*time.Second); err != nil {
		return fmt.Errorf("parando %s: %w", name, err)
	}
	if err := dc.RenameContainer(ctx, old.ID, name+"-pre-update"); err != nil {
		_ = dc.StartContainer(ctx, old.ID)
		return fmt.Errorf("renombrando %s: %w", name, err)
	}
	var newID string
	rollback := func(cause error) error {
		log.Error("la versión nueva no arrancó bien: se vuelve a la anterior", "error", cause)
		if newID != "" {
			_ = dc.RemoveContainerKeepVolumes(ctx, newID)
		}
		_ = dc.RenameContainer(ctx, old.ID, name)
		if err := dc.StartContainer(ctx, old.ID); err != nil {
			return fmt.Errorf("%v; y no se pudo volver a arrancar la versión anterior: %w", cause, err)
		}
		return cause
	}
	log.Info("creando Dockfly con la imagen nueva", "imagen", image)
	if newID, err = dc.Clone(ctx, old, name, image); err != nil {
		return rollback(fmt.Errorf("creando el contenedor nuevo: %w", err))
	}
	if err := dc.StartContainer(ctx, newID); err != nil {
		return rollback(fmt.Errorf("arrancando el contenedor nuevo: %w", err))
	}
	if err := dc.WaitHealthy(ctx, newID, 3*time.Minute); err != nil {
		return rollback(fmt.Errorf("la versión nueva %w", err))
	}
	log.Info("Dockfly actualizado", "imagen", image)
	return dc.RemoveContainerKeepVolumes(ctx, old.ID)
}

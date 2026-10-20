package apps

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/dockfly/dockfly/internal/docker"
	"github.com/dockfly/dockfly/internal/store"
)

// Las apps desde docker-compose.yml se despliegan con un contenedor
// auxiliar efímero que tiene la CLI de Docker y Compose (la imagen de
// Dockfly es scratch y no los incluye) y habla con el Docker del host.

// ComposeImage es la imagen oficial con la CLI de Docker y Compose.
const ComposeImage = "docker:cli"

const (
	overrideFile   = ".dockfly.override.yml"
	labelHelper    = "dockfly.app.helper"
	composeTimeout = 10 * time.Minute
	composeProject = "com.docker.compose.project"
	composeService = "com.docker.compose.service"
)

// ComposeProject es el nombre del proyecto de Compose de una app.
func ComposeProject(app *store.App) string { return "dockfly-" + app.Name }

func (m *Manager) deployCompose(ctx context.Context, app *store.App, d *store.AppDeployment, log io.Writer) error {
	if app.ServerID != nil {
		return errors.New("las apps de Compose solo se pueden desplegar en el servidor principal")
	}
	dc := m.cfg.Docker
	rc, err := m.fetchSource(ctx, app, d, log)
	if err != nil {
		return err
	}
	defer rc.Close()

	file := app.ComposeFile
	if file == "" {
		file = "docker-compose.yml"
	}
	env, err := Env(m.cfg.Secrets, app)
	if err != nil {
		return fmt.Errorf("leyendo las variables de entorno: %w", err)
	}
	envList := make([]string, 0, len(env))
	for k, v := range env {
		envList = append(envList, k+"="+v)
	}

	// El código va al contenedor auxiliar en /project, junto con el override
	// que conecta el servicio web a la red de Dockfly.
	var composeYAML []byte
	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(projectArchive(rc, pw, app.RootDir, file, &composeYAML, overrideYAML(m.cfg.Network, app.ComposeService)))
	}()

	if err := m.ensureImage(ctx, ComposeImage, log); err != nil {
		pr.Close()
		return err
	}
	cmd := []string{"docker", "compose", "-p", ComposeProject(app), "-f", file, "-f", overrideFile,
		"up", "-d", "--build", "--remove-orphans", "--wait", "--wait-timeout", "180"}
	fmt.Fprintf(log, "▶ docker compose -f %s up -d --build --wait\n", file)
	err = m.runHelper(ctx, app, cmd, envList, pr, log)
	pr.Close()
	for _, w := range relativeBinds(composeYAML) {
		fmt.Fprintf(log, "⚠ %s\n", w)
	}
	if err != nil {
		return err
	}

	// El contenedor del servicio web es el que recibe el tráfico.
	if app.ComposeService != "" {
		name, err := m.serviceContainer(ctx, app, app.ComposeService)
		if err != nil {
			return err
		}
		d.Container = name
		_ = m.cfg.Store.UpdateAppDeployment(ctx, d)
	}
	d.Image = "compose: " + file
	_ = m.cfg.Store.UpdateAppDeployment(ctx, d)
	if err := m.cfg.Store.GoLive(context.WithoutCancel(ctx), app.ID, d.ID); err != nil {
		return err
	}
	if err := m.cfg.Apply(context.WithoutCancel(ctx)); err != nil {
		fmt.Fprintf(log, "⚠ no se pudo actualizar Traefik: %v\n", err)
	}
	_ = dc // el resto lo gestiona Compose
	return nil
}

// runHelper ejecuta un comando en el contenedor auxiliar de Compose y, si
// archive no es nil, con el proyecto en /project. El auxiliar habla con
// Docker a través del proxy con permisos mínimos (solo su proyecto, nada de
// contenedores privilegiados) o, sin proxy, con el socket del host.
func (m *Manager) runHelper(ctx context.Context, app *store.App, cmd, env []string, archive io.Reader, log io.Writer) error {
	dc := m.cfg.Docker
	ctx, cancel := context.WithTimeout(ctx, composeTimeout)
	defer cancel()
	spec := docker.ContainerSpec{
		Name:       fmt.Sprintf("dockfly-compose-%s-%d", app.Name, time.Now().UnixNano()),
		Image:      ComposeImage,
		Cmd:        cmd,
		Env:        env,
		WorkingDir: "/project",
		Labels:     map[string]string{labelHelper: app.Name},
	}
	if m.cfg.ComposeProxy != nil {
		sess, err := m.proxySession(ctx, app)
		if err != nil {
			return fmt.Errorf("preparando el proxy de Docker: %w", err)
		}
		defer sess.close()
		spec.Network = sess.network
		spec.Env = append(slices.Clone(env), "DOCKER_HOST=tcp://"+sess.addr)
	} else {
		socket := m.cfg.HostDockerSocket
		if socket == "" {
			socket = "/var/run/docker.sock"
		}
		spec.KeepCaps = true
		spec.Binds = []string{socket + ":/var/run/docker.sock"}
	}
	id, err := dc.CreateContainer(ctx, spec)
	if err != nil {
		return fmt.Errorf("creando el contenedor de Compose: %w", err)
	}
	defer func() {
		bg, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = dc.RemoveContainer(bg, id)
	}()
	if archive != nil {
		if err := dc.PutArchive(ctx, id, "/", archive); err != nil {
			return fmt.Errorf("copiando el proyecto: %w", err)
		}
	}
	if err := dc.StartContainer(ctx, id); err != nil {
		return err
	}
	logs, err := dc.Logs(ctx, id)
	if err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = docker.Demux(logs, log)
	}()
	code, err := dc.Wait(ctx, id)
	logs.Close()
	<-done
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("docker compose terminó con código %d", code)
	}
	return nil
}

// proxySession es la conexión de una ejecución del auxiliar con el proxy:
// una red interna en la que solo están el auxiliar y Dockfly, y un servidor
// que escucha únicamente en la IP de Dockfly en esa red. Quien llega a él
// es, por fuerza, el auxiliar de este proyecto.
type proxySession struct {
	network, addr string
	close         func()
}

func (m *Manager) proxySession(ctx context.Context, app *store.App) (*proxySession, error) {
	dc, self := m.cfg.Docker, m.cfg.SelfContainer
	network := fmt.Sprintf("dockfly-compose-%s-%d", app.Name, time.Now().UnixNano())
	if err := dc.CreateInternalNetwork(ctx, network, map[string]string{labelHelper: app.Name}); err != nil {
		return nil, err
	}
	cleanup := func() {
		bg, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = dc.DisconnectNetwork(bg, network, self)
		if err := dc.RemoveNetwork(bg, network); err != nil {
			m.cfg.Log.Warn("borrando la red del auxiliar de Compose", "red", network, "error", err)
		}
	}
	if err := dc.ConnectNetwork(ctx, network, self); err != nil {
		cleanup()
		return nil, err
	}
	ip, err := dc.ContainerIP(ctx, self, network)
	if err != nil {
		cleanup()
		return nil, err
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(ip, "2375"))
	if err != nil {
		cleanup()
		return nil, err
	}
	srv := &http.Server{Handler: m.cfg.ComposeProxy.Handler(ComposeProject(app)), ReadHeaderTimeout: 30 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	return &proxySession{network: network, addr: ln.Addr().String(), close: func() {
		_ = srv.Close()
		cleanup()
	}}, nil
}

// CleanupProxyNetworks borra las redes de auxiliares que quedaran de una
// ejecución interrumpida (p. ej. si Dockfly se reinició a mitad).
func (m *Manager) CleanupProxyNetworks(ctx context.Context) {
	if m.cfg.ComposeProxy == nil {
		return
	}
	names, err := m.cfg.Docker.NetworksWithLabel(ctx, labelHelper)
	if err != nil {
		return
	}
	for _, n := range names {
		_ = m.cfg.Docker.DisconnectNetwork(ctx, n, m.cfg.SelfContainer)
		_ = m.cfg.Docker.RemoveNetwork(ctx, n)
	}
}

func (m *Manager) ensureImage(ctx context.Context, image string, log io.Writer) error {
	ok, err := m.cfg.Docker.ImageExists(ctx, image)
	if err != nil || ok {
		return err
	}
	fmt.Fprintf(log, "▶ Descargando %s…\n", image)
	return m.cfg.Docker.PullImage(ctx, image, log)
}

// serviceContainer devuelve el nombre del contenedor de un servicio.
func (m *Manager) serviceContainer(ctx context.Context, app *store.App, service string) (string, error) {
	ids, err := m.cfg.Docker.ContainersWithLabel(ctx, composeService+"="+service)
	if err != nil {
		return "", err
	}
	for _, id := range ids {
		info, err := m.cfg.Docker.InspectContainer(ctx, id)
		if err == nil && info.Labels[composeProject] == ComposeProject(app) && info.Running {
			return info.Name, nil
		}
	}
	return "", fmt.Errorf("no hay ningún contenedor en marcha del servicio %q: revisa el nombre del servicio web", service)
}

// projectContainers devuelve los contenedores de un proyecto de Compose.
func (m *Manager) projectContainers(ctx context.Context, app *store.App) ([]string, error) {
	return m.cfg.Docker.ContainersWithLabel(ctx, composeProject+"="+ComposeProject(app))
}

// overrideYAML conecta el servicio web a la red de Dockfly (para que
// Traefik llegue a él) sin tocar el docker-compose.yml del usuario.
func overrideYAML(network, service string) []byte {
	doc := map[string]any{}
	if network != "" && service != "" {
		doc["networks"] = map[string]any{"dockfly": map[string]any{"external": true, "name": network}}
		doc["services"] = map[string]any{service: map[string]any{"networks": []string{"default", "dockfly"}}}
	}
	out, _ := yaml.Marshal(doc)
	return out
}

// projectArchive escribe el proyecto como tar bajo project/, añade el
// override y guarda en composeYAML el contenido del docker-compose.yml.
func projectArchive(gz io.Reader, w io.Writer, rootDir, composeFile string, composeYAML *[]byte, override []byte) error {
	zr, err := gzip.NewReader(gz)
	if err != nil {
		return fmt.Errorf("el código descargado no es un .tar.gz: %w", err)
	}
	prefix := strings.Trim(path.Clean("/"+rootDir), "/")
	tr, tw := tar.NewReader(zr), tar.NewWriter(w)
	tw.WriteHeader(&tar.Header{Name: "project/", Typeflag: tar.TypeDir, Mode: 0o755})
	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeDir && hdr.Typeflag != tar.TypeSymlink {
			continue
		}
		_, rest, _ := strings.Cut(hdr.Name, "/")
		rest = strings.TrimPrefix(path.Clean("/"+rest), "/")
		if prefix != "" {
			var ok bool
			if rest, ok = strings.CutPrefix(rest, prefix+"/"); !ok {
				continue
			}
		}
		if rest == "" || rest == overrideFile {
			continue
		}
		if total += hdr.Size; total > maxContextBytes {
			return fmt.Errorf("el código supera %d MiB", maxContextBytes>>20)
		}
		out := *hdr
		out.Name = "project/" + rest
		if hdr.Typeflag == tar.TypeDir {
			out.Name += "/"
		}
		out.PAXRecords, out.Format = nil, tar.FormatUnknown
		if err := tw.WriteHeader(&out); err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		var dst io.Writer = tw
		var buf strings.Builder
		if rest == composeFile && hdr.Size < 1<<20 {
			dst = io.MultiWriter(tw, &buf)
		}
		if _, err := io.Copy(dst, tr); err != nil {
			return err
		}
		if buf.Len() > 0 {
			*composeYAML = []byte(buf.String())
		}
	}
	if err := tw.WriteHeader(&tar.Header{Name: "project/" + overrideFile, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(override))}); err != nil {
		return err
	}
	if _, err := tw.Write(override); err != nil {
		return err
	}
	return tw.Close()
}

// relativeBinds detecta bind mounts con rutas relativas al proyecto
// ("./datos:/datos"): Compose corre en el contenedor auxiliar y esas rutas
// no existen en el host, así que no funcionarían como se espera.
func relativeBinds(composeYAML []byte) []string {
	var doc struct {
		Services map[string]struct {
			Volumes []any `yaml:"volumes"`
		} `yaml:"services"`
	}
	if yaml.Unmarshal(composeYAML, &doc) != nil {
		return nil
	}
	var out []string
	for name, svc := range doc.Services {
		for _, v := range svc.Volumes {
			src := ""
			switch v := v.(type) {
			case string:
				src, _, _ = strings.Cut(v, ":")
			case map[string]any:
				src, _ = v["source"].(string)
			}
			if strings.HasPrefix(src, ".") || strings.HasPrefix(src, "~") {
				out = append(out, fmt.Sprintf(
					"el servicio %s monta %q, una ruta relativa: no funcionará en Dockfly. Usa un volumen con nombre (p. ej. datos:/ruta).",
					name, src))
			}
		}
	}
	return out
}

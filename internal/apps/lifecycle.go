package apps

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/dockfly/dockfly/internal/store"
)

// current devuelve el contenedor que sirve la app ahora mismo.
func (m *Manager) current(ctx context.Context, app *store.App) (string, error) {
	if app.CurrentDeploymentID == nil {
		return "", errors.New("la app todavía no se ha desplegado")
	}
	d, err := m.cfg.Store.GetAppDeployment(ctx, *app.CurrentDeploymentID)
	if err != nil {
		return "", err
	}
	return d.Container, nil
}

// Stop detiene la app (queda detenida aunque Dockfly o el servidor se
// reinicien) y deja de enrutar su tráfico.
func (m *Manager) Stop(ctx context.Context, app *store.App) error {
	defer m.lockApp(app.ID)()
	names, err := m.containers(ctx, app)
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := m.cfg.Docker.StopContainer(ctx, name, stopTimeout); err != nil {
			return err
		}
	}
	if err := m.cfg.Store.SetAppStopped(ctx, app.ID, true); err != nil {
		return err
	}
	return m.cfg.Apply(ctx)
}

func (m *Manager) Start(ctx context.Context, app *store.App) error {
	defer m.lockApp(app.ID)()
	names, err := m.containers(ctx, app)
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := m.cfg.Docker.StartContainer(ctx, name); err != nil {
			return err
		}
	}
	if err := m.cfg.Store.SetAppStopped(ctx, app.ID, false); err != nil {
		return err
	}
	return m.cfg.Apply(ctx)
}

func (m *Manager) Restart(ctx context.Context, app *store.App) error {
	defer m.lockApp(app.ID)()
	names, err := m.containers(ctx, app)
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := m.cfg.Docker.RestartContainer(ctx, name, stopTimeout); err != nil {
			return err
		}
	}
	return nil
}

// containers son los contenedores de la app: el actual o, con Compose,
// todos los del proyecto.
func (m *Manager) containers(ctx context.Context, app *store.App) ([]string, error) {
	if _, err := m.current(ctx, app); err != nil {
		return nil, err
	}
	if app.Source == store.AppSourceCompose {
		return m.projectContainers(ctx, app)
	}
	name, err := m.current(ctx, app)
	return []string{name}, err
}

// Remove borra los contenedores de la app y, con removeData, sus volúmenes
// (¡sus datos!). El registro en la base de datos lo borra la API.
func (m *Manager) Remove(ctx context.Context, app *store.App, removeData bool) error {
	defer m.lockApp(app.ID)()
	if app.Source == store.AppSourceCompose {
		// compose down funciona con las etiquetas, sin los archivos del proyecto.
		cmd := []string{"docker", "compose", "-p", ComposeProject(app), "down", "--remove-orphans"}
		if removeData {
			cmd = append(cmd, "--volumes")
		}
		// Aunque ningún despliegue llegara a funcionar, Compose pudo dejar
		// creadas redes o volúmenes del proyecto.
		deps, err := m.cfg.Store.ListAppDeployments(ctx, app.ID, 1)
		if err != nil || len(deps) == 0 {
			return err
		}
		return m.runHelper(ctx, app, cmd, nil, nil, io.Discard)
	}
	deps, err := m.cfg.Store.ListAppDeployments(ctx, app.ID, 1000)
	if err != nil {
		return err
	}
	for _, d := range deps {
		if d.Container != "" {
			if err := m.cfg.Docker.RemoveContainer(ctx, d.Container); err != nil {
				return fmt.Errorf("borrando %s: %w", d.Container, err)
			}
		}
		if strings.HasPrefix(d.Image, "dockfly/") {
			_ = m.cfg.Docker.RemoveImage(ctx, d.Image)
		}
	}
	if removeData {
		for _, v := range app.Volumes {
			if err := m.cfg.Docker.RemoveVolume(ctx, VolumeName(app, v)); err != nil {
				return fmt.Errorf("borrando el volumen %s: %w", v.Name, err)
			}
		}
	}
	return nil
}

// SplitCommand separa un comando en argumentos, respetando comillas simples
// y dobles ("npm run start -- --port 3000", "sh -c 'echo hola'").
func SplitCommand(s string) ([]string, error) {
	var args []string
	var cur strings.Builder
	var quote rune
	inArg := false
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, inArg = r, true
		case unicode.IsSpace(r):
			if inArg {
				args = append(args, cur.String())
				cur.Reset()
				inArg = false
			}
		default:
			cur.WriteRune(r)
			inArg = true
		}
	}
	if quote != 0 {
		return nil, errors.New("el comando tiene comillas sin cerrar")
	}
	if inArg {
		args = append(args, cur.String())
	}
	return args, nil
}

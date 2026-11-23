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

// current returns the container serving the app right now.
func (m *Manager) current(ctx context.Context, app *store.App) (string, error) {
	if app.CurrentDeploymentID == nil {
		return "", errors.New("the app has not been deployed yet")
	}
	d, err := m.cfg.Store.GetAppDeployment(ctx, *app.CurrentDeploymentID)
	if err != nil {
		return "", err
	}
	return d.Container, nil
}

// Stop stops the app (it stays stopped even if Dockfly or the server
// restart) and stops routing its traffic.
func (m *Manager) Stop(ctx context.Context, app *store.App) error {
	defer m.lockApp(app.ID)()
	names, err := m.containers(ctx, app)
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := m.Docker(app).StopContainer(ctx, name, stopTimeout); err != nil {
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
		if err := m.Docker(app).StartContainer(ctx, name); err != nil {
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
		if err := m.Docker(app).RestartContainer(ctx, name, stopTimeout); err != nil {
			return err
		}
	}
	return nil
}

// containers are the app's containers: the current one or, with Compose,
// all of the project's.
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

// Remove deletes the app's containers and, with removeData, its volumes
// (its data!). The database record is deleted by the API.
func (m *Manager) Remove(ctx context.Context, app *store.App, removeData bool) error {
	defer m.lockApp(app.ID)()
	if app.Source == store.AppSourceCompose {
		// compose down works off the labels, without the project files.
		cmd := []string{"docker", "compose", "-p", ComposeProject(app), "down", "--remove-orphans"}
		if removeData {
			cmd = append(cmd, "--volumes")
		}
		// Even if no deployment ever worked, Compose may have left the
		// project's networks or volumes created.
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
			if err := m.Docker(app).RemoveContainer(ctx, d.Container); err != nil {
				return fmt.Errorf("deleting %s: %w", d.Container, err)
			}
		}
		if strings.HasPrefix(d.Image, "dockfly/") {
			_ = m.Docker(app).RemoveImage(ctx, d.Image)
		}
	}
	if removeData {
		for _, v := range app.Volumes {
			if err := m.Docker(app).RemoveVolume(ctx, VolumeName(app, v)); err != nil {
				return fmt.Errorf("deleting the volume %s: %w", v.Name, err)
			}
		}
	}
	return nil
}

// SplitCommand splits a command into arguments, honouring single and
// double quotes ("npm run start -- --port 3000", "sh -c 'echo hi'").
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
		return nil, errors.New("the command has an unclosed quote")
	}
	if inArg {
		args = append(args, cur.String())
	}
	return args, nil
}

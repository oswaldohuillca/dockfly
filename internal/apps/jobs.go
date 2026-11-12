package apps

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"time"

	"github.com/dockfly/dockfly/internal/store"
)

// ErrNotDeployed: la app aún no tiene un contenedor donde ejecutar nada.
var ErrNotDeployed = errors.New("la app no está desplegada")

// Límites de una tarea programada (más holgados que los de la consola: una
// tarea puede ser una importación larga).
const (
	jobTimeout   = 30 * time.Minute
	jobMaxOutput = 64 << 10
)

// cappedBuffer guarda como mucho max bytes y recuerda si se pasó.
type cappedBuffer struct {
	bytes.Buffer
	max       int
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.Len(); len(p) > room {
		b.truncated = true
		b.Buffer.Write(p[:max(room, 0)])
		return len(p), nil // se sigue leyendo para que el comando no se bloquee
	}
	return b.Buffer.Write(p)
}

// ExecResult es lo que deja un comando ejecutado en el contenedor de una app.
type ExecResult struct {
	ExitCode  int    `json:"exit_code"`
	Output    string `json:"output"`
	Truncated bool   `json:"truncated"`
	// NoShell: la imagen no tiene sh y el comando se ejecutó directamente,
	// sin tuberías, comillas ni variables.
	NoShell bool `json:"no_shell,omitempty"`
}

// noShell reconoce el error del runtime cuando la imagen no trae sh
// (distroless, scratch): el comando ni siquiera llegó a empezar.
func noShell(code int, output string) bool {
	return (code == 126 || code == 127) && strings.Contains(output, `exec: "sh": executable file not found`)
}

// Exec ejecuta command con sh (o directamente, si la imagen no tiene sh) en
// el contenedor actual de la app, con un
// tiempo y una salida máximos. Si se agota el tiempo devuelve
// context.DeadlineExceeded.
func (m *Manager) Exec(ctx context.Context, app *store.App, command string, timeout time.Duration, maxOutput int) (ExecResult, error) {
	dc := m.Docker(app)
	if dc == nil || app.Current == nil || app.Current.Container == "" {
		return ExecResult{}, ErrNotDeployed
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out := &cappedBuffer{max: maxOutput}
	code, err := dc.ExecStream(ctx, app.Current.Container, []string{"sh", "-c", command}, nil, out, out)
	direct := noShell(code, out.String())
	if direct {
		// shortcut: sin sh se separa por espacios, así que las comillas no
		// agrupan; analizar el comando solo si alguien lo necesita.
		out.Reset()
		code, err = dc.ExecStream(ctx, app.Current.Container, strings.Fields(command), nil, out, out)
	}
	res := ExecResult{ExitCode: code, Output: out.String(), Truncated: out.truncated, NoShell: direct}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return res, context.DeadlineExceeded
	}
	return res, err
}

// RunJob ejecuta una tarea ahora y guarda su resultado. No hace nada si ya
// está en marcha.
func (m *Manager) RunJob(ctx context.Context, job *store.AppJob) {
	m.mu.Lock()
	if m.jobs[job.ID] {
		m.mu.Unlock()
		return
	}
	if m.jobs == nil {
		m.jobs = map[int64]bool{}
	}
	m.jobs[job.ID] = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.jobs, job.ID)
		m.mu.Unlock()
	}()

	st := m.cfg.Store
	_ = st.StartAppJob(ctx, job.ID, time.Now().UTC())
	code, output := -1, ""
	app, err := st.GetApp(ctx, job.AppID)
	if err == nil {
		var res ExecResult
		res, err = m.Exec(ctx, app, job.Command, jobTimeout, jobMaxOutput)
		code, output = res.ExitCode, res.Output
		if res.Truncated {
			output += "\n… salida cortada: era demasiado larga\n"
		}
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		code, output = -1, output+"\nLa tarea superó el tiempo máximo y se detuvo.\n"
	case err != nil:
		code, output = -1, output+"\nNo se pudo ejecutar: "+err.Error()+"\n"
	}
	// El resultado se guarda aunque ctx se haya cancelado al apagar el panel.
	save, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	_ = st.FinishAppJob(save, job.ID, code, output)
	if code != 0 && m.cfg.OnJobFailed != nil && app != nil {
		m.cfg.OnJobFailed(save, app, job, code)
	}
}

// RunJobs lanza las tareas programadas: cada minuto, las activas cuya
// expresión cron (en UTC) coincide. Las de apps paradas o sin desplegar se
// saltan sin contar como fallo.
func (m *Manager) RunJobs(ctx context.Context) {
	for {
		now := time.Now().UTC()
		select {
		case <-ctx.Done():
			return
		case <-time.After(now.Truncate(time.Minute).Add(time.Minute).Sub(now)):
		}
		minute := time.Now().UTC().Truncate(time.Minute)
		jobs, err := m.cfg.Store.EnabledAppJobs(ctx)
		if err != nil {
			continue
		}
		for _, job := range jobs {
			sched, err := ParseSchedule(job.Schedule)
			if err != nil || !sched.Matches(minute) {
				continue
			}
			app, err := m.cfg.Store.GetApp(ctx, job.AppID)
			if err != nil || app.Stopped || app.Current == nil {
				continue
			}
			go m.RunJob(ctx, job)
		}
	}
}

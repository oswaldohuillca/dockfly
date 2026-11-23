package apps

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"time"

	"github.com/dockfly/dockfly/internal/store"
)

// ErrNotDeployed: the app has no container yet to run anything in.
var ErrNotDeployed = errors.New("the app is not deployed")

// Limits of a scheduled job (looser than the console's: a job may well be
// a long import).
const (
	jobTimeout   = 30 * time.Minute
	jobMaxOutput = 64 << 10
)

// cappedBuffer keeps at most max bytes and remembers whether it went over.
type cappedBuffer struct {
	bytes.Buffer
	max       int
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.Len(); len(p) > room {
		b.truncated = true
		b.Buffer.Write(p[:max(room, 0)])
		return len(p), nil // keep reading so the command does not block
	}
	return b.Buffer.Write(p)
}

// ExecResult is what a command run in an app's container leaves behind.
type ExecResult struct {
	ExitCode  int    `json:"exit_code"`
	Output    string `json:"output"`
	Truncated bool   `json:"truncated"`
	// NoShell: the image has no sh and the command was run directly, with
	// no pipes, quotes or variables.
	NoShell bool `json:"no_shell,omitempty"`
}

// noShell recognises the runtime error raised when the image ships no sh
// (distroless, scratch): the command never even started.
func noShell(code int, output string) bool {
	return (code == 126 || code == 127) && strings.Contains(output, `exec: "sh": executable file not found`)
}

// Exec runs command with sh (or directly, if the image has no sh) in the
// app's current container, with a maximum time and a maximum output. On
// timeout it returns context.DeadlineExceeded.
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
		// shortcut: with no sh the command is split on spaces, so quotes do
		// not group; parse it properly only if someone needs it.
		out.Reset()
		code, err = dc.ExecStream(ctx, app.Current.Container, strings.Fields(command), nil, out, out)
	}
	res := ExecResult{ExitCode: code, Output: out.String(), Truncated: out.truncated, NoShell: direct}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return res, context.DeadlineExceeded
	}
	return res, err
}

// RunJob runs a job right now and stores its result. It does nothing if
// the job is already running.
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
			output += "\n… output truncated: it was too long\n"
		}
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		code, output = -1, output+"\nThe job went over its time limit and was stopped.\n"
	case err != nil:
		code, output = -1, output+"\nIt could not be run: "+err.Error()+"\n"
	}
	// The result is stored even if ctx was canceled by the panel shutting down.
	save, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	_ = st.FinishAppJob(save, job.ID, code, output)
	if code != 0 && m.cfg.OnJobFailed != nil && app != nil {
		m.cfg.OnJobFailed(save, app, job, code)
	}
}

// RunJobs fires the scheduled jobs: every minute, the enabled ones whose
// cron expression (in UTC) matches. Those of stopped or undeployed apps are
// skipped without counting as a failure.
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

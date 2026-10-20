package traefik

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// Supervisor ejecuta Traefik como proceso hijo y lo reinicia si termina.
type Supervisor struct {
	Bin        string
	ConfigFile string
	Log        *slog.Logger

	mu        sync.RWMutex
	running   bool
	startedAt time.Time
	lastErr   string
}

type Status struct {
	Running   bool      `json:"running"`
	StartedAt time.Time `json:"started_at,omitzero"`
	LastError string    `json:"last_error,omitempty"`
}

func (s *Supervisor) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Status{Running: s.running, StartedAt: s.startedAt, LastError: s.lastErr}
}

// Run bloquea hasta que ctx se cancela. Al cancelar, envía SIGTERM a Traefik
// y espera a que termine.
func (s *Supervisor) Run(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		started := time.Now()
		err := s.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		s.Log.Error("traefik terminó, reiniciando", "error", err, "en", backoff)

		// Si estuvo vivo un buen rato, el fallo no es de arranque: reinicia rápido.
		if time.Since(started) > time.Minute {
			backoff = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func (s *Supervisor) runOnce(ctx context.Context) error {
	cmd := exec.Command(s.Bin, "--configFile="+s.ConfigFile)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		s.setState(false, err)
		return err
	}
	s.setState(true, nil)
	s.Log.Info("traefik iniciado", "pid", cmd.Process.Pid)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		s.setState(false, err)
		return err
	case <-ctx.Done():
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		s.setState(false, nil)
		return ctx.Err()
	}
}

func (s *Supervisor) setState(running bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = running
	if running {
		s.startedAt = time.Now()
	}
	if err != nil {
		s.lastErr = err.Error()
	}
}

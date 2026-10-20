package traefik

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Supervisor ejecuta Traefik como proceso hijo y lo reinicia si termina.
type Supervisor struct {
	Bin        string
	ConfigFile string
	Log        *slog.Logger
	// Certs recibe cada línea del log de Traefik para detectar los errores
	// de Let's Encrypt.
	Certs *CertTracker
	// Env devuelve variables de entorno extra para Traefik (credenciales de
	// los proveedores DNS). Se consulta en cada arranque.
	Env func() []string

	mu        sync.RWMutex
	running   bool
	startedAt time.Time
	lastErr   string
	restart   chan struct{}
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
	s.mu.Lock()
	if s.restart == nil {
		s.restart = make(chan struct{}, 1)
	}
	s.mu.Unlock()
	backoff := time.Second
	for ctx.Err() == nil {
		started := time.Now()
		err := s.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, errRestart) {
			backoff = time.Second
			continue // reinicio pedido: sin espera
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
	cmd.Env = os.Environ()
	if s.Env != nil {
		cmd.Env = append(cmd.Env, s.Env()...)
	}
	// Pipe propio (no StdoutPipe): Wait no lo cierra antes de tiempo, así que
	// se leen hasta las últimas líneas, que suelen explicar una caída.
	pr, pw, err := os.Pipe()
	if err != nil {
		return err
	}
	cmd.Stdout, cmd.Stderr = pw, pw // un único flujo, en orden
	err = cmd.Start()
	pw.Close() // el hijo tiene su copia: al terminar, la lectura da EOF
	if err != nil {
		pr.Close()
		s.setState(false, err)
		return err
	}
	var logs sync.WaitGroup
	logs.Go(func() {
		defer pr.Close()
		s.pipeLogs(pr)
	})
	s.setState(true, nil)
	s.Log.Info("traefik iniciado", "pid", cmd.Process.Pid)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	stop := func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	}
	select {
	case err := <-done:
		logs.Wait()
		s.setState(false, err)
		return err
	case <-s.restart:
		s.Log.Info("reiniciando traefik para aplicar la configuración estática")
		stop()
		logs.Wait()
		s.setState(false, nil)
		return errRestart
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

var errRestart = errors.New("reinicio solicitado")

// Restart pide reiniciar Traefik (p. ej. tras añadir un puerto de stream,
// que es configuración estática). No bloquea; varias peticiones seguidas
// producen un único reinicio.
func (s *Supervisor) Restart() {
	s.mu.RLock()
	ch := s.restart
	s.mu.RUnlock()
	if ch == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}

// pipeLogs lee el log JSON de Traefik, se lo pasa al rastreador de
// certificados y lo reescribe en formato legible para "docker logs".
func (s *Supervisor) pipeLogs(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if s.Certs != nil {
			s.Certs.Observe(line)
		}
		fmt.Fprintln(os.Stdout, formatLog(line))
	}
}

// formatLog convierte una línea JSON de Traefik en "traefik ERR mensaje
// clave=valor…". Las líneas que no son JSON se dejan tal cual.
func formatLog(line []byte) string {
	var entry map[string]any
	if json.Unmarshal(line, &entry) != nil {
		return string(line)
	}
	level, _ := entry["level"].(string)
	msg, _ := entry["message"].(string)
	var b strings.Builder
	fmt.Fprintf(&b, "traefik %s %s", strings.ToUpper(level), msg)
	keys := make([]string, 0, len(entry))
	for k := range entry {
		if k != "level" && k != "message" && k != "time" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, " %s=%v", k, entry[k])
	}
	return b.String()
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

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

// Supervisor runs Traefik as a child process and restarts it if it exits.
type Supervisor struct {
	Bin        string
	ConfigFile string
	Log        *slog.Logger
	// Certs receives every line of Traefik's log so it can spot Let's
	// Encrypt errors.
	Certs *CertTracker
	// Env returns extra environment variables for Traefik (the credentials
	// of the DNS providers). It is asked on every start.
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

// Run blocks until ctx is cancelled. On cancellation it sends SIGTERM to
// Traefik and waits for it to exit.
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
			continue // restart requested: no wait
		}
		s.Log.Error("traefik exited, restarting", "error", err, "in", backoff)

		// If it stayed alive a good while, this is not a startup failure: restart fast.
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
	// Our own pipe (not StdoutPipe): Wait does not close it early, so even
	// the last lines are read, which usually explain a crash.
	pr, pw, err := os.Pipe()
	if err != nil {
		return err
	}
	cmd.Stdout, cmd.Stderr = pw, pw // a single stream, in order
	err = cmd.Start()
	pw.Close() // the child has its copy: once it exits, reading gives EOF
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
	s.Log.Info("traefik started", "pid", cmd.Process.Pid)

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
		s.Log.Info("restarting traefik to apply the static configuration")
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

var errRestart = errors.New("restart requested")

// Restart asks for a Traefik restart (e.g. after adding a stream port,
// which is static configuration). It does not block; several requests in a
// row produce a single restart.
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

// pipeLogs reads Traefik's JSON log, hands it to the certificate tracker
// and rewrites it in a readable format for "docker logs".
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

// formatLog turns a JSON line from Traefik into "traefik ERR message
// key=value…". Lines that are not JSON are left as they are.
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

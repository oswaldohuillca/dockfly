// Package agent is Dockfly on a remote server: it runs its own Traefik
// (with its certificates) and lets the panel use its Docker and push the
// route configuration to it, all over the tunnel the agent opens towards the
// panel. If the panel goes down, the server's apps keep serving.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/dockfly/dockfly/internal/docker"
	"github.com/dockfly/dockfly/internal/probe"
	"github.com/dockfly/dockfly/internal/sysinfo"
	"github.com/dockfly/dockfly/internal/traefik"
	"github.com/dockfly/dockfly/internal/tunnel"
)

type Config struct {
	PanelURL string
	Token    string
	Version  string
	Docker   *docker.Client
	Traefik  traefik.Options
	// TraefikBin is the Traefik binary ("" = do not launch it, in the tests).
	TraefikBin string
	Log        *slog.Logger
}

// Info is what the agent reports to the panel.
type Info struct {
	Version  string `json:"version"`
	Hostname string `json:"hostname"`
	Traefik  bool   `json:"traefik_running"`
	Docker   bool   `json:"docker_available"`
}

// Run starts the agent and keeps it running until ctx is cancelled.
func Run(ctx context.Context, cfg Config) error {
	if err := traefik.WriteStatic(cfg.Traefik); err != nil {
		return err
	}
	// Until the panel sends the routes, an empty (but valid) configuration:
	// Traefik can already start.
	if _, err := os.Stat(cfg.Traefik.DynamicDir() + "/dockfly.yml"); errors.Is(err, os.ErrNotExist) {
		if err := traefik.WriteDynamic(cfg.Traefik, traefik.Dynamic{}); err != nil {
			return err
		}
	}
	if cfg.Traefik.DockerNet != "" {
		if _, err := cfg.Docker.EnsureSelfNetwork(ctx, cfg.Traefik.DockerNet); err != nil {
			cfg.Log.Warn("could not connect the agent to the apps network", "network", cfg.Traefik.DockerNet, "error", err)
		}
	}

	var wg sync.WaitGroup
	var super *traefik.Supervisor
	if cfg.TraefikBin != "" {
		super = &traefik.Supervisor{Bin: cfg.TraefikBin, ConfigFile: cfg.Traefik.StaticFile(), Log: cfg.Log.With("component", "traefik")}
		wg.Go(func() { super.Run(ctx) })
	}

	api := &localAPI{cfg: cfg, super: super}
	t := &tunnel.Agent{
		PanelURL: cfg.PanelURL, Token: cfg.Token, Version: cfg.Version, Network: cfg.Traefik.DockerNet, Log: cfg.Log,
		Targets: map[string]func(context.Context) (net.Conn, error){
			tunnel.TargetDocker: cfg.Docker.Dial,
			tunnel.TargetAgent:  api.dial,
			tunnel.TargetMetrics: func(ctx context.Context) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "tcp", traefik.MetricsAddr)
			},
		},
	}
	wg.Go(func() { t.Run(ctx) })
	wg.Wait()
	return nil
}

// localAPI is what the panel can ask the agent for.
type localAPI struct {
	cfg   Config
	super *traefik.Supervisor
	once  sync.Once
	ln    *pipeListener
}

func (a *localAPI) dial(ctx context.Context) (net.Conn, error) {
	a.once.Do(func() {
		a.ln = newPipeListener()
		srv := &http.Server{Handler: a.handler(), ReadHeaderTimeout: 10 * time.Second}
		go func() { _ = srv.Serve(a.ln) }()
	})
	return a.ln.dial(ctx)
}

func (a *localAPI) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /info", func(w http.ResponseWriter, r *http.Request) {
		host, _ := os.Hostname()
		info := Info{Version: a.cfg.Version, Hostname: host, Docker: a.cfg.Docker.Available(r.Context())}
		if a.super != nil {
			info.Traefik = a.super.Status().Running
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(info)
	})
	// This server's resources (CPU, memory, disk), for the panel.
	mux.HandleFunc("GET /stats", func(w http.ResponseWriter, r *http.Request) {
		st, err := sysinfo.Read(r.Context(), a.cfg.Traefik.DataDir)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(st)
	})
	// The panel checks from here whether an app is ready: its container's IP
	// is only reachable inside this server.
	mux.HandleFunc("GET /probe", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if !probe.Ready(ctx, r.URL.Query().Get("addr"), r.URL.Query().Get("path")) {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	// The panel generates Traefik's dynamic configuration.
	mux.HandleFunc("PUT /dynamic", func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var probe map[string]any
		if err := yaml.Unmarshal(data, &probe); err != nil {
			http.Error(w, "invalid configuration: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := traefik.WriteDynamicBytes(a.cfg.Traefik, data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

// pipeListener hands in-memory connections to the agent's HTTP server.
type pipeListener struct {
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn), done: make(chan struct{})}
}

func (l *pipeListener) dial(ctx context.Context) (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case l.conns <- server:
		return client, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return pipeAddr{} }

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "agent" }

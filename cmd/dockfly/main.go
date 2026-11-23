package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/dockfly/dockfly/internal/agent"
	"github.com/dockfly/dockfly/internal/api"
	"github.com/dockfly/dockfly/internal/apps"
	"github.com/dockfly/dockfly/internal/auth"
	"github.com/dockfly/dockfly/internal/backup"
	"github.com/dockfly/dockfly/internal/builder"
	"github.com/dockfly/dockfly/internal/cli"
	"github.com/dockfly/dockfly/internal/dbbackup"
	"github.com/dockfly/dockfly/internal/docker"
	"github.com/dockfly/dockfly/internal/dockerproxy"
	"github.com/dockfly/dockfly/internal/egress"
	"github.com/dockfly/dockfly/internal/gitauth"
	"github.com/dockfly/dockfly/internal/notify"
	"github.com/dockfly/dockfly/internal/pages"
	"github.com/dockfly/dockfly/internal/secrets"
	"github.com/dockfly/dockfly/internal/selfupdate"
	"github.com/dockfly/dockfly/internal/store"
	"github.com/dockfly/dockfly/internal/traefik"
	"github.com/dockfly/dockfly/internal/traffic"
	"github.com/dockfly/dockfly/internal/tunnel"
	"github.com/dockfly/dockfly/web"
)

// version is injected at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "healthcheck":
			os.Exit(healthcheck())
		case "backup", "restore":
			os.Exit(backupCommand(os.Args[1], os.Args[2:]))
		case "reset-2fa":
			os.Exit(reset2FACommand(os.Args[2:]))
		case "deploy":
			os.Exit(cli.Deploy(os.Args[2:], os.Stdout, os.Stderr))
		case "agent":
			os.Exit(agentCommand(os.Args[2:]))
		case "egress":
			log := slog.New(slog.NewTextHandler(os.Stderr, nil))
			log.Error("egress proxy", "error", egress.Serve(":"+egress.Port, log))
			os.Exit(1)
		case "self-update":
			// The panel launches this in a helper container (see selfupdate).
			log := slog.New(slog.NewTextHandler(os.Stderr, nil))
			if len(os.Args) != 4 {
				log.Error("usage: dockfly self-update <container> <image>")
				os.Exit(2)
			}
			dc := docker.New(env("DOCKFLY_DOCKER_SOCKET", "/var/run/docker.sock"))
			if err := selfupdate.Helper(context.Background(), dc, os.Args[2], os.Args[3], log); err != nil {
				log.Error(err.Error())
				os.Exit(1)
			}
			os.Exit(0)
		}
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel()}))
	if err := run(log); errors.Is(err, errRestart) {
		// 75 (EX_TEMPFAIL): Docker restarts the container both with
		// "unless-stopped" and with "on-failure".
		os.Exit(75)
	} else if err != nil {
		log.Error("dockfly exited with an error", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	dataDir := env("DOCKFLY_DATA_DIR", "/data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}

	// A backup restored from the panel is applied here, before opening anything.
	if applied, kept, err := backup.ApplyPending(dataDir); err != nil {
		return fmt.Errorf("applying the backup: %w", err)
	} else if applied {
		log.Warn("backup restored; the previous data is in "+kept, "dir", kept)
	}

	st, err := openStore(ctx, dataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	signer, err := auth.LoadOrCreateSigner(filepath.Join(dataDir, "secret.key"))
	if err != nil {
		return err
	}

	// Docker: the mounted socket or, behind a socket proxy,
	// DOCKFLY_DOCKER_SOCKET=tcp://socket-proxy:2375.
	var dc *docker.Client
	socket := env("DOCKFLY_DOCKER_SOCKET", "/var/run/docker.sock")
	if strings.HasPrefix(socket, "tcp://") {
		dc = docker.New(socket)
	} else if _, err := os.Stat(socket); err == nil {
		dc = docker.New(socket)
	} else {
		log.Warn("Docker socket not available: containers cannot be listed", "socket", socket)
		socket = ""
	}

	trusted, err := trustedProxies(os.Getenv("DOCKFLY_TRUSTED_PROXIES"))
	if err != nil {
		return err
	}
	opts := traefik.Options{
		DataDir:        dataDir,
		HTTPAddr:       env("DOCKFLY_HTTP_ADDR", ":80"),
		HTTPSAddr:      env("DOCKFLY_HTTPS_ADDR", ":443"),
		ACMEEmail:      os.Getenv("DOCKFLY_ACME_EMAIL"),
		ACMEStaging:    os.Getenv("DOCKFLY_ACME_STAGING") == "true",
		DockerNet:      env("DOCKFLY_NETWORK", "dockfly"),
		TrustedProxies: trusted,
		LogLevel:       strings.ToUpper(env("DOCKFLY_TRAEFIK_LOG_LEVEL", "warn")),
	}
	// Stream ports and DNS providers are part of Traefik's static
	// configuration.
	if ports, err := (&api.Server{Store: st}).StreamPorts(ctx); err == nil {
		opts.Streams = ports
	}
	if providers, err := st.ListDNSProviders(ctx); err == nil {
		for _, p := range providers {
			opts.DNSProviders = append(opts.DNSProviders, p.Provider)
		}
	}
	if err := traefik.WriteStatic(opts); err != nil {
		return fmt.Errorf("writing Traefik static configuration: %w", err)
	}

	// Static sites: the files live in /data/sites and are served by an
	// internal server that Traefik forwards each site's domains to.
	pagesStorage := &pages.Storage{Root: filepath.Join(dataDir, "sites"), Limits: pages.DefaultLimits}
	if err := pagesStorage.CleanStaging(); err != nil {
		return fmt.Errorf("cleaning up incomplete uploads: %w", err)
	}
	pagesSrv := &pages.Server{}
	staticAddr := env("DOCKFLY_STATIC_ADDR", "127.0.0.1:8099")
	maxUpload, err := strconv.ParseInt(env("DOCKFLY_MAX_UPLOAD_MB", "512"), 10, 64)
	if err != nil || maxUpload < 1 {
		return fmt.Errorf("DOCKFLY_MAX_UPLOAD_MB must be a positive number")
	}

	box, err := secrets.LoadOrCreate(filepath.Join(dataDir, "encryption.key"))
	if err != nil {
		return err
	}
	buildCfg, err := buildConfig()
	if err != nil {
		return err
	}

	certs := &traefik.CertTracker{}
	notifier := &notify.Dispatcher{Store: st, Secrets: box, Log: log.With("component", "notify")}

	srv := &api.Server{
		Store:    st,
		Signer:   signer,
		Docker:   dc,
		Traefik:  opts,
		Frontend: web.Dist(),
		Version:  version,
		Log:      log,

		TrustedProxies: trusted,

		Pages:        pagesSrv,
		PagesStorage: pagesStorage,
		StaticURL:    "http://" + loopback(staticAddr),
		MaxUploadMB:  maxUpload,
		Secrets:      box,
		Git:          gitauth.New(st, box),
		ListenAddr:   env("DOCKFLY_LISTEN", ":81"),
		Certs:        certs,
		Notify:       notifier,
		DataDir:      dataDir,
		Restart: func() {
			restart.Store(true)
			stop()
		},
	}
	buildCfg.Store, buildCfg.Docker, buildCfg.Secrets, buildCfg.Providers = st, dc, box, srv.Git
	buildCfg.Pages, buildCfg.LogDir = pagesStorage, filepath.Join(dataDir, "builds")
	buildCfg.Log, buildCfg.Publish = log.With("component", "builder"), srv.Publish
	buildCfg.OnFinish = srv.OnBuildFinished
	buildCfg.StatusURL = srv.StatusURL
	certs.OnError = srv.OnCertError
	// Builds reach the internet only through a proxy that blocks internal
	// addresses (cloud metadata, the host, the local network).
	if dc != nil && env("DOCKFLY_BUILD_EGRESS", "filtered") != "open" {
		self, inDocker := dc.Self(ctx)
		proxy, err := "", errors.New("Dockfly is not running in a container")
		if inDocker {
			proxy, err = egress.Start(ctx, dc, self.Image)
		}
		if err != nil {
			log.Warn("builds will have unrestricted network access", "reason", err)
		} else {
			buildCfg.Network, buildCfg.EgressProxy = egress.Network, proxy
		}
	}
	srv.Builds = builder.New(buildCfg)
	go func() {
		for {
			builder.PruneLogs(buildCfg.LogDir, 30*24*time.Hour)
			select {
			case <-ctx.Done():
				return
			case <-time.After(24 * time.Hour):
			}
		}
	}()
	srv.Hub = &tunnel.Hub{OnConnect: srv.OnAgentConnect}
	srv.Updater = &selfupdate.Updater{
		Docker: dc, DB: st, DataDir: dataDir, Version: version,
		Socket: env("DOCKFLY_DOCKER_SOCKET", "/var/run/docker.sock"), Log: log.With("component", "update"),
	}
	go srv.Updater.Cleanup(ctx)
	// Fresh install: creating the administrator requires this code, which only
	// someone with access to the server can see.
	if token, err := srv.PrepareSetupToken(ctx); err != nil {
		return fmt.Errorf("preparing the setup code: %w", err)
	} else if token != "" {
		log.Warn("fresh install: use the setup code "+token+" to create the administrator",
			"also_in", filepath.Join(dataDir, "setup-token"))
	}
	srv.Apps = apps.New(apps.Config{
		Remote: srv.RemoteDocker, RemoteNetwork: srv.RemoteNetwork, RemoteProbe: srv.RemoteProbe,
		Store: st, Docker: dc, Secrets: box, Providers: srv.Git, LogDir: filepath.Join(dataDir, "builds"),
		Log: log.With("component", "apps"), Network: opts.DockerNet,
		HostDockerSocket: env("DOCKFLY_HOST_DOCKER_SOCKET", "/var/run/docker.sock"),
		Apply:            srv.Apply, OnFinish: srv.OnAppDeployFinished, OnJobFailed: srv.OnAppJobFailed,
	})
	if dc != nil {
		ensureNetwork(ctx, log, dc, opts.DockerNet)
		startComposeProxy(ctx, log, dc, srv.Apps, opts.DockerNet)
		if self, ok := dc.Self(ctx); ok {
			srv.Image = self.Image
		}
		srv.DBBackups = &dbbackup.Manager{
			Store: st, Docker: dc, DockerFor: srv.Apps.Docker, Storage: srv.BackupStorage, TmpDir: filepath.Join(dataDir, "tmp"),
			Log: log.With("component", "db-backups"), OnFinish: srv.OnDBBackupFinished,
		}
	}
	if err := srv.PrepareCertificates(ctx); err != nil {
		return fmt.Errorf("writing the user-provided certificates: %w", err)
	}
	// The dynamic configuration is regenerated from the database on startup:
	// the database is the single source of truth.
	if err := srv.Apply(ctx); err != nil {
		return fmt.Errorf("writing Traefik dynamic configuration: %w", err)
	}

	var wg sync.WaitGroup
	wg.Go(func() { srv.Builds.Run(ctx) })
	wg.Go(func() { srv.RunMonitors(ctx) })
	wg.Go(func() { srv.Apps.Run(ctx) })
	wg.Go(func() { srv.Apps.RunJobs(ctx) })
	if env("DOCKFLY_TRAEFIK", "on") != "off" {
		collector := &traffic.Collector{
			URL: "http://" + traefik.MetricsAddr + "/metrics", Bounds: traefik.LatencyBuckets,
			Store: st, Log: log.With("component", "traffic"),
		}
		wg.Go(func() { collector.Run(ctx) })
	}
	if srv.DBBackups != nil {
		wg.Go(func() { srv.DBBackups.Run(ctx) })
	}
	if env("DOCKFLY_TRAEFIK", "on") != "off" {
		srv.Super = &traefik.Supervisor{
			Bin:        env("DOCKFLY_TRAEFIK_BIN", "/usr/local/bin/traefik"),
			ConfigFile: opts.StaticFile(),
			Log:        log.With("component", "traefik"),
			Certs:      certs,
			Env:        srv.DNSEnv,
		}
		wg.Go(func() { srv.Super.Run(ctx) })
	} else {
		log.Warn("Traefik disabled (DOCKFLY_TRAEFIK=off): only the configuration is generated")
	}

	httpSrv := &http.Server{
		Addr:              env("DOCKFLY_LISTEN", ":81"),
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// Closes idle keep-alive connections. No global read or write
		// deadlines: the panel has SSE (logs) and the agent tunnel, both
		// long-lived; the deadlines for the session-less routes (login,
		// webhooks) are set by each handler.
		IdleTimeout: 2 * time.Minute,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	staticSrv := &http.Server{
		Addr:              staticAddr,
		Handler:           pagesSrv,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	errc := make(chan error, 2)
	go func() {
		log.Info("Dockfly panel listening", "addr", httpSrv.Addr, "version", version)
		errc <- httpSrv.ListenAndServe()
	}()
	go func() {
		log.Info("static site server listening", "addr", staticAddr)
		errc <- staticSrv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		stop()
		wg.Wait()
		return err
	case <-ctx.Done():
	}

	if restart.Load() {
		log.Warn("restarting to apply the backup")
	} else {
		log.Info("shutting down…")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, hs := range []*http.Server{httpSrv, staticSrv} {
		if err := hs.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("closing HTTP server", "addr", hs.Addr, "error", err)
		}
	}
	wg.Wait()
	if restart.Load() {
		return errRestart
	}
	return nil
}

// restart signals that Dockfly must exit so that Docker restarts it.
var restart atomic.Bool

var errRestart = errors.New("restart requested")

// ensureNetwork creates Dockfly's network if it does not exist and connects
// Dockfly's own container to it: Traefik (which runs inside) reaches the apps
// by their name on that network.
func ensureNetwork(ctx context.Context, log *slog.Logger, dc *docker.Client, network string) {
	if network == "" {
		return
	}
	connected, err := dc.EnsureSelfNetwork(ctx, network)
	switch {
	case err != nil:
		log.Warn("could not connect Dockfly to its network: the apps will not receive traffic", "network", network, "error", err)
	case connected:
		log.Info("Dockfly connected to the apps network", "network", network)
	}
}

// startComposeProxy starts the Docker socket proxy with minimal permissions
// for the Compose helper (only if Dockfly runs in Docker: the helper reaches
// the proxy over a per-run internal network).
func startComposeProxy(ctx context.Context, log *slog.Logger, dc *docker.Client, m *apps.Manager, network string) {
	if env("DOCKFLY_COMPOSE_PROXY", "1") == "0" {
		log.Warn("Docker proxy disabled: Compose apps have full access to Docker")
		return
	}
	self, ok := dc.Self(ctx)
	host, _ := os.Hostname()
	if !ok || host == "" {
		log.Warn("Dockfly is not running in Docker: Compose apps will have full access to Docker")
		return
	}
	m.SetComposeProxy(&dockerproxy.Proxy{Dial: dc.Dial, Log: log.With("component", "docker-proxy"), Protected: self.Mounts, Network: network}, host)
	m.CleanupProxyNetworks(ctx)
	log.Info("Docker proxy for Compose active")
}

// buildConfig reads the build limits from the environment variables.
func buildConfig() (builder.Config, error) {
	num := func(key string, def float64) (float64, error) {
		v, err := strconv.ParseFloat(env(key, strconv.FormatFloat(def, 'f', -1, 64)), 64)
		if err != nil || v <= 0 {
			return 0, fmt.Errorf("%s must be a positive number", key)
		}
		return v, nil
	}
	timeout, err := num("DOCKFLY_BUILD_TIMEOUT_MIN", 15)
	if err != nil {
		return builder.Config{}, err
	}
	mem, err := num("DOCKFLY_BUILD_MEMORY_MB", 2048)
	if err != nil {
		return builder.Config{}, err
	}
	cpus, err := num("DOCKFLY_BUILD_CPUS", 2)
	if err != nil {
		return builder.Config{}, err
	}
	conc, err := num("DOCKFLY_BUILD_CONCURRENCY", 1)
	if err != nil {
		return builder.Config{}, err
	}
	return builder.Config{
		Timeout:     time.Duration(timeout * float64(time.Minute)),
		MemoryMB:    int64(mem),
		CPUs:        cpus,
		Concurrency: int(conc),
	}, nil
}

// backupCommand implements "dockfly backup [file]" (standard output by
// default) and "dockfly restore <file>", for when the panel is not available.
// openStore opens the database: the one in DOCKFLY_DATABASE_URL (postgres://
// or mysql://) or, without it, the SQLite file in the data directory.
func openStore(ctx context.Context, dataDir string) (*store.Store, error) {
	return store.Open(ctx, env("DOCKFLY_DATABASE_URL", filepath.Join(dataDir, "dockfly.db")))
}

func backupCommand(cmd string, args []string) int {
	dataDir := env("DOCKFLY_DATA_DIR", "/data")
	switch cmd {
	case "backup":
		ctx := context.Background()
		st, err := openStore(ctx, dataDir)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		defer st.Close()
		out := os.Stdout
		if len(args) > 0 {
			if out, err = os.Create(args[0]); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			defer out.Close()
		}
		if err := backup.Create(ctx, st, dataDir, version, out); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
	case "restore":
		if len(args) != 1 {
			fmt.Fprintln(os.Stderr, "usage: dockfly restore <backup.tar.gz>")
			return 2
		}
		if os.Getenv("DOCKFLY_DATABASE_URL") != "" {
			fmt.Fprintln(os.Stderr, "error: with DOCKFLY_DATABASE_URL the database is restored with your database server's own tools, and the data directory by hand")
			return 1
		}
		f, err := os.Open(args[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		defer f.Close()
		m, err := backup.Stage(f, dataDir)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		fmt.Fprintf(os.Stderr, "Backup from %s staged: it will be applied the next time Dockfly starts.\n",
			m.CreatedAt.Format("2006-01-02 15:04"))
	}
	return 0
}

// agentCommand implements "dockfly agent --join <panel> --token <token>":
// Dockfly on a remote server, controlled from the panel.
func agentCommand(args []string) int {
	fl := flag.NewFlagSet("agent", flag.ContinueOnError)
	panel := fl.String("join", os.Getenv("DOCKFLY_PANEL_URL"), "panel address")
	token := fl.String("token", os.Getenv("DOCKFLY_AGENT_TOKEN"), "server token")
	// The agent token and the server's whole Docker API travel over this
	// channel: without HTTPS, anyone on the network could steal them.
	insecure := fl.Bool("insecure", os.Getenv("DOCKFLY_AGENT_INSECURE") == "1", "allow an unencrypted connection (testing only)")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if *panel == "" || *token == "" {
		fmt.Fprintln(os.Stderr, "usage: dockfly agent --join https://dockfly.mydomain.com --token dfa_…\n(the full command is in the panel: Servers → Add server)")
		return 2
	}
	if !strings.HasPrefix(*panel, "https://") && !*insecure {
		fmt.Fprintln(os.Stderr, "the panel address must start with https:// (or add --insecure, for local network testing only)")
		return 2
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel()}))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	dataDir := env("DOCKFLY_DATA_DIR", "/data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Error("creating the data directory", "error", err)
		return 1
	}
	socket := env("DOCKFLY_DOCKER_SOCKET", "/var/run/docker.sock")
	cfg := agent.Config{
		PanelURL: *panel, Token: *token, Version: version, Docker: docker.New(socket), Log: log,
		TraefikBin: env("DOCKFLY_TRAEFIK_BIN", "/usr/local/bin/traefik"),
		Traefik: traefik.Options{
			DataDir:     dataDir,
			HTTPAddr:    env("DOCKFLY_HTTP_ADDR", ":80"),
			HTTPSAddr:   env("DOCKFLY_HTTPS_ADDR", ":443"),
			ACMEEmail:   os.Getenv("DOCKFLY_ACME_EMAIL"),
			ACMEStaging: os.Getenv("DOCKFLY_ACME_STAGING") == "true",
			DockerNet:   env("DOCKFLY_NETWORK", "dockfly"),
			LogLevel:    strings.ToUpper(env("DOCKFLY_TRAEFIK_LOG_LEVEL", "warn")),
		},
	}
	if env("DOCKFLY_TRAEFIK", "on") == "off" {
		cfg.TraefikBin = ""
	}
	log.Info("Dockfly agent", "version", version, "panel", *panel)
	if err := agent.Run(ctx, cfg); err != nil {
		log.Error("agent", "error", err)
		return 1
	}
	return 0
}

// reset2FACommand implements "dockfly reset-2fa <email>": it turns off
// two-step verification for someone who lost their phone and their codes.
func reset2FACommand(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: dockfly reset-2fa <email>")
		return 2
	}
	ctx := context.Background()
	st, err := openStore(ctx, env("DOCKFLY_DATA_DIR", "/data"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	defer st.Close()
	if err := api.Reset2FA(ctx, st, args[0]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "Two-step verification disabled for %s.\n", args[0])
	return 0
}

// loopback turns ":8099" into "127.0.0.1:8099" to build a URL.
func loopback(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "127.0.0.1" + addr
	}
	return addr
}

// healthcheck makes HEALTHCHECK usable in a scratch image without curl.
func healthcheck() int {
	addr := loopback(env("DOCKFLY_LISTEN", ":81"))
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + addr + "/api/setup")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

// trustedProxies validates the comma-separated list of CIDR ranges, for
// example "127.0.0.1/32,10.0.0.0/8". A malformed range stops startup: it is
// better to fail than to trust the wrong header.
func trustedProxies(v string) ([]string, error) {
	var out []string
	for p := range strings.SplitSeq(v, ",") {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		if _, err := netip.ParsePrefix(p); err != nil {
			return nil, fmt.Errorf("DOCKFLY_TRUSTED_PROXIES: invalid range %q (use CIDR notation, e.g. 10.0.0.0/8)", p)
		}
		out = append(out, p)
	}
	return out, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func logLevel() slog.Level {
	var l slog.Level
	if err := l.UnmarshalText([]byte(env("DOCKFLY_LOG_LEVEL", "info"))); err != nil {
		return slog.LevelInfo
	}
	return l
}

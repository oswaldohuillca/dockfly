package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/dockfly/dockfly/internal/api"
	"github.com/dockfly/dockfly/internal/apps"
	"github.com/dockfly/dockfly/internal/auth"
	"github.com/dockfly/dockfly/internal/backup"
	"github.com/dockfly/dockfly/internal/builder"
	"github.com/dockfly/dockfly/internal/docker"
	"github.com/dockfly/dockfly/internal/gitauth"
	"github.com/dockfly/dockfly/internal/notify"
	"github.com/dockfly/dockfly/internal/pages"
	"github.com/dockfly/dockfly/internal/secrets"
	"github.com/dockfly/dockfly/internal/store"
	"github.com/dockfly/dockfly/internal/traefik"
	"github.com/dockfly/dockfly/web"
)

// version se inyecta en el build con -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "healthcheck":
			os.Exit(healthcheck())
		case "backup", "restore":
			os.Exit(backupCommand(os.Args[1], os.Args[2:]))
		}
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel()}))
	if err := run(log); errors.Is(err, errRestart) {
		// 75 (EX_TEMPFAIL): Docker reinicia el contenedor tanto con
		// "unless-stopped" como con "on-failure".
		os.Exit(75)
	} else if err != nil {
		log.Error("dockfly terminó con error", "error", err)
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

	// Una copia restaurada desde el panel se aplica aquí, antes de abrir nada.
	if applied, kept, err := backup.ApplyPending(dataDir); err != nil {
		return fmt.Errorf("aplicando la copia de seguridad: %w", err)
	} else if applied {
		log.Warn("copia de seguridad restaurada; los datos anteriores están en "+kept, "dir", kept)
	}

	st, err := store.Open(ctx, filepath.Join(dataDir, "dockfly.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	signer, err := auth.LoadOrCreateSigner(filepath.Join(dataDir, "secret.key"))
	if err != nil {
		return err
	}

	var dc *docker.Client
	socket := env("DOCKFLY_DOCKER_SOCKET", "/var/run/docker.sock")
	if _, err := os.Stat(socket); err == nil {
		dc = docker.New(socket)
	} else {
		log.Warn("socket de Docker no disponible: no se podrán listar contenedores", "socket", socket)
		socket = ""
	}

	opts := traefik.Options{
		DataDir:      dataDir,
		HTTPAddr:     env("DOCKFLY_HTTP_ADDR", ":80"),
		HTTPSAddr:    env("DOCKFLY_HTTPS_ADDR", ":443"),
		ACMEEmail:    os.Getenv("DOCKFLY_ACME_EMAIL"),
		ACMEStaging:  os.Getenv("DOCKFLY_ACME_STAGING") == "true",
		DockerSocket: socket,
		DockerNet:    env("DOCKFLY_NETWORK", "dockfly"),
		LogLevel:     strings.ToUpper(env("DOCKFLY_TRAEFIK_LOG_LEVEL", "warn")),
	}
	// Los puertos de los streams y los proveedores DNS son configuración
	// estática de Traefik.
	if ports, err := (&api.Server{Store: st}).StreamPorts(ctx); err == nil {
		opts.Streams = ports
	}
	if providers, err := st.ListDNSProviders(ctx); err == nil {
		for _, p := range providers {
			opts.DNSProviders = append(opts.DNSProviders, p.Provider)
		}
	}
	if err := traefik.WriteStatic(opts); err != nil {
		return fmt.Errorf("escribiendo configuración estática de Traefik: %w", err)
	}

	trusted, err := trustedProxies(os.Getenv("DOCKFLY_TRUSTED_PROXIES"))
	if err != nil {
		return err
	}

	// Sitios estáticos: los archivos viven en /data/sites y los sirve un
	// servidor interno al que Traefik reenvía los dominios de cada sitio.
	pagesStorage := &pages.Storage{Root: filepath.Join(dataDir, "sites"), Limits: pages.DefaultLimits}
	if err := pagesStorage.CleanStaging(); err != nil {
		return fmt.Errorf("limpiando subidas incompletas: %w", err)
	}
	pagesSrv := &pages.Server{}
	staticAddr := env("DOCKFLY_STATIC_ADDR", "127.0.0.1:8099")
	maxUpload, err := strconv.ParseInt(env("DOCKFLY_MAX_UPLOAD_MB", "512"), 10, 64)
	if err != nil || maxUpload < 1 {
		return fmt.Errorf("DOCKFLY_MAX_UPLOAD_MB debe ser un número positivo")
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
	srv.Builds = builder.New(buildCfg)
	srv.Apps = apps.New(apps.Config{
		Store: st, Docker: dc, Secrets: box, Providers: srv.Git, LogDir: filepath.Join(dataDir, "builds"),
		Log: log.With("component", "apps"), Network: opts.DockerNet,
		HostDockerSocket: env("DOCKFLY_HOST_DOCKER_SOCKET", "/var/run/docker.sock"),
		Apply:            srv.Apply, OnFinish: srv.OnAppDeployFinished,
	})
	if dc != nil {
		ensureNetwork(ctx, log, dc, opts.DockerNet)
	}
	if err := srv.PrepareCertificates(ctx); err != nil {
		return fmt.Errorf("escribiendo los certificados propios: %w", err)
	}
	// La configuración dinámica se regenera desde la base de datos al
	// arrancar: la base de datos es la única fuente de verdad.
	if err := srv.Apply(ctx); err != nil {
		return fmt.Errorf("escribiendo configuración dinámica de Traefik: %w", err)
	}

	var wg sync.WaitGroup
	wg.Go(func() { srv.Builds.Run(ctx) })
	wg.Go(func() { srv.RunMonitors(ctx) })
	wg.Go(func() { srv.Apps.Run(ctx) })
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
		log.Warn("Traefik desactivado (DOCKFLY_TRAEFIK=off): solo se genera la configuración")
	}

	httpSrv := &http.Server{
		Addr:              env("DOCKFLY_LISTEN", ":81"),
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	staticSrv := &http.Server{
		Addr:              staticAddr,
		Handler:           pagesSrv,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	errc := make(chan error, 2)
	go func() {
		log.Info("panel de Dockfly escuchando", "addr", httpSrv.Addr, "version", version)
		errc <- httpSrv.ListenAndServe()
	}()
	go func() {
		log.Info("servidor de sitios estáticos escuchando", "addr", staticAddr)
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
		log.Warn("reiniciando para aplicar la copia de seguridad")
	} else {
		log.Info("apagando…")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, hs := range []*http.Server{httpSrv, staticSrv} {
		if err := hs.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("cerrando servidor HTTP", "addr", hs.Addr, "error", err)
		}
	}
	wg.Wait()
	if restart.Load() {
		return errRestart
	}
	return nil
}

// restart indica que Dockfly debe salir para que Docker lo reinicie.
var restart atomic.Bool

var errRestart = errors.New("reinicio solicitado")

// ensureNetwork crea la red de Dockfly si no existe y conecta a ella el
// propio contenedor: Traefik (que corre dentro) llega a las apps por su
// nombre en esa red.
func ensureNetwork(ctx context.Context, log *slog.Logger, dc *docker.Client, network string) {
	if network == "" {
		return
	}
	if err := dc.CreateNetwork(ctx, network); err != nil {
		log.Warn("no se pudo crear la red de Dockfly", "red", network, "error", err)
		return
	}
	nets, err := dc.SelfNetworks(ctx)
	if err != nil || len(nets) == 0 {
		return // Dockfly no corre en un contenedor (desarrollo)
	}
	if slices.Contains(nets, network) {
		return
	}
	self, _ := os.Hostname()
	if err := dc.ConnectNetwork(ctx, network, self); err != nil {
		log.Warn("no se pudo conectar Dockfly a su red: las apps no recibirán tráfico", "red", network, "error", err)
		return
	}
	log.Info("Dockfly conectado a la red de las apps", "red", network)
}

// buildConfig lee los límites de los builds de las variables de entorno.
func buildConfig() (builder.Config, error) {
	num := func(key string, def float64) (float64, error) {
		v, err := strconv.ParseFloat(env(key, strconv.FormatFloat(def, 'f', -1, 64)), 64)
		if err != nil || v <= 0 {
			return 0, fmt.Errorf("%s debe ser un número positivo", key)
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

// backupCommand implementa "dockfly backup [archivo]" (por defecto a la
// salida estándar) y "dockfly restore <archivo>", para cuando el panel no
// está disponible.
func backupCommand(cmd string, args []string) int {
	dataDir := env("DOCKFLY_DATA_DIR", "/data")
	switch cmd {
	case "backup":
		ctx := context.Background()
		st, err := store.Open(ctx, filepath.Join(dataDir, "dockfly.db"))
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
			fmt.Fprintln(os.Stderr, "uso: dockfly restore <copia.tar.gz>")
			return 2
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
		fmt.Fprintf(os.Stderr, "Copia del %s preparada: se aplicará la próxima vez que arranque Dockfly.\n",
			m.CreatedAt.Format("02/01/2006 15:04"))
	}
	return 0
}

// loopback convierte ":8099" en "127.0.0.1:8099" para construir una URL.
func loopback(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "127.0.0.1" + addr
	}
	return addr
}

// healthcheck permite usar HEALTHCHECK en una imagen scratch sin curl.
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

// trustedProxies valida la lista de rangos CIDR separados por comas, por
// ejemplo "127.0.0.1/32,10.0.0.0/8". Un rango mal escrito impide arrancar:
// es mejor fallar que confiar en la cabecera equivocada.
func trustedProxies(v string) ([]string, error) {
	var out []string
	for p := range strings.SplitSeq(v, ",") {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		if _, err := netip.ParsePrefix(p); err != nil {
			return nil, fmt.Errorf("DOCKFLY_TRUSTED_PROXIES: rango inválido %q (usa notación CIDR, p. ej. 10.0.0.0/8)", p)
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

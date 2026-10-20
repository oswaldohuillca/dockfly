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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/dockfly/dockfly/internal/api"
	"github.com/dockfly/dockfly/internal/auth"
	"github.com/dockfly/dockfly/internal/docker"
	"github.com/dockfly/dockfly/internal/store"
	"github.com/dockfly/dockfly/internal/traefik"
	"github.com/dockfly/dockfly/web"
)

// version se inyecta en el build con -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck())
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel()}))
	if err := run(log); err != nil {
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
	if err := traefik.WriteStatic(opts); err != nil {
		return fmt.Errorf("escribiendo configuración estática de Traefik: %w", err)
	}

	trusted, err := trustedProxies(os.Getenv("DOCKFLY_TRUSTED_PROXIES"))
	if err != nil {
		return err
	}

	srv := &api.Server{
		Store:    st,
		Signer:   signer,
		Docker:   dc,
		Traefik:  opts,
		Frontend: web.Dist(),
		Version:  version,
		Log:      log,

		TrustedProxies: trusted,
	}
	// La configuración dinámica se regenera desde la base de datos al
	// arrancar: la base de datos es la única fuente de verdad.
	if err := srv.Apply(ctx); err != nil {
		return fmt.Errorf("escribiendo configuración dinámica de Traefik: %w", err)
	}

	var wg sync.WaitGroup
	if env("DOCKFLY_TRAEFIK", "on") != "off" {
		srv.Super = &traefik.Supervisor{
			Bin:        env("DOCKFLY_TRAEFIK_BIN", "/usr/local/bin/traefik"),
			ConfigFile: opts.StaticFile(),
			Log:        log.With("component", "traefik"),
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
	errc := make(chan error, 1)
	go func() {
		log.Info("panel de Dockfly escuchando", "addr", httpSrv.Addr, "version", version)
		errc <- httpSrv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		stop()
		wg.Wait()
		return err
	case <-ctx.Done():
	}

	log.Info("apagando…")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("cerrando servidor HTTP", "error", err)
	}
	wg.Wait()
	return nil
}

// healthcheck permite usar HEALTHCHECK en una imagen scratch sin curl.
func healthcheck() int {
	addr := env("DOCKFLY_LISTEN", ":81")
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
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

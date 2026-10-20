// Package traefik genera la configuración de Traefik a partir del estado de
// Dockfly y supervisa el proceso de Traefik.
package traefik

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/dockfly/dockfly/internal/store"
)

const (
	CertResolver = "letsencrypt"

	mwRedirectHTTPS  = "dockfly-redirect-https"
	mwHSTS           = "dockfly-hsts"
	transportNoCheck = "dockfly-insecure"

	dynamicFile = "dockfly.yml"
)

// Options define la configuración estática de Traefik.
type Options struct {
	DataDir      string // directorio persistente (/data)
	HTTPAddr     string // ":80"
	HTTPSAddr    string // ":443"
	ACMEEmail    string
	ACMEStaging  bool
	DockerSocket string // vacío si Docker no está disponible
	DockerNet    string // red en la que Traefik busca contenedores con labels
	LogLevel     string
}

func (o Options) DynamicDir() string { return filepath.Join(o.DataDir, "traefik", "dynamic") }
func (o Options) StaticFile() string { return filepath.Join(o.DataDir, "traefik", "traefik.yml") }

// WriteStatic escribe traefik.yml. Traefik solo lo lee al arrancar.
func WriteStatic(o Options) error {
	if err := os.MkdirAll(o.DynamicDir(), 0o755); err != nil {
		return err
	}

	acme := map[string]any{
		"storage":       filepath.Join(o.DataDir, "traefik", "acme.json"),
		"httpChallenge": map[string]any{"entryPoint": "web"},
	}
	if o.ACMEEmail != "" {
		acme["email"] = o.ACMEEmail
	}
	if o.ACMEStaging {
		acme["caServer"] = "https://acme-staging-v02.api.letsencrypt.org/directory"
	}

	providers := map[string]any{
		"file": map[string]any{"directory": o.DynamicDir(), "watch": true},
		// Aplica los cambios del panel casi al instante (por defecto, 2 s).
		"providersThrottleDuration": "200ms",
	}
	if o.DockerSocket != "" {
		docker := map[string]any{
			"endpoint":         "unix://" + o.DockerSocket,
			"exposedByDefault": false,
		}
		if o.DockerNet != "" {
			docker["network"] = o.DockerNet
		}
		providers["docker"] = docker
	}

	cfg := map[string]any{
		"global": map[string]any{"checkNewVersion": false, "sendAnonymousUsage": false},
		"log":    map[string]any{"level": o.LogLevel, "noColor": true},
		"entryPoints": map[string]any{
			"web":       entryPoint(o.HTTPAddr),
			"websecure": entryPoint(o.HTTPSAddr),
		},
		"providers":             providers,
		"certificatesResolvers": map[string]any{CertResolver: map[string]any{"acme": acme}},
	}
	return writeYAML(o.StaticFile(), cfg)
}

// entryPoint descarta las cabeceras que se hacen pasar por otras
// (X_Forwarded_For por X-Forwarded-For), evitando que un cliente falsifique
// las cabeceras que gestiona Traefik en backends PHP, CGI o WSGI.
func entryPoint(addr string) map[string]any {
	return map[string]any{
		"address": addr,
		"http":    map[string]any{"aliasHeadersStrategy": "delete"},
	}
}

// WriteDynamic regenera la configuración dinámica completa a partir de los
// proxy hosts. Traefik detecta el cambio en caliente, sin reinicios.
func WriteDynamic(o Options, hosts []*store.ProxyHost) error {
	return writeYAML(filepath.Join(o.DynamicDir(), dynamicFile), BuildDynamic(hosts))
}

// BuildDynamic traduce los proxy hosts a la configuración dinámica de Traefik.
func BuildDynamic(hosts []*store.ProxyHost) map[string]any {
	routers := map[string]any{}
	services := map[string]any{}
	middlewares := map[string]any{
		mwRedirectHTTPS: map[string]any{
			"redirectScheme": map[string]any{"scheme": "https", "permanent": true},
		},
		mwHSTS: map[string]any{
			"headers": map[string]any{
				"stsSeconds":           31536000,
				"stsIncludeSubdomains": true,
				"stsPreload":           false,
			},
		},
	}
	needsInsecure := false

	for _, h := range hosts {
		if !h.Enabled || len(h.Domains) == 0 {
			continue
		}
		name := "ph-" + strconv.FormatInt(h.ID, 10)
		rule := hostRule(h.Domains)

		svc := map[string]any{
			"servers":        []map[string]any{{"url": BackendURL(h)}},
			"passHostHeader": h.PreserveHost,
		}
		if h.ForwardScheme == "https" && h.SkipTLSVerify {
			svc["serversTransport"] = transportNoCheck
			needsInsecure = true
		}
		services[name] = map[string]any{"loadBalancer": svc}

		httpRouter := map[string]any{
			"rule":        rule,
			"entryPoints": []string{"web"},
			"service":     name,
		}
		if h.SSL {
			var secureMW []string
			if h.HSTS {
				secureMW = append(secureMW, mwHSTS)
			}
			secure := map[string]any{
				"rule":        rule,
				"entryPoints": []string{"websecure"},
				"service":     name,
				"tls":         map[string]any{"certResolver": CertResolver},
			}
			if len(secureMW) > 0 {
				secure["middlewares"] = secureMW
			}
			routers[name+"-secure"] = secure

			if h.ForceHTTPS {
				httpRouter["middlewares"] = []string{mwRedirectHTTPS}
			}
		}
		routers[name] = httpRouter
	}

	// Traefik rechaza secciones vacías, así que solo incluimos las que tienen contenido.
	httpCfg := map[string]any{"middlewares": middlewares}
	if len(routers) > 0 {
		httpCfg["routers"] = routers
		httpCfg["services"] = services
	}
	if needsInsecure {
		httpCfg["serversTransports"] = map[string]any{
			transportNoCheck: map[string]any{"insecureSkipVerify": true},
		}
	}
	return map[string]any{"http": httpCfg}
}

// BackendURL construye la URL del servicio de destino.
func BackendURL(h *store.ProxyHost) string {
	u := url.URL{
		Scheme: h.ForwardScheme,
		Host:   net.JoinHostPort(h.ForwardHost, strconv.Itoa(h.ForwardPort)),
	}
	return u.String()
}

func hostRule(domains []string) string {
	parts := make([]string, len(domains))
	for i, d := range domains {
		parts[i] = fmt.Sprintf("Host(`%s`)", d)
	}
	return strings.Join(parts, " || ")
}

// writeYAML escribe de forma atómica (archivo temporal + rename) para que
// Traefik nunca lea un archivo a medio escribir.
func writeYAML(path string, v any) error {
	data, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	data = append([]byte("# Generado por Dockfly. No editar: se sobrescribe en cada cambio.\n"), data...)

	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

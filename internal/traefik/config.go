// Package traefik genera la configuración de Traefik a partir del estado de
// Dockfly y supervisa el proceso de Traefik.
package traefik

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/dockfly/dockfly/internal/store"
)

const (
	CertResolver = "letsencrypt"

	mwRedirectHTTPS  = "dockfly-redirect-https"
	mwHSTS           = "dockfly-hsts"
	mwCompress       = "dockfly-compress"
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
func (o Options) ACMEFile() string   { return filepath.Join(o.DataDir, "traefik", "acme.json") }

// WriteStatic escribe traefik.yml. Traefik solo lo lee al arrancar.
func WriteStatic(o Options) error {
	if err := os.MkdirAll(o.DynamicDir(), 0o755); err != nil {
		return err
	}

	acme := map[string]any{
		"storage":       o.ACMEFile(),
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
		// JSON: Dockfly lo analiza (errores de Let's Encrypt) y lo reescribe legible.
		"log": map[string]any{"level": o.LogLevel, "noColor": true, "format": "json"},
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

// Route es una entrada de enrutado: un conjunto de dominios que Traefik
// reenvía a una URL. Proxy hosts y sitios estáticos se traducen a rutas.
type Route struct {
	Name          string // único: da nombre a los routers y al servicio
	Domains       []string
	URL           string
	PreserveHost  bool
	SkipTLSVerify bool
	SSL           bool
	ForceHTTPS    bool
	HSTS          bool
	Compress      bool
	// Middlewares extra (p. ej. los de una lista de acceso), en orden.
	Middlewares []string
}

// ProxyHostRoute traduce un proxy host. Devuelve ok=false si está desactivado.
func ProxyHostRoute(h *store.ProxyHost) (Route, bool) {
	if !h.Enabled || len(h.Domains) == 0 {
		return Route{}, false
	}
	return Route{
		Name:          "ph-" + strconv.FormatInt(h.ID, 10),
		Domains:       h.Domains,
		URL:           BackendURL(h),
		PreserveHost:  h.PreserveHost,
		SkipTLSVerify: h.ForwardScheme == "https" && h.SkipTLSVerify,
		SSL:           h.SSL,
		ForceHTTPS:    h.ForceHTTPS,
		HSTS:          h.HSTS,
	}, true
}

// SiteRoute traduce un sitio estático: todos apuntan al servidor estático de
// Dockfly, que elige el sitio por la cabecera Host (por eso se conserva).
func SiteRoute(site *store.Site, staticURL string) (Route, bool) {
	if len(site.Domains) == 0 {
		return Route{}, false
	}
	return Route{
		Name:         "site-" + strconv.FormatInt(site.ID, 10),
		Domains:      site.Domains,
		URL:          staticURL,
		PreserveHost: true,
		SSL:          site.SSL,
		ForceHTTPS:   site.ForceHTTPS,
		HSTS:         site.HSTS,
		Compress:     true,
	}, true
}

// WriteDynamic regenera la configuración dinámica completa. Traefik detecta
// el cambio en caliente, sin reinicios. extra son middlewares adicionales
// (listas de acceso) a los que pueden referirse las rutas.
func WriteDynamic(o Options, routes []Route, extra map[string]any) error {
	return writeYAML(filepath.Join(o.DynamicDir(), dynamicFile), BuildDynamic(routes, extra))
}

// BuildDynamic traduce las rutas a la configuración dinámica de Traefik.
func BuildDynamic(routes []Route, extra map[string]any) map[string]any {
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
		// gzip/brotli/zstd según lo que acepte el navegador.
		mwCompress: map[string]any{"compress": map[string]any{}},
	}
	for name, mw := range extra {
		middlewares[name] = mw
	}
	needsInsecure := false

	for _, rt := range routes {
		rule := hostRule(rt.Domains)

		svc := map[string]any{
			"servers":        []map[string]any{{"url": rt.URL}},
			"passHostHeader": rt.PreserveHost,
		}
		if rt.SkipTLSVerify {
			svc["serversTransport"] = transportNoCheck
			needsInsecure = true
		}
		services[rt.Name] = map[string]any{"loadBalancer": svc}

		// El control de acceso va primero: no tiene sentido comprimir una
		// respuesta que se va a denegar.
		common := slices.Clone(rt.Middlewares)
		if rt.Compress {
			common = append(common, mwCompress)
		}

		httpRouter := map[string]any{
			"rule":        rule,
			"entryPoints": []string{"web"},
			"service":     rt.Name,
		}
		httpMW := common
		if rt.SSL {
			secureMW := slices.Clone(common)
			if rt.HSTS {
				secureMW = append(secureMW, mwHSTS)
			}
			secure := map[string]any{
				"rule":        rule,
				"entryPoints": []string{"websecure"},
				"service":     rt.Name,
				"tls":         map[string]any{"certResolver": CertResolver},
			}
			if len(secureMW) > 0 {
				secure["middlewares"] = secureMW
			}
			routers[rt.Name+"-secure"] = secure

			if rt.ForceHTTPS {
				// Si se redirige, comprimir no tiene sentido.
				httpMW = []string{mwRedirectHTTPS}
			}
		}
		if len(httpMW) > 0 {
			httpRouter["middlewares"] = httpMW
		}
		routers[rt.Name] = httpRouter
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

// AccessListMiddlewares genera los middlewares de una lista de acceso: IPs
// permitidas y/o usuario y contraseña. Devuelve sus nombres, en orden.
func AccessListMiddlewares(id int64, allowIPs []string, users map[string]string, into map[string]any) []string {
	var names []string
	prefix := "acl-" + strconv.FormatInt(id, 10)
	if len(allowIPs) > 0 {
		into[prefix+"-ip"] = map[string]any{"ipAllowList": map[string]any{"sourceRange": allowIPs}}
		names = append(names, prefix+"-ip")
	}
	if len(users) > 0 {
		list := make([]string, 0, len(users))
		for u, hash := range users {
			list = append(list, u+":"+hash)
		}
		slices.Sort(list)
		into[prefix+"-auth"] = map[string]any{"basicAuth": map[string]any{
			"users": list, "realm": "Dockfly",
			// La aplicación de detrás no necesita (ni debe ver) la contraseña.
			"removeHeader": true,
		}}
		names = append(names, prefix+"-auth")
	}
	return names
}

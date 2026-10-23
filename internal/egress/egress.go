// Package egress da a los builds salida a internet, y solo a internet.
//
// Los contenedores de build corren en una red interna de Docker (sin ruta
// hacia fuera) y salen por un proxy HTTP que Dockfly escucha en esa red. El
// proxy solo conecta con direcciones públicas: un script de build no puede
// leer las credenciales de la nube (169.254.169.254), ni llegar al host, a
// la red local o a otros contenedores.
package egress

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"syscall"
	"time"

	"github.com/dockfly/dockfly/internal/docker"
)

// Network es la red interna de los builds.
const Network = "dockfly-build"

// Port es el puerto del proxy dentro de la red de builds.
const Port = "3128"

// blocked son los rangos que no son internet aunque Go no los marque como
// privados.
var blocked = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), // CGNAT (y metadata de Alibaba Cloud)
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),   // NAT64: puede envolver una IPv4 privada
	netip.MustParsePrefix("64:ff9b:1::/48"), // NAT64 local
	netip.MustParsePrefix("2002::/16"),      // 6to4: ídem
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("100::/64"),
}

// Public indica si ip es una dirección de internet.
func Public(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return false // loopback, link-local, multicast, sin especificar, privadas
	}
	for _, p := range blocked {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// allow decide a qué IPs se conecta (los tests lo cambian).
var allow = Public

var errNotPublic = errors.New("destino no permitido: los builds solo pueden conectar con internet")

// dialer comprueba la IP justo al conectar (después de resolver el nombre),
// así un DNS que responda con una IP interna tampoco sirve.
var dialer = &net.Dialer{
	Timeout: 30 * time.Second,
	Control: func(network, address string, _ syscall.RawConn) error {
		ap, err := netip.ParseAddrPort(address)
		if err != nil || !allow(ap.Addr()) {
			return fmt.Errorf("%s: %w", address, errNotPublic)
		}
		return nil
	},
}

// Proxy es un proxy HTTP de salida: túneles CONNECT (HTTPS, git…) y
// peticiones http:// absolutas.
type Proxy struct {
	Log *slog.Logger

	forward *httputil.ReverseProxy
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.tunnel(w, r)
		return
	}
	if r.URL.Scheme != "http" || r.URL.Host == "" {
		http.Error(w, "esto es un proxy: usa CONNECT o una URL http:// absoluta", http.StatusBadRequest)
		return
	}
	p.forward.ServeHTTP(w, r)
}

func (p *Proxy) tunnel(w http.ResponseWriter, r *http.Request) {
	up, err := dialer.DialContext(r.Context(), "tcp", r.Host)
	if err != nil {
		p.deny(w, r.Host, err)
		return
	}
	rc := http.NewResponseController(w)
	w.WriteHeader(http.StatusOK)
	if err := rc.Flush(); err != nil {
		up.Close()
		return
	}
	down, buf, err := rc.Hijack()
	if err != nil {
		up.Close()
		return
	}
	// Lo que el cliente ya haya enviado tras la cabecera CONNECT.
	if n := buf.Reader.Buffered(); n > 0 {
		pending, _ := buf.Peek(n)
		if _, err := up.Write(pending); err != nil {
			up.Close()
			down.Close()
			return
		}
	}
	done := make(chan struct{}, 2)
	go func() { pipe(up, down); done <- struct{}{} }()
	go func() { pipe(down, up); done <- struct{}{} }()
	<-done
	<-done
	up.Close()
	down.Close()
}

// pipe copia de src a dst y cierra la escritura de dst al acabar (el otro
// sentido sigue abierto hasta que también termine).
func pipe(dst, src net.Conn) {
	_, _ = io.Copy(dst, src)
	if c, ok := dst.(interface{ CloseWrite() error }); ok {
		_ = c.CloseWrite()
	} else {
		dst.Close()
	}
}

func (p *Proxy) deny(w http.ResponseWriter, host string, err error) {
	if errors.Is(err, errNotPublic) {
		p.Log.Warn("build: conexión bloqueada", "destino", host)
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	http.Error(w, err.Error(), http.StatusBadGateway)
}

// NewProxy crea el proxy.
func NewProxy(log *slog.Logger) *Proxy {
	p := &Proxy{Log: log}
	p.forward = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL = pr.In.URL
			pr.Out.Host = pr.In.URL.Host
		},
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			Proxy:                 nil,
			MaxIdleConns:          16,
			IdleConnTimeout:       30 * time.Second,
			ResponseHeaderTimeout: 2 * time.Minute,
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			p.deny(w, r.URL.Host, err)
		},
	}
	return p
}

// Serve sirve el proxy en addr (subcomando "dockfly egress").
func Serve(addr string, log *slog.Logger) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           NewProxy(log),
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       time.Minute,
	}
	return srv.ListenAndServe()
}

// Container es el contenedor del proxy; OutNetwork, su red con salida.
const (
	Container  = "dockfly-egress"
	OutNetwork = "dockfly-egress"
)

// Start prepara la red de builds y (re)crea el contenedor del proxy, con la
// misma imagen que Dockfly, entre esa red y una con salida a internet.
// Dockfly no se conecta a la red de builds: así los builds no llegan al
// panel ni a Traefik por una IP privada. Devuelve la URL del proxy.
func Start(ctx context.Context, dc *docker.Client, image string) (string, error) {
	err := dc.CreateInternalNetwork(ctx, Network, map[string]string{"dockfly.role": "build"})
	var apiErr *docker.APIError
	if err != nil && !(errors.As(err, &apiErr) && apiErr.Status == http.StatusConflict) {
		return "", fmt.Errorf("creando la red %s: %w", Network, err)
	}
	if err := dc.CreateNetwork(ctx, OutNetwork); err != nil {
		return "", fmt.Errorf("creando la red %s: %w", OutNetwork, err)
	}
	// Se recrea en cada arranque para que use la versión actual de Dockfly.
	if err := dc.RemoveContainer(ctx, Container); err != nil {
		return "", err
	}
	id, err := dc.CreateContainer(ctx, docker.ContainerSpec{
		Name: Container, Image: image, Cmd: []string{"egress"},
		User:     "65534:65534",
		Labels:   map[string]string{"dockfly.role": "egress"},
		MemoryMB: 256, CPUs: 1, PidsLimit: 256,
		Network: OutNetwork, Restart: "unless-stopped", ReadOnly: true, NoHealthcheck: true,
	})
	if err != nil {
		return "", fmt.Errorf("creando el contenedor %s: %w", Container, err)
	}
	if err := dc.ConnectNetwork(ctx, Network, id); err != nil {
		return "", fmt.Errorf("conectando %s a %s: %w", Container, Network, err)
	}
	if err := dc.StartContainer(ctx, id); err != nil {
		return "", fmt.Errorf("iniciando %s: %w", Container, err)
	}
	// Por nombre: el DNS de Docker lo resuelve dentro de la red de builds
	// aunque cambie de IP al reiniciarse.
	return "http://" + net.JoinHostPort(Container, Port), nil
}

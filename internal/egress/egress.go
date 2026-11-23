// Package egress gives builds access to the internet, and only to the
// internet.
//
// Build containers run on an internal Docker network (with no route out) and
// reach the outside through an HTTP proxy that Dockfly listens on in that
// network. The proxy only connects to public addresses: a build script cannot
// read the cloud credentials (169.254.169.254), nor reach the host, the local
// network or other containers.
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

// Network is the internal network of the builds.
const Network = "dockfly-build"

// Port is the proxy's port inside the build network.
const Port = "3128"

// blocked are the ranges that are not the internet even though Go does not
// mark them as private.
var blocked = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), // CGNAT (and Alibaba Cloud metadata)
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),   // NAT64: may wrap a private IPv4
	netip.MustParsePrefix("64:ff9b:1::/48"), // local NAT64
	netip.MustParsePrefix("2002::/16"),      // 6to4: same
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("100::/64"),
}

// Public reports whether ip is an internet address.
func Public(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return false // loopback, link-local, multicast, unspecified, private
	}
	for _, p := range blocked {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// LinkLocalOrUnspecified flags the IPs that must not be reached even when
// the admin is the one configuring the destination: the cloud metadata
// endpoint (169.254.169.254, fe80::/10) and the unspecified address.
func LinkLocalOrUnspecified(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

// SafeTransport is an http.Transport that, when connecting (with DNS already
// resolved, so it covers DNS rebinding), rejects LinkLocalOrUnspecified.
// Unlike the build proxy, it allows loopback and private networks: it is used
// for destinations the admin configures (Git accounts, S3 endpoint), where
// self-hosting on the LAN is legitimate, while still not letting SSRF steal
// the cloud credentials.
func SafeTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	d := &net.Dialer{Timeout: 30 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		ap, err := netip.ParseAddrPort(address)
		if err != nil {
			return err
		}
		if LinkLocalOrUnspecified(ap.Addr()) {
			return fmt.Errorf("destination not allowed (metadata/link-local): %s", ap.Addr())
		}
		return nil
	}}
	t.DialContext = d.DialContext
	return t
}

// allow decides which IPs may be connected to (the tests override it).
var allow = Public

var errNotPublic = errors.New("destination not allowed: builds can only connect to the internet")

// dialer checks the IP right as it connects (after resolving the name), so a
// DNS that answers with an internal IP does not work either.
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

// Proxy is an outbound HTTP proxy: CONNECT tunnels (HTTPS, git…) and
// absolute http:// requests.
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
		http.Error(w, "this is a proxy: use CONNECT or an absolute http:// URL", http.StatusBadRequest)
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
	// Whatever the client already sent after the CONNECT header.
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

// pipe copies from src to dst and closes dst for writing when done (the
// other direction stays open until it finishes too).
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
		p.Log.Warn("build: connection blocked", "destination", host)
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	http.Error(w, err.Error(), http.StatusBadGateway)
}

// NewProxy creates the proxy.
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

// Serve serves the proxy on addr (the "dockfly egress" subcommand).
func Serve(addr string, log *slog.Logger) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           NewProxy(log),
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       time.Minute,
	}
	return srv.ListenAndServe()
}

// Container is the proxy's container; OutNetwork, its network with egress.
const (
	Container  = "dockfly-egress"
	OutNetwork = "dockfly-egress"
)

// Start prepares the build network and (re)creates the proxy container, with
// the same image as Dockfly, straddling that network and one with internet
// access. Dockfly does not join the build network: that way builds cannot
// reach the panel or Traefik over a private IP. It returns the proxy URL.
func Start(ctx context.Context, dc *docker.Client, image string) (string, error) {
	err := dc.CreateInternalNetwork(ctx, Network, map[string]string{"dockfly.role": "build"})
	var apiErr *docker.APIError
	if err != nil && !(errors.As(err, &apiErr) && apiErr.Status == http.StatusConflict) {
		return "", fmt.Errorf("creating the %s network: %w", Network, err)
	}
	if err := dc.CreateNetwork(ctx, OutNetwork); err != nil {
		return "", fmt.Errorf("creating the %s network: %w", OutNetwork, err)
	}
	// It is recreated on every startup so that it runs the current Dockfly
	// version.
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
		return "", fmt.Errorf("creating the %s container: %w", Container, err)
	}
	if err := dc.ConnectNetwork(ctx, Network, id); err != nil {
		return "", fmt.Errorf("connecting %s to %s: %w", Container, Network, err)
	}
	if err := dc.StartContainer(ctx, id); err != nil {
		return "", fmt.Errorf("starting %s: %w", Container, err)
	}
	// By name: Docker's DNS resolves it inside the build network even if its
	// IP changes after a restart.
	return "http://" + net.JoinHostPort(Container, Port), nil
}

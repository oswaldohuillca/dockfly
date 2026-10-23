package egress

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

func TestPublic(t *testing.T) {
	for ip, want := range map[string]bool{
		"1.1.1.1": true, "140.82.112.3": true, "2606:4700::1111": true,
		"169.254.169.254": false, "127.0.0.1": false, "10.0.0.5": false, "172.17.0.1": false,
		"192.168.1.1": false, "100.100.100.200": false, "0.0.0.0": false, "255.255.255.255": false,
		"::1": false, "fd00:ec2::254": false, "fe80::1": false, "::ffff:10.0.0.1": false,
		"64:ff9b::a00:1": false, "2002:a00:1::": false, "224.0.0.1": false,
	} {
		if got := Public(netip.MustParseAddr(ip)); got != want {
			t.Errorf("Public(%s) = %v, quiero %v", ip, got, want)
		}
	}
}

func proxyClient(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()
	p := httptest.NewServer(NewProxy(slog.New(slog.DiscardHandler)))
	t.Cleanup(p.Close)
	u, _ := url.Parse(p.URL)
	return p, &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(u)}}
}

// Un build no llega a servicios internos, ni por HTTP ni por CONNECT.
func TestProxyBlocksInternal(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "secreto") }))
	defer backend.Close()
	p, client := proxyClient(t)

	resp, err := client.Get(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || strings.Contains(string(body), "secreto") {
		t.Fatalf("HTTP a loopback: %d %s", resp.StatusCode, body)
	}

	conn, err := net.Dial("tcp", p.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	host := strings.TrimPrefix(backend.URL, "http://")
	fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", host, host)
	cr, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	if cr.StatusCode != http.StatusForbidden {
		t.Fatalf("CONNECT a loopback: %d", cr.StatusCode)
	}
}

// Con destinos permitidos, el proxy reenvía HTTP y abre túneles (HTTPS).
func TestProxyForwards(t *testing.T) {
	allow = func(netip.Addr) bool { return true }
	defer func() { allow = Public }()
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "hola") }))
	defer plain.Close()
	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "seguro") }))
	defer tls.Close()
	_, client := proxyClient(t)

	for _, c := range []struct{ url, want string }{{plain.URL, "hola"}, {tls.URL, "seguro"}} {
		tr := client.Transport.(*http.Transport)
		tr.TLSClientConfig = tls.Client().Transport.(*http.Transport).TLSClientConfig
		resp, err := client.Get(c.url)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != c.want {
			t.Errorf("%s: %d %q", c.url, resp.StatusCode, body)
		}
	}
}

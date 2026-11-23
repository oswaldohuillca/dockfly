package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"
)

func selfSigned(t *testing.T, names ...string) (certPEM, keyPEM string) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: names[0]}, DNSNames: names,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(90 * 24 * time.Hour)}
	der, _ := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	kder, _ := x509.MarshalECPrivateKey(key)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}))
}

func TestCertificatesAndWildcards(t *testing.T) {
	ts, _, dynDir := testServerWithPages(t)
	c := newClient(t, ts)
	c.do("POST", "/api/setup", adminJSON)
	host := func(domain string) (int, map[string]any) {
		return c.do("POST", "/api/proxy-hosts", fmt.Sprintf(`{"domains":[%q],"target_type":"external","forward_host":"1.1.1.1","forward_port":80,"ssl":true}`, domain))
	}

	// With no DNS zone, a wildcard with SSL isn't possible.
	if code, res := host("*.zone.com"); code != 400 || res["code"] != "wildcard_ssl_needs_dns" {
		t.Errorf("wildcard with no zone: %d %v", code, res)
	}

	// A DNS provider from the catalog: its required fields are enforced.
	if code, _ := c.do("PUT", "/api/dns-providers", `{"provider":"cloudflare","zones":["zone.com"],"env":{}}`); code != 400 {
		t.Errorf("cloudflare with no token: %d", code)
	}
	if code, res := c.do("PUT", "/api/dns-providers", `{"provider":"cloudflare","zones":["zone.com"],"env":{"CLOUDFLARE_DNS_API_TOKEN":"cf-secret"}}`); code != 200 {
		t.Fatalf("save provider: %d %v", code, res)
	}
	_, list := c.doList("GET", "/api/dns-providers")
	if strings.Contains(fmt.Sprint(list), "cf-secret") {
		t.Fatal("the DNS credentials must never leave the server")
	}
	// Sending the masked value back keeps the credential.
	c.do("PUT", "/api/dns-providers", fmt.Sprintf(`{"provider":"cloudflare","zones":["zone.com","otra.com"],"env":{"CLOUDFLARE_DNS_API_TOKEN":%q}}`, secretMask))
	if env := serverOf(t, ts).DNSEnv(); len(env) != 1 || env[0] != "CLOUDFLARE_DNS_API_TOKEN=cf-secret" {
		t.Errorf("the credential must be kept for Traefik: %v", env)
	}
	static, _ := os.ReadFile(serverOf(t, ts).Traefik.StaticFile())
	if !strings.Contains(string(static), "dns-cloudflare") || !strings.Contains(string(static), "provider: cloudflare") {
		t.Errorf("traefik.yml must have the DNS resolver:\n%s", static)
	}

	if code, res := host("*.zone.com"); code != 201 {
		t.Fatalf("wildcard with a zone: %d %v", code, res)
	}
	if code, _ := host("app.zone.com"); code != 201 {
		t.Fatal("a specific host in the zone")
	}
	cfg, _ := os.ReadFile(dynDir + "/dockfly.yml")
	if !strings.Contains(string(cfg), "certResolver: dns-cloudflare") || !strings.Contains(string(cfg), "main: '*.zone.com'") {
		t.Errorf("the hosts in the zone must use the DNS resolver:\n%s", cfg)
	}

	// Our own certificate: it covers its domain and Traefik loads it from file.
	cert, key := selfSigned(t, "own.com", "*.own.com")
	_, otherKey := selfSigned(t, "other.com")
	body, _ := json.Marshal(map[string]string{"cert": cert, "key": otherKey})
	if code, _ := c.do("POST", "/api/custom-certificates", string(body)); code != 400 {
		t.Errorf("a key that doesn't match must be rejected: %d", code)
	}
	body, _ = json.Marshal(map[string]string{"cert": cert, "key": key})
	if code, res := c.do("POST", "/api/custom-certificates", string(body)); code != 201 {
		t.Fatalf("upload certificate: %d %v", code, res)
	}
	if code, _ := host("api.own.com"); code != 201 {
		t.Fatal("a host covered by our own certificate")
	}
	cfg, _ = os.ReadFile(dynDir + "/dockfly.yml")
	if !strings.Contains(string(cfg), "certFile:") || !strings.Contains(string(cfg), "keyFile:") {
		t.Errorf("Traefik must load our own certificate:\n%s", cfg)
	}
	keyFile := serverOf(t, ts).certDir() + "/1.key"
	if fi, err := os.Stat(keyFile); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("the key on disk must have 0600 permissions: %v %v", fi, err)
	}
	_, certs := c.doList("GET", "/api/certificates")
	found := false
	for _, x := range certs {
		m := x.(map[string]any)
		if m["domain"] == "api.own.com" {
			found = m["status"] == "valid"
		}
	}
	if !found {
		t.Errorf("our own wildcard must cover api.own.com in the status: %v", certs)
	}
}

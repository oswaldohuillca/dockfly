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

	// Sin zona DNS, un comodín con SSL no es posible.
	if code, res := host("*.zona.com"); code != 400 || !strings.Contains(res["error"].(string), "zona DNS") {
		t.Errorf("comodín sin zona: %d %v", code, res)
	}

	// Proveedor DNS del catálogo: se exigen sus campos obligatorios.
	if code, _ := c.do("PUT", "/api/dns-providers", `{"provider":"cloudflare","zones":["zona.com"],"env":{}}`); code != 400 {
		t.Errorf("cloudflare sin token: %d", code)
	}
	if code, res := c.do("PUT", "/api/dns-providers", `{"provider":"cloudflare","zones":["zona.com"],"env":{"CLOUDFLARE_DNS_API_TOKEN":"cf-secreto"}}`); code != 200 {
		t.Fatalf("guardar proveedor: %d %v", code, res)
	}
	_, list := c.doList("GET", "/api/dns-providers")
	if strings.Contains(fmt.Sprint(list), "cf-secreto") {
		t.Fatal("las credenciales DNS nunca deben salir del servidor")
	}
	// Reenviar el valor enmascarado conserva la credencial.
	c.do("PUT", "/api/dns-providers", fmt.Sprintf(`{"provider":"cloudflare","zones":["zona.com","otra.com"],"env":{"CLOUDFLARE_DNS_API_TOKEN":%q}}`, secretMask))
	if env := serverOf(t, ts).DNSEnv(); len(env) != 1 || env[0] != "CLOUDFLARE_DNS_API_TOKEN=cf-secreto" {
		t.Errorf("la credencial debe conservarse para Traefik: %v", env)
	}
	static, _ := os.ReadFile(serverOf(t, ts).Traefik.StaticFile())
	if !strings.Contains(string(static), "dns-cloudflare") || !strings.Contains(string(static), "provider: cloudflare") {
		t.Errorf("traefik.yml debe tener el resolver DNS:\n%s", static)
	}

	if code, res := host("*.zona.com"); code != 201 {
		t.Fatalf("comodín con zona: %d %v", code, res)
	}
	if code, _ := host("app.zona.com"); code != 201 {
		t.Fatal("host concreto en la zona")
	}
	cfg, _ := os.ReadFile(dynDir + "/dockfly.yml")
	if !strings.Contains(string(cfg), "certResolver: dns-cloudflare") || !strings.Contains(string(cfg), "main: '*.zona.com'") {
		t.Errorf("los hosts de la zona deben usar el resolver DNS:\n%s", cfg)
	}

	// Certificado propio: cubre su dominio y Traefik lo carga de archivo.
	cert, key := selfSigned(t, "propio.com", "*.propio.com")
	_, otherKey := selfSigned(t, "otro.com")
	body, _ := json.Marshal(map[string]string{"cert": cert, "key": otherKey})
	if code, _ := c.do("POST", "/api/custom-certificates", string(body)); code != 400 {
		t.Errorf("una clave que no corresponde debe rechazarse: %d", code)
	}
	body, _ = json.Marshal(map[string]string{"cert": cert, "key": key})
	if code, res := c.do("POST", "/api/custom-certificates", string(body)); code != 201 {
		t.Fatalf("subir certificado: %d %v", code, res)
	}
	if code, _ := host("api.propio.com"); code != 201 {
		t.Fatal("host cubierto por el certificado propio")
	}
	cfg, _ = os.ReadFile(dynDir + "/dockfly.yml")
	if !strings.Contains(string(cfg), "certFile:") || !strings.Contains(string(cfg), "keyFile:") {
		t.Errorf("Traefik debe cargar el certificado propio:\n%s", cfg)
	}
	keyFile := serverOf(t, ts).certDir() + "/1.key"
	if fi, err := os.Stat(keyFile); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("la clave en disco debe tener permisos 0600: %v %v", fi, err)
	}
	_, certs := c.doList("GET", "/api/certificates")
	found := false
	for _, x := range certs {
		m := x.(map[string]any)
		if m["domain"] == "api.propio.com" {
			found = m["status"] == "valid"
		}
	}
	if !found {
		t.Errorf("el comodín propio debe cubrir api.propio.com en el estado: %v", certs)
	}
}

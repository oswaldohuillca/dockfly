package traefik

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// El error real que dio Let's Encrypt (staging) para un dominio sin DNS.
const realNXDomain = `unable to generate a certificate for the domains [web.ejemplo.com]: resolver: one or more domains had a problem: [web.ejemplo.com: invalid authorization: acme: error: 400 :: urn:ietf:params:acme:error:dns :: DNS problem: NXDOMAIN looking up A for web.ejemplo.com - check that a DNS record exists for this domain]`

func makeCert(t *testing.T, domain string, notBefore, notAfter time.Time, issuer string) string {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), DNSNames: []string{domain},
		Subject: pkix.Name{CommonName: domain}, Issuer: pkix.Name{CommonName: issuer},
		NotBefore: notBefore, NotAfter: notAfter,
	}
	// Autofirmado: el emisor es el propio sujeto, así que lo fijamos aparte.
	tpl.Subject = pkix.Name{CommonName: issuer}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func writeACME(t *testing.T, certs map[string]string) string {
	t.Helper()
	type cert struct {
		Domain      map[string]any `json:"domain"`
		Certificate string         `json:"certificate"`
	}
	var list []cert
	for d, c := range certs {
		list = append(list, cert{Domain: map[string]any{"main": d}, Certificate: c})
	}
	data, _ := json.Marshal(map[string]any{CertResolver: map[string]any{"Certificates": list}})
	path := filepath.Join(t.TempDir(), "acme.json")
	os.WriteFile(path, data, 0o600)
	return path
}

func logLine(domains []string, msg string, at time.Time) []byte {
	b, _ := json.Marshal(map[string]any{
		"level": "error", "message": "Unable to obtain ACME certificate for domains",
		"error": msg, "domains": domains, "time": at.Format(time.RFC3339), "providerName": "letsencrypt.acme",
	})
	return b
}

func TestCertStatus(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	acme := writeACME(t, map[string]string{
		"ok.com":       makeCert(t, "ok.com", now.Add(-10*day), now.Add(80*day), "R11"),
		"pronto.com":   makeCert(t, "pronto.com", now.Add(-80*day), now.Add(5*day), "R11"),
		"caducado.com": makeCert(t, "caducado.com", now.Add(-100*day), now.Add(-day), "R11"),
		"renueva.com":  makeCert(t, "renueva.com", now.Add(-70*day), now.Add(20*day), "(STAGING) Ersatz Edamame E1"),
		"viejo.com":    makeCert(t, "viejo.com", now.Add(-day), now.Add(89*day), "R11"),
	})

	var notified []string
	tr := &CertTracker{OnError: func(d []string, _ string) { notified = append(notified, d...) }}
	tr.Observe(logLine([]string{"web.ejemplo.com"}, realNXDomain, now.Add(-time.Hour)))
	tr.Observe(logLine([]string{"renueva.com"}, "acme: error: 429 :: rateLimited :: too many certificates", now.Add(-time.Hour)))
	// Un error de antes de emitir el certificado actual ya no cuenta.
	tr.Observe(logLine([]string{"viejo.com"}, "timeout during connect", now.Add(-2*day)))
	tr.Observe([]byte(`{"level":"info","message":"Testing certificate renew..."}`))
	tr.Observe([]byte("no es json"))

	got, err := tr.Status(acme, []string{"ok.com", "pronto.com", "caducado.com", "renueva.com", "viejo.com", "web.ejemplo.com", "nuevo.com"}, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	byDomain := map[string]CertStatus{}
	for _, s := range got {
		byDomain[s.Domain] = s
	}
	want := map[string]string{
		"ok.com": CertValid, "pronto.com": CertExpiring, "caducado.com": CertExpired,
		"renueva.com": CertValid, "viejo.com": CertValid, "web.ejemplo.com": CertError, "nuevo.com": CertPending,
	}
	for d, w := range want {
		if byDomain[d].Status != w {
			t.Errorf("%s: estado %q, esperaba %q", d, byDomain[d].Status, w)
		}
	}
	if s := byDomain["web.ejemplo.com"]; !strings.Contains(s.Hint, "registro DNS") {
		t.Errorf("el error NXDOMAIN debe dar una pista sobre el DNS: %q", s.Hint)
	}
	if s := byDomain["renueva.com"]; s.Error == "" || !s.Staging || !strings.Contains(s.Hint, "limitó") {
		t.Errorf("con certificado vigente, un error de renovación debe mostrarse: %+v", s)
	}
	if s := byDomain["viejo.com"]; s.Error != "" {
		t.Errorf("un error anterior al certificado actual no debe mostrarse: %q", s.Error)
	}
	if s := byDomain["ok.com"]; s.NotAfter == nil || s.Issuer != "R11" || s.Staging {
		t.Errorf("certificado válido: %+v", s)
	}
	if strings.Join(notified, ",") != "web.ejemplo.com,renueva.com,viejo.com" {
		t.Errorf("cada error debe notificarse: %v", notified)
	}

	// Sin acme.json (instalación nueva) todo está pendiente.
	if got, err := tr.Status(filepath.Join(t.TempDir(), "no-existe.json"), []string{"x.com"}, now, nil); err != nil || got[0].Status != CertPending {
		t.Errorf("sin acme.json: %+v %v", got, err)
	}
}

func TestFormatLog(t *testing.T) {
	got := formatLog([]byte(`{"level":"error","message":"Algo falló","error":"boom","time":"2026-10-20T12:00:00Z","b":1}`))
	if got != "traefik ERROR Algo falló b=1 error=boom" {
		t.Errorf("formatLog: %q", got)
	}
	if formatLog([]byte("texto plano")) != "texto plano" {
		t.Error("las líneas que no son JSON se dejan tal cual")
	}
}

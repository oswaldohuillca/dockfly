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

// The real error Let's Encrypt (staging) gave for a domain without DNS.
const realNXDomain = `unable to generate a certificate for the domains [web.example.com]: resolver: one or more domains had a problem: [web.example.com: invalid authorization: acme: error: 400 :: urn:ietf:params:acme:error:dns :: DNS problem: NXDOMAIN looking up A for web.example.com - check that a DNS record exists for this domain]`

func makeCert(t *testing.T, domain string, notBefore, notAfter time.Time, issuer string) string {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), DNSNames: []string{domain},
		Subject: pkix.Name{CommonName: domain}, Issuer: pkix.Name{CommonName: issuer},
		NotBefore: notBefore, NotAfter: notAfter,
	}
	// Self-signed: the issuer is the subject itself, so we set it apart.
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
		"soon.com":     makeCert(t, "soon.com", now.Add(-80*day), now.Add(5*day), "R11"),
		"expired.com":  makeCert(t, "expired.com", now.Add(-100*day), now.Add(-day), "R11"),
		"renewing.com": makeCert(t, "renewing.com", now.Add(-70*day), now.Add(20*day), "(STAGING) Ersatz Edamame E1"),
		"old.com":      makeCert(t, "old.com", now.Add(-day), now.Add(89*day), "R11"),
	})

	var notified []string
	tr := &CertTracker{OnError: func(d []string, _ string) { notified = append(notified, d...) }}
	tr.Observe(logLine([]string{"web.example.com"}, realNXDomain, now.Add(-time.Hour)))
	tr.Observe(logLine([]string{"renewing.com"}, "acme: error: 429 :: rateLimited :: too many certificates", now.Add(-time.Hour)))
	// An error from before the current certificate was issued no longer counts.
	tr.Observe(logLine([]string{"old.com"}, "timeout during connect", now.Add(-2*day)))
	tr.Observe([]byte(`{"level":"info","message":"Testing certificate renew..."}`))
	tr.Observe([]byte("not json"))

	got, err := tr.Status(acme, []string{"ok.com", "soon.com", "expired.com", "renewing.com", "old.com", "web.example.com", "new.com"}, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	byDomain := map[string]CertStatus{}
	for _, s := range got {
		byDomain[s.Domain] = s
	}
	want := map[string]string{
		"ok.com": CertValid, "soon.com": CertExpiring, "expired.com": CertExpired,
		"renewing.com": CertValid, "old.com": CertValid, "web.example.com": CertError, "new.com": CertPending,
	}
	for d, w := range want {
		if byDomain[d].Status != w {
			t.Errorf("%s: status %q, wanted %q", d, byDomain[d].Status, w)
		}
	}
	if s := byDomain["web.example.com"]; !strings.Contains(s.Hint, "DNS record") {
		t.Errorf("the NXDOMAIN error must hint at the DNS: %q", s.Hint)
	}
	if s := byDomain["renewing.com"]; s.Error == "" || !s.Staging || !strings.Contains(s.Hint, "rate-limited") {
		t.Errorf("with a current certificate, a renewal error must still show: %+v", s)
	}
	if s := byDomain["old.com"]; s.Error != "" {
		t.Errorf("an error older than the current certificate must not show: %q", s.Error)
	}
	if s := byDomain["ok.com"]; s.NotAfter == nil || s.Issuer != "R11" || s.Staging {
		t.Errorf("valid certificate: %+v", s)
	}
	if strings.Join(notified, ",") != "web.example.com,renewing.com,old.com" {
		t.Errorf("every error must be notified: %v", notified)
	}

	// Without acme.json (a fresh install) everything is pending.
	if got, err := tr.Status(filepath.Join(t.TempDir(), "does-not-exist.json"), []string{"x.com"}, now, nil); err != nil || got[0].Status != CertPending {
		t.Errorf("without acme.json: %+v %v", got, err)
	}
}

func TestFormatLog(t *testing.T) {
	got := formatLog([]byte(`{"level":"error","message":"Something failed","error":"boom","time":"2026-10-20T12:00:00Z","b":1}`))
	if got != "traefik ERROR Something failed b=1 error=boom" {
		t.Errorf("formatLog: %q", got)
	}
	if formatLog([]byte("plain text")) != "plain text" {
		t.Error("lines that are not JSON are left as they are")
	}
}

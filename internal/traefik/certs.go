package traefik

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Certificate status of a domain.
const (
	CertValid    = "valid"    // issued and current
	CertExpiring = "expiring" // expires soon and the renewal is not arriving
	CertExpired  = "expired"
	CertError    = "error"   // Let's Encrypt rejected it and there is no cert
	CertPending  = "pending" // requested, still no answer
)

// Traefik renews 30 days before expiry: if fewer than 14 days are left,
// the renewal is failing.
const expiringThreshold = 14 * 24 * time.Hour

type CertStatus struct {
	Domain   string     `json:"domain"`
	Status   string     `json:"status"`
	NotAfter *time.Time `json:"not_after,omitempty"`
	Issuer   string     `json:"issuer,omitempty"`
	Staging  bool       `json:"staging"` // issued by Let's Encrypt's staging environment
	// Let's Encrypt's last error. With a current certificate, it means the
	// renewal is failing.
	Error   string     `json:"error,omitempty"`
	ErrorAt *time.Time `json:"error_at,omitempty"`
	Hint    string     `json:"hint,omitempty"`
}

type acmeError struct {
	msg string
	at  time.Time
}

// CertTracker remembers the Let's Encrypt errors Traefik writes to its
// log: it exposes them through no API.
type CertTracker struct {
	mu     sync.Mutex
	errors map[string]acmeError
	// OnError, if not nil, is called on every new failure (notifications).
	OnError func(domains []string, msg string)
}

var bracketDomains = regexp.MustCompile(`for the domains \[([^\]]+)\]`)

// Observe parses one line of Traefik's JSON log.
func (t *CertTracker) Observe(line []byte) {
	if len(line) == 0 || line[0] != '{' {
		return
	}
	var entry struct {
		Message string   `json:"message"`
		Error   string   `json:"error"`
		Domains []string `json:"domains"`
		Time    string   `json:"time"`
	}
	if json.Unmarshal(line, &entry) != nil || !strings.HasPrefix(entry.Message, "Unable to obtain ACME certificate") {
		return
	}
	domains := entry.Domains
	if len(domains) == 0 {
		if m := bracketDomains.FindStringSubmatch(entry.Error); m != nil {
			domains = strings.Fields(strings.ReplaceAll(m[1], ",", " "))
		}
	}
	at, err := time.Parse(time.RFC3339, entry.Time)
	if err != nil {
		at = time.Now()
	}
	t.mu.Lock()
	if t.errors == nil {
		t.errors = map[string]acmeError{}
	}
	for _, d := range domains {
		t.errors[strings.ToLower(d)] = acmeError{msg: entry.Error, at: at.UTC()}
	}
	onError := t.OnError
	t.mu.Unlock()
	if onError != nil && len(domains) > 0 {
		onError(domains, entry.Error)
	}
}

// Status combines the certificates stored in acme.json with the observed
// errors, for every domain with SSL.
func (t *CertTracker) Status(acmePath string, domains []string, now time.Time, custom map[string]*x509.Certificate) ([]CertStatus, error) {
	certs, err := ReadACME(acmePath)
	if err != nil {
		return nil, err
	}
	// Custom certificates take precedence, as they do in Traefik.
	for d, c := range custom {
		certs[d] = c
	}
	// A wildcard covers one level: app.example.com uses the *.example.com one.
	lookup := func(d string) (*x509.Certificate, bool) {
		if c, ok := certs[d]; ok {
			return c, true
		}
		if _, parent, ok := strings.Cut(d, "."); ok {
			c, ok := certs["*."+parent]
			return c, ok
		}
		return nil, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]CertStatus, 0, len(domains))
	for _, d := range domains {
		st := CertStatus{Domain: d, Status: CertPending}
		cert, found := lookup(d)
		if c := cert; found {
			na := c.NotAfter.UTC()
			st.NotAfter, st.Issuer = &na, issuerName(c)
			st.Staging = strings.Contains(strings.ToUpper(st.Issuer), "STAGING") || strings.Contains(st.Issuer, "(STAGING)")
			switch left := c.NotAfter.Sub(now); {
			case left <= 0:
				st.Status = CertExpired
			case left < expiringThreshold:
				st.Status = CertExpiring
			default:
				st.Status = CertValid
			}
		}
		// An error older than the current certificate no longer matters.
		if e, ok := t.errors[d]; ok && (st.NotAfter == nil || e.at.After(cert.NotBefore)) {
			at := e.at
			st.Error, st.ErrorAt, st.Hint = e.msg, &at, Hint(e.msg)
			if st.NotAfter == nil {
				st.Status = CertError
			}
		}
		out = append(out, st)
	}
	return out, nil
}

// ReadACME reads the certificates Traefik stored in acme.json, indexed by
// domain.
func ReadACME(path string) (map[string]*x509.Certificate, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) || len(data) == 0 {
		return map[string]*x509.Certificate{}, nil
	}
	if err != nil {
		return nil, err
	}
	var resolvers map[string]struct {
		Certificates []struct {
			Domain struct {
				Main string   `json:"main"`
				SANs []string `json:"sans"`
			} `json:"domain"`
			Certificate string `json:"certificate"` // base64-encoded PEM
		} `json:"Certificates"`
	}
	if err := json.Unmarshal(data, &resolvers); err != nil {
		return nil, err
	}
	out := map[string]*x509.Certificate{}
	for _, r := range resolvers {
		for _, c := range r.Certificates {
			raw, err := base64.StdEncoding.DecodeString(c.Certificate)
			if err != nil {
				continue
			}
			block, _ := pem.Decode(raw)
			if block == nil {
				continue
			}
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				continue
			}
			names := append([]string{c.Domain.Main}, c.Domain.SANs...)
			names = append(names, cert.DNSNames...)
			for _, n := range names {
				if n = strings.ToLower(n); n != "" {
					out[n] = cert
				}
			}
		}
	}
	return out, nil
}

func issuerName(c *x509.Certificate) string {
	if c.Issuer.CommonName != "" {
		if len(c.Issuer.Organization) > 0 {
			return c.Issuer.Organization[0] + " " + c.Issuer.CommonName
		}
		return c.Issuer.CommonName
	}
	if len(c.Issuer.Organization) > 0 {
		return c.Issuer.Organization[0]
	}
	return ""
}

// Hint turns the usual Let's Encrypt errors into what needs checking.
func Hint(msg string) string {
	m := strings.ToLower(msg)
	switch {
	case strings.Contains(m, "nxdomain"), strings.Contains(m, "no valid a records"), strings.Contains(m, "servfail"):
		return "The domain has no DNS record (A/AAAA) pointing to this server."
	case strings.Contains(m, "ratelimited"), strings.Contains(m, "too many"):
		return "Let's Encrypt rate-limited the requests for this domain: wait a while before retrying."
	case strings.Contains(m, "caa"):
		return "A CAA record on the domain does not allow Let's Encrypt to issue certificates."
	case strings.Contains(m, "timeout"), strings.Contains(m, "connection refused"), strings.Contains(m, "connection reset"):
		return "Let's Encrypt could not reach port 80 on this server: check the firewall and that the DNS points here."
	case strings.Contains(m, "unauthorized"), strings.Contains(m, "invalid response"):
		return "The domain seems to point at another server: Let's Encrypt did not find the challenge response here."
	case strings.Contains(m, "rejectedidentifier"), strings.Contains(m, "policy forbids"):
		return "Let's Encrypt does not issue certificates for this domain (e.g. .local or an IP)."
	}
	return ""
}

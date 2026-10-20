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

// Estado del certificado de un dominio.
const (
	CertValid    = "valid"    // emitido y vigente
	CertExpiring = "expiring" // caduca pronto y la renovación no llega
	CertExpired  = "expired"
	CertError    = "error"   // Let's Encrypt lo rechazó y no hay certificado
	CertPending  = "pending" // pedido, todavía sin respuesta
)

// Traefik renueva a 30 días de la caducidad: si quedan menos de 14, la
// renovación está fallando.
const expiringThreshold = 14 * 24 * time.Hour

type CertStatus struct {
	Domain   string     `json:"domain"`
	Status   string     `json:"status"`
	NotAfter *time.Time `json:"not_after,omitempty"`
	Issuer   string     `json:"issuer,omitempty"`
	Staging  bool       `json:"staging"` // emitido por el entorno de pruebas de Let's Encrypt
	// Último error de Let's Encrypt. Con un certificado vigente, indica que
	// la renovación está fallando.
	Error   string     `json:"error,omitempty"`
	ErrorAt *time.Time `json:"error_at,omitempty"`
	Hint    string     `json:"hint,omitempty"`
}

type acmeError struct {
	msg string
	at  time.Time
}

// CertTracker recuerda los errores de Let's Encrypt que Traefik escribe en
// su log: no los expone en ninguna API.
type CertTracker struct {
	mu     sync.Mutex
	errors map[string]acmeError
	// OnError, si no es nil, se llama con cada fallo nuevo (notificaciones).
	OnError func(domains []string, msg string)
}

var bracketDomains = regexp.MustCompile(`for the domains \[([^\]]+)\]`)

// Observe analiza una línea del log JSON de Traefik.
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

// Status combina los certificados guardados en acme.json con los errores
// observados, para cada dominio con SSL.
func (t *CertTracker) Status(acmePath string, domains []string, now time.Time, custom map[string]*x509.Certificate) ([]CertStatus, error) {
	certs, err := ReadACME(acmePath)
	if err != nil {
		return nil, err
	}
	// Los certificados propios tienen prioridad, como en Traefik.
	for d, c := range custom {
		certs[d] = c
	}
	// Un comodín cubre un nivel: app.ejemplo.com usa el de *.ejemplo.com.
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
		// Un error anterior a la emisión del certificado actual ya no importa.
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

// ReadACME lee los certificados que Traefik guardó en acme.json, indexados
// por dominio.
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
			Certificate string `json:"certificate"` // PEM en base64
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

// Hint traduce los errores habituales de Let's Encrypt a qué hay que revisar.
func Hint(msg string) string {
	m := strings.ToLower(msg)
	switch {
	case strings.Contains(m, "nxdomain"), strings.Contains(m, "no valid a records"), strings.Contains(m, "servfail"):
		return "El dominio no tiene un registro DNS (A/AAAA) que apunte a este servidor."
	case strings.Contains(m, "ratelimited"), strings.Contains(m, "too many"):
		return "Let's Encrypt limitó las peticiones para este dominio: espera un rato antes de reintentar."
	case strings.Contains(m, "caa"):
		return "Un registro CAA del dominio no autoriza a Let's Encrypt a emitir certificados."
	case strings.Contains(m, "timeout"), strings.Contains(m, "connection refused"), strings.Contains(m, "connection reset"):
		return "Let's Encrypt no pudo conectar con el puerto 80 de este servidor: revisa el firewall y que el DNS apunte aquí."
	case strings.Contains(m, "unauthorized"), strings.Contains(m, "invalid response"):
		return "El dominio parece apuntar a otro servidor: Let's Encrypt no encontró aquí la respuesta del desafío."
	case strings.Contains(m, "rejectedidentifier"), strings.Contains(m, "policy forbids"):
		return "Let's Encrypt no emite certificados para este dominio (p. ej. .local o una IP)."
	}
	return ""
}

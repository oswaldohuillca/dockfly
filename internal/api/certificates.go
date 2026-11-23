package api

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/dockfly/dockfly/internal/store"
	"github.com/dockfly/dockfly/internal/traefik"
)

// --- Catalog of DNS providers (lego's most common ones) ---

type dnsField struct {
	Env      string `json:"env"`
	Label    string `json:"label"`
	Secret   bool   `json:"secret"`
	Optional bool   `json:"optional"`
}

type dnsProviderInfo struct {
	Code   string     `json:"code"`
	Name   string     `json:"name"`
	Fields []dnsField `json:"fields"`
	Help   string     `json:"help"`
}

// The variable names are verified against the Traefik v3.7 binary.
var dnsCatalog = []dnsProviderInfo{
	{Code: "cloudflare", Name: "Cloudflare", Help: "Create an API token with the Zone → DNS → Edit permission for the zone.",
		Fields: []dnsField{{Env: "CLOUDFLARE_DNS_API_TOKEN", Label: "API token", Secret: true}}},
	{Code: "route53", Name: "Amazon Route 53", Help: "An IAM user with permissions on route53:ChangeResourceRecordSets for the zone.",
		Fields: []dnsField{{Env: "AWS_ACCESS_KEY_ID", Label: "Access key ID"}, {Env: "AWS_SECRET_ACCESS_KEY", Label: "Secret access key", Secret: true},
			{Env: "AWS_REGION", Label: "Region", Optional: true}, {Env: "AWS_HOSTED_ZONE_ID", Label: "Hosted zone ID", Optional: true}}},
	{Code: "digitalocean", Name: "DigitalOcean", Help: "A personal token with write permission.",
		Fields: []dnsField{{Env: "DO_AUTH_TOKEN", Label: "Token", Secret: true}}},
	{Code: "ovh", Name: "OVH", Help: "Create the keys at api.ovh.com/createToken with access to /domain/zone.",
		Fields: []dnsField{{Env: "OVH_ENDPOINT", Label: "Endpoint (ovh-eu, ovh-ca…)"}, {Env: "OVH_APPLICATION_KEY", Label: "Application key"},
			{Env: "OVH_APPLICATION_SECRET", Label: "Application secret", Secret: true}, {Env: "OVH_CONSUMER_KEY", Label: "Consumer key", Secret: true}}},
	{Code: "gandiv5", Name: "Gandi", Fields: []dnsField{{Env: "GANDIV5_PERSONAL_ACCESS_TOKEN", Label: "Personal access token", Secret: true}}},
	{Code: "godaddy", Name: "GoDaddy", Fields: []dnsField{{Env: "GODADDY_API_KEY", Label: "API key"}, {Env: "GODADDY_API_SECRET", Label: "API secret", Secret: true}}},
	{Code: "namecheap", Name: "Namecheap", Help: "Enable API access and add the server's IP to the allowlist.",
		Fields: []dnsField{{Env: "NAMECHEAP_API_USER", Label: "User"}, {Env: "NAMECHEAP_API_KEY", Label: "API key", Secret: true}}},
	{Code: "porkbun", Name: "Porkbun", Fields: []dnsField{{Env: "PORKBUN_API_KEY", Label: "API key"}, {Env: "PORKBUN_SECRET_API_KEY", Label: "Secret API key", Secret: true}}},
	{Code: "duckdns", Name: "Duck DNS", Fields: []dnsField{{Env: "DUCKDNS_TOKEN", Label: "Token", Secret: true}}},
}

var (
	providerCodeRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,30}$`)
	envKeyRe       = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,63}$`)
)

// dnsEnvDenied are variable names that never reach the Traefik process even
// if they match the pattern: they would alter library loading or network egress.
var dnsEnvDenied = map[string]bool{
	"LD_PRELOAD": true, "LD_LIBRARY_PATH": true, "LD_AUDIT": true, "PATH": true,
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "ALL_PROXY": true, "NO_PROXY": true,
}

var (
	errDNSProviderCodeInvalid = newError("dns_provider_code_invalid", "Invalid provider code (e.g. cloudflare, hetznerv2…)")
	errDNSZoneInvalid         = newError("dns_zone_invalid", "Zones: invalid domain: {domain}")
	errDNSNoZones             = newError("dns_no_zones", "Zones: enter at least one domain")
	errDNSZoneWildcard        = newError("dns_zone_wildcard", "Zones are domains without a wildcard (e.g. example.com)")
	errDNSEnvNameInvalid      = newError("dns_env_name_invalid", "Invalid variable name: \"{name}\"")
	errDNSFieldMissing        = newError("dns_field_missing", "{field} is missing")
	errDNSEnvRequired         = newError("dns_env_required", "Enter the provider's environment variables (see the lego documentation)")
	errDNSZoneTaken           = newError("dns_zone_taken", "The zone {zone} is already managed by {provider}")
	errDNSProviderNotFound    = newError("dns_provider_not_found", "Provider not found")
	errCertKeyPairInvalid     = newError("cert_key_pair_invalid", "The certificate and key are invalid or don't match: {detail}")
	errCertUnreadable         = newError("cert_unreadable", "Unreadable certificate")
	errCertNoDomains          = newError("cert_no_domains", "The certificate doesn't name any domain")
	errCertNotFound           = newError("cert_not_found", "Certificate not found")
	errWildcardSSLNeedsDNS    = newError("wildcard_ssl_needs_dns", "For a wildcard domain with SSL, set up its DNS zone in Settings → Certificates (Let's Encrypt only issues wildcards via DNS) or upload your own certificate")
)

func catalogEntry(code string) (dnsProviderInfo, bool) {
	i := slices.IndexFunc(dnsCatalog, func(p dnsProviderInfo) bool { return p.Code == code })
	if i < 0 {
		return dnsProviderInfo{}, false
	}
	return dnsCatalog[i], true
}

func (s *Server) handleDNSCatalog(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, dnsCatalog)
}

type dnsProviderView struct {
	*store.DNSProvider
	Env map[string]string `json:"env"` // secrets masked out
}

func (s *Server) dnsEnv(p *store.DNSProvider) map[string]string {
	env := map[string]string{}
	if raw, err := s.Secrets.Open(p.EnvEnc); err == nil {
		_ = json.Unmarshal(raw, &env)
	}
	return env
}

func (s *Server) handleListDNSProviders(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListDNSProviders(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	out := make([]dnsProviderView, len(list))
	for i, p := range list {
		env := s.dnsEnv(p)
		for k := range env {
			env[k] = secretMask // any of these variables may be a credential
		}
		out[i] = dnsProviderView{DNSProvider: p, Env: env}
	}
	writeJSON(w, http.StatusOK, out)
}

type dnsProviderInput struct {
	Provider string            `json:"provider"`
	Zones    []string          `json:"zones"`
	Env      map[string]string `json:"env"` // a masked value keeps the stored one
}

func (in *dnsProviderInput) build(previous map[string]string) (*store.DNSProvider, map[string]string, error) {
	code := strings.ToLower(strings.TrimSpace(in.Provider))
	if !providerCodeRe.MatchString(code) {
		return nil, nil, errDNSProviderCodeInvalid
	}
	zones, err := normalizeDomains(in.Zones)
	if err != nil {
		var e apiError
		if errors.As(err, &e) && e.Code == errInvalidDomain.Code {
			return nil, nil, errDNSZoneInvalid.With("domain", e.Params["domain"])
		}
		return nil, nil, errDNSNoZones
	}
	for _, z := range zones {
		if strings.HasPrefix(z, "*.") {
			return nil, nil, errDNSZoneWildcard
		}
	}
	env := map[string]string{}
	for k, v := range in.Env {
		k = strings.TrimSpace(k)
		if !envKeyRe.MatchString(k) || dnsEnvDenied[k] {
			return nil, nil, errDNSEnvNameInvalid.With("name", k)
		}
		if v == secretMask {
			v = previous[k]
		}
		if v = strings.TrimSpace(v); v != "" {
			env[k] = v
		}
	}
	if info, ok := catalogEntry(code); ok {
		for _, f := range info.Fields {
			if !f.Optional && env[f.Env] == "" {
				return nil, nil, errDNSFieldMissing.With("field", f.Label, "env", f.Env)
			}
		}
	} else if len(env) == 0 {
		return nil, nil, errDNSEnvRequired
	}
	return &store.DNSProvider{Provider: code, Zones: zones}, env, nil
}

func (s *Server) handleSaveDNSProvider(w http.ResponseWriter, r *http.Request) {
	var in dnsProviderInput
	if !decode(w, r, &in) {
		return
	}
	ctx := r.Context()
	list, err := s.Store.ListDNSProviders(ctx)
	if err != nil {
		s.internalError(w, err)
		return
	}
	var existing *store.DNSProvider
	for _, p := range list {
		if p.Provider == strings.ToLower(strings.TrimSpace(in.Provider)) {
			existing = p
		}
	}
	previous := map[string]string{}
	if existing != nil {
		previous = s.dnsEnv(existing)
	}
	p, env, err := in.build(previous)
	if err != nil {
		writeErrorFrom(w, http.StatusBadRequest, err, errUnexpected)
		return
	}
	// A zone can only have one provider.
	for _, other := range list {
		if other.Provider == p.Provider {
			continue
		}
		for _, z := range p.Zones {
			if slices.Contains(other.Zones, z) {
				writeError(w, http.StatusConflict, errDNSZoneTaken.With("zone", z, "provider", other.Provider))
				return
			}
		}
	}
	raw, _ := json.Marshal(env)
	p.EnvEnc = s.Secrets.Seal(raw)
	if existing != nil {
		p.ID = existing.ID
		err = s.Store.UpdateDNSProvider(ctx, p)
	} else {
		err = s.Store.CreateDNSProvider(ctx, p)
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	// The credentials reach Traefik at startup: it always gets restarted.
	if err := s.syncDNSProviders(ctx, true); err != nil {
		s.internalError(w, err)
		return
	}
	s.applyAndRespond(w, r, http.StatusOK, map[string]any{"id": p.ID, "traefik_restarted": s.Super != nil})
}

func (s *Server) handleDeleteDNSProvider(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeleteDNSProvider(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, errDNSProviderNotFound)
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	if err := s.syncDNSProviders(r.Context(), true); err != nil {
		s.internalError(w, err)
		return
	}
	if err := s.Apply(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// syncDNSProviders rewrites traefik.yml with one resolver per provider and
// restarts Traefik (which reads the credentials at startup).
func (s *Server) syncDNSProviders(ctx context.Context, restart bool) error {
	list, err := s.Store.ListDNSProviders(ctx)
	if err != nil {
		return err
	}
	var codes []string
	for _, p := range list {
		codes = append(codes, p.Provider)
	}
	slices.Sort(codes)
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	s.Traefik.DNSProviders = codes
	if err := traefik.WriteStatic(s.Traefik); err != nil {
		return err
	}
	if restart && s.Super != nil {
		s.Super.Restart()
	}
	return nil
}

// DNSEnv is every provider's credentials, for Traefik's environment.
func (s *Server) DNSEnv() []string {
	list, err := s.Store.ListDNSProviders(context.Background())
	if err != nil {
		return nil
	}
	var out []string
	for _, p := range list {
		for k, v := range s.dnsEnv(p) {
			out = append(out, k+"="+v)
		}
	}
	return out
}

// --- Own certificates ---

func (s *Server) certDir() string { return filepath.Join(s.DataDir, "certs") }

func (s *Server) handleListCustomCerts(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListCertificates(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleUploadCustomCert(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
		Cert string `json:"cert"`
		Key  string `json:"key"`
	}
	if !decode(w, r, &in) {
		return
	}
	// X509KeyPair checks that the key matches the certificate.
	pair, err := tls.X509KeyPair([]byte(in.Cert), []byte(in.Key))
	if err != nil {
		writeError(w, http.StatusBadRequest, errCertKeyPairInvalid.With("detail", err.Error()))
		return
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		writeError(w, http.StatusBadRequest, errCertUnreadable)
		return
	}
	domains := leaf.DNSNames
	if len(domains) == 0 && leaf.Subject.CommonName != "" {
		domains = []string{leaf.Subject.CommonName}
	}
	if len(domains) == 0 {
		writeError(w, http.StatusBadRequest, errCertNoDomains)
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = domains[0]
	}
	c := &store.Certificate{Name: name, Domains: domains, Issuer: leaf.Issuer.CommonName, NotAfter: leaf.NotAfter.UTC(),
		CertPEM: in.Cert, KeyEnc: s.Secrets.Seal([]byte(in.Key))}
	if err := s.Store.CreateCertificate(r.Context(), c); err != nil {
		s.internalError(w, err)
		return
	}
	if err := s.writeCertFiles(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	s.applyAndRespond(w, r, http.StatusCreated, c)
}

func (s *Server) handleDeleteCustomCert(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeleteCertificate(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, errCertNotFound)
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	if err := s.writeCertFiles(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	if err := s.Apply(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeCertFiles writes our own certificates to disk (Traefik reads them from
// files) and removes the ones that no longer exist. The key goes to disk in
// the clear, with 0600 permissions: that's how Traefik needs it.
func (s *Server) writeCertFiles(ctx context.Context) error {
	list, err := s.Store.ListCertificates(ctx)
	if err != nil {
		return err
	}
	dir := s.certDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	keep := map[string]bool{}
	for _, c := range list {
		key, err := s.Secrets.Open(c.KeyEnc)
		if err != nil {
			return err
		}
		base := strconv.FormatInt(c.ID, 10)
		keep[base+".crt"], keep[base+".key"] = true, true
		if err := os.WriteFile(filepath.Join(dir, base+".crt"), []byte(c.CertPEM), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, base+".key"), key, 0o600); err != nil {
			return err
		}
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !keep[e.Name()] {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	return nil
}

// --- Picking the certificate for each route ---

// certPlan decides, for a set of domains with SSL, where the certificate
// comes from: one of ours that covers them, Let's Encrypt over DNS if they
// sit in a configured zone, or Let's Encrypt over HTTP.
type certPlan struct {
	custom []*store.Certificate
	dns    []*store.DNSProvider
}

func (s *Server) loadCertPlan(ctx context.Context) (*certPlan, error) {
	custom, err := s.Store.ListCertificates(ctx)
	if err != nil {
		return nil, err
	}
	dns, err := s.Store.ListDNSProviders(ctx)
	if err != nil {
		return nil, err
	}
	return &certPlan{custom: custom, dns: dns}, nil
}

// covers reports whether a certificate for names covers domain (wildcards
// cover a single level).
func covers(names []string, domain string) bool {
	for _, n := range names {
		if n == domain {
			return true
		}
		if rest, ok := strings.CutPrefix(n, "*."); ok {
			if _, parent, ok := strings.Cut(domain, "."); ok && parent == rest && !strings.HasPrefix(domain, "*.") {
				return true
			}
		}
	}
	return false
}

// inZone reports whether domain belongs to the zone (or is the zone).
func inZone(zone, domain string) bool {
	domain = strings.TrimPrefix(domain, "*.")
	return domain == zone || strings.HasSuffix(domain, "."+zone)
}

func (p *certPlan) resolver(domains []string) string {
	for _, c := range p.custom {
		if !slices.ContainsFunc(domains, func(d string) bool { return !covers(c.Domains, d) }) {
			return traefik.NoResolver
		}
	}
	for _, prov := range p.dns {
		all := !slices.ContainsFunc(domains, func(d string) bool {
			return !slices.ContainsFunc(prov.Zones, func(z string) bool { return inZone(z, d) })
		})
		if all {
			return traefik.DNSResolver(prov.Provider)
		}
	}
	return ""
}

// checkSSL answers 400 if SSL is asked for a wildcard covered by neither one
// of our certificates nor a DNS zone: Let's Encrypt doesn't issue wildcards
// over HTTP.
func (s *Server) checkSSL(w http.ResponseWriter, r *http.Request, domains []string, ssl bool) bool {
	if !ssl || !slices.ContainsFunc(domains, func(d string) bool { return strings.HasPrefix(d, "*.") }) {
		return true
	}
	plan, err := s.loadCertPlan(r.Context())
	if err != nil {
		s.internalError(w, err)
		return false
	}
	if plan.resolver(domains) == "" {
		writeError(w, http.StatusBadRequest, errWildcardSSLNeedsDNS)
		return false
	}
	return true
}

// customCertMap returns our own certificates indexed by domain, for the
// certificate status view.
func (s *Server) customCertMap(ctx context.Context) map[string]*x509.Certificate {
	out := map[string]*x509.Certificate{}
	list, err := s.Store.ListCertificates(ctx)
	if err != nil {
		return out
	}
	for _, c := range list {
		block, _ := pem.Decode([]byte(c.CertPEM))
		if block == nil {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			continue
		}
		for _, d := range c.Domains {
			out[d] = cert
		}
	}
	return out
}

// PrepareCertificates writes our own certificates to disk. It's called at
// startup, before the first Apply (Traefik's configuration references them).
func (s *Server) PrepareCertificates(ctx context.Context) error { return s.writeCertFiles(ctx) }

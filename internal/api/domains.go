package api

import (
	"net/http"
	"regexp"
	"slices"
	"strings"
)

var domainRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$|^localhost$`)

// normalizeDomains lowercases, drops the trailing dot and the duplicates,
// and validates every domain.
func normalizeDomains(in []string) ([]string, error) {
	var out []string
	for _, d := range in {
		d = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(d)), ".")
		if d == "" || slices.Contains(out, d) {
			continue
		}
		// A single-level wildcard is allowed: *.example.com.
		if !domainRe.MatchString(strings.TrimPrefix(d, "*.")) || strings.Contains(strings.TrimPrefix(d, "*."), "*") {
			return nil, errInvalidDomain.With("domain", d)
		}
		out = append(out, d)
	}
	if len(out) == 0 {
		return nil, errNoDomains
	}
	return out, nil
}

var (
	errInvalidDomain = newError("invalid_domain", "Invalid domain: {domain}")
	errNoDomains     = newError("no_domains", "Enter at least one domain")
	// {kind} is proxy_host, site, app or redirect.
	errDomainInUse = newError("domain_in_use", "The domain {domain} is already used by another {kind}")
)

// Kinds of resource that use domains (they also appear in errDomainInUse).
const (
	ownerProxyHost = "proxy_host"
	ownerSite      = "site"
	ownerApp       = "app"
	ownerRedirect  = "redirect"
)

// owner identifies the resource being edited, so it isn't taken to be in
// conflict with itself. id = 0 when creating.
type owner struct {
	kind string
	id   int64
}

// checkDomains answers 409 if another proxy host or another site already uses
// one of the domains: Traefik can't route one domain to two targets.
func (s *Server) checkDomains(w http.ResponseWriter, r *http.Request, domains []string, self owner) bool {
	ctx := r.Context()
	hosts, err := s.Store.ListProxyHosts(ctx)
	if err != nil {
		s.internalError(w, err)
		return false
	}
	sites, err := s.Store.ListSites(ctx)
	if err != nil {
		s.internalError(w, err)
		return false
	}

	conflict := func(kind string, id int64, theirs []string) bool {
		if kind == self.kind && id == self.id {
			return false
		}
		for _, d := range domains {
			if slices.Contains(theirs, d) {
				writeError(w, http.StatusConflict, errDomainInUse.With("domain", d, "kind", kind))
				return true
			}
		}
		return false
	}
	for _, h := range hosts {
		if conflict(ownerProxyHost, h.ID, h.Domains) {
			return false
		}
	}
	for _, st := range sites {
		if conflict(ownerSite, st.ID, st.Domains) {
			return false
		}
	}
	appList, err := s.Store.ListApps(ctx)
	if err != nil {
		s.internalError(w, err)
		return false
	}
	for _, a := range appList {
		if conflict(ownerApp, a.ID, a.Domains) {
			return false
		}
	}
	redirects, err := s.Store.ListRedirects(ctx)
	if err != nil {
		s.internalError(w, err)
		return false
	}
	for _, rd := range redirects {
		if conflict(ownerRedirect, rd.ID, rd.Domains) {
			return false
		}
	}
	return true
}

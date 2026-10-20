package api

import (
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/dockfly/dockfly/internal/traefik"
)

// handleCertificates devuelve el estado del certificado de cada dominio con
// SSL activado (proxy hosts y sitios).
func (s *Server) handleCertificates(w http.ResponseWriter, r *http.Request) {
	domains, err := s.sslDomainsCtx(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	tracker := s.Certs
	if tracker == nil {
		tracker = &traefik.CertTracker{}
	}
	status, err := tracker.Status(s.Traefik.ACMEFile(), domains, time.Now(), s.customCertMap(r.Context()))
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) sslDomainsCtx(ctx context.Context) ([]string, error) {
	var domains []string
	hosts, err := s.Store.ListProxyHosts(ctx)
	if err != nil {
		return nil, err
	}
	for _, h := range hosts {
		if h.SSL && h.Enabled {
			domains = append(domains, h.Domains...)
		}
	}
	sites, err := s.Store.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	for _, st := range sites {
		if st.SSL {
			domains = append(domains, st.Domains...)
		}
	}
	redirects, err := s.Store.ListRedirects(ctx)
	if err != nil {
		return nil, err
	}
	for _, rd := range redirects {
		if rd.SSL && rd.Enabled {
			domains = append(domains, rd.Domains...)
		}
	}
	appList, err := s.Store.ListApps(ctx)
	if err != nil {
		return nil, err
	}
	for _, a := range appList {
		if a.SSL {
			domains = append(domains, a.Domains...)
		}
	}
	slices.Sort(domains)
	return slices.Compact(domains), nil
}

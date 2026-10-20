package api

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
)

var domainRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$|^localhost$`)

// normalizeDomains pasa a minúsculas, quita el punto final y los duplicados,
// y valida cada dominio.
func normalizeDomains(in []string) ([]string, error) {
	var out []string
	for _, d := range in {
		d = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(d)), ".")
		if d == "" || slices.Contains(out, d) {
			continue
		}
		if !domainRe.MatchString(d) {
			return nil, fmt.Errorf("dominio inválido: %q", d)
		}
		out = append(out, d)
	}
	if len(out) == 0 {
		return nil, errors.New("indica al menos un dominio")
	}
	return out, nil
}

const (
	ownerProxyHost = "proxy host"
	ownerSite      = "sitio"
)

// owner identifica al recurso que se está editando, para no considerarlo
// en conflicto consigo mismo. id = 0 al crear.
type owner struct {
	kind string
	id   int64
}

// checkDomains responde 409 si algún dominio ya lo usa otro proxy host u
// otro sitio: Traefik no puede enrutar un mismo dominio a dos destinos.
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
				writeError(w, http.StatusConflict, fmt.Sprintf("el dominio %s ya está en uso por otro %s", d, kind))
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
	return true
}

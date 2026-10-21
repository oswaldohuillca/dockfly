package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/dockfly/dockfly/internal/gitprovider"
	"github.com/dockfly/dockfly/internal/pages"
	"github.com/dockfly/dockfly/internal/store"
	"github.com/dockfly/dockfly/internal/traefik"
)

// Las previews publican cada rama (que no sea la de producción) de un sitio
// en su propia URL: <rama>--<sitio>.<dominio de previews>. Todas cuelgan de
// un único comodín, así que basta un registro DNS y un certificado comodín.

// keepPreviewDeployments es cuántos despliegues se guardan por preview.
const keepPreviewDeployments = 3

// previewRouteName es la ruta de Traefik del comodín de previews.
const previewRouteName = "previews"

// previewSlug convierte el nombre de una rama en una etiqueta DNS:
// "feature/Nuevo_Login" → "feature-nuevo-login".
func previewSlug(branch string) string {
	var b strings.Builder
	dash := false
	for _, c := range strings.ToLower(branch) {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			b.WriteRune(c)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.TrimRight(b.String(), "-")
	if len(s) > 40 {
		s = strings.TrimRight(s[:40], "-")
	}
	return s
}

// previewLabel es la primera etiqueta del dominio de una preview, sin pasar
// de los 63 caracteres que permite DNS.
func previewLabel(preview, site string) string {
	if room := 63 - len(site) - 2; len(preview) > room {
		preview = strings.TrimRight(preview[:max(room, 1)], "-")
	}
	label := preview + "--" + site
	if len(label) > 63 {
		label = strings.TrimRight(label[:63], "-")
	}
	return label
}

func previewHost(domain, site, preview string) string {
	return previewLabel(preview, site) + "." + domain
}

func (s *Server) previewDomain(ctx context.Context) (string, error) {
	return s.Store.GetSetting(ctx, store.SettingPreviewDomain)
}

// previewURL es la dirección pública de una preview ("" si no hay dominio).
func (s *Server) previewURL(ctx context.Context, site *store.Site, preview string) string {
	domain, err := s.previewDomain(ctx)
	if err != nil || domain == "" {
		return ""
	}
	scheme := "http"
	if plan, err := s.loadCertPlan(ctx); err == nil && plan.resolver([]string{"*." + domain}) != "" {
		scheme = "https"
	}
	return scheme + "://" + previewHost(domain, site.Name, preview)
}

// handleSitePush decide qué hacer con un push a la rama de un sitio:
// construir producción, construir (o borrar) su preview, o nada.
func (s *Server) handleSitePush(ctx context.Context, site *store.Site, push gitprovider.Push) (buildID int64, status string, err error) {
	if push.Branch == site.Branch {
		if push.Deleted {
			return 0, "ignorado: se borró la rama de producción", nil
		}
		b, err := s.Builds.Enqueue(ctx, site.ID, store.TriggerPush, push.Branch, "", push.Commit)
		if err != nil {
			return 0, "", err
		}
		return b.ID, "en cola", nil
	}
	domain, err := s.previewDomain(ctx)
	if err != nil {
		return 0, "", err
	}
	preview := previewSlug(push.Branch)
	if !site.Previews || domain == "" || preview == "" {
		return 0, "ignorado: la rama " + push.Branch + " no se despliega", nil
	}
	if push.Deleted {
		if err := s.removePreview(ctx, site.ID, preview); err != nil {
			return 0, "", err
		}
		return 0, "preview borrada", nil
	}
	b, err := s.Builds.Enqueue(ctx, site.ID, store.TriggerPush, push.Branch, preview, push.Commit)
	if err != nil {
		return 0, "", err
	}
	return b.ID, "en cola (preview)", nil
}

// removePreview borra todos los despliegues de una preview y deja de servirla.
func (s *Server) removePreview(ctx context.Context, siteID int64, preview string) error {
	deps, err := s.Store.PreviewDeployments(ctx, siteID, preview)
	if err != nil {
		return err
	}
	for _, d := range deps {
		if err := s.Store.DeleteDeployment(ctx, siteID, d.ID); err != nil {
			return err
		}
	}
	// Primero se deja de servir y luego se borran los archivos.
	if err := s.Apply(ctx); err != nil {
		return err
	}
	for _, d := range deps {
		if err := s.PagesStorage.RemoveDeployment(siteID, d.ID); err != nil {
			s.Log.Error("borrando archivos de la preview", "deployment", d.ID, "error", err)
		}
	}
	return nil
}

// prunePreview conserva solo los keepPreviewDeployments más recientes.
func (s *Server) prunePreview(ctx context.Context, siteID int64, preview string) {
	deps, err := s.Store.PreviewDeployments(ctx, siteID, preview) // del más nuevo al más viejo
	if err != nil || len(deps) <= keepPreviewDeployments {
		return
	}
	for _, d := range deps[keepPreviewDeployments:] {
		if err := s.Store.DeleteDeployment(ctx, siteID, d.ID); err != nil {
			continue
		}
		_ = s.PagesStorage.RemoveDeployment(siteID, d.ID)
	}
}

// previewRoutes añade a la tabla del servidor estático las previews de cada
// sitio y devuelve la ruta de Traefik del comodín (ok=false si no hay
// dominio de previews).
func (s *Server) previewRoutes(ctx context.Context, sites []*store.Site, table map[string]pages.Route,
	acl map[int64][]string) (wildcard traefik.Route, protectedPreviews []traefik.Route, ok bool, err error) {
	domain, err := s.previewDomain(ctx)
	if err != nil || domain == "" {
		return traefik.Route{}, nil, false, err
	}
	// Los previews de un sitio protegido llevan la lista de acceso del sitio
	// en un router propio por host (gana al comodín por prioridad). Se
	// devuelven como rutas extra.
	for _, site := range sites {
		if !site.Previews {
			continue
		}
		deps, err := s.Store.ListPreviews(ctx, site.ID)
		if err != nil {
			return traefik.Route{}, nil, false, err
		}
		for _, d := range deps {
			host := previewHost(domain, site.Name, d.Preview)
			if _, taken := table[host]; !taken {
				table[host] = pages.Route{Dir: s.PagesStorage.DeploymentDir(site.ID, d.ID), DeploymentID: d.ID, SPA: site.SPA}
			}
			if site.AccessListID != nil {
				protectedPreviews = append(protectedPreviews, previewSiteRoute(site, host, s.StaticURL))
			}
		}
	}
	rt := traefik.Route{
		Name: previewRouteName, Domains: []string{"*." + domain}, URL: s.StaticURL,
		PreserveHost: true, Compress: true,
	}
	if raw, _ := s.Store.GetSetting(ctx, store.SettingPreviewAccessList); raw != "" {
		var id int64
		if _, err := fmt.Sscan(raw, &id); err == nil {
			rt.Middlewares = acl[id]
		}
	}
	// Con certificado comodín (zona DNS o propio), las previews van por HTTPS.
	ssl := false
	if plan, err := s.loadCertPlan(ctx); err == nil && plan.resolver(rt.Domains) != "" {
		rt.SSL, rt.ForceHTTPS, ssl = true, true, true
	}
	for i := range protectedPreviews {
		protectedPreviews[i].SSL, protectedPreviews[i].ForceHTTPS = ssl, ssl
		protectedPreviews[i].Resolver = traefik.NoResolver // el comodín ya tiene el certificado
	}
	return rt, protectedPreviews, true, nil
}

// previewSiteRoute es el router de un host de preview concreto, con la lista
// de acceso de su sitio.
func previewSiteRoute(site *store.Site, host, staticURL string) traefik.Route {
	return traefik.Route{
		Name: fmt.Sprintf("preview-%d-%s", site.ID, host), Domains: []string{host},
		URL: staticURL, PreserveHost: true, Compress: true,
	}
}

type previewView struct {
	Preview    string            `json:"preview"`
	Branch     string            `json:"branch"`
	URL        string            `json:"url"`
	Deployment *store.Deployment `json:"deployment"`
}

func (s *Server) handleListPreviews(w http.ResponseWriter, r *http.Request) {
	site, ok := s.loadSite(w, r)
	if !ok {
		return
	}
	deps, err := s.Store.ListPreviews(r.Context(), site.ID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	out := []previewView{}
	for _, d := range deps {
		out = append(out, previewView{
			Preview: d.Preview, Branch: d.Branch, Deployment: d,
			URL: s.previewURL(r.Context(), site, d.Preview),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleDeletePreview(w http.ResponseWriter, r *http.Request) {
	site, ok := s.loadSite(w, r)
	if !ok {
		return
	}
	if err := s.removePreview(r.Context(), site.ID, chi.URLParam(r, "preview")); err != nil {
		s.internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// StatusURL es el enlace del estado del commit: la web publicada si el build
// salió bien y, si no, el sitio en el panel (si se conoce la URL pública).
func (s *Server) StatusURL(ctx context.Context, site *store.Site, b *store.Build, depID int64) string {
	if depID != 0 {
		if b.Preview != "" {
			return s.previewURL(ctx, site, b.Preview)
		}
		if len(site.Domains) > 0 && !strings.HasPrefix(site.Domains[0], "*.") {
			scheme := "http"
			if site.SSL {
				scheme = "https"
			}
			return scheme + "://" + site.Domains[0]
		}
	}
	return s.panelLink(ctx, fmt.Sprintf("/sites/%d", site.ID))
}

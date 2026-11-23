package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/dockfly/dockfly/internal/gitprovider"
	"github.com/dockfly/dockfly/internal/routing"
	"github.com/dockfly/dockfly/internal/store"
)

// Previews publish every branch of a site other than production on its own
// URL: <branch>--<site>.<preview domain>. They all hang off a single
// wildcard, so one DNS record and one wildcard certificate are enough.

// keepPreviewDeployments is how many deployments are kept per preview.
const keepPreviewDeployments = 3

// previewSlug turns a branch name into a DNS label:
// "feature/New_Login" → "feature-new-login".
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

func (s *Server) previewDomain(ctx context.Context) (string, error) {
	return s.Store.GetSetting(ctx, store.SettingPreviewDomain)
}

// previewURL is the public address of a preview ("" when there is no domain).
func (s *Server) previewURL(ctx context.Context, site *store.Site, preview string) string {
	domain, err := s.previewDomain(ctx)
	if err != nil || domain == "" {
		return ""
	}
	scheme := "http"
	if plan, err := s.loadCertPlan(ctx); err == nil && plan.resolver([]string{"*." + domain}) != "" {
		scheme = "https"
	}
	return scheme + "://" + routing.PreviewHost(domain, site.Name, preview)
}

// handleSitePush decides what to do with a push to one of a site's
// branches: build production, build (or delete) its preview, or nothing.
func (s *Server) handleSitePush(ctx context.Context, site *store.Site, push gitprovider.Push) (buildID int64, status string, err error) {
	if push.Branch == site.Branch {
		if push.Deleted {
			return 0, "ignored: the production branch was deleted", nil
		}
		b, err := s.Builds.Enqueue(ctx, site.ID, store.TriggerPush, push.Branch, "", push.Commit)
		if err != nil {
			return 0, "", err
		}
		return b.ID, "queued", nil
	}
	domain, err := s.previewDomain(ctx)
	if err != nil {
		return 0, "", err
	}
	preview := previewSlug(push.Branch)
	if !site.Previews || domain == "" || preview == "" {
		return 0, "ignored: branch " + push.Branch + " is not deployed", nil
	}
	if push.Deleted {
		if err := s.removePreview(ctx, site.ID, preview); err != nil {
			return 0, "", err
		}
		return 0, "preview deleted", nil
	}
	b, err := s.Builds.Enqueue(ctx, site.ID, store.TriggerPush, push.Branch, preview, push.Commit)
	if err != nil {
		return 0, "", err
	}
	return b.ID, "queued (preview)", nil
}

// removePreview deletes every deployment of a preview and stops serving it.
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
	// Stop serving it first, and only then delete the files.
	if err := s.Apply(ctx); err != nil {
		return err
	}
	for _, d := range deps {
		if err := s.PagesStorage.RemoveDeployment(siteID, d.ID); err != nil {
			s.Log.Error("removing preview files", "deployment", d.ID, "error", err)
		}
	}
	return nil
}

// prunePreview keeps only the keepPreviewDeployments most recent ones.
func (s *Server) prunePreview(ctx context.Context, siteID int64, preview string) {
	deps, err := s.Store.PreviewDeployments(ctx, siteID, preview) // newest to oldest
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

// previews gathers the published previews of the sites that have them.
func (s *Server) previews(ctx context.Context, sites []*store.Site) (routing.Previews, error) {
	var p routing.Previews
	// The list counts as in use even when previews are disabled.
	if raw, _ := s.Store.GetSetting(ctx, store.SettingPreviewAccessList); raw != "" {
		var id int64
		if _, err := fmt.Sscan(raw, &id); err == nil {
			p.AccessListID = &id
		}
	}
	domain, err := s.previewDomain(ctx)
	if err != nil || domain == "" {
		return p, err
	}
	p.Domain, p.BySite = domain, map[int64][]*store.Deployment{}
	for _, site := range sites {
		if !site.Previews {
			continue
		}
		if p.BySite[site.ID], err = s.Store.ListPreviews(ctx, site.ID); err != nil {
			return routing.Previews{}, err
		}
	}
	return p, nil
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

// StatusURL is the link for the commit status: the published site if the
// build succeeded and, failing that, the site in the panel (when the public
// URL is known).
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

package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/dockfly/dockfly/internal/pages"
	"github.com/dockfly/dockfly/internal/store"
)

// keepDeployments es cuántos despliegues se conservan por sitio para poder
// hacer rollback. El activo nunca se borra.
const keepDeployments = 10

// uploadHeader debe acompañar a las subidas multipart. Un formulario HTML de
// otro sitio no puede añadir cabeceras propias, así que esto (junto con la
// cookie SameSite=Strict) bloquea CSRF en la única ruta que no es JSON.
const uploadHeader = "X-Dockfly-Upload"

var siteNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

var (
	errSiteNameInvalid     = newError("site_name_invalid", "The name can only contain lowercase letters, numbers and hyphens (max. 63)")
	errSiteNameTaken       = newError("site_name_taken", "A site with that name already exists")
	errUploadHeaderMissing = newError("upload_header_missing", "The {header} header is missing")
	errMultipartExpected   = newError("multipart_expected", "multipart/form-data was expected")
	errUploadTooLarge      = newError("upload_too_large", "The upload exceeds {max_mb} MiB")
	errDeploymentInvalid   = newError("deployment_invalid", "Invalid deployment: {detail}")
	errDeploymentActive    = newError("deployment_active", "The published deployment can't be deleted; activate another one first")
	errSiteNotFound        = newError("site_not_found", "Site not found")
	errInvalidDeploymentID = newError("invalid_deployment_id", "Invalid deployment ID")
	errDeploymentNotFound  = newError("deployment_not_found", "Deployment not found")
)

type siteInput struct {
	Name       string   `json:"name"`
	Domains    []string `json:"domains"`
	SSL        bool     `json:"ssl"`
	ForceHTTPS bool     `json:"force_https"`
	HSTS       bool     `json:"hsts"`
	SPA        bool     `json:"spa"`
	Previews   bool     `json:"previews"`
	AccessList *int64   `json:"access_list_id"`
}

func (in *siteInput) normalize() (*store.Site, error) {
	name := strings.ToLower(strings.TrimSpace(in.Name))
	if !siteNameRe.MatchString(name) {
		return nil, errSiteNameInvalid
	}
	domains, err := normalizeDomains(in.Domains)
	if err != nil {
		return nil, err
	}
	return &store.Site{
		Name:       name,
		Domains:    domains,
		SSL:        in.SSL,
		ForceHTTPS: in.SSL && in.ForceHTTPS,
		HSTS:       in.SSL && in.HSTS,
		SPA:        in.SPA,
		Previews:   in.Previews,

		AccessListID: in.AccessList,
	}, nil
}

func (s *Server) handleListSites(w http.ResponseWriter, r *http.Request) {
	sites, err := s.Store.ListSites(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sites)
}

func (s *Server) handleGetSite(w http.ResponseWriter, r *http.Request) {
	site, ok := s.loadSite(w, r)
	if !ok {
		return
	}
	deps, err := s.Store.ListDeployments(r.Context(), site.ID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"site": site, "deployments": deps})
}

func (s *Server) handleCreateSite(w http.ResponseWriter, r *http.Request) {
	site, ok := s.readSite(w, r, 0)
	if !ok {
		return
	}
	if err := s.Store.CreateSite(r.Context(), site); err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, errSiteNameTaken)
			return
		}
		s.internalError(w, err)
		return
	}
	s.applyAndRespond(w, r, http.StatusCreated, site)
}

func (s *Server) handleUpdateSite(w http.ResponseWriter, r *http.Request) {
	existing, ok := s.loadSite(w, r)
	if !ok {
		return
	}
	site, ok := s.readSite(w, r, existing.ID)
	if !ok {
		return
	}
	site.ID, site.CreatedAt = existing.ID, existing.CreatedAt
	site.ActiveDeploymentID = existing.ActiveDeploymentID
	if err := s.Store.UpdateSite(r.Context(), site); err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, errSiteNameTaken)
			return
		}
		s.internalError(w, err)
		return
	}
	s.applyAndRespond(w, r, http.StatusOK, site)
}

func (s *Server) handleDeleteSite(w http.ResponseWriter, r *http.Request) {
	site, ok := s.loadSite(w, r)
	if !ok {
		return
	}
	s.deleteWebhook(r.Context(), site)
	// Primero se deja de servir y luego se borran los archivos.
	if err := s.Store.DeleteSite(r.Context(), site.ID); err != nil {
		s.internalError(w, err)
		return
	}
	if err := s.Apply(r.Context()); err != nil {
		s.internalError(w, fmt.Errorf("aplicando configuración: %w", err))
		return
	}
	if err := s.PagesStorage.RemoveSite(site.ID); err != nil {
		s.Log.Error("borrando archivos del sitio", "site", site.ID, "error", err)
	}
	if err := s.Builds.ClearCache(r.Context(), site.ID); err != nil {
		s.Log.Warn("borrando la caché de builds del sitio", "site", site.ID, "error", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleUploadDeployment recibe un despliegue nuevo y lo publica. Acepta un
// .zip (campo "archive") o los archivos de una carpeta, uno por campo, con el
// nombre "file:<ruta relativa>".
func (s *Server) handleUploadDeployment(w http.ResponseWriter, r *http.Request) {
	site, ok := s.loadSite(w, r)
	if !ok {
		return
	}
	if r.Header.Get(uploadHeader) != "1" {
		writeError(w, http.StatusBadRequest, errUploadHeaderMissing.With("header", uploadHeader))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.MaxUploadMB<<20)
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, errMultipartExpected)
		return
	}

	staging, err := s.PagesStorage.NewStaging(site.ID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	committed := false
	defer func() {
		if !committed {
			s.PagesStorage.Discard(staging)
		}
	}()

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			s.uploadError(w, err)
			return
		}
		name := part.FormName()
		switch {
		case name == "archive":
			err = s.addArchive(staging, site.ID, part)
		case strings.HasPrefix(name, "file:"):
			// FormName conserva la ruta; FileName la recortaría a su base.
			err = staging.Add(strings.TrimPrefix(name, "file:"), part)
		}
		part.Close()
		if err != nil {
			s.uploadError(w, err)
			return
		}
	}

	files, size, err := staging.Finish()
	if err != nil {
		s.uploadError(w, err)
		return
	}
	dep := &store.Deployment{SiteID: site.ID, Source: store.SourceUpload, FileCount: files, TotalBytes: size}
	if err := s.Store.CreateDeployment(r.Context(), dep); err != nil {
		s.internalError(w, err)
		return
	}
	if err := s.PagesStorage.Commit(staging, site.ID, dep.ID); err != nil {
		_ = s.Store.DeleteDeployment(r.Context(), site.ID, dep.ID)
		s.internalError(w, err)
		return
	}
	committed = true

	// Como en Pages, cada subida se publica en cuanto termina.
	if err := s.Publish(r.Context(), site.ID, dep.ID); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, dep)
}

// addArchive guarda el .zip en un temporal, porque zip necesita acceso
// aleatorio (el índice está al final del archivo), y lo extrae.
func (s *Server) addArchive(staging *pages.Staging, siteID int64, part io.Reader) error {
	tmp, err := os.CreateTemp(s.PagesStorage.SiteDir(siteID), ".staging-upload-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	size, err := io.Copy(tmp, part)
	if err != nil {
		return err
	}
	return staging.AddZip(tmp, size)
}

func (s *Server) uploadError(w http.ResponseWriter, err error) {
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		writeError(w, http.StatusRequestEntityTooLarge, errUploadTooLarge.With("max_mb", s.MaxUploadMB))
	case errors.Is(err, pages.ErrInvalid):
		// El detalle viene de pages (en español), sin el prefijo de ErrInvalid.
		detail := strings.TrimPrefix(err.Error(), pages.ErrInvalid.Error()+": ")
		writeError(w, http.StatusBadRequest, errDeploymentInvalid.With("detail", detail))
	default:
		s.internalError(w, err)
	}
}

func (s *Server) handleActivateDeployment(w http.ResponseWriter, r *http.Request) {
	site, dep, ok := s.loadDeployment(w, r)
	if !ok {
		return
	}
	if err := s.activate(r.Context(), site.ID, dep.ID); err != nil {
		s.internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteDeployment(w http.ResponseWriter, r *http.Request) {
	site, dep, ok := s.loadDeployment(w, r)
	if !ok {
		return
	}
	if site.ActiveDeploymentID != nil && *site.ActiveDeploymentID == dep.ID {
		writeError(w, http.StatusConflict, errDeploymentActive)
		return
	}
	if err := s.Store.DeleteDeployment(r.Context(), site.ID, dep.ID); err != nil {
		s.internalError(w, err)
		return
	}
	if err := s.PagesStorage.RemoveDeployment(site.ID, dep.ID); err != nil {
		s.Log.Error("borrando archivos del despliegue", "deployment", dep.ID, "error", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

// activate publica un despliegue: cambia el puntero y regenera la tabla del
// servidor estático. El cambio es atómico para los visitantes.
func (s *Server) activate(ctx context.Context, siteID, deploymentID int64) error {
	if err := s.Store.SetActiveDeployment(ctx, siteID, &deploymentID); err != nil {
		return err
	}
	return s.Apply(ctx)
}

// Publish activa un despliegue nuevo y limpia los antiguos. Lo usan tanto
// la subida directa como los builds.
func (s *Server) Publish(ctx context.Context, siteID, deploymentID int64) error {
	// Una preview no se activa: se sirve siempre su último despliegue.
	if dep, err := s.Store.GetDeployment(ctx, siteID, deploymentID); err == nil && dep.Preview != "" {
		if err := s.Apply(ctx); err != nil {
			return err
		}
		s.prunePreview(ctx, siteID, dep.Preview)
		return nil
	}
	if err := s.activate(ctx, siteID, deploymentID); err != nil {
		return err
	}
	s.pruneDeployments(ctx, siteID)
	return nil
}

// pruneDeployments borra los despliegues más antiguos, conservando los
// keepDeployments más recientes y siempre el publicado.
func (s *Server) pruneDeployments(ctx context.Context, siteID int64) {
	site, err := s.Store.GetSite(ctx, siteID)
	if err != nil {
		return
	}
	deps, err := s.Store.ListDeployments(ctx, siteID) // del más nuevo al más viejo
	if err != nil || len(deps) <= keepDeployments {
		return
	}
	for _, d := range deps[keepDeployments:] {
		if site.ActiveDeploymentID != nil && *site.ActiveDeploymentID == d.ID {
			continue
		}
		if err := s.Store.DeleteDeployment(ctx, siteID, d.ID); err != nil {
			s.Log.Error("borrando despliegue antiguo", "deployment", d.ID, "error", err)
			continue
		}
		_ = s.PagesStorage.RemoveDeployment(siteID, d.ID)
	}
}

// --- Utilidades ---

func (s *Server) readSite(w http.ResponseWriter, r *http.Request, selfID int64) (*store.Site, bool) {
	var in siteInput
	if !decode(w, r, &in) {
		return nil, false
	}
	site, err := in.normalize()
	if err != nil {
		writeErrorFrom(w, http.StatusBadRequest, err, errUnexpected)
		return nil, false
	}
	if !s.checkDomains(w, r, site.Domains, owner{kind: ownerSite, id: selfID}) || !s.checkAccessList(w, r, site.AccessListID) ||
		!s.checkSSL(w, r, site.Domains, site.SSL) {
		return nil, false
	}
	return site, true
}

func (s *Server) loadSite(w http.ResponseWriter, r *http.Request) (*store.Site, bool) {
	id, ok := pathID(w, r)
	if !ok {
		return nil, false
	}
	site, err := s.Store.GetSite(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, errSiteNotFound)
		return nil, false
	} else if err != nil {
		s.internalError(w, err)
		return nil, false
	}
	return site, true
}

func (s *Server) loadDeployment(w http.ResponseWriter, r *http.Request) (*store.Site, *store.Deployment, bool) {
	site, ok := s.loadSite(w, r)
	if !ok {
		return nil, nil, false
	}
	depID, err := strconv.ParseInt(chi.URLParam(r, "deploymentID"), 10, 64)
	if err != nil || depID < 1 {
		writeError(w, http.StatusBadRequest, errInvalidDeploymentID)
		return nil, nil, false
	}
	dep, err := s.Store.GetDeployment(r.Context(), site.ID, depID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, errDeploymentNotFound)
		return nil, nil, false
	} else if err != nil {
		s.internalError(w, err)
		return nil, nil, false
	}
	return site, dep, true
}

func (s *Server) applyAndRespond(w http.ResponseWriter, r *http.Request, status int, v any) {
	if err := s.Apply(r.Context()); err != nil {
		s.internalError(w, fmt.Errorf("aplicando configuración: %w", err))
		return
	}
	writeJSON(w, status, v)
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// isMultipartUpload indica si la petición es una subida legítima del panel.
func isMultipartUpload(r *http.Request) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mt == "multipart/form-data" && r.Header.Get(uploadHeader) == "1"
}

package store

import (
	"context"
	"time"
)

// Origen de un despliegue. Por ahora solo subida directa; los builds desde
// GitHub/GitLab añadirán sus propios valores.
const SourceUpload = "upload"

// Site es un sitio estático servido por Dockfly, al estilo de Cloudflare Pages.
type Site struct {
	ID         int64    `bun:",pk,autoincrement" json:"id"`
	Name       string   `json:"name"`
	Domains    []string `json:"domains"`
	SSL        bool     `bun:"ssl" json:"ssl"`
	ForceHTTPS bool     `bun:"force_https" json:"force_https"`
	HSTS       bool     `bun:"hsts" json:"hsts"`
	// SPA sirve index.html para cualquier ruta que no exista (React Router, Vue Router…).
	SPA bool `bun:"spa" json:"spa"`
	// Previews construye las ramas que no son la de producción en su propia URL.
	Previews     bool   `json:"previews"`
	AccessListID *int64 `json:"access_list_id"`
	// Locations envían una parte del dominio (/api…) a otro destino; el resto
	// lo sirve el sitio.
	Locations          []ProxyLocation `json:"locations"`
	ActiveDeploymentID *int64          `json:"active_deployment_id"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`

	SiteGit

	ActiveDeployment *Deployment `bun:"rel:belongs-to,join:active_deployment_id=id" json:"active_deployment,omitempty"`
}

// SiteGit es la configuración de build de un sitio enlazado a un
// repositorio. Repo vacío = el sitio se despliega solo por subida directa.
type SiteGit struct {
	GitConnectionID *int64 `json:"git_connection_id"`
	Repo            string `json:"repo"` // "usuario/repo" o "grupo/subgrupo/proyecto"
	Branch          string `json:"branch"`
	RootDir         string `json:"root_dir"`
	InstallCommand  string `json:"install_command"`
	BuildCommand    string `json:"build_command"`
	OutputDir       string `json:"output_dir"`
	BuildImage      string `json:"build_image"`
	BuildEnvEnc     []byte `json:"-"` // map[string]string en JSON, cifrado
	WebhookID       string `json:"webhook_id"`
	WebhookSecret   string `json:"-"`
}

// gitColumns son las columnas de SiteGit: solo las escribe UpdateSiteGit,
// para que editar el nombre o los dominios no borre la configuración de build.
var gitColumns = []string{
	"git_connection_id", "repo", "branch", "root_dir", "install_command", "build_command",
	"output_dir", "build_image", "build_env_enc", "webhook_id", "webhook_secret",
}

// Deployment es una versión inmutable de los archivos de un sitio.
type Deployment struct {
	ID         int64     `bun:",pk,autoincrement" json:"id"`
	SiteID     int64     `json:"site_id"`
	Source     string    `json:"source"`
	FileCount  int       `json:"file_count"`
	TotalBytes int64     `json:"total_bytes"`
	CreatedAt  time.Time `json:"created_at"`
	// Solo para despliegues que vienen de un build.
	CommitSHA     string `json:"commit_sha"`
	CommitMessage string `json:"commit_message"`
	Branch        string `json:"branch"`
	// Preview identifica la rama ("" = producción).
	Preview string `json:"preview"`
}

func (s *Site) AfterScanRow(context.Context) error {
	s.CreatedAt, s.UpdatedAt = s.CreatedAt.UTC(), s.UpdatedAt.UTC()
	if s.Locations == nil {
		s.Locations = []ProxyLocation{}
	}
	return nil
}

func (d *Deployment) AfterScanRow(context.Context) error {
	d.CreatedAt = d.CreatedAt.UTC()
	return nil
}

// SourceGitHub y SourceGitLab marcan los despliegues que vienen de un build.
const (
	SourceGitHub = "github"
	SourceGitLab = "gitlab"
)

func (s *Store) ListSites(ctx context.Context) ([]*Site, error) {
	sites := []*Site{}
	err := s.db.NewSelect().Model(&sites).Relation("ActiveDeployment").Order("site.id").Scan(ctx)
	return sites, err
}

func (s *Store) GetSite(ctx context.Context, id int64) (*Site, error) {
	site := new(Site)
	err := s.db.NewSelect().Model(site).Relation("ActiveDeployment").Where("site.id = ?", id).Scan(ctx)
	if err != nil {
		return nil, notFound(err)
	}
	return site, nil
}

func (s *Store) CreateSite(ctx context.Context, site *Site) error {
	site.CreatedAt = now()
	site.UpdatedAt = site.CreatedAt
	_, err := s.db.NewInsert().Model(site).ExcludeColumn("active_deployment_id").ExcludeColumn(gitColumns...).Exec(ctx)
	return err
}

// UpdateSite guarda la configuración editable. El despliegue activo se cambia
// solo con SetActiveDeployment, para que editar un dominio no pueda pisar un
// despliegue publicado mientras tanto.
func (s *Store) UpdateSite(ctx context.Context, site *Site) error {
	site.UpdatedAt = now()
	return mustAffect(s.db.NewUpdate().Model(site).WherePK().
		ExcludeColumn("created_at", "active_deployment_id").ExcludeColumn(gitColumns...).Exec(ctx))
}

// UpdateSiteGit guarda (o, con un SiteGit vacío, borra) la configuración de
// build de un sitio.
func (s *Store) UpdateSiteGit(ctx context.Context, siteID int64, g *SiteGit) error {
	site := &Site{ID: siteID, SiteGit: *g, UpdatedAt: now()}
	return mustAffect(s.db.NewUpdate().Model(site).WherePK().Column(append(gitColumns, "updated_at")...).Exec(ctx))
}

func (s *Store) DeleteSite(ctx context.Context, id int64) error {
	return mustAffect(s.db.NewDelete().Model((*Site)(nil)).Where("id = ?", id).Exec(ctx))
}

func (s *Store) SetActiveDeployment(ctx context.Context, siteID int64, deploymentID *int64) error {
	return mustAffect(s.db.NewUpdate().Model((*Site)(nil)).
		Set("active_deployment_id = ?", deploymentID).
		Set("updated_at = ?", now()).
		Where("id = ?", siteID).Exec(ctx))
}

// ListDeployments devuelve los despliegues de producción (sin previews).
func (s *Store) ListDeployments(ctx context.Context, siteID int64) ([]*Deployment, error) {
	deps := []*Deployment{}
	err := s.db.NewSelect().Model(&deps).Where("site_id = ? AND preview = ''", siteID).Order("id DESC").Scan(ctx)
	return deps, err
}

// ListPreviews devuelve el despliegue más reciente de cada preview del sitio.
func (s *Store) ListPreviews(ctx context.Context, siteID int64) ([]*Deployment, error) {
	deps := []*Deployment{}
	err := s.db.NewSelect().Model(&deps).
		Where("site_id = ? AND preview <> ''", siteID).
		Where("id IN (SELECT MAX(id) FROM deployments WHERE site_id = ? AND preview <> '' GROUP BY preview)", siteID).
		Order("id DESC").Scan(ctx)
	return deps, err
}

// PreviewDeployments devuelve todos los despliegues de una preview.
func (s *Store) PreviewDeployments(ctx context.Context, siteID int64, preview string) ([]*Deployment, error) {
	deps := []*Deployment{}
	err := s.db.NewSelect().Model(&deps).Where("site_id = ? AND preview = ?", siteID, preview).Order("id DESC").Scan(ctx)
	return deps, err
}

func (s *Store) GetDeployment(ctx context.Context, siteID, id int64) (*Deployment, error) {
	d := new(Deployment)
	if err := s.db.NewSelect().Model(d).Where("id = ? AND site_id = ?", id, siteID).Scan(ctx); err != nil {
		return nil, notFound(err)
	}
	return d, nil
}

func (s *Store) CreateDeployment(ctx context.Context, d *Deployment) error {
	d.CreatedAt = now()
	_, err := s.db.NewInsert().Model(d).Exec(ctx)
	return err
}

func (s *Store) DeleteDeployment(ctx context.Context, siteID, id int64) error {
	return mustAffect(s.db.NewDelete().Model((*Deployment)(nil)).
		Where("id = ? AND site_id = ?", id, siteID).Exec(ctx))
}

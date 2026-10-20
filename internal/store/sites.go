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
	SPA                bool      `bun:"spa" json:"spa"`
	ActiveDeploymentID *int64    `json:"active_deployment_id"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`

	ActiveDeployment *Deployment `bun:"rel:belongs-to,join:active_deployment_id=id" json:"active_deployment,omitempty"`
}

// Deployment es una versión inmutable de los archivos de un sitio.
type Deployment struct {
	ID         int64     `bun:",pk,autoincrement" json:"id"`
	SiteID     int64     `json:"site_id"`
	Source     string    `json:"source"`
	FileCount  int       `json:"file_count"`
	TotalBytes int64     `json:"total_bytes"`
	CreatedAt  time.Time `json:"created_at"`
}

func (s *Site) AfterScanRow(context.Context) error {
	s.CreatedAt, s.UpdatedAt = s.CreatedAt.UTC(), s.UpdatedAt.UTC()
	return nil
}

func (d *Deployment) AfterScanRow(context.Context) error {
	d.CreatedAt = d.CreatedAt.UTC()
	return nil
}

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
	_, err := s.db.NewInsert().Model(site).ExcludeColumn("active_deployment_id").Exec(ctx)
	return err
}

// UpdateSite guarda la configuración editable. El despliegue activo se cambia
// solo con SetActiveDeployment, para que editar un dominio no pueda pisar un
// despliegue publicado mientras tanto.
func (s *Store) UpdateSite(ctx context.Context, site *Site) error {
	site.UpdatedAt = now()
	return mustAffect(s.db.NewUpdate().Model(site).WherePK().
		ExcludeColumn("created_at", "active_deployment_id").Exec(ctx))
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

func (s *Store) ListDeployments(ctx context.Context, siteID int64) ([]*Deployment, error) {
	deps := []*Deployment{}
	err := s.db.NewSelect().Model(&deps).Where("site_id = ?", siteID).Order("id DESC").Scan(ctx)
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

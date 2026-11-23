package store

import (
	"context"
	"time"
)

// Source of a deployment. For now only a direct upload; builds from
// GitHub/GitLab add their own values.
const SourceUpload = "upload"

// Site is a static site served by Dockfly, in the style of Cloudflare Pages.
type Site struct {
	ID         int64    `bun:",pk,autoincrement" json:"id"`
	Name       string   `json:"name"`
	Domains    []string `json:"domains"`
	SSL        bool     `bun:"ssl" json:"ssl"`
	ForceHTTPS bool     `bun:"force_https" json:"force_https"`
	HSTS       bool     `bun:"hsts" json:"hsts"`
	// SPA serves index.html for any path that does not exist (React Router, Vue Router…).
	SPA bool `bun:"spa" json:"spa"`
	// Previews builds the branches other than production on their own URL.
	Previews     bool   `json:"previews"`
	AccessListID *int64 `json:"access_list_id"`
	// Locations send part of the domain (/api…) to a different target; the site
	// serves the rest.
	Locations          []ProxyLocation `json:"locations"`
	ActiveDeploymentID *int64          `json:"active_deployment_id"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`

	SiteGit

	ActiveDeployment *Deployment `bun:"rel:belongs-to,join:active_deployment_id=id" json:"active_deployment,omitempty"`
}

// SiteGit is the build configuration of a site linked to a repository. An
// empty Repo = the site is only deployed by direct upload.
type SiteGit struct {
	GitConnectionID *int64 `json:"git_connection_id"`
	Repo            string `json:"repo"` // "user/repo" or "group/subgroup/project"
	Branch          string `json:"branch"`
	RootDir         string `json:"root_dir"`
	InstallCommand  string `json:"install_command"`
	BuildCommand    string `json:"build_command"`
	OutputDir       string `json:"output_dir"`
	BuildImage      string `json:"build_image"`
	BuildEnvEnc     []byte `json:"-"` // map[string]string as JSON, encrypted
	WebhookID       string `json:"webhook_id"`
	WebhookSecret   string `json:"-"`
	// DeployHook is the secret of the URL that triggers a build ("" = no hook).
	// It is not in gitColumns: only SetSiteDeployHook writes it.
	DeployHook string `json:"-"`
}

// gitColumns are the columns of SiteGit: only UpdateSiteGit writes them, so
// that editing the name or the domains does not wipe the build configuration.
var gitColumns = []string{
	"git_connection_id", "repo", "branch", "root_dir", "install_command", "build_command",
	"output_dir", "build_image", "build_env_enc", "webhook_id", "webhook_secret",
}

// Deployment is an immutable version of a site's files.
type Deployment struct {
	ID         int64     `bun:",pk,autoincrement" json:"id"`
	SiteID     int64     `json:"site_id"`
	Source     string    `json:"source"`
	FileCount  int       `json:"file_count"`
	TotalBytes int64     `json:"total_bytes"`
	CreatedAt  time.Time `json:"created_at"`
	// Only for deployments that come from a build.
	CommitSHA     string `json:"commit_sha"`
	CommitMessage string `json:"commit_message"`
	Branch        string `json:"branch"`
	// Preview identifies the branch ("" = production).
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

// SourceGitHub and SourceGitLab mark the deployments that come from a build.
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

// UpdateSite saves the editable configuration. The active deployment is only
// changed through SetActiveDeployment, so that editing a domain cannot
// clobber a deployment published in the meantime.
func (s *Store) UpdateSite(ctx context.Context, site *Site) error {
	site.UpdatedAt = now()
	return mustAffect(s.db.NewUpdate().Model(site).WherePK().
		ExcludeColumn("created_at", "active_deployment_id", "deploy_hook").ExcludeColumn(gitColumns...).Exec(ctx))
}

// UpdateSiteGit saves (or, with an empty SiteGit, clears) a site's build
// configuration.
func (s *Store) UpdateSiteGit(ctx context.Context, siteID int64, g *SiteGit) error {
	site := &Site{ID: siteID, SiteGit: *g, UpdatedAt: now()}
	return mustAffect(s.db.NewUpdate().Model(site).WherePK().Column(append(gitColumns, "updated_at")...).Exec(ctx))
}

// SetSiteDeployHook saves (or, with "", clears) the deploy hook secret.
func (s *Store) SetSiteDeployHook(ctx context.Context, siteID int64, token string) error {
	return mustAffect(s.db.NewUpdate().Model((*Site)(nil)).Set("deploy_hook = ?", token).Where("id = ?", siteID).Exec(ctx))
}

// GetSiteByDeployHook looks up the site a deploy hook belongs to.
func (s *Store) GetSiteByDeployHook(ctx context.Context, token string) (*Site, error) {
	if token == "" {
		return nil, ErrNotFound
	}
	site := new(Site)
	if err := s.db.NewSelect().Model(site).Where("deploy_hook = ?", token).Scan(ctx); err != nil {
		return nil, notFound(err)
	}
	return site, nil
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

// ListDeployments returns the production deployments (previews excluded).
func (s *Store) ListDeployments(ctx context.Context, siteID int64) ([]*Deployment, error) {
	deps := []*Deployment{}
	err := s.db.NewSelect().Model(&deps).Where("site_id = ? AND preview = ''", siteID).Order("id DESC").Scan(ctx)
	return deps, err
}

// ListPreviews returns the most recent deployment of each preview of the site.
func (s *Store) ListPreviews(ctx context.Context, siteID int64) ([]*Deployment, error) {
	deps := []*Deployment{}
	err := s.db.NewSelect().Model(&deps).
		Where("site_id = ? AND preview <> ''", siteID).
		Where("id IN (SELECT MAX(id) FROM deployments WHERE site_id = ? AND preview <> '' GROUP BY preview)", siteID).
		Order("id DESC").Scan(ctx)
	return deps, err
}

// PreviewDeployments returns every deployment of one preview.
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

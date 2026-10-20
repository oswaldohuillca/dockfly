package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/uptrace/bun"
)

// Formas de autenticar una conexión.
const (
	AuthToken       = "token"        // token de acceso personal pegado a mano
	AuthGitHubApp   = "github_app"   // instalación de la GitHub App de Dockfly
	AuthGitLabOAuth = "gitlab_oauth" // autorización OAuth de GitLab
)

// GitConnection es una cuenta u organización de GitHub o GitLab vinculada.
type GitConnection struct {
	ID          int64     `bun:",pk,autoincrement" json:"id"`
	Provider    string    `json:"provider"` // "github" | "gitlab"
	AuthType    string    `json:"auth_type"`
	BaseURL     string    `json:"base_url"` // URL de la API: permite GitHub Enterprise y GitLab propio
	Username    string    `json:"username"` // usuario u organización
	AccountType string    `json:"account_type"`
	SettingsURL string    `json:"settings_url"`
	CreatedAt   time.Time `json:"created_at"`

	// Token de acceso (personal u OAuth). Vacío en las instalaciones de la
	// GitHub App, cuyos tokens se generan al momento y caducan en una hora.
	TokenEnc        []byte     `json:"-"`
	RefreshTokenEnc []byte     `json:"-"`
	TokenExpiresAt  *time.Time `json:"-"`
	RedirectURI     string     `json:"-"` // GitLab la exige también al refrescar

	GitHubAppID    *int64 `bun:"github_app_id" json:"github_app_id"`
	InstallationID *int64 `json:"installation_id"`
	GitLabAppID    *int64 `bun:"gitlab_app_id" json:"gitlab_app_id"`
}

// GitHubApp es la GitHub App propia de esta instalación de Dockfly.
type GitHubApp struct {
	// Sin el nombre explícito, Bun lo deduciría como "git_hub_apps".
	bun.BaseModel `bun:"table:github_apps"`

	ID               int64     `bun:",pk,autoincrement" json:"id"`
	WebURL           string    `json:"web_url"`
	APIURL           string    `bun:"api_url" json:"api_url"`
	AppID            int64     `json:"app_id"`
	Slug             string    `json:"slug"`
	Name             string    `json:"name"`
	Owner            string    `json:"owner"`
	HTMLURL          string    `bun:"html_url" json:"html_url"`
	ClientID         string    `json:"client_id"`
	ClientSecretEnc  []byte    `json:"-"`
	WebhookSecretEnc []byte    `json:"-"`
	PrivateKeyEnc    []byte    `json:"-"`
	WebhookActive    bool      `json:"webhook_active"`
	CreatedAt        time.Time `json:"created_at"`
}

// GitLabApp es una aplicación OAuth registrada en una instancia de GitLab.
type GitLabApp struct {
	bun.BaseModel `bun:"table:gitlab_apps"`

	ID              int64     `bun:",pk,autoincrement" json:"id"`
	WebURL          string    `json:"web_url"`
	ClientID        string    `json:"client_id"`
	ClientSecretEnc []byte    `json:"-"`
	CreatedAt       time.Time `json:"created_at"`
}

func (a *GitHubApp) AfterScanRow(context.Context) error {
	a.CreatedAt = a.CreatedAt.UTC()
	return nil
}

func (a *GitLabApp) AfterScanRow(context.Context) error {
	a.CreatedAt = a.CreatedAt.UTC()
	return nil
}

func (c *GitConnection) AfterScanRow(context.Context) error {
	c.CreatedAt = c.CreatedAt.UTC()
	return nil
}

func (s *Store) ListGitConnections(ctx context.Context) ([]*GitConnection, error) {
	out := []*GitConnection{}
	err := s.db.NewSelect().Model(&out).Order("id").Scan(ctx)
	return out, err
}

func (s *Store) GetGitConnection(ctx context.Context, id int64) (*GitConnection, error) {
	c := &GitConnection{ID: id}
	if err := s.db.NewSelect().Model(c).WherePK().Scan(ctx); err != nil {
		return nil, notFound(err)
	}
	return c, nil
}

func (s *Store) CreateGitConnection(ctx context.Context, c *GitConnection) error {
	c.CreatedAt = now()
	if c.AuthType == "" {
		c.AuthType = AuthToken
	}
	_, err := s.db.NewInsert().Model(c).Exec(ctx)
	return err
}

// UpsertInstallation registra (o actualiza) la conexión de una instalación
// de la GitHub App. Instalar dos veces la misma cuenta no la duplica.
func (s *Store) UpsertInstallation(ctx context.Context, c *GitConnection) error {
	existing := new(GitConnection)
	err := s.db.NewSelect().Model(existing).
		Where("github_app_id = ? AND installation_id = ?", c.GitHubAppID, c.InstallationID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		// Las instalaciones no guardan token (se genera al momento), pero la
		// columna es NOT NULL desde la primera versión: va vacía, no nula.
		if c.TokenEnc == nil {
			c.TokenEnc = []byte{}
		}
		return s.CreateGitConnection(ctx, c)
	}
	if err != nil {
		return err
	}
	c.ID, c.CreatedAt = existing.ID, existing.CreatedAt
	_, err = s.db.NewUpdate().Model(c).WherePK().Column("username", "account_type", "settings_url").Exec(ctx)
	return err
}

// ConnectionsByInstallation devuelve las conexiones de una instalación.
func (s *Store) ConnectionsByInstallation(ctx context.Context, appID, installationID int64) ([]*GitConnection, error) {
	out := []*GitConnection{}
	err := s.db.NewSelect().Model(&out).
		Where("github_app_id = ? AND installation_id = ?", appID, installationID).Scan(ctx)
	return out, err
}

// SaveGitLabConnection guarda una autorización OAuth de GitLab. Si el mismo
// usuario vuelve a autorizar, se actualizan sus tokens en lugar de duplicarla.
func (s *Store) SaveGitLabConnection(ctx context.Context, c *GitConnection) error {
	existing := new(GitConnection)
	err := s.db.NewSelect().Model(existing).
		Where("gitlab_app_id = ? AND username = ?", c.GitLabAppID, c.Username).Limit(1).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return s.CreateGitConnection(ctx, c)
	}
	if err != nil {
		return err
	}
	c.ID, c.CreatedAt = existing.ID, existing.CreatedAt
	_, err = s.db.NewUpdate().Model(c).WherePK().
		Column("token_enc", "refresh_token_enc", "token_expires_at", "redirect_uri").Exec(ctx)
	return err
}

// UpdateConnectionTokens guarda un token OAuth renovado.
func (s *Store) UpdateConnectionTokens(ctx context.Context, c *GitConnection) error {
	return mustAffect(s.db.NewUpdate().Model(c).WherePK().
		Column("token_enc", "refresh_token_enc", "token_expires_at").Exec(ctx))
}

func (s *Store) DeleteGitConnection(ctx context.Context, id int64) error {
	return mustAffect(s.db.NewDelete().Model((*GitConnection)(nil)).Where("id = ?", id).Exec(ctx))
}

// --- Ajustes ---

type setting struct {
	Key   string `bun:",pk"`
	Value string
}

const SettingPublicURL = "public_url"

// GetSetting devuelve "" si el ajuste no existe.
func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	st := &setting{Key: key}
	err := s.db.NewSelect().Model(st).WherePK().Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return st.Value, err
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.NewInsert().Model(&setting{Key: key, Value: value}).
		On("CONFLICT (key) DO UPDATE").Set("value = EXCLUDED.value").Exec(ctx)
	return err
}

// --- Builds ---

const (
	BuildQueued   = "queued"
	BuildRunning  = "running"
	BuildSuccess  = "success"
	BuildFailed   = "failed"
	BuildCanceled = "canceled"
)

const (
	TriggerPush   = "push"
	TriggerManual = "manual"
)

type Build struct {
	ID            int64      `bun:",pk,autoincrement" json:"id"`
	SiteID        int64      `json:"site_id"`
	Status        string     `json:"status"`
	Trigger       string     `json:"trigger"`
	Branch        string     `json:"branch"`
	CommitSHA     string     `json:"commit_sha"`
	CommitMessage string     `json:"commit_message"`
	DeploymentID  *int64     `json:"deployment_id"`
	Error         string     `json:"error"`
	CreatedAt     time.Time  `json:"created_at"`
	StartedAt     *time.Time `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at"`
}

func (b *Build) AfterScanRow(context.Context) error {
	b.CreatedAt = b.CreatedAt.UTC()
	if b.StartedAt != nil {
		t := b.StartedAt.UTC()
		b.StartedAt = &t
	}
	if b.FinishedAt != nil {
		t := b.FinishedAt.UTC()
		b.FinishedAt = &t
	}
	return nil
}

// Finished indica si el build ya no va a cambiar de estado.
func (b *Build) Finished() bool {
	return b.Status == BuildSuccess || b.Status == BuildFailed || b.Status == BuildCanceled
}

func (s *Store) CreateBuild(ctx context.Context, b *Build) error {
	b.CreatedAt = now()
	_, err := s.db.NewInsert().Model(b).Exec(ctx)
	return err
}

func (s *Store) GetBuild(ctx context.Context, id int64) (*Build, error) {
	b := &Build{ID: id}
	if err := s.db.NewSelect().Model(b).WherePK().Scan(ctx); err != nil {
		return nil, notFound(err)
	}
	return b, nil
}

func (s *Store) ListBuilds(ctx context.Context, siteID int64, limit int) ([]*Build, error) {
	out := []*Build{}
	err := s.db.NewSelect().Model(&out).Where("site_id = ?", siteID).Order("id DESC").Limit(int64(limit)).Scan(ctx)
	return out, err
}

func (s *Store) ListBuildsByStatus(ctx context.Context, status string) ([]*Build, error) {
	out := []*Build{}
	err := s.db.NewSelect().Model(&out).Where("status = ?", status).Order("id").Scan(ctx)
	return out, err
}

// MarkBuildStarted pasa un build de queued a running. Devuelve ErrNotFound si
// ya no estaba en cola (p. ej. se canceló mientras esperaba).
func (s *Store) MarkBuildStarted(ctx context.Context, id int64) error {
	return mustAffect(s.db.NewUpdate().Model((*Build)(nil)).
		Set("status = ?", BuildRunning).Set("started_at = ?", now()).
		Where("id = ? AND status = ?", id, BuildQueued).Exec(ctx))
}

// FinishBuild fija el estado final. Solo actúa sobre builds sin terminar,
// así que un build cancelado no puede "resucitar" como exitoso.
func (s *Store) FinishBuild(ctx context.Context, id int64, status, errMsg string, deploymentID *int64) error {
	return mustAffect(s.db.NewUpdate().Model((*Build)(nil)).
		Set("status = ?", status).Set("error = ?", errMsg).Set("deployment_id = ?", deploymentID).
		Set("finished_at = ?", now()).
		Where("id = ? AND status IN (?, ?)", id, BuildQueued, BuildRunning).Exec(ctx))
}

// SupersedeQueued cancela los builds en cola de un sitio, excepto keepID:
// si llegan varios pushes seguidos, solo merece la pena construir el último.
func (s *Store) SupersedeQueued(ctx context.Context, siteID, keepID int64) ([]int64, error) {
	var ids []int64
	err := s.db.NewSelect().Model((*Build)(nil)).Column("id").
		Where("site_id = ? AND status = ? AND id <> ?", siteID, BuildQueued, keepID).Scan(ctx, &ids)
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	for _, id := range ids {
		if err := s.FinishBuild(ctx, id, BuildCanceled, "reemplazado por un commit más reciente", nil); err != nil &&
			!errors.Is(err, ErrNotFound) {
			return nil, err
		}
	}
	return ids, nil
}

// SetBuildCommit guarda el commit que se va a construir (en los builds
// manuales se conoce al empezar, no al encolar).
func (s *Store) SetBuildCommit(ctx context.Context, id int64, sha, message string) error {
	return mustAffect(s.db.NewUpdate().Model((*Build)(nil)).
		Set("commit_sha = ?", sha).Set("commit_message = ?", message).
		Where("id = ?", id).Exec(ctx))
}

// --- Aplicaciones OAuth ---

func (s *Store) ListGitHubApps(ctx context.Context) ([]*GitHubApp, error) {
	out := []*GitHubApp{}
	err := s.db.NewSelect().Model(&out).Order("id").Scan(ctx)
	return out, err
}

func (s *Store) GetGitHubApp(ctx context.Context, id int64) (*GitHubApp, error) {
	a := &GitHubApp{ID: id}
	if err := s.db.NewSelect().Model(a).WherePK().Scan(ctx); err != nil {
		return nil, notFound(err)
	}
	return a, nil
}

// GitHubAppByAppID busca la app por el ID que le asignó GitHub.
func (s *Store) GitHubAppByAppID(ctx context.Context, appID int64) (*GitHubApp, error) {
	a := new(GitHubApp)
	if err := s.db.NewSelect().Model(a).Where("app_id = ?", appID).Limit(1).Scan(ctx); err != nil {
		return nil, notFound(err)
	}
	return a, nil
}

func (s *Store) CreateGitHubApp(ctx context.Context, a *GitHubApp) error {
	a.CreatedAt = now()
	_, err := s.db.NewInsert().Model(a).Exec(ctx)
	return err
}

func (s *Store) UpdateGitHubAppWebhook(ctx context.Context, a *GitHubApp) error {
	return mustAffect(s.db.NewUpdate().Model(a).WherePK().Column("webhook_secret_enc", "webhook_active").Exec(ctx))
}

func (s *Store) DeleteGitHubApp(ctx context.Context, id int64) error {
	return mustAffect(s.db.NewDelete().Model((*GitHubApp)(nil)).Where("id = ?", id).Exec(ctx))
}

func (s *Store) ListGitLabApps(ctx context.Context) ([]*GitLabApp, error) {
	out := []*GitLabApp{}
	err := s.db.NewSelect().Model(&out).Order("id").Scan(ctx)
	return out, err
}

func (s *Store) GetGitLabApp(ctx context.Context, id int64) (*GitLabApp, error) {
	a := &GitLabApp{ID: id}
	if err := s.db.NewSelect().Model(a).WherePK().Scan(ctx); err != nil {
		return nil, notFound(err)
	}
	return a, nil
}

// SaveGitLabApp crea la app o, si ya hay una para esa instancia, la reemplaza.
func (s *Store) SaveGitLabApp(ctx context.Context, a *GitLabApp) error {
	a.CreatedAt = now()
	_, err := s.db.NewInsert().Model(a).
		On("CONFLICT (web_url) DO UPDATE").
		Set("client_id = EXCLUDED.client_id").Set("client_secret_enc = EXCLUDED.client_secret_enc").
		Returning("id").Exec(ctx)
	return err
}

func (s *Store) DeleteGitLabApp(ctx context.Context, id int64) error {
	return mustAffect(s.db.NewDelete().Model((*GitLabApp)(nil)).Where("id = ?", id).Exec(ctx))
}

// SitesUsingConnections devuelve los sitios enlazados a alguna de las conexiones.
func (s *Store) SitesUsingConnections(ctx context.Context, ids []int64) ([]*Site, error) {
	out := []*Site{}
	if len(ids) == 0 {
		return out, nil
	}
	err := s.db.NewSelect().Model(&out).Where("git_connection_id IN (?)", bun.In(ids)).Scan(ctx)
	return out, err
}

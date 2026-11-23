package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/uptrace/bun"
)

// Ways of authenticating a connection.
const (
	AuthToken       = "token"        // personal access token pasted by hand
	AuthGitHubApp   = "github_app"   // installation of Dockfly's GitHub App
	AuthGitLabOAuth = "gitlab_oauth" // GitLab OAuth authorization
)

// GitConnection is a linked GitHub or GitLab account or organization.
type GitConnection struct {
	ID          int64     `bun:",pk,autoincrement" json:"id"`
	Provider    string    `json:"provider"` // "github" | "gitlab"
	AuthType    string    `json:"auth_type"`
	BaseURL     string    `json:"base_url"` // API URL: allows GitHub Enterprise and self-hosted GitLab
	Username    string    `json:"username"` // user or organization
	AccountType string    `json:"account_type"`
	SettingsURL string    `json:"settings_url"`
	CreatedAt   time.Time `json:"created_at"`

	// Access token (personal or OAuth). Empty for GitHub App installations,
	// whose tokens are minted on demand and expire in an hour.
	TokenEnc        []byte     `json:"-"`
	RefreshTokenEnc []byte     `json:"-"`
	TokenExpiresAt  *time.Time `json:"-"`
	RedirectURI     string     `json:"-"` // GitLab requires it on refresh too

	GitHubAppID    *int64 `bun:"github_app_id" json:"github_app_id"`
	InstallationID *int64 `json:"installation_id"`
	GitLabAppID    *int64 `bun:"gitlab_app_id" json:"gitlab_app_id"`
}

// GitHubApp is this Dockfly installation's own GitHub App.
type GitHubApp struct {
	// Without the explicit name, Bun would infer "git_hub_apps".
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

// GitLabApp is an OAuth application registered on a GitLab instance.
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

// UpsertInstallation records (or updates) the connection of a GitHub App
// installation. Installing the same account twice does not duplicate it.
func (s *Store) UpsertInstallation(ctx context.Context, c *GitConnection) error {
	existing := new(GitConnection)
	err := s.db.NewSelect().Model(existing).
		Where("github_app_id = ? AND installation_id = ?", c.GitHubAppID, c.InstallationID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		// Installations store no token (it is minted on demand), but the column
		// has been NOT NULL since the first version: store it empty, not null.
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

// ConnectionsByInstallation returns the connections of one installation.
func (s *Store) ConnectionsByInstallation(ctx context.Context, appID, installationID int64) ([]*GitConnection, error) {
	out := []*GitConnection{}
	err := s.db.NewSelect().Model(&out).
		Where("github_app_id = ? AND installation_id = ?", appID, installationID).Scan(ctx)
	return out, err
}

// SaveGitLabConnection stores a GitLab OAuth authorization. If the same user
// authorizes again, their tokens are updated instead of being duplicated.
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

// UpdateConnectionTokens stores a refreshed OAuth token.
func (s *Store) UpdateConnectionTokens(ctx context.Context, c *GitConnection) error {
	return mustAffect(s.db.NewUpdate().Model(c).WherePK().
		Column("token_enc", "refresh_token_enc", "token_expires_at").Exec(ctx))
}

func (s *Store) DeleteGitConnection(ctx context.Context, id int64) error {
	return mustAffect(s.db.NewDelete().Model((*GitConnection)(nil)).Where("id = ?", id).Exec(ctx))
}

// --- Settings ---

type setting struct {
	Key   string `bun:",pk"`
	Value string
}

const (
	SettingPublicURL = "public_url"
	// SettingPreviewDomain is the domain the previews are published under
	// (branch--site.<domain>).
	SettingPreviewDomain = "preview_domain"
	// SettingPreviewAccessList protects every preview with an access list.
	SettingPreviewAccessList = "preview_access_list"
)

// GetSetting returns "" if the setting does not exist.
func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	st := &setting{Key: key}
	err := s.db.NewSelect().Model(st).WherePK().Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return st.Value, err
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.upsert(s.db.NewInsert().Model(&setting{Key: key, Value: value}), "key", "value").Exec(ctx)
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
	TriggerHook   = "hook" // the site's deploy hook
)

type Build struct {
	ID            int64      `bun:",pk,autoincrement" json:"id"`
	SiteID        int64      `json:"site_id"`
	Status        string     `json:"status"`
	Trigger       string     `json:"trigger"`
	Branch        string     `json:"branch"`
	Preview       string     `json:"preview"` // "" = production
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

// Finished reports whether the build will no longer change state.
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

// MarkBuildStarted moves a build from queued to running. It returns
// ErrNotFound if it was no longer queued (for example it was canceled while
// it waited).
func (s *Store) MarkBuildStarted(ctx context.Context, id int64) error {
	return mustAffect(s.db.NewUpdate().Model((*Build)(nil)).
		Set("status = ?", BuildRunning).Set("started_at = ?", now()).
		Where("id = ? AND status = ?", id, BuildQueued).Exec(ctx))
}

// FinishBuild sets the final status. It only acts on unfinished builds, so a
// canceled build cannot come back to life as a successful one.
func (s *Store) FinishBuild(ctx context.Context, id int64, status, errMsg string, deploymentID *int64) error {
	return mustAffect(s.db.NewUpdate().Model((*Build)(nil)).
		Set("status = ?", status).Set("error = ?", errMsg).Set("deployment_id = ?", deploymentID).
		Set("finished_at = ?", now()).
		Where("id = ? AND status IN (?, ?)", id, BuildQueued, BuildRunning).Exec(ctx))
}

// SupersedeQueued cancels the queued builds of the same site and the same
// preview (or production), except keepID: when several pushes arrive in a
// row, only the last one is worth building.
func (s *Store) SupersedeQueued(ctx context.Context, siteID, keepID int64) ([]int64, error) {
	keep, err := s.GetBuild(ctx, keepID)
	if err != nil {
		return nil, err
	}
	var ids []int64
	err = s.db.NewSelect().Model((*Build)(nil)).Column("id").
		Where("site_id = ? AND status = ? AND id <> ? AND preview = ?", siteID, BuildQueued, keepID, keep.Preview).Scan(ctx, &ids)
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	for _, id := range ids {
		if err := s.FinishBuild(ctx, id, BuildCanceled, "superseded by a more recent commit", nil); err != nil &&
			!errors.Is(err, ErrNotFound) {
			return nil, err
		}
	}
	return ids, nil
}

// SetBuildCommit stores the commit that is going to be built (for manual
// builds it is only known when the build starts, not when it is queued).
func (s *Store) SetBuildCommit(ctx context.Context, id int64, sha, message string) error {
	return mustAffect(s.db.NewUpdate().Model((*Build)(nil)).
		Set("commit_sha = ?", sha).Set("commit_message = ?", message).
		Where("id = ?", id).Exec(ctx))
}

// --- OAuth applications ---

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

// GitHubAppByAppID looks the app up by the ID GitHub assigned to it.
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

// SaveGitLabApp creates the app or, if one already exists for that instance,
// replaces it.
func (s *Store) SaveGitLabApp(ctx context.Context, a *GitLabApp) error {
	a.CreatedAt = now()
	if _, err := s.upsert(s.db.NewInsert().Model(a), "web_url", "client_id", "client_secret_enc").Exec(ctx); err != nil {
		return err
	}
	// The id of the surviving row (on replace, MySQL's INSERT does not return it).
	return s.db.NewSelect().Model(a).Column("id").Where("web_url = ?", a.WebURL).Scan(ctx)
}

func (s *Store) DeleteGitLabApp(ctx context.Context, id int64) error {
	return mustAffect(s.db.NewDelete().Model((*GitLabApp)(nil)).Where("id = ?", id).Exec(ctx))
}

// SitesUsingConnections returns the sites linked to any of the connections.
func (s *Store) SitesUsingConnections(ctx context.Context, ids []int64) ([]*Site, error) {
	out := []*Site{}
	if len(ids) == 0 {
		return out, nil
	}
	err := s.db.NewSelect().Model(&out).Where("git_connection_id IN (?)", bun.In(ids)).Scan(ctx)
	return out, err
}

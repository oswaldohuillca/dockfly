package store

import (
	"context"
	"errors"
	"time"
)

const (
	AppSourceImage   = "image"
	AppSourceGit     = "git"     // the repository's Dockerfile
	AppSourceCompose = "compose" // the repository's docker-compose.yml
)

// Statuses of an app deployment.
const (
	DeployQueued   = "queued"
	DeployRunning  = "running"  // building / starting up
	DeployLive     = "live"     // the one serving right now
	DeployReplaced = "replaced" // was live and another one took over
	DeployFailed   = "failed"
	DeployCanceled = "canceled"
)

type AppVolume struct {
	Name string `json:"name"` // identifier within the app (e.g. "data")
	Path string `json:"path"` // path inside the container
}

type App struct {
	ID              int64       `bun:",pk,autoincrement" json:"id"`
	Name            string      `json:"name"`
	Source          string      `json:"source"`
	Image           string      `json:"image"`
	GitConnectionID *int64      `json:"git_connection_id"`
	Repo            string      `json:"repo"`
	Branch          string      `json:"branch"`
	RootDir         string      `json:"root_dir"`
	Dockerfile      string      `json:"dockerfile"`
	ComposeFile     string      `json:"compose_file"`
	ComposeService  string      `json:"compose_service"` // the service that receives the web traffic
	WebhookID       string      `json:"webhook_id"`
	WebhookSecret   string      `json:"-"`
	Port            int         `json:"port"`
	Domains         []string    `json:"domains"`
	SSL             bool        `bun:"ssl" json:"ssl"`
	ForceHTTPS      bool        `bun:"force_https" json:"force_https"`
	HSTS            bool        `bun:"hsts" json:"hsts"`
	EnvEnc          []byte      `json:"-"`
	Volumes         []AppVolume `json:"volumes"`
	Command         string      `json:"command"`
	MemoryMB        int         `json:"memory_mb"`
	CPUs            float64     `bun:"cpus" json:"cpus"`
	HealthPath      string      `json:"health_path"`
	AccessListID    *int64      `json:"access_list_id"`
	Stopped         bool        `json:"stopped"`
	// ServerID is the remote server it runs on (nil = the panel's own). It
	// does not change after the app is created.
	ServerID            *int64    `json:"server_id"`
	CurrentDeploymentID *int64    `json:"current_deployment_id"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`

	Current *AppDeployment `bun:"rel:belongs-to,join:current_deployment_id=id" json:"current,omitempty"`
}

type AppDeployment struct {
	ID            int64      `bun:",pk,autoincrement" json:"id"`
	AppID         int64      `json:"app_id"`
	Status        string     `json:"status"`
	Trigger       string     `json:"trigger"`
	Image         string     `json:"image"`
	Container     string     `json:"container"`
	CommitSHA     string     `json:"commit_sha"`
	CommitMessage string     `json:"commit_message"`
	Error         string     `json:"error"`
	CreatedAt     time.Time  `json:"created_at"`
	StartedAt     *time.Time `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at"`
}

func (a *App) AfterScanRow(context.Context) error {
	a.CreatedAt, a.UpdatedAt = a.CreatedAt.UTC(), a.UpdatedAt.UTC()
	return nil
}

func (d *AppDeployment) AfterScanRow(context.Context) error {
	d.CreatedAt = d.CreatedAt.UTC()
	for _, t := range []**time.Time{&d.StartedAt, &d.FinishedAt} {
		if *t != nil {
			u := (**t).UTC()
			*t = &u
		}
	}
	return nil
}

// Finished reports whether the deployment will no longer change (other than
// going from live to replaced).
func (d *AppDeployment) Finished() bool {
	return d.Status != DeployQueued && d.Status != DeployRunning
}

// appStateColumns are the ones the deployment lifecycle changes, not the form.
var appStateColumns = []string{"created_at", "current_deployment_id", "stopped", "webhook_id", "webhook_secret", "server_id"}

func (s *Store) ListApps(ctx context.Context) ([]*App, error) {
	out := []*App{}
	err := s.db.NewSelect().Model(&out).Relation("Current").Order("app.id").Scan(ctx)
	return out, err
}

func (s *Store) GetApp(ctx context.Context, id int64) (*App, error) {
	a := new(App)
	if err := s.db.NewSelect().Model(a).Relation("Current").Where("app.id = ?", id).Scan(ctx); err != nil {
		return nil, notFound(err)
	}
	return a, nil
}

func (s *Store) CreateApp(ctx context.Context, a *App) error {
	a.CreatedAt = now()
	a.UpdatedAt = a.CreatedAt
	_, err := s.db.NewInsert().Model(a).ExcludeColumn("current_deployment_id").Exec(ctx)
	return err
}

// UpdateApp saves the form's configuration without touching the deployment
// state (current container, stopped flag, webhook).
func (s *Store) UpdateApp(ctx context.Context, a *App) error {
	a.UpdatedAt = now()
	return mustAffect(s.db.NewUpdate().Model(a).WherePK().ExcludeColumn(appStateColumns...).Exec(ctx))
}

func (s *Store) SetAppWebhook(ctx context.Context, id int64, hookID, secret string) error {
	return mustAffect(s.db.NewUpdate().Model((*App)(nil)).
		Set("webhook_id = ?", hookID).Set("webhook_secret = ?", secret).Where("id = ?", id).Exec(ctx))
}

func (s *Store) SetAppStopped(ctx context.Context, id int64, stopped bool) error {
	return mustAffect(s.db.NewUpdate().Model((*App)(nil)).Set("stopped = ?", stopped).Where("id = ?", id).Exec(ctx))
}

func (s *Store) DeleteApp(ctx context.Context, id int64) error {
	return mustAffect(s.db.NewDelete().Model((*App)(nil)).Where("id = ?", id).Exec(ctx))
}

// --- Deployments ---

func (s *Store) CreateAppDeployment(ctx context.Context, d *AppDeployment) error {
	d.CreatedAt = now()
	_, err := s.db.NewInsert().Model(d).Exec(ctx)
	return err
}

func (s *Store) GetAppDeployment(ctx context.Context, id int64) (*AppDeployment, error) {
	d := &AppDeployment{ID: id}
	if err := s.db.NewSelect().Model(d).WherePK().Scan(ctx); err != nil {
		return nil, notFound(err)
	}
	return d, nil
}

func (s *Store) ListAppDeployments(ctx context.Context, appID int64, limit int) ([]*AppDeployment, error) {
	out := []*AppDeployment{}
	err := s.db.NewSelect().Model(&out).Where("app_id = ?", appID).Order("id DESC").Limit(int64(limit)).Scan(ctx)
	return out, err
}

func (s *Store) ListAppDeploymentsByStatus(ctx context.Context, status string) ([]*AppDeployment, error) {
	out := []*AppDeployment{}
	err := s.db.NewSelect().Model(&out).Where("status = ?", status).Order("id").Scan(ctx)
	return out, err
}

func (s *Store) StartAppDeployment(ctx context.Context, id int64) error {
	return mustAffect(s.db.NewUpdate().Model((*AppDeployment)(nil)).
		Set("status = ?", DeployRunning).Set("started_at = ?", now()).
		Where("id = ? AND status = ?", id, DeployQueued).Exec(ctx))
}

// UpdateAppDeployment saves the data that becomes known as the deploy runs.
func (s *Store) UpdateAppDeployment(ctx context.Context, d *AppDeployment) error {
	_, err := s.db.NewUpdate().Model(d).WherePK().Column("image", "container", "commit_sha", "commit_message").Exec(ctx)
	return err
}

// FinishAppDeployment sets the result of a running or queued deployment.
func (s *Store) FinishAppDeployment(ctx context.Context, id int64, status, errMsg string) error {
	return mustAffect(s.db.NewUpdate().Model((*AppDeployment)(nil)).
		Set("status = ?", status).Set("error = ?", errMsg).Set("finished_at = ?", now()).
		Where("id = ? AND status IN (?, ?)", id, DeployQueued, DeployRunning).Exec(ctx))
}

// GoLive marks a deployment as its app's live one and the previous one as
// replaced, in a single transaction.
func (s *Store) GoLive(ctx context.Context, appID, deploymentID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.NewUpdate().Model((*AppDeployment)(nil)).Set("status = ?", DeployReplaced).
		Where("app_id = ? AND status = ? AND id <> ?", appID, DeployLive, deploymentID).Exec(ctx); err != nil {
		return err
	}
	res, err := tx.NewUpdate().Model((*AppDeployment)(nil)).
		Set("status = ?", DeployLive).Set("error = ''").Set("finished_at = ?", now()).
		Where("id = ? AND status = ?", deploymentID, DeployRunning).Exec(ctx)
	if err := mustAffect(res, err); err != nil {
		return errors.New("the deployment was no longer running (was it canceled?)")
	}
	if _, err := tx.NewUpdate().Model((*App)(nil)).Set("current_deployment_id = ?", deploymentID).
		Set("stopped = FALSE").Set("updated_at = ?", now()).Where("id = ?", appID).Exec(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

// SupersedeQueuedApp cancels an app's queued deployments, except keepID.
func (s *Store) SupersedeQueuedApp(ctx context.Context, appID, keepID int64) error {
	_, err := s.db.NewUpdate().Model((*AppDeployment)(nil)).
		Set("status = ?", DeployCanceled).Set("error = ?", "superseded by a more recent deployment").
		Set("finished_at = ?", now()).
		Where("app_id = ? AND status = ? AND id <> ?", appID, DeployQueued, keepID).Exec(ctx)
	return err
}

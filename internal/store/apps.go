package store

import (
	"context"
	"errors"
	"time"
)

const (
	AppSourceImage   = "image"
	AppSourceGit     = "git"     // Dockerfile del repositorio
	AppSourceCompose = "compose" // docker-compose.yml del repositorio
)

// Estados de un despliegue de app.
const (
	DeployQueued   = "queued"
	DeployRunning  = "running"  // construyendo / arrancando
	DeployLive     = "live"     // es el que sirve ahora mismo
	DeployReplaced = "replaced" // estuvo activo y lo sustituyó otro
	DeployFailed   = "failed"
	DeployCanceled = "canceled"
)

type AppVolume struct {
	Name string `json:"name"` // identificador dentro de la app (p. ej. "data")
	Path string `json:"path"` // ruta en el contenedor
}

type App struct {
	ID                  int64       `bun:",pk,autoincrement" json:"id"`
	Name                string      `json:"name"`
	Source              string      `json:"source"`
	Image               string      `json:"image"`
	GitConnectionID     *int64      `json:"git_connection_id"`
	Repo                string      `json:"repo"`
	Branch              string      `json:"branch"`
	RootDir             string      `json:"root_dir"`
	Dockerfile          string      `json:"dockerfile"`
	ComposeFile         string      `json:"compose_file"`
	ComposeService      string      `json:"compose_service"` // el servicio que recibe el tráfico web
	WebhookID           string      `json:"webhook_id"`
	WebhookSecret       string      `json:"-"`
	Port                int         `json:"port"`
	Domains             []string    `json:"domains"`
	SSL                 bool        `bun:"ssl" json:"ssl"`
	ForceHTTPS          bool        `bun:"force_https" json:"force_https"`
	HSTS                bool        `bun:"hsts" json:"hsts"`
	EnvEnc              []byte      `json:"-"`
	Volumes             []AppVolume `json:"volumes"`
	Command             string      `json:"command"`
	MemoryMB            int         `json:"memory_mb"`
	CPUs                float64     `bun:"cpus" json:"cpus"`
	HealthPath          string      `json:"health_path"`
	AccessListID        *int64      `json:"access_list_id"`
	Stopped             bool        `json:"stopped"`
	CurrentDeploymentID *int64      `json:"current_deployment_id"`
	CreatedAt           time.Time   `json:"created_at"`
	UpdatedAt           time.Time   `json:"updated_at"`

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

// Finished indica si el despliegue ya no va a cambiar (salvo pasar de live a replaced).
func (d *AppDeployment) Finished() bool {
	return d.Status != DeployQueued && d.Status != DeployRunning
}

// appStateColumns son las que cambia el ciclo de despliegue, no el formulario.
var appStateColumns = []string{"created_at", "current_deployment_id", "stopped", "webhook_id", "webhook_secret"}

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

// UpdateApp guarda la configuración del formulario sin tocar el estado del
// despliegue (contenedor actual, detenida, webhook).
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

// --- Despliegues ---

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

// UpdateAppDeployment guarda los datos que se conocen sobre la marcha.
func (s *Store) UpdateAppDeployment(ctx context.Context, d *AppDeployment) error {
	_, err := s.db.NewUpdate().Model(d).WherePK().Column("image", "container", "commit_sha", "commit_message").Exec(ctx)
	return err
}

// FinishAppDeployment fija el resultado de un despliegue en curso o en cola.
func (s *Store) FinishAppDeployment(ctx context.Context, id int64, status, errMsg string) error {
	return mustAffect(s.db.NewUpdate().Model((*AppDeployment)(nil)).
		Set("status = ?", status).Set("error = ?", errMsg).Set("finished_at = ?", now()).
		Where("id = ? AND status IN (?, ?)", id, DeployQueued, DeployRunning).Exec(ctx))
}

// GoLive marca un despliegue como el activo de su app y el anterior como
// sustituido, en una transacción.
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
		return errors.New("el despliegue ya no estaba en curso (¿se canceló?)")
	}
	if _, err := tx.NewUpdate().Model((*App)(nil)).Set("current_deployment_id = ?", deploymentID).
		Set("stopped = FALSE").Set("updated_at = ?", now()).Where("id = ?", appID).Exec(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

// SupersedeQueuedApp cancela los despliegues en cola de una app, salvo keepID.
func (s *Store) SupersedeQueuedApp(ctx context.Context, appID, keepID int64) error {
	_, err := s.db.NewUpdate().Model((*AppDeployment)(nil)).
		Set("status = ?", DeployCanceled).Set("error = ?", "reemplazado por un despliegue más reciente").
		Set("finished_at = ?", now()).
		Where("app_id = ? AND status = ? AND id <> ?", appID, DeployQueued, keepID).Exec(ctx)
	return err
}

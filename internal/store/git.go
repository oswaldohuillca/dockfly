package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// GitConnection es una cuenta de GitHub o GitLab vinculada con un token.
type GitConnection struct {
	ID        int64     `bun:",pk,autoincrement" json:"id"`
	Provider  string    `json:"provider"` // "github" | "gitlab"
	BaseURL   string    `json:"base_url"` // URL de la API: permite GitHub Enterprise y GitLab propio
	Username  string    `json:"username"`
	TokenEnc  []byte    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
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
	_, err := s.db.NewInsert().Model(c).Exec(ctx)
	return err
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

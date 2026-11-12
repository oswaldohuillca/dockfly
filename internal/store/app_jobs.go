package store

import (
	"context"
	"time"

	"github.com/uptrace/bun"
)

// AppJob es una tarea programada: un comando que se ejecuta en el
// contenedor de la app cuando toca su expresión cron.
type AppJob struct {
	bun.BaseModel `bun:"table:app_jobs"`

	ID           int64      `bun:",pk,autoincrement" json:"id"`
	AppID        int64      `json:"app_id"`
	Command      string     `json:"command"`
	Schedule     string     `json:"schedule"`
	Enabled      bool       `json:"enabled"`
	LastRunAt    *time.Time `json:"last_run_at"`
	LastExitCode *int       `json:"last_exit_code"`
	LastOutput   string     `json:"last_output"`
	CreatedAt    time.Time  `json:"created_at"`
}

func (j *AppJob) AfterScanRow(context.Context) error {
	j.CreatedAt = j.CreatedAt.UTC()
	if j.LastRunAt != nil {
		t := j.LastRunAt.UTC()
		j.LastRunAt = &t
	}
	return nil
}

func (s *Store) ListAppJobs(ctx context.Context, appID int64) ([]*AppJob, error) {
	jobs := []*AppJob{}
	err := s.db.NewSelect().Model(&jobs).Where("app_id = ?", appID).Order("id").Scan(ctx)
	return jobs, err
}

func (s *Store) EnabledAppJobs(ctx context.Context) ([]*AppJob, error) {
	jobs := []*AppJob{}
	err := s.db.NewSelect().Model(&jobs).Where("enabled").Order("id").Scan(ctx)
	return jobs, err
}

func (s *Store) GetAppJob(ctx context.Context, id int64) (*AppJob, error) {
	j := &AppJob{ID: id}
	if err := s.db.NewSelect().Model(j).WherePK().Scan(ctx); err != nil {
		return nil, notFound(err)
	}
	return j, nil
}

func (s *Store) CreateAppJob(ctx context.Context, j *AppJob) error {
	j.CreatedAt = now()
	_, err := s.db.NewInsert().Model(j).Exec(ctx)
	return err
}

// UpdateAppJob guarda lo que edita el formulario, no el resultado de la última ejecución.
func (s *Store) UpdateAppJob(ctx context.Context, j *AppJob) error {
	return mustAffect(s.db.NewUpdate().Model(j).Column("command", "schedule", "enabled").WherePK().Exec(ctx))
}

func (s *Store) DeleteAppJob(ctx context.Context, id int64) error {
	return mustAffect(s.db.NewDelete().Model((*AppJob)(nil)).Where("id = ?", id).Exec(ctx))
}

// StartAppJob marca el inicio de una ejecución: sin código de salida = en marcha.
func (s *Store) StartAppJob(ctx context.Context, id int64, at time.Time) error {
	_, err := s.db.NewUpdate().Model((*AppJob)(nil)).Set("last_run_at = ?", at).Set("last_exit_code = NULL").
		Where("id = ?", id).Exec(ctx)
	return err
}

func (s *Store) FinishAppJob(ctx context.Context, id int64, exitCode int, output string) error {
	_, err := s.db.NewUpdate().Model((*AppJob)(nil)).Set("last_exit_code = ?", exitCode).Set("last_output = ?", output).
		Where("id = ?", id).Exec(ctx)
	return err
}

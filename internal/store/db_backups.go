package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/uptrace/bun"
)

// Statuses of a database backup.
const (
	DBBackupRunning = "running"
	DBBackupSuccess = "success"
	DBBackupFailed  = "failed"
)

// DBBackupPolicy says how often a database is backed up and how many backups
// are kept.
type DBBackupPolicy struct {
	bun.BaseModel `bun:"table:db_backup_policies"`

	AppID         int64     `bun:",pk" json:"app_id"`
	Enabled       bool      `json:"enabled"`
	IntervalHours int       `json:"interval_hours"`
	Keep          int       `json:"keep"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type DBBackup struct {
	bun.BaseModel `bun:"table:db_backups"`

	ID         int64      `bun:",pk,autoincrement" json:"id"`
	AppID      int64      `json:"app_id"`
	Status     string     `json:"status"`
	Trigger    string     `json:"trigger"`
	Engine     string     `json:"engine"`
	Location   string     `json:"location"`
	ObjectKey  string     `json:"object_key"`
	Size       int64      `json:"size"`
	Error      string     `json:"error"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

func (b *DBBackup) AfterScanRow(context.Context) error {
	b.CreatedAt = b.CreatedAt.UTC()
	if b.FinishedAt != nil {
		t := b.FinishedAt.UTC()
		b.FinishedAt = &t
	}
	return nil
}

// DefaultDBBackupPolicy is the policy of an app that does not have one yet.
func DefaultDBBackupPolicy(appID int64) *DBBackupPolicy {
	return &DBBackupPolicy{AppID: appID, IntervalHours: 24, Keep: 7}
}

func (s *Store) GetDBBackupPolicy(ctx context.Context, appID int64) (*DBBackupPolicy, error) {
	p := &DBBackupPolicy{AppID: appID}
	err := s.db.NewSelect().Model(p).WherePK().Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultDBBackupPolicy(appID), nil
	}
	return p, err
}

func (s *Store) SaveDBBackupPolicy(ctx context.Context, p *DBBackupPolicy) error {
	p.UpdatedAt = now()
	_, err := s.upsert(s.db.NewInsert().Model(p), "app_id", "enabled", "interval_hours", "keep", "updated_at").Exec(ctx)
	return err
}

// EnabledDBBackupPolicies returns the policies that are turned on.
func (s *Store) EnabledDBBackupPolicies(ctx context.Context) ([]*DBBackupPolicy, error) {
	list := []*DBBackupPolicy{}
	err := s.db.NewSelect().Model(&list).Where("enabled").Scan(ctx)
	return list, err
}

func (s *Store) CreateDBBackup(ctx context.Context, b *DBBackup) error {
	b.CreatedAt = now()
	_, err := s.db.NewInsert().Model(b).Exec(ctx)
	return err
}

// FinishDBBackup stores the result of a backup.
func (s *Store) FinishDBBackup(ctx context.Context, b *DBBackup) error {
	t := now()
	b.FinishedAt = &t
	_, err := s.db.NewUpdate().Model(b).Column("status", "location", "object_key", "size", "error", "finished_at").WherePK().Exec(ctx)
	return err
}

func (s *Store) ListDBBackups(ctx context.Context, appID int64) ([]*DBBackup, error) {
	list := []*DBBackup{}
	err := s.db.NewSelect().Model(&list).Where("app_id = ?", appID).Order("id DESC").Limit(200).Scan(ctx)
	return list, err
}

func (s *Store) GetDBBackup(ctx context.Context, appID, id int64) (*DBBackup, error) {
	b := new(DBBackup)
	if err := s.db.NewSelect().Model(b).Where("id = ? AND app_id = ?", id, appID).Scan(ctx); err != nil {
		return nil, notFound(err)
	}
	return b, nil
}

func (s *Store) DeleteDBBackup(ctx context.Context, id int64) error {
	return mustAffect(s.db.NewDelete().Model((*DBBackup)(nil)).Where("id = ?", id).Exec(ctx))
}

// LastDBBackup returns an app's latest backup (nil if there is none).
func (s *Store) LastDBBackup(ctx context.Context, appID int64) (*DBBackup, error) {
	b := new(DBBackup)
	err := s.db.NewSelect().Model(b).Where("app_id = ?", appID).Order("id DESC").Limit(1).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return b, err
}

// FailRunningDBBackups marks as failed the backups that were left half-done
// (Dockfly restarted while they were running).
func (s *Store) FailRunningDBBackups(ctx context.Context) error {
	_, err := s.db.NewUpdate().Model((*DBBackup)(nil)).
		Set("status = ?", DBBackupFailed).Set("error = ?", "interrupted: Dockfly restarted").Set("finished_at = ?", now()).
		Where("status = ?", DBBackupRunning).Exec(ctx)
	return err
}

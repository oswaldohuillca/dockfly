package store

import (
	"context"
	"time"
)

// RedirectHost sends one or more domains to another URL.
type RedirectHost struct {
	ID           int64     `bun:",pk,autoincrement" json:"id"`
	Domains      []string  `json:"domains"`
	Target       string    `json:"target"`
	Permanent    bool      `json:"permanent"`     // 301; otherwise 302
	PreservePath bool      `json:"preserve_path"` // old.com/x → new.com/x
	SSL          bool      `bun:"ssl" json:"ssl"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (r *RedirectHost) AfterScanRow(context.Context) error {
	r.CreatedAt, r.UpdatedAt = r.CreatedAt.UTC(), r.UpdatedAt.UTC()
	return nil
}

func (s *Store) ListRedirects(ctx context.Context) ([]*RedirectHost, error) {
	out := []*RedirectHost{}
	err := s.db.NewSelect().Model(&out).Order("id").Scan(ctx)
	return out, err
}

func (s *Store) GetRedirect(ctx context.Context, id int64) (*RedirectHost, error) {
	r := &RedirectHost{ID: id}
	if err := s.db.NewSelect().Model(r).WherePK().Scan(ctx); err != nil {
		return nil, notFound(err)
	}
	return r, nil
}

func (s *Store) CreateRedirect(ctx context.Context, r *RedirectHost) error {
	r.CreatedAt = now()
	r.UpdatedAt = r.CreatedAt
	_, err := s.db.NewInsert().Model(r).Exec(ctx)
	return err
}

func (s *Store) UpdateRedirect(ctx context.Context, r *RedirectHost) error {
	r.UpdatedAt = now()
	return mustAffect(s.db.NewUpdate().Model(r).WherePK().ExcludeColumn("created_at").Exec(ctx))
}

func (s *Store) DeleteRedirect(ctx context.Context, id int64) error {
	return mustAffect(s.db.NewDelete().Model((*RedirectHost)(nil)).Where("id = ?", id).Exec(ctx))
}

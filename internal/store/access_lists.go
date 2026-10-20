package store

import (
	"context"
	"time"
)

type AccessUser struct {
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash,omitempty"`
}

// AccessList protege proxy hosts, sitios y apps. Si tiene usuarios e IPs,
// se exigen ambas cosas.
type AccessList struct {
	ID        int64        `bun:",pk,autoincrement" json:"id"`
	Name      string       `json:"name"`
	Users     []AccessUser `json:"users"`
	AllowIPs  []string     `bun:"allow_ips" json:"allow_ips"`
	CreatedAt time.Time    `json:"created_at"`
}

func (a *AccessList) AfterScanRow(context.Context) error {
	a.CreatedAt = a.CreatedAt.UTC()
	return nil
}

func (s *Store) ListAccessLists(ctx context.Context) ([]*AccessList, error) {
	out := []*AccessList{}
	err := s.db.NewSelect().Model(&out).Order("id").Scan(ctx)
	return out, err
}

func (s *Store) GetAccessList(ctx context.Context, id int64) (*AccessList, error) {
	a := &AccessList{ID: id}
	if err := s.db.NewSelect().Model(a).WherePK().Scan(ctx); err != nil {
		return nil, notFound(err)
	}
	return a, nil
}

func (s *Store) CreateAccessList(ctx context.Context, a *AccessList) error {
	a.CreatedAt = now()
	_, err := s.db.NewInsert().Model(a).Exec(ctx)
	return err
}

func (s *Store) UpdateAccessList(ctx context.Context, a *AccessList) error {
	return mustAffect(s.db.NewUpdate().Model(a).WherePK().Column("name", "users", "allow_ips").Exec(ctx))
}

func (s *Store) DeleteAccessList(ctx context.Context, id int64) error {
	return mustAffect(s.db.NewDelete().Model((*AccessList)(nil)).Where("id = ?", id).Exec(ctx))
}

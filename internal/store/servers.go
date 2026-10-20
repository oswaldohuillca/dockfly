package store

import (
	"context"
	"time"
)

// Server es un servidor remoto con un agente de Dockfly.
type Server struct {
	ID         int64      `bun:",pk,autoincrement" json:"id"`
	Name       string     `json:"name"`
	TokenHash  string     `json:"-"`
	LastSeenAt *time.Time `json:"last_seen_at"`
	LastAddr   string     `json:"last_addr"`
	Version    string     `json:"version"`
	CreatedAt  time.Time  `json:"created_at"`
}

func (s *Server) AfterScanRow(context.Context) error {
	s.CreatedAt = s.CreatedAt.UTC()
	if s.LastSeenAt != nil {
		t := s.LastSeenAt.UTC()
		s.LastSeenAt = &t
	}
	return nil
}

func (s *Store) ListServers(ctx context.Context) ([]*Server, error) {
	list := []*Server{}
	return list, s.db.NewSelect().Model(&list).Order("name").Scan(ctx)
}

func (s *Store) GetServer(ctx context.Context, id int64) (*Server, error) {
	sv := &Server{ID: id}
	if err := s.db.NewSelect().Model(sv).WherePK().Scan(ctx); err != nil {
		return nil, notFound(err)
	}
	return sv, nil
}

func (s *Store) ServerByTokenHash(ctx context.Context, hash string) (*Server, error) {
	sv := new(Server)
	if err := s.db.NewSelect().Model(sv).Where("token_hash = ?", hash).Scan(ctx); err != nil {
		return nil, notFound(err)
	}
	return sv, nil
}

func (s *Store) CreateServer(ctx context.Context, sv *Server) error {
	sv.CreatedAt = now()
	_, err := s.db.NewInsert().Model(sv).Exec(ctx)
	return err
}

func (s *Store) SetServerToken(ctx context.Context, id int64, hash string) error {
	return mustAffect(s.db.NewUpdate().Model((*Server)(nil)).Set("token_hash = ?", hash).Where("id = ?", id).Exec(ctx))
}

// ServerSeen guarda cuándo y desde dónde se conectó su agente.
func (s *Store) ServerSeen(ctx context.Context, id int64, addr, version string) error {
	_, err := s.db.NewUpdate().Model((*Server)(nil)).
		Set("last_seen_at = ?", now()).Set("last_addr = ?", addr).Set("version = ?", version).
		Where("id = ?", id).Exec(ctx)
	return err
}

func (s *Store) DeleteServer(ctx context.Context, id int64) error {
	return mustAffect(s.db.NewDelete().Model((*Server)(nil)).Where("id = ?", id).Exec(ctx))
}

// CountServerApps cuenta las apps de un servidor.
func (s *Store) CountServerApps(ctx context.Context, id int64) (int, error) {
	n, err := s.db.NewSelect().Model((*App)(nil)).Where("server_id = ?", id).Count(ctx)
	return int(n), err
}

package store

import (
	"context"
	"time"
)

// Stream reenvía un puerto TCP o UDP a otro servicio (bases de datos,
// servidores de juegos, MQTT…).
type Stream struct {
	ID          int64     `bun:",pk,autoincrement" json:"id"`
	Protocol    string    `json:"protocol"`
	ListenPort  int       `json:"listen_port"`
	ForwardHost string    `json:"forward_host"`
	ForwardPort int       `json:"forward_port"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
}

func (s *Stream) AfterScanRow(context.Context) error {
	s.CreatedAt = s.CreatedAt.UTC()
	return nil
}

func (s *Store) ListStreams(ctx context.Context) ([]*Stream, error) {
	out := []*Stream{}
	err := s.db.NewSelect().Model(&out).Order("listen_port").Scan(ctx)
	return out, err
}

func (s *Store) GetStream(ctx context.Context, id int64) (*Stream, error) {
	st := &Stream{ID: id}
	if err := s.db.NewSelect().Model(st).WherePK().Scan(ctx); err != nil {
		return nil, notFound(err)
	}
	return st, nil
}

func (s *Store) CreateStream(ctx context.Context, st *Stream) error {
	st.CreatedAt = now()
	_, err := s.db.NewInsert().Model(st).Exec(ctx)
	return err
}

func (s *Store) UpdateStream(ctx context.Context, st *Stream) error {
	return mustAffect(s.db.NewUpdate().Model(st).WherePK().ExcludeColumn("created_at").Exec(ctx))
}

func (s *Store) DeleteStream(ctx context.Context, id int64) error {
	return mustAffect(s.db.NewDelete().Model((*Stream)(nil)).Where("id = ?", id).Exec(ctx))
}

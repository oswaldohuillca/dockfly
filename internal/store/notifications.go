package store

import (
	"context"
	"time"
)

type NotificationChannel struct {
	ID         int64      `bun:",pk,autoincrement" json:"id"`
	Type       string     `json:"type"`
	Name       string     `json:"name"`
	ConfigEnc  []byte     `json:"-"`
	Events     []string   `json:"events"`
	Enabled    bool       `json:"enabled"`
	LastError  string     `json:"last_error"`
	LastSentAt *time.Time `json:"last_sent_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

func (c *NotificationChannel) AfterScanRow(context.Context) error {
	c.CreatedAt = c.CreatedAt.UTC()
	if c.LastSentAt != nil {
		t := c.LastSentAt.UTC()
		c.LastSentAt = &t
	}
	return nil
}

func (s *Store) ListNotificationChannels(ctx context.Context) ([]*NotificationChannel, error) {
	out := []*NotificationChannel{}
	err := s.db.NewSelect().Model(&out).Order("id").Scan(ctx)
	return out, err
}

func (s *Store) GetNotificationChannel(ctx context.Context, id int64) (*NotificationChannel, error) {
	c := &NotificationChannel{ID: id}
	if err := s.db.NewSelect().Model(c).WherePK().Scan(ctx); err != nil {
		return nil, notFound(err)
	}
	return c, nil
}

func (s *Store) CreateNotificationChannel(ctx context.Context, c *NotificationChannel) error {
	c.CreatedAt = now()
	_, err := s.db.NewInsert().Model(c).Exec(ctx)
	return err
}

func (s *Store) UpdateNotificationChannel(ctx context.Context, c *NotificationChannel) error {
	return mustAffect(s.db.NewUpdate().Model(c).WherePK().
		Column("name", "config_enc", "events", "enabled").Exec(ctx))
}

func (s *Store) DeleteNotificationChannel(ctx context.Context, id int64) error {
	return mustAffect(s.db.NewDelete().Model((*NotificationChannel)(nil)).Where("id = ?", id).Exec(ctx))
}

// RecordNotificationResult guarda el resultado del último envío, para
// mostrarlo en el panel.
func (s *Store) RecordNotificationResult(ctx context.Context, id int64, errMsg string) error {
	q := s.db.NewUpdate().Model((*NotificationChannel)(nil)).Set("last_error = ?", errMsg).Where("id = ?", id)
	if errMsg == "" {
		q = q.Set("last_sent_at = ?", now())
	}
	_, err := q.Exec(ctx)
	return err
}

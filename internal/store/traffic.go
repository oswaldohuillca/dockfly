package store

import (
	"context"

	"github.com/uptrace/bun"
)

// TrafficMinute es el tráfico de un dueño durante un minuto.
type TrafficMinute struct {
	bun.BaseModel `bun:"table:traffic_minutes"`

	Owner       string    `bun:",pk"`
	Minute      int64     `bun:",pk"`
	Requests    int64     `bun:"requests"`
	S4xx        int64     `bun:"s4xx"`
	S5xx        int64     `bun:"s5xx"`
	DurationSum float64   `bun:"duration_sum"`
	Buckets     []float64 `bun:"buckets"`
}

// AddTraffic suma minutos de tráfico (si el minuto ya existe, se acumula).
func (s *Store) AddTraffic(ctx context.Context, rows []*TrafficMinute) error {
	if len(rows) == 0 {
		return nil
	}
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for _, r := range rows {
			old := &TrafficMinute{Owner: r.Owner, Minute: r.Minute}
			if err := tx.NewSelect().Model(old).WherePK().Scan(ctx); err == nil {
				r.Requests += old.Requests
				r.S4xx += old.S4xx
				r.S5xx += old.S5xx
				r.DurationSum += old.DurationSum
				for i := range r.Buckets {
					if i < len(old.Buckets) {
						r.Buckets[i] += old.Buckets[i]
					}
				}
			}
			if _, err := tx.NewInsert().Model(r).On("CONFLICT (owner, minute) DO UPDATE").
				Set("requests = EXCLUDED.requests, s4xx = EXCLUDED.s4xx, s5xx = EXCLUDED.s5xx").
				Set("duration_sum = EXCLUDED.duration_sum, buckets = EXCLUDED.buckets").Exec(ctx); err != nil {
				return err
			}
		}
		return nil
	})
}

// Traffic devuelve los minutos de tráfico desde from (inclusive). Con owner
// vacío, los de todos los dueños.
func (s *Store) Traffic(ctx context.Context, owner string, from int64) ([]*TrafficMinute, error) {
	rows := []*TrafficMinute{}
	q := s.db.NewSelect().Model(&rows).Where("minute >= ?", from).Order("minute")
	if owner != "" {
		q = q.Where("owner = ?", owner)
	}
	return rows, q.Scan(ctx)
}

// PruneTraffic borra el tráfico anterior a before.
func (s *Store) PruneTraffic(ctx context.Context, before int64) error {
	_, err := s.db.NewDelete().Model((*TrafficMinute)(nil)).Where("minute < ?", before).Exec(ctx)
	return err
}

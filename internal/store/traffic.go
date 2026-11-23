package store

import (
	"context"

	"github.com/uptrace/bun"
)

// TrafficMinute is one owner's traffic over one minute.
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

// AddTraffic adds traffic minutes (if the minute already exists, it accumulates).
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
			if _, err := s.upsert(tx.NewInsert().Model(r), "owner, minute",
				"requests", "s4xx", "s5xx", "duration_sum", "buckets").Exec(ctx); err != nil {
				return err
			}
		}
		return nil
	})
}

// Traffic returns the traffic minutes from from on (inclusive). With an empty
// owner, those of every owner.
func (s *Store) Traffic(ctx context.Context, owner string, from int64) ([]*TrafficMinute, error) {
	rows := []*TrafficMinute{}
	q := s.db.NewSelect().Model(&rows).Where("minute >= ?", from).Order("minute")
	if owner != "" {
		q = q.Where("owner = ?", owner)
	}
	return rows, q.Scan(ctx)
}

// PruneTraffic deletes the traffic older than before.
func (s *Store) PruneTraffic(ctx context.Context, before int64) error {
	_, err := s.db.NewDelete().Model((*TrafficMinute)(nil)).Where("minute < ?", before).Exec(ctx)
	return err
}

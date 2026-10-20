package traffic

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"time"

	"github.com/dockfly/dockfly/internal/store"
)

// Retention es cuánto tiempo se guarda el tráfico.
const Retention = 7 * 24 * time.Hour

// Collector lee las métricas de Traefik cada minuto y guarda lo que ha
// cambiado desde la lectura anterior.
type Collector struct {
	URL    string // http://127.0.0.1:8082/metrics
	Bounds []float64
	Store  *store.Store
	Log    *slog.Logger

	prev map[string]*Counters
	http *http.Client
}

func (c *Collector) Run(ctx context.Context) {
	c.http = &http.Client{Timeout: 10 * time.Second}
	// La referencia se toma en cuanto Traefik responde: lo que pase desde
	// entonces cuenta.
	for c.prev == nil {
		if err := c.collect(ctx, time.Now()); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	prune := time.NewTicker(time.Hour)
	defer prune.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-prune.C:
			_ = c.Store.PruneTraffic(ctx, time.Now().Add(-Retention).Unix()/60)
		case <-t.C:
			if err := c.collect(ctx, time.Now()); err != nil && ctx.Err() == nil {
				c.Log.Debug("leyendo las métricas de Traefik", "error", err)
			}
		}
	}
}

func (c *Collector) collect(ctx context.Context, now time.Time) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Traefik respondió %s", resp.Status)
	}
	cur, err := Parse(resp.Body, c.Bounds)
	if err != nil {
		return err
	}
	// La primera lectura solo sirve de referencia: lo anterior no se sabe
	// cuándo pasó.
	if c.prev == nil {
		c.prev = cur
		return nil
	}
	minute := now.Unix()/60 - 1 // lo leído corresponde al minuto que acaba de terminar
	var rows []*store.TrafficMinute
	for owner, counters := range cur {
		d := Delta(c.prev[owner], counters)
		if d.Requests < 0.5 {
			continue
		}
		rows = append(rows, &store.TrafficMinute{
			Owner: owner, Minute: minute,
			Requests: int64(math.Round(d.Requests)), S4xx: int64(math.Round(d.S4xx)), S5xx: int64(math.Round(d.S5xx)),
			DurationSum: d.Sum, Buckets: d.Buckets,
		})
	}
	c.prev = cur
	return c.Store.AddTraffic(ctx, rows)
}

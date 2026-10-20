package traffic

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dockfly/dockfly/internal/store"
)

func TestCollector(t *testing.T) {
	var total atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := total.Load()
		fmt.Fprintf(w, "traefik_router_requests_total{code=\"200\",router=\"app-4-secure@file\"} %d\n", n)
		fmt.Fprintf(w, "traefik_router_request_duration_seconds_bucket{code=\"200\",router=\"app-4-secure@file\",le=\"0.1\"} %d\n", n)
		fmt.Fprintf(w, "traefik_router_request_duration_seconds_bucket{code=\"200\",router=\"app-4-secure@file\",le=\"+Inf\"} %d\n", n)
	}))
	defer srv.Close()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	c := &Collector{URL: srv.URL, Bounds: []float64{0.1}, Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), http: srv.Client()}

	now := time.Date(2026, 10, 20, 12, 0, 30, 0, time.UTC)
	total.Store(1000) // lo acumulado antes de que Dockfly arrancara no cuenta
	if err := c.collect(ctx, now); err != nil {
		t.Fatal(err)
	}
	total.Store(1042)
	if err := c.collect(ctx, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	// Dos lecturas en el mismo minuto (p. ej. tras reiniciar) se suman.
	total.Store(1050)
	if err := c.collect(ctx, now.Add(time.Minute+10*time.Second)); err != nil {
		t.Fatal(err)
	}
	rows, err := st.Traffic(ctx, "app-4", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Requests != 50 || rows[0].Minute != now.Unix()/60 || rows[0].Buckets[0] != 50 {
		t.Fatalf("filas: %+v", rows)
	}
	if err := st.PruneTraffic(ctx, now.Unix()/60+1); err != nil {
		t.Fatal(err)
	}
	if rows, _ := st.Traffic(ctx, "", 0); len(rows) != 0 {
		t.Errorf("tras podar: %+v", rows)
	}
}

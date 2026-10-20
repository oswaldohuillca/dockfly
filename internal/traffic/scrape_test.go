package traffic

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/dockfly/dockfly/internal/store"
)

const sample = `# HELP traefik_router_requests_total How many HTTP requests are processed on a router.
# TYPE traefik_router_requests_total counter
traefik_router_requests_total{code="200",method="GET",protocol="http",router="ph-1-secure@file",service="ph-1@file"} 90
traefik_router_requests_total{code="404",method="GET",protocol="http",router="ph-1-secure@file",service="ph-1@file"} 6
traefik_router_requests_total{code="502",method="POST",protocol="http",router="ph-1@file",service="ph-1@file"} 4
traefik_router_requests_total{code="200",method="GET",protocol="http",router="site-2@file",service="site-2@file"} 10
traefik_router_requests_total{code="200",method="GET",protocol="http",router="api@internal",service="api@internal"} 99
traefik_router_request_duration_seconds_bucket{code="200",method="GET",protocol="http",router="ph-1-secure@file",service="ph-1@file",le="0.1"} 50
traefik_router_request_duration_seconds_bucket{code="200",method="GET",protocol="http",router="ph-1-secure@file",service="ph-1@file",le="0.5"} 90
traefik_router_request_duration_seconds_bucket{code="200",method="GET",protocol="http",router="ph-1-secure@file",service="ph-1@file",le="+Inf"} 90
traefik_router_request_duration_seconds_bucket{code="502",method="POST",protocol="http",router="ph-1@file",service="ph-1@file",le="0.1"} 0
traefik_router_request_duration_seconds_bucket{code="502",method="POST",protocol="http",router="ph-1@file",service="ph-1@file",le="0.5"} 0
traefik_router_request_duration_seconds_bucket{code="502",method="POST",protocol="http",router="ph-1@file",service="ph-1@file",le="+Inf"} 10
traefik_router_request_duration_seconds_sum{code="200",method="GET",protocol="http",router="ph-1-secure@file",service="ph-1@file"} 12.5
traefik_router_request_duration_seconds_count{code="200",method="GET",protocol="http",router="ph-1-secure@file",service="ph-1@file"} 90
`

func TestParse(t *testing.T) {
	bounds := []float64{0.1, 0.5}
	got, err := Parse(strings.NewReader(sample), bounds)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("dueños: %v", got)
	}
	ph := got["ph-1"]
	if ph.Requests != 100 || ph.S4xx != 6 || ph.S5xx != 4 || ph.Sum != 12.5 {
		t.Errorf("ph-1 (HTTP y HTTPS juntos): %+v", ph)
	}
	if want := []float64{50, 90, 100}; !equal(ph.Buckets, want) {
		t.Errorf("buckets: %v, quería %v", ph.Buckets, want)
	}
	if got["site-2"].Requests != 10 {
		t.Errorf("site-2: %+v", got["site-2"])
	}
}

func TestDeltaAndQuantile(t *testing.T) {
	prev := &Counters{Requests: 100, S5xx: 4, Buckets: []float64{50, 90, 100}, Sum: 10}
	cur := &Counters{Requests: 160, S5xx: 5, Buckets: []float64{100, 150, 160}, Sum: 16}
	d := Delta(prev, cur)
	if d.Requests != 60 || d.S5xx != 1 || !equal(d.Buckets, []float64{50, 60, 60}) || d.Sum != 6 {
		t.Errorf("delta: %+v", d)
	}
	// Traefik reiniciado: los contadores vuelven a empezar.
	if d := Delta(cur, &Counters{Requests: 3, Buckets: []float64{3, 3, 3}}); d.Requests != 3 {
		t.Errorf("tras reiniciar: %+v", d)
	}
	bounds := []float64{0.1, 0.5}
	// 50 de 60 por debajo de 0.1 s: la mediana cae dentro del primer bucket.
	if q := Quantile(0.5, d.Buckets, bounds); math.Abs(q-0.06) > 1e-9 {
		t.Errorf("p50 = %v", q)
	}
	if q := Quantile(0.95, d.Buckets, bounds); q <= 0.1 || q > 0.5 {
		t.Errorf("p95 = %v", q)
	}
	if q := Quantile(0.95, []float64{0, 0, 0}, bounds); q != 0 {
		t.Errorf("sin peticiones: %v", q)
	}
}

func equal(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSeries(t *testing.T) {
	from := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	m := from.Unix() / 60
	rows := []*store.TrafficMinute{
		{Owner: "ph-1", Minute: m, Requests: 10, S5xx: 1, DurationSum: 1, Buckets: []float64{10, 10, 10}},
		{Owner: "ph-1", Minute: m + 30, Requests: 20, S4xx: 2, DurationSum: 4, Buckets: []float64{0, 20, 20}},
		{Owner: "ph-1", Minute: m + 61, Requests: 5, DurationSum: 0.5, Buckets: []float64{5, 5, 5}},
		{Owner: "site-2", Minute: m + 5, Requests: 7, Buckets: []float64{7, 7, 7}},
		{Owner: "ph-1", Minute: m + 500, Requests: 99}, // fuera del rango
	}
	var phRows []*store.TrafficMinute
	for _, r := range rows {
		if r.Owner == "ph-1" {
			phRows = append(phRows, r)
		}
	}
	points, totals := Series(phRows, from, time.Hour, 2, []float64{0.1, 0.5})
	if len(points) != 2 || points[0].Requests != 30 || points[0].S4xx != 2 || points[0].S5xx != 1 || points[1].Requests != 5 {
		t.Errorf("puntos: %+v", points)
	}
	if totals.Requests != 35 || math.Abs(totals.AvgMs-5.5/35*1000) > 1e-6 {
		t.Errorf("totales: %+v", totals)
	}
	if points[0].P50 <= 0 || points[0].P95 < points[0].P50 {
		t.Errorf("latencia: %+v", points[0])
	}
	sums := Summaries(rows, from, 2, []float64{0.1, 0.5})
	if len(sums) != 2 || sums["site-2"].Requests != 7 || len(sums["ph-1"].Spark) != 2 || sums["ph-1"].Spark[1] != 5 {
		t.Errorf("resúmenes: %+v", sums)
	}
}

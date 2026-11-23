package traffic

import (
	"time"

	"github.com/dockfly/dockfly/internal/store"
)

// Point is the traffic of one interval of a chart.
type Point struct {
	T        time.Time `json:"t"`
	Requests int64     `json:"requests"`
	S4xx     int64     `json:"s4xx"`
	S5xx     int64     `json:"s5xx"`
	// Latency in milliseconds (0 when there are no requests).
	P50 float64 `json:"p50_ms"`
	P95 float64 `json:"p95_ms"`
}

// Totals summarizes a period.
type Totals struct {
	Requests int64   `json:"requests"`
	S4xx     int64   `json:"s4xx"`
	S5xx     int64   `json:"s5xx"`
	P50      float64 `json:"p50_ms"`
	P95      float64 `json:"p95_ms"`
	AvgMs    float64 `json:"avg_ms"`
}

type acc struct {
	requests, s4xx, s5xx int64
	sum                  float64
	buckets              []float64
}

func (a *acc) add(r *store.TrafficMinute) {
	a.requests += r.Requests
	a.s4xx += r.S4xx
	a.s5xx += r.S5xx
	a.sum += r.DurationSum
	if len(a.buckets) < len(r.Buckets) {
		a.buckets = append(a.buckets, make([]float64, len(r.Buckets)-len(a.buckets))...)
	}
	for i, v := range r.Buckets {
		a.buckets[i] += v
	}
}

func (a *acc) totals(bounds []float64) Totals {
	t := Totals{Requests: a.requests, S4xx: a.s4xx, S5xx: a.s5xx}
	if a.requests > 0 {
		t.P50 = Quantile(0.5, a.buckets, bounds) * 1000
		t.P95 = Quantile(0.95, a.buckets, bounds) * 1000
		t.AvgMs = a.sum / float64(a.requests) * 1000
	}
	return t
}

// Series spreads the minutes over n equal intervals starting at from and
// also returns the total for the period.
func Series(rows []*store.TrafficMinute, from time.Time, step time.Duration, n int, bounds []float64) ([]Point, Totals) {
	accs := make([]acc, n)
	var all acc
	start := from.Unix() / 60
	stepMin := int64(step / time.Minute)
	for _, r := range rows {
		i := (r.Minute - start) / stepMin
		if i < 0 || i >= int64(n) {
			continue
		}
		accs[i].add(r)
		all.add(r)
	}
	points := make([]Point, n)
	for i := range accs {
		t := accs[i].totals(bounds)
		points[i] = Point{
			T: from.Add(time.Duration(i) * step).UTC(), Requests: t.Requests, S4xx: t.S4xx, S5xx: t.S5xx, P50: t.P50, P95: t.P95,
		}
	}
	return points, all.totals(bounds)
}

// OwnerSummary is an owner's summary for the lists.
type OwnerSummary struct {
	Totals
	// Spark holds the requests of each hour (from the oldest to the current).
	Spark []int64 `json:"spark"`
}

// Summaries summarizes each owner's traffic over hours hours starting at
// from.
func Summaries(rows []*store.TrafficMinute, from time.Time, hours int, bounds []float64) map[string]OwnerSummary {
	byOwner := map[string][]*store.TrafficMinute{}
	for _, r := range rows {
		byOwner[r.Owner] = append(byOwner[r.Owner], r)
	}
	out := map[string]OwnerSummary{}
	for owner, list := range byOwner {
		points, totals := Series(list, from, time.Hour, hours, bounds)
		spark := make([]int64, len(points))
		for i, p := range points {
			spark[i] = p.Requests
		}
		out[owner] = OwnerSummary{Totals: totals, Spark: spark}
	}
	return out
}

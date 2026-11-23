// Package traffic collects the traffic of each proxy host, site and app from
// Traefik's metrics and stores it minute by minute.
package traffic

import (
	"bufio"
	"io"
	"math"
	"strconv"
	"strings"
)

// Counters are an owner's cumulative counters (proxy host, site…) since
// Traefik started.
type Counters struct {
	Requests float64
	S4xx     float64
	S5xx     float64
	// Buckets[i] counts the requests that took <= LatencyBuckets[i]
	// (cumulative, as in Prometheus); the last one is +Inf.
	Buckets []float64
	Sum     float64 // seconds
}

func (c *Counters) ensure(n int) {
	if len(c.Buckets) < n {
		c.Buckets = append(c.Buckets, make([]float64, n-len(c.Buckets))...)
	}
}

// Owner maps a Traefik router ("ph-3-secure@file") to the owner that
// generated it ("ph-3"). Traefik's internal routers have no owner.
func Owner(router string) string {
	name, provider, _ := strings.Cut(router, "@")
	if provider != "file" {
		return ""
	}
	name = strings.TrimSuffix(name, "-secure")
	// Protected previews have one router per host (preview-1-<host>): they
	// count together with the other previews.
	if strings.HasPrefix(name, "preview-") {
		return "previews"
	}
	// Custom locations (ph-3-loc0, site-2-loc1) count as their proxy host or
	// their site.
	if i := strings.LastIndex(name, "-loc"); i > 0 {
		name = name[:i]
	}
	return name
}

// Parse reads Traefik's /metrics output and adds up each owner's counters.
// bounds are the bounds of the latency histogram.
func Parse(r io.Reader, bounds []float64) (map[string]*Counters, error) {
	out := map[string]*Counters{}
	get := func(owner string) *Counters {
		c := out[owner]
		if c == nil {
			c = &Counters{}
			c.ensure(len(bounds) + 1)
			out[owner] = c
		}
		return c
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "traefik_router_") {
			continue
		}
		name, labels, value, ok := parseLine(line)
		if !ok {
			continue
		}
		owner := Owner(labels["router"])
		if owner == "" {
			continue
		}
		switch name {
		case "traefik_router_requests_total":
			c := get(owner)
			c.Requests += value
			switch labels["code"][:min(1, len(labels["code"]))] {
			case "4":
				c.S4xx += value
			case "5":
				c.S5xx += value
			}
		case "traefik_router_request_duration_seconds_bucket":
			i := bucketIndex(labels["le"], bounds)
			if i >= 0 {
				get(owner).Buckets[i] += value
			}
		case "traefik_router_request_duration_seconds_sum":
			get(owner).Sum += value
		}
	}
	return out, sc.Err()
}

func bucketIndex(le string, bounds []float64) int {
	if le == "+Inf" {
		return len(bounds)
	}
	v, err := strconv.ParseFloat(le, 64)
	if err != nil {
		return -1
	}
	for i, b := range bounds {
		if math.Abs(b-v) < 1e-9 {
			return i
		}
	}
	return -1
}

// parseLine reads one sample: name{label="value",…} value
func parseLine(line string) (name string, labels map[string]string, value float64, ok bool) {
	open := strings.IndexByte(line, '{')
	closeIdx := strings.LastIndexByte(line, '}')
	if open < 0 || closeIdx < open {
		return "", nil, 0, false
	}
	name = line[:open]
	labels = map[string]string{}
	rest := line[open+1 : closeIdx]
	for rest != "" {
		eq := strings.IndexByte(rest, '=')
		if eq < 0 || eq+1 >= len(rest) || rest[eq+1] != '"' {
			return "", nil, 0, false
		}
		key := strings.TrimSpace(rest[:eq])
		// Quoted value, with \" and \\ escaped.
		var b strings.Builder
		i := eq + 2
		for ; i < len(rest); i++ {
			if rest[i] == '\\' && i+1 < len(rest) {
				i++
				b.WriteByte(rest[i])
				continue
			}
			if rest[i] == '"' {
				break
			}
			b.WriteByte(rest[i])
		}
		labels[key] = b.String()
		rest = strings.TrimLeft(rest[min(i+1, len(rest)):], ",")
	}
	fields := strings.Fields(line[closeIdx+1:])
	if len(fields) == 0 {
		return "", nil, 0, false
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return "", nil, 0, false
	}
	return name, labels, v, true
}

// Delta is what happened between two readings. If Traefik restarted (the
// counters go down), the new reading counts in full.
func Delta(prev, cur *Counters) Counters {
	if prev == nil || cur.Requests < prev.Requests {
		return *cur
	}
	d := Counters{
		Requests: cur.Requests - prev.Requests,
		S4xx:     max(cur.S4xx-prev.S4xx, 0),
		S5xx:     max(cur.S5xx-prev.S5xx, 0),
		Sum:      max(cur.Sum-prev.Sum, 0),
		Buckets:  make([]float64, len(cur.Buckets)),
	}
	for i := range cur.Buckets {
		if i < len(prev.Buckets) {
			d.Buckets[i] = max(cur.Buckets[i]-prev.Buckets[i], 0)
		} else {
			d.Buckets[i] = cur.Buckets[i]
		}
	}
	return d
}

// Quantile estimates quantile q (0.5, 0.95…) of a cumulative histogram,
// interpolating inside the bucket the way Prometheus does. It returns
// seconds.
func Quantile(q float64, buckets, bounds []float64) float64 {
	if len(buckets) == 0 {
		return 0
	}
	total := buckets[len(buckets)-1]
	if total <= 0 {
		return 0
	}
	rank := q * total
	prevCount, prevBound := 0.0, 0.0
	for i, count := range buckets {
		if count >= rank {
			if i >= len(bounds) {
				// In +Inf: the most that can be said is the last bound.
				return bounds[len(bounds)-1]
			}
			inBucket := count - prevCount
			if inBucket <= 0 {
				return bounds[i]
			}
			return prevBound + (bounds[i]-prevBound)*(rank-prevCount)/inBucket
		}
		prevCount = count
		if i < len(bounds) {
			prevBound = bounds[i]
		}
	}
	return bounds[len(bounds)-1]
}

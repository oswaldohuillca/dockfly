// Package traffic recoge el tráfico de cada proxy host, sitio y app a partir
// de las métricas de Traefik y lo guarda minuto a minuto.
package traffic

import (
	"bufio"
	"io"
	"math"
	"strconv"
	"strings"
)

// Counters son los contadores acumulados de un dueño (proxy host, sitio…)
// desde que arrancó Traefik.
type Counters struct {
	Requests float64
	S4xx     float64
	S5xx     float64
	// Buckets[i] cuenta las peticiones que tardaron <= LatencyBuckets[i]
	// (acumulado, como en Prometheus); el último es +Inf.
	Buckets []float64
	Sum     float64 // segundos
}

func (c *Counters) ensure(n int) {
	if len(c.Buckets) < n {
		c.Buckets = append(c.Buckets, make([]float64, n-len(c.Buckets))...)
	}
}

// Owner traduce un router de Traefik ("ph-3-secure@file") al dueño que lo
// generó ("ph-3"). Los routers internos de Traefik no tienen dueño.
func Owner(router string) string {
	name, provider, _ := strings.Cut(router, "@")
	if provider != "file" {
		return ""
	}
	name = strings.TrimSuffix(name, "-secure")
	// Las ubicaciones personalizadas (ph-3-loc0) cuentan como su proxy host.
	if i := strings.LastIndex(name, "-loc"); i > 0 && strings.HasPrefix(name, "ph-") {
		name = name[:i]
	}
	return name
}

// Parse lee la salida de /metrics de Traefik y suma los contadores de cada
// dueño. bounds son los límites del histograma de latencia.
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

// parseLine lee una muestra: nombre{etiqueta="valor",…} valor
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
		// Valor entre comillas, con \" y \\ escapados.
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

// Delta es lo que ha pasado entre dos lecturas. Si Traefik se reinició (los
// contadores bajan), la lectura nueva cuenta entera.
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

// Quantile estima el cuantil q (0.5, 0.95…) de un histograma acumulado,
// interpolando dentro del bucket como hace Prometheus. Devuelve segundos.
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
				// En +Inf: lo más que se puede decir es el último límite.
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

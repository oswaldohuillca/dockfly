// Package sysinfo lee el uso de CPU, memoria y disco de la máquina. Dentro
// de un contenedor, /proc/stat y /proc/meminfo siguen siendo los del host,
// que es lo que interesa: los recursos del servidor, no los del contenedor.
package sysinfo

import (
	"errors"
	"strconv"
	"strings"
)

// ErrUnsupported: solo hay lectura de recursos en Linux.
var ErrUnsupported = errors.New("recursos del sistema no disponibles en esta plataforma")

// Stats es una foto de los recursos de un servidor. Los tamaños van en bytes.
type Stats struct {
	CPUPercent    float64 `json:"cpu_percent"`
	Cores         int     `json:"cores"`
	Load1         float64 `json:"load1"`
	Load5         float64 `json:"load5"`
	Load15        float64 `json:"load15"`
	MemTotal      uint64  `json:"mem_total"`
	MemUsed       uint64  `json:"mem_used"`
	SwapTotal     uint64  `json:"swap_total"`
	SwapUsed      uint64  `json:"swap_used"`
	DiskTotal     uint64  `json:"disk_total"`
	DiskUsed      uint64  `json:"disk_used"`
	UptimeSeconds int64   `json:"uptime_seconds"`
}

// cpuTimes saca de la primera línea de /proc/stat el tiempo total y el ocioso
// (idle + iowait), en ticks.
func cpuTimes(stat string) (total, idle uint64, ok bool) {
	line, _, _ := strings.Cut(stat, "\n")
	f := strings.Fields(line)
	if len(f) < 5 || f[0] != "cpu" {
		return 0, 0, false
	}
	for i, s := range f[1:] {
		v, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return 0, 0, false
		}
		// guest y guest_nice (campos 9 y 10) ya van dentro de user y nice.
		if i < 8 {
			total += v
		}
		if i == 3 || i == 4 {
			idle += v
		}
	}
	return total, idle, true
}

// cpuPercent es el uso de CPU entre dos lecturas de /proc/stat.
func cpuPercent(before, after string) float64 {
	t0, i0, ok0 := cpuTimes(before)
	t1, i1, ok1 := cpuTimes(after)
	if !ok0 || !ok1 || t1 <= t0 || i1 < i0 {
		return 0
	}
	return 100 * (1 - float64(i1-i0)/float64(t1-t0))
}

// meminfo devuelve, en bytes, los campos de /proc/meminfo (que van en kB).
func meminfo(data string) map[string]uint64 {
	out := map[string]uint64{}
	for line := range strings.SplitSeq(data, "\n") {
		key, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			continue
		}
		if v, err := strconv.ParseUint(f[0], 10, 64); err == nil {
			out[key] = v * 1024
		}
	}
	return out
}

func sub(a, b uint64) uint64 {
	if b > a {
		return 0
	}
	return a - b
}

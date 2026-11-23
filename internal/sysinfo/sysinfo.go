// Package sysinfo reads the machine's CPU, memory and disk usage. Inside a
// container, /proc/stat and /proc/meminfo are still the host's, which is what
// matters here: the server's resources, not the container's.
package sysinfo

import (
	"errors"
	"strconv"
	"strings"
)

// ErrUnsupported: resource readings are only available on Linux.
var ErrUnsupported = errors.New("system resources are not available on this platform")

// Stats is a snapshot of a server's resources. Sizes are in bytes.
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

// cpuTimes takes the total and the idle time (idle + iowait) from the first
// line of /proc/stat, in ticks.
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
		// guest and guest_nice (fields 9 and 10) are already inside user and nice.
		if i < 8 {
			total += v
		}
		if i == 3 || i == 4 {
			idle += v
		}
	}
	return total, idle, true
}

// cpuPercent is the CPU usage between two readings of /proc/stat.
func cpuPercent(before, after string) float64 {
	t0, i0, ok0 := cpuTimes(before)
	t1, i1, ok1 := cpuTimes(after)
	if !ok0 || !ok1 || t1 <= t0 || i1 < i0 {
		return 0
	}
	return 100 * (1 - float64(i1-i0)/float64(t1-t0))
}

// meminfo returns the fields of /proc/meminfo (which are in kB) in bytes.
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

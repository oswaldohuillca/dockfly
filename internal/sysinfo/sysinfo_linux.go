package sysinfo

import (
	"context"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Read mide los recursos de la máquina; el disco es el del sistema de
// archivos de diskPath. Tarda ~200 ms: el uso de CPU sale de dos lecturas.
func Read(ctx context.Context, diskPath string) (Stats, error) {
	before, err := os.ReadFile("/proc/stat")
	if err != nil {
		return Stats{}, err
	}
	select {
	case <-ctx.Done():
		return Stats{}, ctx.Err()
	case <-time.After(200 * time.Millisecond):
	}
	after, err := os.ReadFile("/proc/stat")
	if err != nil {
		return Stats{}, err
	}
	st := Stats{CPUPercent: cpuPercent(string(before), string(after)), Cores: runtime.NumCPU()}

	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		m := meminfo(string(data))
		st.MemTotal, st.MemUsed = m["MemTotal"], sub(m["MemTotal"], m["MemAvailable"])
		st.SwapTotal, st.SwapUsed = m["SwapTotal"], sub(m["SwapTotal"], m["SwapFree"])
	}
	if data, err := os.ReadFile("/proc/loadavg"); err == nil {
		if f := strings.Fields(string(data)); len(f) >= 3 {
			st.Load1, _ = strconv.ParseFloat(f[0], 64)
			st.Load5, _ = strconv.ParseFloat(f[1], 64)
			st.Load15, _ = strconv.ParseFloat(f[2], 64)
		}
	}
	if data, err := os.ReadFile("/proc/uptime"); err == nil {
		if f := strings.Fields(string(data)); len(f) >= 1 {
			up, _ := strconv.ParseFloat(f[0], 64)
			st.UptimeSeconds = int64(up)
		}
	}
	var fs syscall.Statfs_t
	if err := syscall.Statfs(diskPath, &fs); err == nil {
		// Como df: lo que no está disponible para Dockfly cuenta como usado.
		st.DiskTotal = fs.Blocks * uint64(fs.Bsize)
		st.DiskUsed = sub(st.DiskTotal, fs.Bavail*uint64(fs.Bsize))
	}
	return st, nil
}

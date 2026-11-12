package sysinfo

import "testing"

func TestCPUPercent(t *testing.T) {
	// 1000 ticks entre lecturas, 750 ociosos (700 idle + 50 iowait): 25 % de uso.
	before := "cpu  100 0 100 800 0 0 0 0 0 0\ncpu0 1 1 1 1\n"
	after := "cpu  300 0 150 1500 50 0 0 0 0 0\n"
	if got := cpuPercent(before, after); got != 25 {
		t.Fatalf("cpuPercent = %v, se esperaba 25", got)
	}
	if got := cpuPercent("basura", after); got != 0 {
		t.Fatalf("con una lectura inválida = %v, se esperaba 0", got)
	}
}

func TestMeminfo(t *testing.T) {
	m := meminfo("MemTotal:       16000 kB\nMemAvailable:    4000 kB\nHugePages_Total:       0\n")
	if m["MemTotal"] != 16000*1024 || m["MemAvailable"] != 4000*1024 {
		t.Fatalf("meminfo = %v", m)
	}
}

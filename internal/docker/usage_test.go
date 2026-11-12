package docker

import (
	"encoding/json"
	"testing"
)

func TestContainerUsage(t *testing.T) {
	// Recorte de una respuesta real de /containers/{id}/stats (cgroup v2).
	const body = `{"cpu_stats":{"cpu_usage":{"total_usage":600},"system_cpu_usage":3000,"online_cpus":4},
		"precpu_stats":{"cpu_usage":{"total_usage":100},"system_cpu_usage":1000},
		"memory_stats":{"usage":500,"limit":2000,"stats":{"inactive_file":100}}}`
	var s containerStats
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatal(err)
	}
	if got, want := s.usage(), (ContainerUsage{CPUPercent: 25, MemUsed: 400, MemLimit: 2000}); got != want {
		t.Errorf("esperaba %+v, obtuve %+v", want, got)
	}
	// Primera muestra de un contenedor recién arrancado: sin precpu no hay porcentaje.
	var first containerStats
	_ = json.Unmarshal([]byte(`{"cpu_stats":{"cpu_usage":{"total_usage":5},"system_cpu_usage":0},"memory_stats":{}}`), &first)
	if got := first.usage(); got.CPUPercent != 0 {
		t.Errorf("sin muestra previa la CPU debe ser 0, es %v", got.CPUPercent)
	}
}

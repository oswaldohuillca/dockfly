package api

import (
	"testing"

	"github.com/dockfly/dockfly/internal/notify"
	"github.com/dockfly/dockfly/internal/sysinfo"
)

func TestResourceWatch(t *testing.T) {
	w := resourceWatch{}
	disk := func(used uint64) sysinfo.Stats { return sysinfo.Stats{DiskUsed: used, DiskTotal: 100} }
	kinds := func(evs []notify.Event) (out []string) {
		for _, e := range evs {
			out = append(out, e.Kind)
		}
		return
	}
	steps := []struct {
		st   sysinfo.Stats
		want string
	}{
		{disk(50), ""},
		{disk(92), notify.EventResourceHigh}, // el disco avisa a la primera
		{disk(95), ""},                       // y no repite
		{disk(88), ""},                       // dentro del margen: sigue en aviso
		{disk(80), notify.EventResourceNormal},
		{disk(91), notify.EventResourceHigh},
	}
	for i, s := range steps {
		got := kinds(w.observe("srv", s.st))
		if (s.want == "" && len(got) != 0) || (s.want != "" && (len(got) != 1 || got[0] != s.want)) {
			t.Errorf("paso %d: esperaba %q, obtuve %v", i, s.want, got)
		}
	}

	// La memoria necesita 5 lecturas seguidas: un pico suelto no avisa.
	w = resourceWatch{}
	mem := sysinfo.Stats{MemUsed: 95, MemTotal: 100}
	for i := 0; i < 4; i++ {
		if evs := w.observe("srv", mem); len(evs) != 0 {
			t.Fatalf("lectura %d: aviso prematuro %v", i, kinds(evs))
		}
	}
	w.observe("srv", sysinfo.Stats{MemUsed: 10, MemTotal: 100}) // baja: se reinicia la cuenta
	for i := 0; i < 4; i++ {
		w.observe("srv", mem)
	}
	if evs := w.observe("srv", mem); len(evs) != 1 {
		t.Errorf("a la quinta lectura seguida debe avisar, obtuve %v", kinds(evs))
	}
}

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
		{disk(92), notify.EventResourceHigh}, // disk alerts on the first reading
		{disk(95), ""},                       // and does not repeat
		{disk(88), ""},                       // within the margin: still alerting
		{disk(80), notify.EventResourceNormal},
		{disk(91), notify.EventResourceHigh},
	}
	for i, s := range steps {
		got := kinds(w.observe("srv", s.st))
		if (s.want == "" && len(got) != 0) || (s.want != "" && (len(got) != 1 || got[0] != s.want)) {
			t.Errorf("step %d: expected %q, got %v", i, s.want, got)
		}
	}

	// Memory needs 5 readings in a row: a lone spike does not alert.
	w = resourceWatch{}
	mem := sysinfo.Stats{MemUsed: 95, MemTotal: 100}
	for i := 0; i < 4; i++ {
		if evs := w.observe("srv", mem); len(evs) != 0 {
			t.Fatalf("reading %d: premature alert %v", i, kinds(evs))
		}
	}
	w.observe("srv", sysinfo.Stats{MemUsed: 10, MemTotal: 100}) // drops: the count restarts
	for i := 0; i < 4; i++ {
		w.observe("srv", mem)
	}
	if evs := w.observe("srv", mem); len(evs) != 1 {
		t.Errorf("it must alert on the fifth reading in a row, got %v", kinds(evs))
	}
}

package apps

import (
	"testing"
	"time"
)

func TestSchedule(t *testing.T) {
	at := func(s string) time.Time {
		v, err := time.Parse("2006-01-02 15:04", s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	cases := []struct {
		expr, when string
		want       bool
	}{
		{"* * * * *", "2026-11-12 08:31", true},
		{"0 3 * * *", "2026-11-12 03:00", true},
		{"0 3 * * *", "2026-11-12 03:01", false},
		{"*/15 * * * *", "2026-11-12 10:45", true},
		{"*/15 * * * *", "2026-11-12 10:50", false},
		{"0 9-17 * * 1-5", "2026-11-12 12:00", true},  // jueves
		{"0 9-17 * * 1-5", "2026-11-14 12:00", false}, // sábado
		{"0 0 1,15 * *", "2026-11-15 00:00", true},
		{"0 0 * * 7", "2026-11-15 00:00", true},  // 7 = domingo
		{"0 0 1 * 1", "2026-11-16 00:00", true},  // día 1 O lunes, como en cron
		{"0 0 1 * 1", "2026-11-17 00:00", false}, // ni día 1 ni lunes
		{"5/20 * * * *", "2026-11-12 10:25", true},
	}
	for _, c := range cases {
		s, err := ParseSchedule(c.expr)
		if err != nil {
			t.Errorf("%q: %v", c.expr, err)
			continue
		}
		if got := s.Matches(at(c.when)); got != c.want {
			t.Errorf("%q en %s: esperaba %v", c.expr, c.when, c.want)
		}
	}
	for _, bad := range []string{"", "* * * *", "60 * * * *", "* 24 * * *", "*/0 * * * *", "a * * * *", "5-1 * * * *", "* * 0 * *", "@daily"} {
		if _, err := ParseSchedule(bad); err == nil {
			t.Errorf("%q debería ser inválida", bad)
		}
	}
}

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
		{"0 9-17 * * 1-5", "2026-11-12 12:00", true},  // Thursday
		{"0 9-17 * * 1-5", "2026-11-14 12:00", false}, // Saturday
		{"0 0 1,15 * *", "2026-11-15 00:00", true},
		{"0 0 * * 7", "2026-11-15 00:00", true},  // 7 = Sunday
		{"0 0 1 * 1", "2026-11-16 00:00", true},  // day 1 OR Monday, as in cron
		{"0 0 1 * 1", "2026-11-17 00:00", false}, // neither day 1 nor Monday
		{"5/20 * * * *", "2026-11-12 10:25", true},
	}
	for _, c := range cases {
		s, err := ParseSchedule(c.expr)
		if err != nil {
			t.Errorf("%q: %v", c.expr, err)
			continue
		}
		if got := s.Matches(at(c.when)); got != c.want {
			t.Errorf("%q at %s: wanted %v", c.expr, c.when, c.want)
		}
	}
	for _, bad := range []string{"", "* * * *", "60 * * * *", "* 24 * * *", "*/0 * * * *", "a * * * *", "5-1 * * * *", "* * 0 * *", "@daily"} {
		if _, err := ParseSchedule(bad); err == nil {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestNoShell(t *testing.T) {
	const missing = "OCI runtime exec failed: exec failed: unable to start container process: exec: \"sh\": executable file not found in $PATH\r\n"
	if !noShell(127, missing) {
		t.Error("an image with no sh must be recognised")
	}
	if noShell(127, "sh: 1: foo: not found\n") || noShell(0, missing) {
		t.Error("a missing command, or one that finished fine, is not an image without sh")
	}
}

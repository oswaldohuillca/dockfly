package apps

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule es una expresión cron de cinco campos (minuto, hora, día del mes,
// mes, día de la semana) ya interpretada. Cada campo es un mapa de bits.
type Schedule struct {
	minute, hour, dom, month, dow uint64
	// Como en cron: si se restringen a la vez el día del mes y el de la
	// semana, basta con que coincida uno.
	domAny, dowAny bool
}

var cronFields = []struct {
	name     string
	min, max int
}{{"minute", 0, 59}, {"hour", 0, 23}, {"day of month", 1, 31}, {"month", 1, 12}, {"day of week", 0, 7}}

// ParseSchedule devuelve errores en inglés: se muestran tal cual en el panel.
// Admite *, valores, rangos (1-5), listas (1,15) y pasos (*/10, 0-30/5).
func ParseSchedule(expr string) (Schedule, error) {
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return Schedule{}, fmt.Errorf("expected 5 fields (minute hour day month weekday), got %d", len(parts))
	}
	var bits [5]uint64
	for i, part := range parts {
		f := cronFields[i]
		for _, item := range strings.Split(part, ",") {
			rng, stepStr, hasStep := strings.Cut(item, "/")
			lo, hi, step := f.min, f.max, 1
			if rng != "*" {
				a, b, isRange := strings.Cut(rng, "-")
				var err error
				if lo, err = strconv.Atoi(a); err != nil {
					return Schedule{}, fmt.Errorf("%s: %q is not a number", f.name, a)
				}
				hi = lo
				if isRange {
					if hi, err = strconv.Atoi(b); err != nil {
						return Schedule{}, fmt.Errorf("%s: %q is not a number", f.name, b)
					}
				} else if hasStep {
					hi = f.max // "5/10" = desde 5, cada 10
				}
			}
			if hasStep {
				var err error
				if step, err = strconv.Atoi(stepStr); err != nil || step < 1 {
					return Schedule{}, fmt.Errorf("%s: the step %q must be a number greater than 0", f.name, stepStr)
				}
			}
			if lo < f.min || hi > f.max || lo > hi {
				return Schedule{}, fmt.Errorf("%s: %q is outside %d-%d", f.name, item, f.min, f.max)
			}
			for v := lo; v <= hi; v += step {
				bits[i] |= 1 << v
			}
		}
	}
	if bits[4]&(1<<7) != 0 { // 7 también es domingo
		bits[4] |= 1
	}
	return Schedule{minute: bits[0], hour: bits[1], dom: bits[2], month: bits[3], dow: bits[4],
		domAny: parts[2] == "*", dowAny: parts[4] == "*"}, nil
}

// Matches indica si la tarea toca en el minuto de t.
func (s Schedule) Matches(t time.Time) bool {
	has := func(bits uint64, v int) bool { return bits&(1<<v) != 0 }
	if !has(s.minute, t.Minute()) || !has(s.hour, t.Hour()) || !has(s.month, int(t.Month())) {
		return false
	}
	dom, dow := has(s.dom, t.Day()), has(s.dow, int(t.Weekday()))
	if !s.domAny && !s.dowAny {
		return dom || dow
	}
	return dom && dow
}

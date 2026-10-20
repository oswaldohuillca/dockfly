package api

import (
	"net/http"
	"regexp"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/dockfly/dockfly/internal/traefik"
	"github.com/dockfly/dockfly/internal/traffic"
)

// Rangos de las gráficas de tráfico: duración y tamaño de cada barra.
var trafficRanges = map[string]struct{ span, step time.Duration }{
	"1h":  {time.Hour, time.Minute},
	"24h": {24 * time.Hour, 30 * time.Minute},
	"7d":  {7 * 24 * time.Hour, 4 * time.Hour},
}

var ownerRe = regexp.MustCompile(`^(ph|site|app|rd)-[0-9]+$|^previews$`)

// handleTraffic devuelve la gráfica de tráfico de un proxy host, sitio o app.
func (s *Server) handleTraffic(w http.ResponseWriter, r *http.Request) {
	owner := chi.URLParam(r, "owner")
	if !ownerRe.MatchString(owner) {
		writeError(w, http.StatusBadRequest, "dueño inválido")
		return
	}
	name := r.URL.Query().Get("range")
	if name == "" {
		name = "24h"
	}
	rg, ok := trafficRanges[name]
	if !ok {
		writeError(w, http.StatusBadRequest, "rango inválido: usa 1h, 24h o 7d")
		return
	}
	n := int(rg.span / rg.step)
	// Las barras empiezan en un múltiplo del paso: así no bailan al refrescar.
	end := time.Now().UTC().Truncate(rg.step).Add(rg.step)
	from := end.Add(-time.Duration(n) * rg.step)
	rows, err := s.Store.Traffic(r.Context(), owner, from.Unix()/60)
	if err != nil {
		s.internalError(w, err)
		return
	}
	points, totals := traffic.Series(rows, from, rg.step, n, traefik.LatencyBuckets)
	writeJSON(w, http.StatusOK, map[string]any{"points": points, "totals": totals, "step_seconds": rg.step.Seconds()})
}

// handleTrafficSummary resume las últimas 24 horas de cada dueño, para las listas.
func (s *Server) handleTrafficSummary(w http.ResponseWriter, r *http.Request) {
	end := time.Now().UTC().Truncate(time.Hour).Add(time.Hour)
	from := end.Add(-24 * time.Hour)
	rows, err := s.Store.Traffic(r.Context(), "", from.Unix()/60)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, traffic.Summaries(rows, from, 24, traefik.LatencyBuckets))
}

package api

import (
	"errors"
	"net/http"

	"github.com/dockfly/dockfly/internal/selfupdate"
)

var (
	errUpdateUnavailable = newError("update_unavailable", "Updates from the panel aren't available here")
	errUpdateBusy        = newError("update_in_progress", "An update is already in progress")
	errUpdateFailed      = newError("update_failed", "The update couldn't start: {detail}")
)

// handleGetUpdate dice si hay una versión nueva y si se puede actualizar
// desde el panel.
func (s *Server) handleGetUpdate(w http.ResponseWriter, r *http.Request) {
	if s.Updater == nil {
		writeError(w, http.StatusNotFound, errUpdateUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, s.Updater.Info(r.Context()))
}

// handleStartUpdate actualiza Dockfly (solo admin, con su contraseña). El
// panel deja de responder unos segundos mientras se recrea el contenedor.
func (s *Server) handleStartUpdate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	if _, ok := s.requirePassword(w, r, in.Password); !ok {
		return
	}
	if s.Updater == nil {
		writeError(w, http.StatusNotFound, errUpdateUnavailable)
		return
	}
	if err := s.Updater.Start(r.Context()); errors.Is(err, selfupdate.ErrBusy) {
		writeError(w, http.StatusConflict, errUpdateBusy)
		return
	} else if err != nil {
		writeError(w, http.StatusBadRequest, errUpdateFailed.With("detail", err.Error()))
		return
	}
	s.Log.Info("actualización de Dockfly iniciada", "por", currentUser(r).Email)
	writeJSON(w, http.StatusAccepted, s.Updater.Info(r.Context()))
}

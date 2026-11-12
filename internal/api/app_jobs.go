package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/dockfly/dockfly/internal/apps"
	"github.com/dockfly/dockfly/internal/store"
)

// Tareas programadas de una app: un comando en su contenedor según una
// expresión cron en UTC.

var (
	errJobNotFound    = newError("job_not_found", "Scheduled task not found")
	errJobCommand     = newError("job_command_required", "Type the command to run")
	errJobSchedule    = newError("job_schedule_invalid", "Invalid schedule: {detail}")
	errJobNotDeployed = newError("job_app_not_running", "The app must be deployed and running to run the task")
)

type jobInput struct {
	Command  string `json:"command"`
	Schedule string `json:"schedule"`
	Enabled  bool   `json:"enabled"`
}

// read valida el cuerpo y lo normaliza.
func (in *jobInput) read(w http.ResponseWriter, r *http.Request) bool {
	if !decode(w, r, in) {
		return false
	}
	in.Command = strings.TrimSpace(in.Command)
	in.Schedule = strings.Join(strings.Fields(in.Schedule), " ")
	if in.Command == "" {
		writeError(w, http.StatusBadRequest, errJobCommand)
		return false
	}
	if _, err := apps.ParseSchedule(in.Schedule); err != nil {
		writeError(w, http.StatusBadRequest, errJobSchedule.With("detail", err.Error()))
		return false
	}
	return true
}

// loadAppJob carga la tarea {jobID} comprobando que es de la app {id}.
func (s *Server) loadAppJob(w http.ResponseWriter, r *http.Request) (*store.App, *store.AppJob, bool) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return nil, nil, false
	}
	id, _ := strconv.ParseInt(chi.URLParam(r, "jobID"), 10, 64)
	job, err := s.Store.GetAppJob(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && job.AppID != a.ID) {
		writeError(w, http.StatusNotFound, errJobNotFound)
		return nil, nil, false
	} else if err != nil {
		s.internalError(w, err)
		return nil, nil, false
	}
	return a, job, true
}

func (s *Server) handleListAppJobs(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	jobs, err := s.Store.ListAppJobs(r.Context(), a.ID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, jobs)
}

func (s *Server) handleCreateAppJob(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	var in jobInput
	if !in.read(w, r) {
		return
	}
	job := &store.AppJob{AppID: a.ID, Command: in.Command, Schedule: in.Schedule, Enabled: in.Enabled}
	if err := s.Store.CreateAppJob(r.Context(), job); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, job)
}

func (s *Server) handleUpdateAppJob(w http.ResponseWriter, r *http.Request) {
	_, job, ok := s.loadAppJob(w, r)
	if !ok {
		return
	}
	var in jobInput
	if !in.read(w, r) {
		return
	}
	job.Command, job.Schedule, job.Enabled = in.Command, in.Schedule, in.Enabled
	if err := s.Store.UpdateAppJob(r.Context(), job); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleDeleteAppJob(w http.ResponseWriter, r *http.Request) {
	_, job, ok := s.loadAppJob(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeleteAppJob(r.Context(), job.ID); err != nil {
		s.internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRunAppJob lanza la tarea ahora, sin esperar a su horario. Responde
// enseguida: el resultado aparece en la tarea cuando termina.
func (s *Server) handleRunAppJob(w http.ResponseWriter, r *http.Request) {
	a, job, ok := s.loadAppJob(w, r)
	if !ok {
		return
	}
	if a.Stopped || a.Current == nil || a.Current.Container == "" {
		writeError(w, http.StatusConflict, errJobNotDeployed)
		return
	}
	// Sigue aunque el navegador cierre la petición.
	go s.Apps.RunJob(context.WithoutCancel(r.Context()), job)
	w.WriteHeader(http.StatusAccepted)
}

package api

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/dockfly/dockfly/internal/store"
)

type redirectInput struct {
	Domains      []string `json:"domains"`
	Target       string   `json:"target"`
	Permanent    *bool    `json:"permanent"`
	PreservePath *bool    `json:"preserve_path"`
	SSL          bool     `json:"ssl"`
	Enabled      *bool    `json:"enabled"`
}

func (in *redirectInput) toRedirect() (*store.RedirectHost, error) {
	domains, err := normalizeDomains(in.Domains)
	if err != nil {
		return nil, err
	}
	target := strings.TrimRight(strings.TrimSpace(in.Target), "/")
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("el destino debe ser una URL completa, p. ej. https://nuevo.com")
	}
	// "$" tiene significado en el reemplazo de redirectRegex.
	if strings.Contains(target, "$") {
		return nil, errors.New("el destino no puede contener «$»")
	}
	for _, d := range domains {
		if strings.EqualFold(u.Hostname(), d) && (u.Path == "" || u.Path == "/") {
			return nil, errors.New("el destino no puede ser el propio dominio: sería un bucle de redirecciones")
		}
	}
	return &store.RedirectHost{
		Domains: domains, Target: target, SSL: in.SSL,
		Permanent:    in.Permanent == nil || *in.Permanent,
		PreservePath: in.PreservePath == nil || *in.PreservePath,
		Enabled:      in.Enabled == nil || *in.Enabled,
	}, nil
}

func (s *Server) handleListRedirects(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListRedirects(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) readRedirect(w http.ResponseWriter, r *http.Request, selfID int64) (*store.RedirectHost, bool) {
	var in redirectInput
	if !decode(w, r, &in) {
		return nil, false
	}
	rd, err := in.toRedirect()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	if !s.checkDomains(w, r, rd.Domains, owner{kind: ownerRedirect, id: selfID}) || !s.checkSSL(w, r, rd.Domains, rd.SSL) {
		return nil, false
	}
	return rd, true
}

func (s *Server) handleCreateRedirect(w http.ResponseWriter, r *http.Request) {
	rd, ok := s.readRedirect(w, r, 0)
	if !ok {
		return
	}
	if err := s.Store.CreateRedirect(r.Context(), rd); err != nil {
		s.internalError(w, err)
		return
	}
	s.applyAndRespond(w, r, http.StatusCreated, rd)
}

func (s *Server) handleUpdateRedirect(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	existing, err := s.Store.GetRedirect(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "redirección no encontrada")
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	rd, ok := s.readRedirect(w, r, id)
	if !ok {
		return
	}
	rd.ID, rd.CreatedAt = id, existing.CreatedAt
	if err := s.Store.UpdateRedirect(r.Context(), rd); err != nil {
		s.internalError(w, err)
		return
	}
	s.applyAndRespond(w, r, http.StatusOK, rd)
}

func (s *Server) handleDeleteRedirect(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeleteRedirect(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "redirección no encontrada")
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	if err := s.Apply(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func redirectRouteName(id int64) string { return "rd-" + strconv.FormatInt(id, 10) }

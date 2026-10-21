package api

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/dockfly/dockfly/internal/store"
)

// hostnameRe valida nombres DNS (RFC 1123) y nombres de contenedor Docker.
var hostnameRe = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9_.-]{0,251}[a-zA-Z0-9])?$`)

type proxyHostInput struct {
	Domains       []string `json:"domains"`
	TargetType    string   `json:"target_type"`
	ForwardScheme string   `json:"forward_scheme"`
	ForwardHost   string   `json:"forward_host"`
	ForwardPort   int      `json:"forward_port"`
	SSL           bool     `json:"ssl"`
	ForceHTTPS    bool     `json:"force_https"`
	HSTS          bool     `json:"hsts"`
	SkipTLSVerify bool     `json:"skip_tls_verify"`
	PreserveHost  *bool    `json:"preserve_host"`
	AccessListID  *int64   `json:"access_list_id"`
	Enabled       *bool    `json:"enabled"`
}

var (
	errProxyTargetTypeInvalid = newError("proxy_target_type_invalid", "Invalid target type")
	errProxySchemeInvalid     = newError("proxy_scheme_invalid", "The scheme must be http or https")
	errProxyHostRequired      = newError("proxy_forward_host_required", "Enter the target host")
	errProxyHostInvalid       = newError("proxy_forward_host_invalid", "Invalid target host: \"{host}\" (no http:// or paths)")
	errProxyPortInvalid       = newError("proxy_port_invalid", "The port must be between 1 and 65535")
	errProxyHostNotFound      = newError("proxy_host_not_found", "Proxy host not found")
)

func (in *proxyHostInput) normalize() (*store.ProxyHost, error) {
	h := &store.ProxyHost{
		TargetType:    in.TargetType,
		ForwardScheme: strings.ToLower(strings.TrimSpace(in.ForwardScheme)),
		ForwardHost:   strings.TrimSpace(in.ForwardHost),
		ForwardPort:   in.ForwardPort,
		SSL:           in.SSL,
		ForceHTTPS:    in.SSL && in.ForceHTTPS,
		HSTS:          in.SSL && in.HSTS,
		Enabled:       in.Enabled == nil || *in.Enabled,
		PreserveHost:  in.PreserveHost == nil || *in.PreserveHost,
		AccessListID:  in.AccessListID,
	}

	domains, err := normalizeDomains(in.Domains)
	if err != nil {
		return nil, err
	}
	h.Domains = domains

	switch h.TargetType {
	case store.TargetDocker, store.TargetHost, store.TargetExternal:
	default:
		return nil, errProxyTargetTypeInvalid
	}
	if h.ForwardScheme == "" {
		h.ForwardScheme = "http"
	}
	if h.ForwardScheme != "http" && h.ForwardScheme != "https" {
		return nil, errProxySchemeInvalid
	}
	h.SkipTLSVerify = h.ForwardScheme == "https" && in.SkipTLSVerify

	if h.ForwardHost == "" {
		return nil, errProxyHostRequired
	}
	if net.ParseIP(h.ForwardHost) == nil && !hostnameRe.MatchString(h.ForwardHost) {
		return nil, errProxyHostInvalid.With("host", h.ForwardHost)
	}
	if h.ForwardPort < 1 || h.ForwardPort > 65535 {
		return nil, errProxyPortInvalid
	}
	return h, nil
}

func (s *Server) handleListProxyHosts(w http.ResponseWriter, r *http.Request) {
	hosts, err := s.Store.ListProxyHosts(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, hosts)
}

func (s *Server) handleCreateProxyHost(w http.ResponseWriter, r *http.Request) {
	h, ok := s.readProxyHost(w, r, 0)
	if !ok {
		return
	}
	if err := s.Store.CreateProxyHost(r.Context(), h); err != nil {
		s.internalError(w, err)
		return
	}
	s.afterChange(w, r, h, http.StatusCreated)
}

func (s *Server) handleUpdateProxyHost(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	existing, err := s.Store.GetProxyHost(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, errProxyHostNotFound)
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	h, ok := s.readProxyHost(w, r, id)
	if !ok {
		return
	}
	h.ID, h.CreatedAt = id, existing.CreatedAt
	if err := s.Store.UpdateProxyHost(r.Context(), h); err != nil {
		s.internalError(w, err)
		return
	}
	s.afterChange(w, r, h, http.StatusOK)
}

func (s *Server) handleDeleteProxyHost(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	err := s.Store.DeleteProxyHost(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, errProxyHostNotFound)
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	if err := s.Apply(r.Context()); err != nil {
		s.internalError(w, fmt.Errorf("aplicando configuración: %w", err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// readProxyHost decodifica, valida y comprueba que ningún dominio esté ya en
// uso (selfID se excluye al editar).
func (s *Server) readProxyHost(w http.ResponseWriter, r *http.Request, selfID int64) (*store.ProxyHost, bool) {
	var in proxyHostInput
	if !decode(w, r, &in) {
		return nil, false
	}
	h, err := in.normalize()
	if err != nil {
		writeErrorFrom(w, http.StatusBadRequest, err, errUnexpected)
		return nil, false
	}
	if !s.checkDomains(w, r, h.Domains, owner{kind: ownerProxyHost, id: selfID}) || !s.checkAccessList(w, r, h.AccessListID) ||
		!s.checkSSL(w, r, h.Domains, h.SSL) {
		return nil, false
	}
	return h, true
}

// afterChange aplica la configuración en Traefik y, si el destino es un
// contenedor, se asegura de que Traefik pueda alcanzarlo por red.
func (s *Server) afterChange(w http.ResponseWriter, r *http.Request, h *store.ProxyHost, status int) {
	var warnings []string
	if h.TargetType == store.TargetDocker {
		if err := s.ensureReachable(r, h.ForwardHost); err != nil {
			warnings = append(warnings, err.Error())
		}
	}
	if err := s.Apply(r.Context()); err != nil {
		s.internalError(w, fmt.Errorf("aplicando configuración: %w", err))
		return
	}
	writeJSON(w, status, map[string]any{"proxy_host": h, "warnings": warnings})
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, errInvalidID)
		return 0, false
	}
	return id, true
}

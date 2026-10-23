package api

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"sort"
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
	// Locations: ubicaciones personalizadas (/api → otro destino).
	Locations []store.ProxyLocation `json:"locations"`
}

var (
	errProxyTargetTypeInvalid = newError("proxy_target_type_invalid", "Invalid target type")
	errProxySchemeInvalid     = newError("proxy_scheme_invalid", "The scheme must be http or https")
	errProxyHostRequired      = newError("proxy_forward_host_required", "Enter the target host")
	errProxyHostInvalid       = newError("proxy_forward_host_invalid", "Invalid target host: \"{host}\" (no http:// or paths)")
	errProxyPortInvalid       = newError("proxy_port_invalid", "The port must be between 1 and 65535")
	errProxyHostNotFound      = newError("proxy_host_not_found", "Proxy host not found")

	errLocationPathInvalid   = newError("location_path_invalid", "Invalid location \"{path}\": it must start with / and have no spaces, ? or #")
	errLocationPathRoot      = newError("location_path_root", "The location / is the whole domain: use a more specific path, such as /api")
	errLocationPathDuplicate = newError("location_path_duplicate", "The location {path} is repeated")
	errLocationAccessInvalid = newError("location_access_invalid", "Invalid access for the location {path}")
	// errLocation envuelve un error del destino de una ubicación.
	errLocation = newError("location_invalid", "Location {path}: {detail}")
)

// maxLocations limita las ubicaciones de un proxy host.
const maxLocations = 50

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

	scheme, err := checkTarget(h.TargetType, h.ForwardScheme, h.ForwardHost, h.ForwardPort)
	if err != nil {
		return nil, err
	}
	h.ForwardScheme = scheme
	h.SkipTLSVerify = h.ForwardScheme == "https" && in.SkipTLSVerify
	locations, err := normalizeLocations(in.Locations)
	if err != nil {
		return nil, err
	}
	h.Locations = locations
	return h, nil
}

// checkTarget valida un destino (el del proxy host o el de una ubicación).
// Devuelve el esquema normalizado.
func checkTarget(targetType, scheme, host string, port int) (string, error) {
	switch targetType {
	case store.TargetDocker, store.TargetHost, store.TargetExternal:
	default:
		return "", errProxyTargetTypeInvalid
	}
	if scheme == "" {
		scheme = "http"
	}
	if scheme != "http" && scheme != "https" {
		return "", errProxySchemeInvalid
	}
	if host == "" {
		return "", errProxyHostRequired
	}
	if net.ParseIP(host) == nil && !hostnameRe.MatchString(host) {
		return "", errProxyHostInvalid.With("host", host)
	}
	if port < 1 || port > 65535 {
		return "", errProxyPortInvalid
	}
	return scheme, nil
}

// normalizeLocations valida las ubicaciones y las ordena de la más larga a
// la más corta (como las elige Traefik).
func normalizeLocations(in []store.ProxyLocation) ([]store.ProxyLocation, error) {
	out := make([]store.ProxyLocation, 0, len(in))
	seen := map[string]bool{}
	for _, l := range in[:min(len(in), maxLocations)] {
		path := strings.TrimSpace(l.Path)
		if len(path) > 1 {
			path = strings.TrimRight(path, "/")
		}
		switch {
		case path == "/" || path == "":
			return nil, errLocationPathRoot
		case !strings.HasPrefix(path, "/") || strings.ContainsAny(path, " \t?#`") || len(path) > 200:
			return nil, errLocationPathInvalid.With("path", path)
		case seen[path]:
			return nil, errLocationPathDuplicate.With("path", path)
		}
		seen[path] = true
		l.Path = path
		l.ForwardScheme = strings.ToLower(strings.TrimSpace(l.ForwardScheme))
		l.ForwardHost = strings.TrimSpace(l.ForwardHost)
		scheme, err := checkTarget(l.TargetType, l.ForwardScheme, l.ForwardHost, l.ForwardPort)
		if err != nil {
			var e apiError
			if errors.As(err, &e) {
				return nil, errLocation.With("path", path, "detail", e.Error())
			}
			return nil, err
		}
		l.ForwardScheme = scheme
		switch l.Access {
		case "", store.LocationAccessInherit:
			l.Access, l.AccessListID = store.LocationAccessInherit, nil
		case store.LocationAccessPublic:
			l.AccessListID = nil
		case store.LocationAccessList:
			if l.AccessListID == nil {
				return nil, errLocationAccessInvalid.With("path", path)
			}
		default:
			return nil, errLocationAccessInvalid.With("path", path)
		}
		out = append(out, l)
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i].Path) > len(out[j].Path) })
	return out, nil
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
	for _, l := range h.Locations {
		if !s.checkAccessList(w, r, l.AccessListID) {
			return nil, false
		}
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
	for _, l := range h.Locations {
		if l.TargetType == store.TargetDocker && l.ForwardHost != h.ForwardHost {
			if err := s.ensureReachable(r, l.ForwardHost); err != nil {
				warnings = append(warnings, l.Path+": "+err.Error())
			}
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

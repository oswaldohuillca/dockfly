package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/dockfly/dockfly/internal/store"
	"github.com/dockfly/dockfly/internal/traefik"
)

var (
	errStreamProtocolInvalid = newError("stream_protocol_invalid", "The protocol must be tcp or udp")
	errStreamPortsInvalid    = newError("stream_ports_invalid", "Ports must be between 1 and 65535")
	errStreamHostInvalid     = newError("stream_forward_host_invalid", "Enter the target host (a container, an app or an IP)")
	errStreamPortReserved    = newError("stream_port_reserved", "Port {port} is used by Dockfly ({addr})")
	errStreamPortTaken       = newError("stream_port_taken", "There's already a {protocol} stream on port {port}")
	errStreamNotFound        = newError("stream_not_found", "Stream not found")
)

type streamInput struct {
	Protocol    string `json:"protocol"`
	ListenPort  int    `json:"listen_port"`
	ForwardHost string `json:"forward_host"`
	ForwardPort int    `json:"forward_port"`
	Enabled     *bool  `json:"enabled"`
}

// reservedPorts no pueden usarse para streams: los usa Dockfly.
func (s *Server) reservedPorts() map[int]string {
	r := map[int]string{}
	for _, addr := range []string{s.Traefik.HTTPAddr, s.Traefik.HTTPSAddr, s.ListenAddr, s.StaticURL} {
		addr = strings.TrimPrefix(addr, "http://")
		if _, p, err := net.SplitHostPort(addr); err == nil {
			if n, err := strconv.Atoi(p); err == nil {
				r[n] = addr
			}
		}
	}
	return r
}

func (s *Server) readStream(w http.ResponseWriter, r *http.Request, selfID int64) (*store.Stream, bool) {
	var in streamInput
	if !decode(w, r, &in) {
		return nil, false
	}
	st := &store.Stream{
		Protocol: strings.ToLower(in.Protocol), ListenPort: in.ListenPort,
		ForwardHost: strings.TrimSpace(in.ForwardHost), ForwardPort: in.ForwardPort,
		Enabled: in.Enabled == nil || *in.Enabled,
	}
	fail := func(e apiError) (*store.Stream, bool) {
		writeError(w, http.StatusBadRequest, e)
		return nil, false
	}
	switch {
	case st.Protocol != "tcp" && st.Protocol != "udp":
		return fail(errStreamProtocolInvalid)
	case st.ListenPort < 1 || st.ListenPort > 65535 || st.ForwardPort < 1 || st.ForwardPort > 65535:
		return fail(errStreamPortsInvalid)
	case st.ForwardHost == "" || (net.ParseIP(st.ForwardHost) == nil && !hostnameRe.MatchString(st.ForwardHost)):
		return fail(errStreamHostInvalid)
	}
	if st.Protocol == "tcp" {
		if used, ok := s.reservedPorts()[st.ListenPort]; ok {
			return fail(errStreamPortReserved.With("port", st.ListenPort, "addr", used))
		}
	}
	existing, err := s.Store.ListStreams(r.Context())
	if err != nil {
		s.internalError(w, err)
		return nil, false
	}
	for _, o := range existing {
		if o.ID != selfID && o.Protocol == st.Protocol && o.ListenPort == st.ListenPort {
			return fail(errStreamPortTaken.With("protocol", st.Protocol, "port", st.ListenPort))
		}
	}
	return st, true
}

type streamView struct {
	*store.Stream
	// Published indica si el puerto está publicado en el contenedor de
	// Dockfly; nil si no se puede saber (Dockfly no corre en Docker).
	Published *bool `json:"published"`
}

func (s *Server) handleListStreams(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListStreams(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	var published map[string]bool
	known := false
	if s.Docker != nil {
		published, known = s.Docker.SelfPublishedPorts(r.Context())
	}
	out := make([]streamView, len(list))
	for i, st := range list {
		out[i] = streamView{Stream: st}
		if known {
			p := published[fmt.Sprintf("%d/%s", st.ListenPort, st.Protocol)]
			out[i].Published = &p
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateStream(w http.ResponseWriter, r *http.Request) {
	st, ok := s.readStream(w, r, 0)
	if !ok {
		return
	}
	if err := s.Store.CreateStream(r.Context(), st); err != nil {
		s.internalError(w, err)
		return
	}
	s.streamChanged(w, r, http.StatusCreated, st)
}

func (s *Server) handleUpdateStream(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	existing, err := s.Store.GetStream(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, errStreamNotFound)
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	st, ok := s.readStream(w, r, id)
	if !ok {
		return
	}
	st.ID, st.CreatedAt = id, existing.CreatedAt
	if err := s.Store.UpdateStream(r.Context(), st); err != nil {
		s.internalError(w, err)
		return
	}
	s.streamChanged(w, r, http.StatusOK, st)
}

func (s *Server) handleDeleteStream(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeleteStream(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, errStreamNotFound)
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	s.streamChanged(w, r, http.StatusOK, nil)
}

// streamChanged aplica la parte dinámica y, si cambió el conjunto de
// puertos, la estática (con reinicio de Traefik).
func (s *Server) streamChanged(w http.ResponseWriter, r *http.Request, status int, st *store.Stream) {
	restarted, err := s.syncStreamPorts(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	if err := s.Apply(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, status, map[string]any{"stream": st, "traefik_restarted": restarted})
}

// syncStreamPorts reescribe traefik.yml si los puertos de los streams
// activos cambiaron, y reinicia Traefik para que los escuche.
func (s *Server) syncStreamPorts(ctx context.Context) (bool, error) {
	ports, err := s.StreamPorts(ctx)
	if err != nil {
		return false, err
	}
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	if slices.Equal(ports, s.Traefik.Streams) {
		return false, nil
	}
	s.Traefik.Streams = ports
	if err := traefik.WriteStatic(s.Traefik); err != nil {
		return false, err
	}
	if s.Super != nil {
		s.Super.Restart()
	}
	return true, nil
}

// StreamPorts son los puertos de los streams activos, ordenados.
func (s *Server) StreamPorts(ctx context.Context) ([]traefik.StreamPort, error) {
	list, err := s.Store.ListStreams(ctx)
	if err != nil {
		return nil, err
	}
	var ports []traefik.StreamPort
	for _, st := range list {
		if st.Enabled {
			ports = append(ports, traefik.StreamPort{Protocol: st.Protocol, Port: st.ListenPort})
		}
	}
	slices.SortFunc(ports, func(a, b traefik.StreamPort) int {
		if a.Port != b.Port {
			return a.Port - b.Port
		}
		return strings.Compare(a.Protocol, b.Protocol)
	})
	return ports, nil
}

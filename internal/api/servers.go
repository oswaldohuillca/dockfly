package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/dockfly/dockfly/internal/docker"
	"github.com/dockfly/dockfly/internal/store"
	"github.com/dockfly/dockfly/internal/traefik"
	"github.com/dockfly/dockfly/internal/tunnel"
)

// Servidores remotos: cada uno ejecuta "dockfly agent", que se conecta al
// panel por un túnel. El panel usa su Docker para desplegar las apps y le
// envía las rutas de su Traefik.

const agentTokenPrefix = "dfa_"

var (
	errServerInvalidName = newError("server_invalid_name", "The name can only contain lowercase letters, numbers and hyphens")
	errServerNameTaken   = newError("server_name_taken", "A server with that name already exists")
	errServerNotFound    = newError("server_not_found", "Server not found")
	errServerHasApps     = newError("server_has_apps", "The server has {count} apps: delete them first")
	errAgentTokenMissing = newError("agent_token_missing", "The server token is missing")
	errAgentTokenInvalid = newError("agent_token_invalid", "Invalid server token: generate a new one in the panel")
)

var serverNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)

// remotes guarda un cliente Docker por servidor (se crean al primer uso).
type remotes struct {
	mu      sync.Mutex
	clients map[int64]*docker.Client
	// Los envíos de configuración a cada servidor van de uno en uno, y uno
	// que ya quedó viejo (llegó otro después) no se envía.
	push map[int64]*sync.Mutex
	gen  map[int64]uint64
}

// nextPush reserva el turno de envío a un servidor.
func (r *remotes) nextPush(id int64) (*sync.Mutex, uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.push == nil {
		r.push, r.gen = map[int64]*sync.Mutex{}, map[int64]uint64{}
	}
	if r.push[id] == nil {
		r.push[id] = &sync.Mutex{}
	}
	r.gen[id]++
	return r.push[id], r.gen[id]
}

func (r *remotes) latest(id int64, gen uint64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.gen[id] == gen
}

// RemoteDocker es el Docker de un servidor remoto.
func (s *Server) RemoteDocker(id int64) *docker.Client {
	s.remotes.mu.Lock()
	defer s.remotes.mu.Unlock()
	if s.remotes.clients == nil {
		s.remotes.clients = map[int64]*docker.Client{}
	}
	c, ok := s.remotes.clients[id]
	if !ok {
		c = docker.NewRemote(fmt.Sprintf("servidor %d", id), func(ctx context.Context) (net.Conn, error) {
			return s.Hub.Dial(ctx, id, tunnel.TargetDocker)
		})
		s.remotes.clients[id] = c
	}
	return c
}

// RemoteNetwork es la red de Dockfly en un servidor remoto (la que informó
// su agente al conectarse).
func (s *Server) RemoteNetwork(id int64) string {
	if info, ok := s.Hub.Online(id); ok && info.Network != "" {
		return info.Network
	}
	return "dockfly"
}

// RemoteProbe pide al agente que compruebe si una app está lista.
func (s *Server) RemoteProbe(ctx context.Context, id int64, hostport, path string) bool {
	q := url.Values{"addr": {hostport}, "path": {path}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://agente/probe?"+q.Encode(), nil)
	if err != nil {
		return false
	}
	resp, err := s.agentClient(id).Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusNoContent
}

// agentClient habla con la API del agente de un servidor.
func (s *Server) agentClient(id int64) *http.Client {
	return &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return s.Hub.Dial(ctx, id, tunnel.TargetAgent)
		},
	}}
}

type serverView struct {
	*store.Server
	Online bool `json:"online"`
	Apps   int  `json:"apps"`
}

func (s *Server) handleListServers(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListServers(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	out := make([]serverView, len(list))
	for i, sv := range list {
		_, online := s.Hub.Online(sv.ID)
		n, _ := s.Store.CountServerApps(r.Context(), sv.ID)
		out[i] = serverView{Server: sv, Online: online, Apps: n}
	}
	writeJSON(w, http.StatusOK, out)
}

// joinCommand es lo que se ejecuta en el servidor nuevo para conectarlo.
func (s *Server) joinCommand(ctx context.Context, r *http.Request, token string) string {
	panel, _ := s.Store.GetSetting(ctx, store.SettingPublicURL)
	if panel == "" {
		scheme := "http"
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			scheme = "https"
		}
		panel = scheme + "://" + r.Host
	}
	image := s.Image
	if image == "" {
		image = "ghcr.io/<propietario>/dockfly"
	}
	return fmt.Sprintf("docker run -d --name dockfly-agent --restart unless-stopped \\\n"+
		"  -p 80:80 -p 443:443 \\\n"+
		"  -v /var/run/docker.sock:/var/run/docker.sock \\\n"+
		"  -v dockfly-agent:/data \\\n"+
		"  %s agent --join %s --token %s", image, panel, token)
}

func (s *Server) handleCreateServer(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	name := strings.ToLower(strings.TrimSpace(in.Name))
	if !serverNameRe.MatchString(name) {
		writeError(w, http.StatusBadRequest, errServerInvalidName)
		return
	}
	token := agentTokenPrefix + newToken()[len(tokenPrefix):]
	sv := &store.Server{Name: name, TokenHash: hashToken(token)}
	if err := s.Store.CreateServer(r.Context(), sv); isUniqueViolation(err) {
		writeError(w, http.StatusConflict, errServerNameTaken)
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"server": serverView{Server: sv}, "token": token, "command": s.joinCommand(r.Context(), r, token),
	})
}

func (s *Server) loadServer(w http.ResponseWriter, r *http.Request) (*store.Server, bool) {
	id, ok := pathID(w, r)
	if !ok {
		return nil, false
	}
	sv, err := s.Store.GetServer(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, errServerNotFound)
		return nil, false
	} else if err != nil {
		s.internalError(w, err)
		return nil, false
	}
	return sv, true
}

// handleServerToken genera un token nuevo (el anterior deja de valer).
func (s *Server) handleServerToken(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadServer(w, r)
	if !ok {
		return
	}
	token := agentTokenPrefix + newToken()[len(tokenPrefix):]
	if err := s.Store.SetServerToken(r.Context(), sv.ID, hashToken(token)); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "command": s.joinCommand(r.Context(), r, token)})
}

func (s *Server) handleDeleteServer(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.loadServer(w, r)
	if !ok {
		return
	}
	if n, err := s.Store.CountServerApps(r.Context(), sv.ID); err != nil {
		s.internalError(w, err)
		return
	} else if n > 0 {
		writeError(w, http.StatusConflict, errServerHasApps.With("count", n))
		return
	}
	if err := s.Store.DeleteServer(r.Context(), sv.ID); err != nil {
		s.internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Lo que llama el agente ---

func (s *Server) agentServer(w http.ResponseWriter, r *http.Request) (*store.Server, bool) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || !strings.HasPrefix(token, agentTokenPrefix) {
		writeError(w, http.StatusUnauthorized, errAgentTokenMissing)
		return nil, false
	}
	sv, err := s.Store.ServerByTokenHash(r.Context(), hashToken(token))
	if err != nil {
		writeError(w, http.StatusUnauthorized, errAgentTokenInvalid)
		return nil, false
	}
	return sv, true
}

func (s *Server) handleAgentControl(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.agentServer(w, r)
	if !ok {
		return
	}
	addr := middleware.GetClientIP(r.Context())
	version := r.Header.Get("X-Dockfly-Agent-Version")
	_ = s.Store.ServerSeen(r.Context(), sv.ID, addr, version)
	s.Log.Info("agente conectado", "servidor", sv.Name, "ip", addr, "versión", version)
	network := r.Header.Get("X-Dockfly-Agent-Network")
	if network == "" {
		network = "dockfly"
	}
	s.Hub.ServeControl(w, r, sv.ID, tunnel.Info{Version: version, Addr: addr, Network: network, Since: time.Now()})
	_ = s.Store.ServerSeen(context.WithoutCancel(r.Context()), sv.ID, addr, version)
	s.Log.Info("agente desconectado", "servidor", sv.Name)
}

func (s *Server) handleAgentData(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.agentServer(w, r)
	if !ok {
		return
	}
	s.Hub.ServeData(w, r, sv.ID, chi.URLParam(r, "connID"))
}

// --- Rutas de cada servidor ---

// pushServer envía a un servidor la configuración de Traefik de sus apps.
func (s *Server) pushServer(ctx context.Context, id int64, routes []traefik.Route, extra map[string]any) error {
	data, err := traefik.RenderDynamic(traefik.Dynamic{Routes: routes, Middlewares: extra})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, "http://agente/dynamic", bytes.NewReader(data))
	if err != nil {
		return err
	}
	resp, err := s.agentClient(id).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("el agente respondió %s", resp.Status)
	}
	return nil
}

// OnAgentConnect envía su configuración al agente que se acaba de conectar
// (Apply la envía a todos los servidores conectados).
func (s *Server) OnAgentConnect(int64) {
	if err := s.Apply(context.Background()); err != nil {
		s.Log.Warn("aplicando la configuración", "error", err)
	}
}

// pushServers envía a cada servidor conectado las rutas de sus apps. Un
// servidor desconectado conserva su última configuración: sus apps siguen
// sirviendo, y la recibirá al volver a conectarse.
func (s *Server) pushServers(routes map[int64][]traefik.Route, extra map[string]any) {
	servers, err := s.Store.ListServers(context.Background())
	if err != nil {
		return
	}
	for _, sv := range servers {
		if _, online := s.Hub.Online(sv.ID); !online {
			continue
		}
		mu, gen := s.remotes.nextPush(sv.ID)
		go func() {
			mu.Lock()
			defer mu.Unlock()
			if !s.remotes.latest(sv.ID, gen) {
				return // ya hay una configuración más nueva en camino
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := s.pushServer(ctx, sv.ID, routes[sv.ID], extra); err != nil {
				s.Log.Warn("enviando la configuración al servidor", "servidor", sv.Name, "error", err)
			}
		}()
	}
}

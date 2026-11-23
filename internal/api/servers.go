package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	"github.com/dockfly/dockfly/internal/sysinfo"
	"github.com/dockfly/dockfly/internal/traefik"
	"github.com/dockfly/dockfly/internal/tunnel"
)

// Remote servers: each one runs "dockfly agent", which connects to the panel
// through a tunnel. The panel uses its Docker to deploy the apps and sends it
// the routes for its Traefik.

const agentTokenPrefix = "dfa_"

var (
	errServerInvalidName = newError("server_invalid_name", "The name can only contain lowercase letters, numbers and hyphens")
	errServerNameTaken   = newError("server_name_taken", "A server with that name already exists")
	errServerNotFound    = newError("server_not_found", "Server not found")
	errServerHasApps     = newError("server_has_apps", "The server has {count} apps: delete them first")
	errServerOffline     = newError("server_offline", "The server is not connected")
	errServerStats       = newError("server_stats_unavailable", "Couldn't read the server's resources")
	errAgentOutdated     = newError("agent_outdated", "The server's agent is too old to report resources: update it")
	errAgentTokenMissing = newError("agent_token_missing", "The server token is missing")
	errAgentTokenInvalid = newError("agent_token_invalid", "Invalid server token: generate a new one in the panel")
)

var serverNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)

// remotes keeps one Docker client per server (created on first use).
type remotes struct {
	mu      sync.Mutex
	clients map[int64]*docker.Client
	// Configuration pushes to a given server go one at a time, and one that
	// has gone stale (another came after it) is not sent at all.
	push map[int64]*sync.Mutex
	gen  map[int64]uint64
}

// nextPush claims the next push slot for a server.
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

// RemoteDocker is the Docker of a remote server.
func (s *Server) RemoteDocker(id int64) *docker.Client {
	s.remotes.mu.Lock()
	defer s.remotes.mu.Unlock()
	if s.remotes.clients == nil {
		s.remotes.clients = map[int64]*docker.Client{}
	}
	c, ok := s.remotes.clients[id]
	if !ok {
		c = docker.NewRemote(fmt.Sprintf("server %d", id), func(ctx context.Context) (net.Conn, error) {
			return s.Hub.Dial(ctx, id, tunnel.TargetDocker)
		})
		s.remotes.clients[id] = c
	}
	return c
}

// RemoteNetwork is Dockfly's network on a remote server (the one its agent
// reported when it connected).
func (s *Server) RemoteNetwork(id int64) string {
	if info, ok := s.Hub.Online(id); ok && info.Network != "" {
		return info.Network
	}
	return "dockfly"
}

// RemoteProbe asks the agent to check whether an app is ready.
func (s *Server) RemoteProbe(ctx context.Context, id int64, hostport, path string) bool {
	q := url.Values{"addr": {hostport}, "path": {path}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://agent/probe?"+q.Encode(), nil)
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

// agentClient talks to the API of a server's agent.
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

// handleServerStats returns a server's CPU, memory and disk usage. The id 0
// is the panel itself; remote ones are asked of their agent.
func (s *Server) handleServerStats(w http.ResponseWriter, r *http.Request) {
	if chi.URLParam(r, "id") == "0" {
		st, err := sysinfo.Read(r.Context(), s.DataDir)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, errServerStats)
			return
		}
		writeJSON(w, http.StatusOK, st)
		return
	}
	sv, ok := s.loadServer(w, r)
	if !ok {
		return
	}
	st, err := s.remoteStats(r.Context(), sv.ID)
	if err != nil {
		writeErrorFrom(w, http.StatusServiceUnavailable, err, errServerStats)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// remoteStats asks an agent for its server's resources.
func (s *Server) remoteStats(ctx context.Context, id int64) (sysinfo.Stats, error) {
	var st sysinfo.Stats
	if _, online := s.Hub.Online(id); !online {
		return st, errServerOffline
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://agent/stats", nil)
	if err != nil {
		return st, errServerStats
	}
	resp, err := s.agentClient(id).Do(req)
	if err != nil {
		return st, errServerStats
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		// An agent older than this version has no /stats.
		return st, errAgentOutdated
	default:
		return st, errServerStats
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&st); err != nil {
		return st, errServerStats
	}
	return st, nil
}

// joinCommand builds the command to run on the new server to connect it.
// warning != "" when the public HTTPS URL is missing: the agent's token and
// the server's Docker API travel over that channel, so it has to be
// encrypted.
func (s *Server) joinCommand(ctx context.Context, token string) (cmd, warning string) {
	panel, _ := s.Store.GetSetting(ctx, store.SettingPublicURL)
	if !strings.HasPrefix(panel, "https://") {
		return "", "set the panel's public URL (https://) in Integrations first: the agent connects to it and the channel has to be encrypted"
	}
	image := s.Image
	if image == "" {
		image = "ghcr.io/oswaldohuillca/dockfly:latest"
	}
	return fmt.Sprintf("docker run -d --name dockfly-agent --restart unless-stopped \\\n"+
		"  -p 80:80 -p 443:443 \\\n"+
		"  -v /var/run/docker.sock:/var/run/docker.sock \\\n"+
		"  -v dockfly-agent:/data \\\n"+
		"  %s agent --join %s --token %s", image, panel, token), ""
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
	cmd, warning := s.joinCommand(r.Context(), token)
	writeJSON(w, http.StatusCreated, map[string]any{
		"server": serverView{Server: sv}, "token": token, "command": cmd, "warning": warning,
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

// handleServerToken issues a new token (the previous one stops working).
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
	// Kick the connected agent: its old token is void and it must reconnect.
	if s.Hub != nil {
		s.Hub.Disconnect(sv.ID)
	}
	cmd, warning := s.joinCommand(r.Context(), token)
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "command": cmd, "warning": warning})
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
	if s.Hub != nil {
		s.Hub.Disconnect(sv.ID)
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- What the agent calls ---

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
	s.Log.Info("agent connected", "server", sv.Name, "ip", addr, "version", version)
	network := r.Header.Get("X-Dockfly-Agent-Network")
	if network == "" {
		network = "dockfly"
	}
	s.Hub.ServeControl(w, r, sv.ID, tunnel.Info{Version: version, Addr: addr, Network: network, Since: time.Now()})
	_ = s.Store.ServerSeen(context.WithoutCancel(r.Context()), sv.ID, addr, version)
	s.Log.Info("agent disconnected", "server", sv.Name)
}

func (s *Server) handleAgentData(w http.ResponseWriter, r *http.Request) {
	sv, ok := s.agentServer(w, r)
	if !ok {
		return
	}
	s.Hub.ServeData(w, r, sv.ID, chi.URLParam(r, "connID"))
}

// --- Each server's routes ---

// pushServer sends a server the Traefik configuration for its apps.
func (s *Server) pushServer(ctx context.Context, id int64, routes []traefik.Route, extra map[string]any) error {
	// Send this agent only the middlewares its own routes use (the access
	// list hashes of other servers must not leak to this one).
	used := map[string]any{}
	for _, rt := range routes {
		for _, name := range rt.Middlewares {
			if mw, ok := extra[name]; ok {
				used[name] = mw
			}
		}
	}
	data, err := traefik.RenderDynamic(traefik.Dynamic{Routes: routes, Middlewares: used})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, "http://agent/dynamic", bytes.NewReader(data))
	if err != nil {
		return err
	}
	resp, err := s.agentClient(id).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("the agent replied %s", resp.Status)
	}
	return nil
}

// OnAgentConnect sends its configuration to the agent that has just
// connected (Apply sends it to every connected server).
func (s *Server) OnAgentConnect(int64) {
	if err := s.Apply(context.Background()); err != nil {
		s.Log.Warn("applying the configuration", "error", err)
	}
}

// pushServers sends every connected server the routes of its apps. A
// disconnected server keeps its last configuration: its apps keep serving,
// and it will get the new one when it connects again.
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
				return // a newer configuration is already on its way
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := s.pushServer(ctx, sv.ID, routes[sv.ID], extra); err != nil {
				s.Log.Warn("sending the configuration to the server", "server", sv.Name, "error", err)
			}
		}()
	}
}

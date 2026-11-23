// Package tunnel connects the panel with the agents on remote servers
// without opening any port on them: the agent keeps a control channel open
// towards the panel and, when the panel needs to talk to the server (its
// Docker or the agent API), it asks the agent to open a data connection,
// which the agent also initiates. That is how it works behind NAT and
// firewalls.
package tunnel

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// Targets of a connection inside the remote server.
const (
	TargetDocker  = "docker"  // the Docker socket
	TargetAgent   = "agent"   // the agent's own API (Traefik configuration…)
	TargetMetrics = "metrics" // the metrics of its Traefik
)

// Protocol is the Upgrade value of the data connections.
const Protocol = "dockfly-tunnel"

// keepalive is how often the panel writes on the control channel, so that
// proxies and NAT do not close it for being idle.
const keepalive = 20 * time.Second

// ErrOffline means the server has no agent connected.
var ErrOffline = errors.New("the server is not connected")

// message is what the panel sends over the control channel (one JSON line).
type message struct {
	Open   string `json:"open,omitempty"`   // id of the connection it must open
	Target string `json:"target,omitempty"` // TargetDocker or TargetAgent
	Ping   bool   `json:"ping,omitempty"`
}

type session struct {
	send chan message
	done chan struct{}
	info Info
}

// Info is what the agent reports about itself when it connects.
type Info struct {
	Version  string    `json:"version"`
	Hostname string    `json:"hostname"`
	Addr     string    `json:"addr"`    // IP it connected from
	Network  string    `json:"network"` // Dockfly network on the server
	Since    time.Time `json:"since"`
}

// Hub is the panel side.
type Hub struct {
	// OnConnect, if not nil, is called every time an agent connects (for
	// example to send it its configuration).
	OnConnect func(serverID int64)

	mu       sync.Mutex
	sessions map[int64]*session
	waiting  map[string]chan net.Conn
	owner    map[string]int64 // connection id → server that must open it
}

func (h *Hub) init() {
	if h.sessions == nil {
		h.sessions = map[int64]*session{}
		h.waiting = map[string]chan net.Conn{}
		h.owner = map[string]int64{}
	}
}

// Online returns the info of a server's connected agent.
func (h *Hub) Online(serverID int64) (Info, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.init()
	s, ok := h.sessions[serverID]
	if !ok {
		return Info{}, false
	}
	return s.info, true
}

// Disconnect closes the session of that server's agent, if it is connected.
// Deleting a server and rotating its token both use it.
func (h *Hub) Disconnect(serverID int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.init()
	if s, ok := h.sessions[serverID]; ok {
		close(s.done)
		delete(h.sessions, serverID)
	}
}

// ServeControl serves the control channel of an already authenticated
// agent. It lasts as long as the connection does; if the server already had
// an agent connected, the new one replaces it.
func (h *Hub) ServeControl(w http.ResponseWriter, r *http.Request, serverID int64, info Info) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "the server does not support streaming responses", http.StatusInternalServerError)
		return
	}
	s := &session{send: make(chan message, 64), done: make(chan struct{}), info: info}
	h.mu.Lock()
	h.init()
	if old, ok := h.sessions[serverID]; ok {
		close(old.done)
	}
	h.sessions[serverID] = s
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		if h.sessions[serverID] == s {
			delete(h.sessions, serverID)
		}
		h.mu.Unlock()
	}()

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	if h.OnConnect != nil {
		go h.OnConnect(serverID)
	}

	enc := json.NewEncoder(w)
	tick := time.NewTicker(keepalive)
	defer tick.Stop()
	for {
		var m message
		select {
		case <-r.Context().Done():
			return
		case <-s.done:
			return // another agent took its place
		case <-tick.C:
			m = message{Ping: true}
		case m = <-s.send:
		}
		if err := enc.Encode(m); err != nil {
			return
		}
		flusher.Flush()
	}
}

// ServeData takes a data connection opened by the agent and hands it to
// whoever asked for it with Dial.
func (h *Hub) ServeData(w http.ResponseWriter, r *http.Request, serverID int64, id string) {
	h.mu.Lock()
	h.init()
	ch, ok := h.waiting[id]
	owner := h.owner[id]
	if ok {
		delete(h.waiting, id)
		delete(h.owner, id)
	}
	h.mu.Unlock()
	if !ok || owner != serverID {
		http.Error(w, "unknown or expired connection", http.StatusNotFound)
		return
	}
	if r.Header.Get("Upgrade") != Protocol {
		http.Error(w, "expected Upgrade: "+Protocol, http.StatusBadRequest)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "the server does not support tunnel connections", http.StatusInternalServerError)
		return
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		return
	}
	_, _ = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: "+Protocol+"\r\nConnection: Upgrade\r\n\r\n")
	var c net.Conn = conn
	if buf.Reader.Buffered() > 0 {
		c = &bufferedConn{Conn: conn, r: buf.Reader}
	}
	select {
	case ch <- c:
	default:
		conn.Close() // whoever asked for it gave up waiting
	}
}

// Dial opens a connection to a target on the remote server.
func (h *Hub) Dial(ctx context.Context, serverID int64, target string) (net.Conn, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	id := hex.EncodeToString(b)
	ch := make(chan net.Conn, 1)

	h.mu.Lock()
	h.init()
	s, ok := h.sessions[serverID]
	if ok {
		h.waiting[id] = ch
		h.owner[id] = serverID
	}
	h.mu.Unlock()
	if !ok {
		return nil, ErrOffline
	}
	cleanup := func() {
		h.mu.Lock()
		delete(h.waiting, id)
		delete(h.owner, id)
		h.mu.Unlock()
	}

	select {
	case s.send <- message{Open: id, Target: target}:
	case <-ctx.Done():
		cleanup()
		return nil, ctx.Err()
	}
	timeout := time.NewTimer(15 * time.Second)
	defer timeout.Stop()
	select {
	case c := <-ch:
		return c, nil
	case <-ctx.Done():
		cleanup()
		return nil, ctx.Err()
	case <-timeout.C:
		cleanup()
		return nil, fmt.Errorf("the agent did not respond in time")
	case <-s.done:
		cleanup()
		return nil, ErrOffline
	}
}

type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (b *bufferedConn) Read(p []byte) (int, error) { return b.r.Read(p) }

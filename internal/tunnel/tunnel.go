// Package tunnel conecta el panel con los agentes de los servidores remotos
// sin abrir puertos en ellos: el agente mantiene abierto un canal de control
// hacia el panel y, cuando el panel necesita hablar con el servidor (su
// Docker o la API del agente), le pide que abra una conexión de datos, que
// el agente también inicia. Así funciona detrás de NAT y cortafuegos.
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

// Destinos de una conexión dentro del servidor remoto.
const (
	TargetDocker  = "docker"  // el socket de Docker
	TargetAgent   = "agent"   // la API del propio agente (configuración de Traefik…)
	TargetMetrics = "metrics" // las métricas de su Traefik
)

// Protocol es el valor de Upgrade de las conexiones de datos.
const Protocol = "dockfly-tunnel"

// keepalive es cada cuánto el panel escribe en el canal de control, para
// que proxies y NAT no lo cierren por inactividad.
const keepalive = 20 * time.Second

// ErrOffline indica que el servidor no tiene ningún agente conectado.
var ErrOffline = errors.New("el servidor no está conectado")

// message es lo que el panel envía por el canal de control (una línea JSON).
type message struct {
	Open   string `json:"open,omitempty"`   // id de la conexión que debe abrir
	Target string `json:"target,omitempty"` // TargetDocker o TargetAgent
	Ping   bool   `json:"ping,omitempty"`
}

type session struct {
	send chan message
	done chan struct{}
	info Info
}

// Info es lo que el agente cuenta de sí mismo al conectarse.
type Info struct {
	Version  string    `json:"version"`
	Hostname string    `json:"hostname"`
	Addr     string    `json:"addr"`    // IP desde la que se conectó
	Network  string    `json:"network"` // red de Dockfly en el servidor
	Since    time.Time `json:"since"`
}

// Hub es el lado del panel.
type Hub struct {
	// OnConnect, si no es nil, se llama cada vez que un agente se conecta
	// (p. ej. para enviarle su configuración).
	OnConnect func(serverID int64)

	mu       sync.Mutex
	sessions map[int64]*session
	waiting  map[string]chan net.Conn
	owner    map[string]int64 // id de conexión → servidor que debe abrirla
}

func (h *Hub) init() {
	if h.sessions == nil {
		h.sessions = map[int64]*session{}
		h.waiting = map[string]chan net.Conn{}
		h.owner = map[string]int64{}
	}
}

// Online devuelve la información del agente conectado de un servidor.
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

// ServeControl atiende el canal de control de un agente ya autenticado.
// Dura lo que dure la conexión; si el servidor ya tenía un agente
// conectado, el nuevo lo sustituye.
func (h *Hub) ServeControl(w http.ResponseWriter, r *http.Request, serverID int64, info Info) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "el servidor no permite respuestas en streaming", http.StatusInternalServerError)
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
			return // otro agente ocupó su lugar
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

// ServeData recibe una conexión de datos que abre el agente y se la entrega
// a quien la pidió con Dial.
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
		http.Error(w, "conexión desconocida o caducada", http.StatusNotFound)
		return
	}
	if r.Header.Get("Upgrade") != Protocol {
		http.Error(w, "se esperaba Upgrade: "+Protocol, http.StatusBadRequest)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "el servidor no permite conexiones de túnel", http.StatusInternalServerError)
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
		conn.Close() // quien la pidió ya se cansó de esperar
	}
}

// Dial abre una conexión con un destino del servidor remoto.
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
		return nil, fmt.Errorf("el agente no respondió a tiempo")
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

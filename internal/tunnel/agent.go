package tunnel

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Agent es el lado del servidor remoto: se conecta al panel y atiende las
// conexiones que le pide.
type Agent struct {
	PanelURL string // https://dockfly.midominio.com
	Token    string
	Version  string
	// Network es la red de Dockfly en este servidor (donde van las apps).
	Network string
	// Targets abre la conexión local de cada destino (el socket de Docker,
	// la API del agente…).
	Targets map[string]func(ctx context.Context) (net.Conn, error)
	Log     *slog.Logger
	// HTTP es el cliente del canal de control (sin timeout global).
	HTTP *http.Client

	mu        sync.Mutex
	connected bool
}

// Connected indica si el canal de control está abierto ahora mismo.
func (a *Agent) Connected() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.connected
}

func (a *Agent) setConnected(v bool) {
	a.mu.Lock()
	a.connected = v
	a.mu.Unlock()
}

// Run mantiene la conexión con el panel, reconectando si se corta.
func (a *Agent) Run(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		start := time.Now()
		err := a.control(ctx)
		a.setConnected(false)
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second // estuvo conectado un buen rato
		}
		a.Log.Warn("conexión con el panel perdida; reintentando", "error", err, "en", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func (a *Agent) client() *http.Client {
	if a.HTTP != nil {
		return a.HTTP
	}
	return &http.Client{}
}

func (a *Agent) control(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(a.PanelURL, "/")+"/api/agent/control", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.Token)
	req.Header.Set("X-Dockfly-Agent-Version", a.Version)
	req.Header.Set("X-Dockfly-Agent-Network", a.Network)
	resp, err := a.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &e) == nil && e.Error != "" {
			return fmt.Errorf("el panel respondió %d: %s", resp.StatusCode, e.Error)
		}
		return fmt.Errorf("el panel respondió %s", resp.Status)
	}
	a.setConnected(true)
	a.Log.Info("conectado al panel", "panel", a.PanelURL)

	// Si el panel deja de hablar (ni pings), la conexión está muerta.
	alive := time.AfterFunc(3*keepalive, func() { resp.Body.Close() })
	defer alive.Stop()
	dec := json.NewDecoder(resp.Body)
	for {
		var m message
		if err := dec.Decode(&m); err != nil {
			return err
		}
		alive.Reset(3 * keepalive)
		if m.Open != "" {
			go a.serve(ctx, m.Open, m.Target)
		}
	}
}

// serve abre la conexión de datos hacia el panel y la une con el destino local.
func (a *Agent) serve(ctx context.Context, id, target string) {
	open := a.Targets[target]
	if open == nil {
		a.Log.Warn("el panel pidió un destino desconocido", "destino", target)
		return
	}
	local, err := open(ctx)
	if err != nil {
		a.Log.Warn("no se pudo abrir el destino local", "destino", target, "error", err)
		return
	}
	defer local.Close()
	remote, err := a.dialData(ctx, id)
	if err != nil {
		a.Log.Warn("no se pudo abrir la conexión de datos", "error", err)
		return
	}
	defer remote.Close()
	pipe(local, remote)
}

func (a *Agent) dialData(ctx context.Context, id string) (net.Conn, error) {
	u, err := url.Parse(strings.TrimRight(a.PanelURL, "/") + "/api/agent/data/" + id)
	if err != nil {
		return nil, err
	}
	conn, err := dialURL(ctx, u)
	if err != nil {
		return nil, err
	}
	req, _ := http.NewRequest(http.MethodPost, u.String(), nil)
	req.Header.Set("Authorization", "Bearer "+a.Token)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", Protocol)
	if err := req.Write(conn); err != nil {
		conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		conn.Close()
		return nil, fmt.Errorf("el panel respondió %s a la conexión de datos", resp.Status)
	}
	if br.Buffered() > 0 {
		return &bufferedConn{Conn: conn, r: br}, nil
	}
	return conn, nil
}

// pipe copia en los dos sentidos hasta que uno de los lados cierra.
func pipe(a, b net.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		} else {
			_ = dst.Close()
		}
		done <- struct{}{}
	}
	go cp(a, b)
	go cp(b, a)
	<-done
	<-done
}

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
	"time"
)

// Agent is the remote server side: it connects to the panel and serves the
// connections the panel asks for.
type Agent struct {
	PanelURL string // https://dockfly.mydomain.com
	Token    string
	Version  string
	// Network is the Dockfly network on this server (where the apps go).
	Network string
	// Targets opens the local connection of each target (the Docker socket,
	// the agent API…).
	Targets map[string]func(ctx context.Context) (net.Conn, error)
	Log     *slog.Logger
	// HTTP is the client for the control channel (without a global timeout).
	HTTP *http.Client
}

// Run keeps the connection with the panel, reconnecting if it drops.
func (a *Agent) Run(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		start := time.Now()
		err := a.control(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second // it stayed connected for a good while
		}
		a.Log.Warn("connection with the panel lost; retrying", "error", err, "in", backoff)
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
			return fmt.Errorf("the panel responded %d: %s", resp.StatusCode, e.Error)
		}
		return fmt.Errorf("the panel responded %s", resp.Status)
	}
	a.Log.Info("connected to the panel", "panel", a.PanelURL)

	// If the panel stops talking (not even pings), the connection is dead.
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

// serve opens the data connection towards the panel and joins it with the
// local target.
func (a *Agent) serve(ctx context.Context, id, target string) {
	open := a.Targets[target]
	if open == nil {
		a.Log.Warn("the panel asked for an unknown target", "target", target)
		return
	}
	local, err := open(ctx)
	if err != nil {
		a.Log.Warn("could not open the local target", "target", target, "error", err)
		return
	}
	defer local.Close()
	remote, err := a.dialData(ctx, id)
	if err != nil {
		a.Log.Warn("could not open the data connection", "error", err)
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
		return nil, fmt.Errorf("the panel responded %s to the data connection", resp.Status)
	}
	if br.Buffered() > 0 {
		return &bufferedConn{Conn: conn, r: br}, nil
	}
	return conn, nil
}

// pipe copies in both directions until one of the sides closes.
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

package api

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dockfly/dockfly/internal/store"
	"github.com/dockfly/dockfly/internal/tunnel"
)

// pipeDial serves an HTTP handler over an in-memory connection.
func pipeDial(h http.Handler) func(context.Context) (net.Conn, error) {
	return func(context.Context) (net.Conn, error) {
		a, b := net.Pipe()
		go func() {
			srv := &http.Server{Handler: h}
			_ = srv.Serve(&singleListener{c: b})
		}()
		return a, nil
	}
}

type singleListener struct {
	c    net.Conn
	once sync.Once
	wait chan struct{}
}

func (l *singleListener) Accept() (net.Conn, error) {
	var c net.Conn
	l.once.Do(func() { c, l.wait = l.c, make(chan struct{}) })
	if c != nil {
		return c, nil
	}
	<-l.wait
	return nil, net.ErrClosed
}
func (l *singleListener) Close() error   { return nil }
func (l *singleListener) Addr() net.Addr { return l.c.LocalAddr() }

func TestServersAndAgent(t *testing.T) {
	ts, dyn := testServer(t)
	s := serverOf(t, ts)
	c := newClient(t, ts)
	c.do("POST", "/api/setup", adminJSON)
	c.do("PUT", "/api/settings", `{"public_url":"https://dockfly.example.com"}`)

	code, body := c.do("POST", "/api/servers", `{"name":"Madrid-1"}`)
	if code != 201 {
		t.Fatalf("creating a server: %d %v", code, body)
	}
	token := body["token"].(string)
	cmd := body["command"].(string)
	if !strings.HasPrefix(token, "dfa_") || !strings.Contains(cmd, "agent --join https://dockfly.example.com --token "+token) {
		t.Errorf("token/command: %s\n%s", token, cmd)
	}
	if code, _ := c.do("POST", "/api/servers", `{"name":"madrid-1"}`); code != 409 {
		t.Errorf("duplicate name: %d", code)
	}

	// An agent with a bogus token doesn't get in.
	bad, _ := http.NewRequest("GET", ts.URL+"/api/agent/control", nil)
	bad.Header.Set("Authorization", "Bearer dfa_fake")
	if resp, err := http.DefaultClient.Do(bad); err != nil || resp.StatusCode != 401 {
		t.Fatalf("bogus token: %v %v", resp.StatusCode, err)
	}

	// The agent: it receives Traefik's configuration and serves a fake Docker.
	var mu sync.Mutex
	var config string
	agentAPI := http.NewServeMux()
	agentAPI.HandleFunc("PUT /dynamic", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		config = string(b)
		mu.Unlock()
		w.WriteHeader(204)
	})
	fakeDocker := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/containers/dockfly-app-api-1/json") {
			fmt.Fprint(w, `{"Name":"/dockfly-app-api-1","State":{"Status":"running","Running":true},"Config":{"Labels":{}}}`)
			return
		}
		w.WriteHeader(404)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agent := &tunnel.Agent{
		PanelURL: ts.URL, Token: token, Version: "test", Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Targets: map[string]func(context.Context) (net.Conn, error){
			tunnel.TargetAgent: pipeDial(agentAPI), tunnel.TargetDocker: pipeDial(fakeDocker),
		},
	}
	go agent.Run(ctx)
	waitFor(t, "the agent to connect", func() bool { _, ok := s.Hub.Online(1); return ok })
	if _, list := c.doList("GET", "/api/servers"); list[0].(map[string]any)["online"] != true || list[0].(map[string]any)["last_addr"] == "" {
		t.Errorf("server: %v", list)
	}

	// An app on the remote server: its route goes to its Traefik, not the local one.
	if code, body := c.do("POST", "/api/apps", `{"name":"web","source":"compose","connection_id":1,"repo":"a/b","branch":"main","server_id":1}`); code != 400 {
		t.Errorf("compose on a remote server: %d %v", code, body)
	}
	code, body = c.do("POST", "/api/apps", `{"name":"api","source":"image","image":"traefik/whoami","port":80,"domains":["api.remote.test"],"server_id":1}`)
	if code != 201 {
		t.Fatalf("creating a remote app: %d %v", code, body)
	}
	ctx2 := context.Background()
	d := &store.AppDeployment{AppID: 1, Status: store.DeployRunning, Container: "dockfly-app-api-1"}
	s.Store.CreateAppDeployment(ctx2, d)
	s.Store.GoLive(ctx2, 1, d.ID)
	if err := s.Apply(ctx2); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the agent to receive the route", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return strings.Contains(config, "api.remote.test") && strings.Contains(config, "http://dockfly-app-api-1:80")
	})
	local, _ := os.ReadFile(filepath.Join(dyn, "dockfly.yml"))
	if strings.Contains(string(local), "api.remote.test") {
		t.Error("the remote app must not be routed by the local Traefik")
	}

	// The server's Docker, over the tunnel.
	if _, app := c.do("GET", "/api/apps/1", ""); app["app"].(map[string]any)["state"] != "running" {
		t.Errorf("state of the remote app: %v", app["app"])
	}

	// With apps it can't be deleted; once disconnected, the app shows "offline".
	if code, _ := c.do("DELETE", "/api/servers/1", ""); code != 409 {
		t.Errorf("deleting with apps: %d", code)
	}
	cancel()
	waitFor(t, "the agent to disconnect", func() bool { _, ok := s.Hub.Online(1); return !ok })
	if _, app := c.do("GET", "/api/apps/1", ""); app["app"].(map[string]any)["state"] != "offline" {
		t.Errorf("app on a disconnected server: %v", app["app"])
	}

	// Rotating the token voids the previous one.
	_, rot := c.do("POST", "/api/servers/1/token", "")
	if rot["token"] == token {
		t.Fatal("the token must change")
	}
	req, _ := http.NewRequest("GET", ts.URL+"/api/agent/control", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 401 {
		t.Errorf("old token: %d", resp.StatusCode)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Without a public HTTPS URL the agent's command is not generated (the
// channel has to be encrypted: it carries the token and the remote server's
// Docker API).
func TestServerCommandNeedsHTTPS(t *testing.T) {
	ts, _ := testServer(t)
	c := newClient(t, ts)
	c.do("POST", "/api/setup", adminJSON)
	if _, body := c.do("POST", "/api/servers", `{"name":"s1"}`); body["command"].(string) != "" || body["warning"].(string) == "" {
		t.Errorf("with no public URL it must warn and give no command: %v", body)
	}
	c.do("PUT", "/api/settings", `{"public_url":"http://insecure.test"}`)
	if _, body := c.do("POST", "/api/servers", `{"name":"s2"}`); body["command"].(string) != "" {
		t.Errorf("with an http (not https) URL it must not give a command either: %v", body)
	}
	c.do("PUT", "/api/settings", `{"public_url":"https://secure.test"}`)
	if _, body := c.do("POST", "/api/servers", `{"name":"s3"}`); body["command"].(string) == "" || body["warning"].(string) != "" {
		t.Errorf("with https it must give the command: %v", body)
	}
}

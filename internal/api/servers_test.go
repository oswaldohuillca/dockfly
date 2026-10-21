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

// pipeDial sirve un handler HTTP en una conexión en memoria.
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
	c.do("PUT", "/api/settings", `{"public_url":"https://dockfly.ejemplo.com"}`)

	code, body := c.do("POST", "/api/servers", `{"name":"Madrid-1"}`)
	if code != 201 {
		t.Fatalf("crear servidor: %d %v", code, body)
	}
	token := body["token"].(string)
	cmd := body["command"].(string)
	if !strings.HasPrefix(token, "dfa_") || !strings.Contains(cmd, "agent --join https://dockfly.ejemplo.com --token "+token) {
		t.Errorf("token/comando: %s\n%s", token, cmd)
	}
	if code, _ := c.do("POST", "/api/servers", `{"name":"madrid-1"}`); code != 409 {
		t.Errorf("nombre repetido: %d", code)
	}

	// Un agente con un token falso no entra.
	bad, _ := http.NewRequest("GET", ts.URL+"/api/agent/control", nil)
	bad.Header.Set("Authorization", "Bearer dfa_falso")
	if resp, err := http.DefaultClient.Do(bad); err != nil || resp.StatusCode != 401 {
		t.Fatalf("token falso: %v %v", resp.StatusCode, err)
	}

	// El agente: recibe la configuración de Traefik y sirve un Docker falso.
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
	waitFor(t, "el agente se conecte", func() bool { _, ok := s.Hub.Online(1); return ok })
	if _, list := c.doList("GET", "/api/servers"); list[0].(map[string]any)["online"] != true || list[0].(map[string]any)["last_addr"] == "" {
		t.Errorf("servidor: %v", list)
	}

	// Una app del servidor remoto: su ruta va a su Traefik, no al local.
	if code, body := c.do("POST", "/api/apps", `{"name":"web","source":"compose","connection_id":1,"repo":"a/b","branch":"main","server_id":1}`); code != 400 {
		t.Errorf("compose en remoto: %d %v", code, body)
	}
	code, body = c.do("POST", "/api/apps", `{"name":"api","source":"image","image":"traefik/whoami","port":80,"domains":["api.remoto.test"],"server_id":1}`)
	if code != 201 {
		t.Fatalf("crear app remota: %d %v", code, body)
	}
	ctx2 := context.Background()
	d := &store.AppDeployment{AppID: 1, Status: store.DeployRunning, Container: "dockfly-app-api-1"}
	s.Store.CreateAppDeployment(ctx2, d)
	s.Store.GoLive(ctx2, 1, d.ID)
	if err := s.Apply(ctx2); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "el agente reciba la ruta", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return strings.Contains(config, "api.remoto.test") && strings.Contains(config, "http://dockfly-app-api-1:80")
	})
	local, _ := os.ReadFile(filepath.Join(dyn, "dockfly.yml"))
	if strings.Contains(string(local), "api.remoto.test") {
		t.Error("la app remota no debe enrutarse en el Traefik local")
	}

	// El Docker del servidor, por el túnel.
	if _, app := c.do("GET", "/api/apps/1", ""); app["app"].(map[string]any)["state"] != "running" {
		t.Errorf("estado de la app remota: %v", app["app"])
	}

	// Con apps no se puede borrar; desconectado, la app aparece «offline».
	if code, _ := c.do("DELETE", "/api/servers/1", ""); code != 409 {
		t.Errorf("borrar con apps: %d", code)
	}
	cancel()
	waitFor(t, "el agente se desconecte", func() bool { _, ok := s.Hub.Online(1); return !ok })
	if _, app := c.do("GET", "/api/apps/1", ""); app["app"].(map[string]any)["state"] != "offline" {
		t.Errorf("app de un servidor desconectado: %v", app["app"])
	}

	// Al cambiar el token, el anterior deja de valer.
	_, rot := c.do("POST", "/api/servers/1/token", "")
	if rot["token"] == token {
		t.Fatal("el token debe cambiar")
	}
	req, _ := http.NewRequest("GET", ts.URL+"/api/agent/control", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 401 {
		t.Errorf("token viejo: %d", resp.StatusCode)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("esperando a que %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Sin URL pública HTTPS no se genera el comando del agente (el canal debe ir
// cifrado: lleva el token y la API de Docker del servidor remoto).
func TestServerCommandNeedsHTTPS(t *testing.T) {
	ts, _ := testServer(t)
	c := newClient(t, ts)
	c.do("POST", "/api/setup", adminJSON)
	if _, body := c.do("POST", "/api/servers", `{"name":"s1"}`); body["command"].(string) != "" || body["warning"].(string) == "" {
		t.Errorf("sin URL pública debe avisar y no dar comando: %v", body)
	}
	c.do("PUT", "/api/settings", `{"public_url":"http://inseguro.test"}`)
	if _, body := c.do("POST", "/api/servers", `{"name":"s2"}`); body["command"].(string) != "" {
		t.Errorf("con URL http (no https) tampoco debe dar comando: %v", body)
	}
	c.do("PUT", "/api/settings", `{"public_url":"https://seguro.test"}`)
	if _, body := c.do("POST", "/api/servers", `{"name":"s3"}`); body["command"].(string) == "" || body["warning"].(string) != "" {
		t.Errorf("con https debe dar el comando: %v", body)
	}
}

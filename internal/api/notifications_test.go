package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dockfly/dockfly/internal/store"
)

func TestNotificationChannels(t *testing.T) {
	var mu sync.Mutex
	var received []map[string]any
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/roto" {
			w.WriteHeader(410)
			return
		}
		var ev map[string]any
		json.NewDecoder(r.Body).Decode(&ev)
		mu.Lock()
		received = append(received, ev)
		mu.Unlock()
	}))
	defer hook.Close()

	ts, _, _ := testServerWithPages(t)
	c := newClient(t, ts)
	c.do("POST", "/api/setup", adminJSON)

	if code, body := c.do("POST", "/api/notifications", `{"type":"webhook","name":"x","events":[],"config":{"url":"https://a"}}`); code != 400 {
		t.Errorf("sin eventos: %d %v", code, body)
	}
	if code, _ := c.do("POST", "/api/notifications", `{"type":"sms","name":"x","events":["build_failed"],"config":{}}`); code != 400 {
		t.Errorf("tipo desconocido: %d", code)
	}

	code, ch := c.do("POST", "/api/notifications", fmt.Sprintf(
		`{"type":"webhook","name":"Mi webhook","events":["build_failed"],"config":{"url":%q,"secret":"muy-secreto"}}`, hook.URL+"/ok"))
	if code != 201 {
		t.Fatalf("crear canal: %d %v", code, ch)
	}
	cfg := ch["config"].(map[string]any)
	if cfg["secret"] != secretMask || cfg["url"] != hook.URL+"/ok" {
		t.Errorf("el secreto nunca debe devolverse: %v", cfg)
	}
	_, list := c.doList("GET", "/api/notifications")
	if strings.Contains(fmt.Sprint(list), "muy-secreto") {
		t.Fatal("el secreto aparece en el listado")
	}

	// Editar sin tocar el secreto (llega enmascarado) lo conserva.
	id := int(ch["id"].(float64))
	code, _ = c.do("PUT", fmt.Sprintf("/api/notifications/%d", id), fmt.Sprintf(
		`{"type":"webhook","name":"Renombrado","events":["build_failed","build_succeeded"],"config":{"url":%q,"secret":%q}}`, hook.URL+"/ok", secretMask))
	if code != 200 {
		t.Fatalf("editar: %d", code)
	}
	if code, _ := c.do("POST", fmt.Sprintf("/api/notifications/%d/test", id), ""); code != 204 {
		t.Fatalf("probar: %d", code)
	}
	mu.Lock()
	if len(received) != 1 || received[0]["event"] != "test" {
		t.Errorf("el aviso de prueba debe llegar: %v", received)
	}
	mu.Unlock()

	// Un canal roto devuelve el error exacto al probarlo.
	_, bad := c.do("POST", "/api/notifications", fmt.Sprintf(`{"type":"webhook","name":"roto","events":["build_failed"],"config":{"url":%q}}`, hook.URL+"/roto"))
	if code, body := c.do("POST", fmt.Sprintf("/api/notifications/%d/test", int(bad["id"].(float64))), ""); code != 502 ||
		!strings.Contains(body["error"].(string), "410") {
		t.Errorf("probar un canal roto: %d %v", code, body)
	}
}

func TestBuildFailureIsNotified(t *testing.T) {
	got := make(chan map[string]any, 4)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ev map[string]any
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &ev)
		got <- ev
	}))
	defer hook.Close()

	ts, _, _ := testServerWithPages(t)
	c := newClient(t, ts)
	c.do("POST", "/api/setup", adminJSON)
	c.do("PUT", "/api/settings", `{"public_url":"https://dockfly.ejemplo.com"}`)
	c.do("POST", "/api/sites", `{"name":"web","domains":["web.test"]}`)
	c.do("POST", "/api/notifications", fmt.Sprintf(`{"type":"webhook","name":"w","events":["build_failed"],"config":{"url":%q}}`, hook.URL))

	srv := serverOf(t, ts)
	ctx := context.Background()
	b := &store.Build{SiteID: 1, Status: store.BuildQueued, Trigger: store.TriggerPush, Branch: "main", CommitSHA: "abc1234def", CommitMessage: "Rompe todo"}
	srv.Store.CreateBuild(ctx, b)
	srv.Store.FinishBuild(ctx, b.ID, store.BuildFailed, "el build terminó con código 1", nil)
	srv.OnBuildFinished(ctx, b.ID)

	select {
	case ev := <-got:
		if ev["event"] != "build_failed" || !strings.Contains(ev["title"].(string), "web falló") ||
			!strings.Contains(ev["message"].(string), "abc1234 Rompe todo") || ev["url"] != "https://dockfly.ejemplo.com/sites/1" {
			t.Errorf("aviso del build fallido: %v", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no llegó el aviso del build fallido")
	}

	// Un build exitoso no le llega a un canal que solo quiere los fallidos.
	b2 := &store.Build{SiteID: 1, Status: store.BuildQueued, Trigger: store.TriggerPush, Branch: "main"}
	srv.Store.CreateBuild(ctx, b2)
	srv.Store.FinishBuild(ctx, b2.ID, store.BuildSuccess, "", nil)
	srv.OnBuildFinished(ctx, b2.ID)
	select {
	case ev := <-got:
		t.Errorf("no debía llegar: %v", ev)
	case <-time.After(300 * time.Millisecond):
	}

	// Errores de certificado: uno por dominio y día, aunque Traefik reintente.
	c.do("POST", "/api/notifications", fmt.Sprintf(`{"type":"webhook","name":"c","events":["cert_error"],"config":{"url":%q}}`, hook.URL))
	srv.OnCertError([]string{"web.test"}, "acme: error: 400 :: DNS problem: NXDOMAIN")
	srv.OnCertError([]string{"web.test"}, "acme: error: 400 :: DNS problem: NXDOMAIN")
	ev := <-got
	if !strings.Contains(ev["message"].(string), "registro DNS") {
		t.Errorf("el aviso de certificado debe incluir la pista: %v", ev)
	}
	select {
	case ev := <-got:
		t.Errorf("el segundo error del mismo dominio no debe avisarse: %v", ev)
	case <-time.After(300 * time.Millisecond):
	}
}

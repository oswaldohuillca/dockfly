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
		if r.URL.Path == "/broken" {
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
		t.Errorf("no events: %d %v", code, body)
	}
	if code, _ := c.do("POST", "/api/notifications", `{"type":"sms","name":"x","events":["build_failed"],"config":{}}`); code != 400 {
		t.Errorf("unknown type: %d", code)
	}

	code, ch := c.do("POST", "/api/notifications", fmt.Sprintf(
		`{"type":"webhook","name":"My webhook","events":["build_failed"],"config":{"url":%q,"secret":"very-secret"}}`, hook.URL+"/ok"))
	if code != 201 {
		t.Fatalf("create channel: %d %v", code, ch)
	}
	cfg := ch["config"].(map[string]any)
	if cfg["secret"] != secretMask || cfg["url"] != secretMask {
		t.Errorf("the secret and the url must never be returned: %v", cfg)
	}
	_, list := c.doList("GET", "/api/notifications")
	if strings.Contains(fmt.Sprint(list), "very-secret") {
		t.Fatal("the secret shows up in the listing")
	}

	// Editing without touching the secret (it arrives masked) keeps it.
	id := int(ch["id"].(float64))
	code, _ = c.do("PUT", fmt.Sprintf("/api/notifications/%d", id), fmt.Sprintf(
		`{"type":"webhook","name":"Renamed","events":["build_failed","build_succeeded"],"config":{"url":%q,"secret":%q}}`, hook.URL+"/ok", secretMask))
	if code != 200 {
		t.Fatalf("edit: %d", code)
	}
	if code, _ := c.do("POST", fmt.Sprintf("/api/notifications/%d/test", id), ""); code != 204 {
		t.Fatalf("test: %d", code)
	}
	mu.Lock()
	if len(received) != 1 || received[0]["event"] != "test" {
		t.Errorf("the test notification must arrive: %v", received)
	}
	mu.Unlock()

	// A broken channel returns the exact error when tested.
	_, bad := c.do("POST", "/api/notifications", fmt.Sprintf(`{"type":"webhook","name":"broken","events":["build_failed"],"config":{"url":%q}}`, hook.URL+"/broken"))
	if code, body := c.do("POST", fmt.Sprintf("/api/notifications/%d/test", int(bad["id"].(float64))), ""); code != 502 ||
		!strings.Contains(body["error"].(string), "410") {
		t.Errorf("test a broken channel: %d %v", code, body)
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
	c.do("PUT", "/api/settings", `{"public_url":"https://dockfly.example.com"}`)
	c.do("POST", "/api/sites", `{"name":"web","domains":["web.test"]}`)
	c.do("POST", "/api/notifications", fmt.Sprintf(`{"type":"webhook","name":"w","events":["build_failed"],"config":{"url":%q}}`, hook.URL))

	srv := serverOf(t, ts)
	ctx := context.Background()
	b := &store.Build{SiteID: 1, Status: store.BuildQueued, Trigger: store.TriggerPush, Branch: "main", CommitSHA: "abc1234def", CommitMessage: "Breaks everything"}
	srv.Store.CreateBuild(ctx, b)
	srv.Store.FinishBuild(ctx, b.ID, store.BuildFailed, "the build exited with code 1", nil)
	srv.OnBuildFinished(ctx, b.ID)

	select {
	case ev := <-got:
		if ev["event"] != "build_failed" || !strings.Contains(ev["title"].(string), "web failed") ||
			!strings.Contains(ev["message"].(string), "abc1234 Breaks everything") || ev["url"] != "https://dockfly.example.com/sites/1" {
			t.Errorf("failed build notification: %v", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the failed build notification never arrived")
	}

	// A successful build does not reach a channel that only wants failures.
	b2 := &store.Build{SiteID: 1, Status: store.BuildQueued, Trigger: store.TriggerPush, Branch: "main"}
	srv.Store.CreateBuild(ctx, b2)
	srv.Store.FinishBuild(ctx, b2.ID, store.BuildSuccess, "", nil)
	srv.OnBuildFinished(ctx, b2.ID)
	select {
	case ev := <-got:
		t.Errorf("nothing should have arrived: %v", ev)
	case <-time.After(300 * time.Millisecond):
	}

	// Certificate errors: one per domain per day, however often Traefik retries.
	c.do("POST", "/api/notifications", fmt.Sprintf(`{"type":"webhook","name":"c","events":["cert_error"],"config":{"url":%q}}`, hook.URL))
	srv.OnCertError([]string{"web.test"}, "acme: error: 400 :: DNS problem: NXDOMAIN")
	srv.OnCertError([]string{"web.test"}, "acme: error: 400 :: DNS problem: NXDOMAIN")
	ev := <-got
	if !strings.Contains(ev["message"].(string), "DNS record") {
		t.Errorf("the certificate notification must include the hint: %v", ev)
	}
	select {
	case ev := <-got:
		t.Errorf("the second error for the same domain must not be notified: %v", ev)
	case <-time.After(300 * time.Millisecond):
	}
}

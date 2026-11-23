package notify

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dockfly/dockfly/internal/secrets"
	"github.com/dockfly/dockfly/internal/store"
)

var ev = Event{Kind: EventBuildFailed, Severity: Error, Title: "Build #4 of “web” failed",
	Message: "the build exited with code 1 <script>", URL: "https://dockfly.x/sites/1", Time: time.Unix(1_800_000_000, 0).UTC()}

type captured struct {
	mu   sync.Mutex
	path string
	hdr  http.Header
	body []byte
}

func capture(t *testing.T) (*httptest.Server, *captured) {
	c := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.path, c.hdr = r.URL.Path, r.Header.Clone()
		c.body, _ = io.ReadAll(r.Body)
		if strings.Contains(r.URL.Path, "fails") {
			w.WriteHeader(404)
			io.WriteString(w, `{"message":"Unknown Webhook"}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, c
}

func TestHTTPSenders(t *testing.T) {
	srv, got := capture(t)
	ctx := context.Background()

	if err := (&Discord{WebhookURL: srv.URL + "/d"}).Send(ctx, ev); err != nil {
		t.Fatal(err)
	}
	var d struct {
		Embeds []struct {
			Title string `json:"title"`
			URL   string `json:"url"`
			Color int    `json:"color"`
		} `json:"embeds"`
	}
	json.Unmarshal(got.body, &d)
	if d.Embeds[0].Title != ev.Title || d.Embeds[0].URL != ev.URL || d.Embeds[0].Color != 0xdc2626 {
		t.Errorf("Discord: %s", got.body)
	}

	(&Slack{WebhookURL: srv.URL + "/s"}).Send(ctx, ev)
	var s struct{ Text string }
	json.Unmarshal(got.body, &s)
	if !strings.Contains(s.Text, "*Build #4 of “web” failed*") || !strings.Contains(s.Text, "|Open in Dockfly>") ||
		!strings.Contains(s.Text, "&lt;script&gt;") {
		t.Errorf("Slack must escape < and > in the message (they are link syntax): %q", s.Text)
	}

	(&Telegram{BotToken: "123:ABC", ChatID: "-100", APIURL: srv.URL}).Send(ctx, ev)
	var tg struct{ Text string }
	json.Unmarshal(got.body, &tg)
	if got.path != "/bot123:ABC/sendMessage" || !strings.Contains(tg.Text, "&lt;script&gt;") {
		t.Errorf("Telegram must escape the HTML in the message: %s %s", got.path, got.body)
	}

	(&Webhook{URL: srv.URL + "/w", Secret: "s3"}).Send(ctx, ev)
	mac := hmac.New(sha256.New, []byte("s3"))
	mac.Write(got.body)
	if got.hdr.Get("X-Dockfly-Signature") != "sha256="+hex.EncodeToString(mac.Sum(nil)) || got.hdr.Get("X-Dockfly-Event") != EventBuildFailed {
		t.Errorf("the webhook must be signed: %v", got.hdr)
	}

	err := (&Discord{WebhookURL: srv.URL + "/fails"}).Send(ctx, ev)
	if err == nil || !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "Unknown Webhook") {
		t.Errorf("an error from the service must be shown as is: %v", err)
	}
}

// smtpServer is a minimal SMTP server: it records the message it receives.
func smtpServer(t *testing.T) (port string, msg <-chan string) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	out := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		say := func(s string) { io.WriteString(conn, s+"\r\n") }
		say("220 test ESMTP")
		var data strings.Builder
		var auth string
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			cmd := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(cmd, "EHLO"):
				say("250-test")
				say("250 AUTH PLAIN")
			case strings.HasPrefix(cmd, "AUTH PLAIN"):
				raw, _ := base64.StdEncoding.DecodeString(strings.Fields(strings.TrimSpace(line))[2])
				auth = strings.ReplaceAll(string(raw), "\x00", "|")
				say("235 ok")
			case strings.HasPrefix(cmd, "MAIL"), strings.HasPrefix(cmd, "RCPT"):
				say("250 ok")
			case cmd == "DATA":
				say("354 go ahead")
				for {
					l, _ := r.ReadString('\n')
					if l == ".\r\n" {
						break
					}
					data.WriteString(l)
				}
				say("250 accepted")
			case cmd == "QUIT":
				say("221 bye")
				out <- "AUTH=" + auth + "\n" + data.String()
				return
			default:
				say("250 ok")
			}
		}
	}()
	_, port, _ = net.SplitHostPort(ln.Addr().String())
	return port, out
}

func TestEmail(t *testing.T) {
	port, msg := smtpServer(t)
	p := 0
	for _, c := range port {
		p = p*10 + int(c-'0')
	}
	// Without TLS, authentication is only allowed against localhost: just
	// enough for the test.
	e := &Email{Host: "127.0.0.1", Port: p, Security: "none", Username: "dock", Password: "pw",
		From: "dockfly@x.com", To: []string{"oswa@x.com", "other@x.com"}}
	if err := e.Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	m := <-msg
	if !strings.Contains(m, "AUTH=|dock|pw") {
		t.Errorf("authentication: %s", m)
	}
	if !strings.Contains(m, "Subject: =?utf-8?q?") || !strings.Contains(m, "To: oswa@x.com, other@x.com") ||
		!strings.Contains(m, "https://dockfly.x/sites/1") {
		t.Errorf("message:\n%s", m)
	}
	if err := (&Email{Host: "x", Port: 25, Security: "weird", From: "a", To: []string{"b"}}).validate(); err == nil {
		t.Error("an unknown security mode must be rejected")
	}
}

func TestDispatcher(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	box, _ := secrets.New(bytes.Repeat([]byte{3}, 32))
	srv, got := capture(t)

	add := func(name string, events []string, enabled bool) *store.NotificationChannel {
		cfg, _ := json.Marshal(Webhook{URL: srv.URL + "/" + name})
		c := &store.NotificationChannel{Type: "webhook", Name: name, ConfigEnc: box.Seal(cfg), Events: events, Enabled: enabled}
		st.CreateNotificationChannel(ctx, c)
		return c
	}
	add("builds", []string{EventBuildFailed}, true)
	add("certs", []string{EventCertError}, true)
	add("disabled", []string{EventBuildFailed}, false)

	d := &Dispatcher{Store: st, Secrets: box, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	d.Notify(ev)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got.mu.Lock()
		p := got.path
		got.mu.Unlock()
		if p != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	if got.path != "/builds" {
		t.Errorf("only the enabled channel subscribed to build_failed must receive it, it reached %q", got.path)
	}
	channels, _ := st.ListNotificationChannels(ctx)
	if channels[0].LastSentAt == nil || channels[1].LastSentAt != nil {
		t.Error("the send must be recorded only on the channel that received it")
	}

	// A channel that fails stores the error so the panel can show it.
	bad := add("fails", []string{EventBuildFailed}, true)
	if err := d.SendTo(ctx, bad, ev); err == nil {
		t.Fatal("expected an error")
	}
	if c, _ := st.GetNotificationChannel(ctx, bad.ID); !strings.Contains(c.LastError, "404") {
		t.Errorf("last_error: %q", c.LastError)
	}

	if !d.Once("k", time.Hour) || d.Once("k", time.Hour) {
		t.Error("Once must let through only the first time")
	}
	d.Forget("k")
	if !d.Once("k", time.Hour) {
		t.Error("after Forget it must alert again")
	}
}

// A network failure must not carry the URL: Telegram's contains the token.
func TestPostJSONHidesURL(t *testing.T) {
	err := postJSON(context.Background(), "http://127.0.0.1:1/bot123:SECRET/sendMessage", map[string]string{}, nil)
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("the error must not include the URL: %v", err)
	}
	err = postJSON(context.Background(), "http://[::1/botSECRET", map[string]string{}, nil)
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("nor must a malformed URL: %v", err)
	}
}

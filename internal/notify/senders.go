package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"mime"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var httpClient = &http.Client{Timeout: 20 * time.Second}

func postJSON(ctx context.Context, rawURL string, body any, headers map[string]string) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Dockfly")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("respondió %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

func validURL(raw, prefix string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" && u.Scheme != "http" || u.Host == "" {
		return fmt.Errorf("URL inválida")
	}
	if prefix != "" && !strings.HasPrefix(raw, prefix) {
		return fmt.Errorf("la URL debe empezar por %s", prefix)
	}
	return nil
}

var emoji = map[Severity]string{Info: "ℹ️", Success: "✅", Warning: "⚠️", Error: "❌"}

// --- Discord ---

type Discord struct {
	WebhookURL string `json:"webhook_url"`
}

func (d *Discord) validate() error { return validURL(d.WebhookURL, "") }

var discordColor = map[Severity]int{Info: 0x3b82f6, Success: 0x16a34a, Warning: 0xd97706, Error: 0xdc2626}

func (d *Discord) Send(ctx context.Context, ev Event) error {
	embed := map[string]any{
		"title": ev.Title, "description": ev.Message, "color": discordColor[ev.Severity],
		"timestamp": ev.Time.Format(time.RFC3339), "footer": map[string]string{"text": "Dockfly"},
	}
	if ev.URL != "" {
		embed["url"] = ev.URL
	}
	return postJSON(ctx, d.WebhookURL, map[string]any{"username": "Dockfly", "embeds": []any{embed}}, nil)
}

// --- Slack ---

type Slack struct {
	WebhookURL string `json:"webhook_url"`
}

func (s *Slack) validate() error { return validURL(s.WebhookURL, "") }

// slackEscape escapa lo que Slack interpreta como sintaxis: "<…>" son
// enlaces y menciones, así que un "<" en el mensaje lo rompería.
var slackEscape = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func (s *Slack) Send(ctx context.Context, ev Event) error {
	text := fmt.Sprintf("%s *%s*\n%s", emoji[ev.Severity], slackEscape.Replace(ev.Title), slackEscape.Replace(ev.Message))
	if ev.URL != "" {
		text += fmt.Sprintf("\n<%s|Abrir en Dockfly>", ev.URL)
	}
	return postJSON(ctx, s.WebhookURL, map[string]string{"text": text}, nil)
}

// --- Telegram ---

type Telegram struct {
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
	// APIURL permite usar un servidor propio de la Bot API (y los tests).
	APIURL string `json:"api_url,omitempty"`
}

func (t *Telegram) validate() error {
	if t.BotToken == "" || t.ChatID == "" {
		return fmt.Errorf("%w: token del bot y chat ID", errMissing)
	}
	return nil
}

func (t *Telegram) Send(ctx context.Context, ev Event) error {
	api := t.APIURL
	if api == "" {
		api = "https://api.telegram.org"
	}
	text := fmt.Sprintf("%s <b>%s</b>\n%s", emoji[ev.Severity], html.EscapeString(ev.Title), html.EscapeString(ev.Message))
	if ev.URL != "" {
		text += fmt.Sprintf("\n<a href=\"%s\">Abrir en Dockfly</a>", html.EscapeString(ev.URL))
	}
	return postJSON(ctx, api+"/bot"+t.BotToken+"/sendMessage", map[string]any{
		"chat_id": t.ChatID, "text": text, "parse_mode": "HTML", "disable_web_page_preview": true,
	}, nil)
}

// --- Webhook genérico ---

// Webhook envía el evento como JSON. Con Secret, firma el cuerpo con
// HMAC-SHA256 en X-Dockfly-Signature (como hace GitHub), para que el
// receptor compruebe que viene de Dockfly.
type Webhook struct {
	URL    string `json:"url"`
	Secret string `json:"secret,omitempty"`
}

func (w *Webhook) validate() error { return validURL(w.URL, "") }

func (w *Webhook) Send(ctx context.Context, ev Event) error {
	body, _ := json.Marshal(ev)
	headers := map[string]string{"X-Dockfly-Event": ev.Kind}
	if w.Secret != "" {
		mac := hmac.New(sha256.New, []byte(w.Secret))
		mac.Write(body)
		headers["X-Dockfly-Signature"] = "sha256=" + hex.EncodeToString(mac.Sum(nil))
	}
	return postJSON(ctx, w.URL, json.RawMessage(body), headers)
}

// --- Email ---

type Email struct {
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	Security string   `json:"security"` // "starttls" (587), "tls" (465) o "none"
	Username string   `json:"username,omitempty"`
	Password string   `json:"password,omitempty"`
	From     string   `json:"from"`
	To       []string `json:"to"`
}

func (e *Email) validate() error {
	if e.Host == "" || e.Port == 0 || e.From == "" || len(e.To) == 0 {
		return fmt.Errorf("%w: servidor, puerto, remitente y destinatarios", errMissing)
	}
	switch e.Security {
	case "starttls", "tls", "none":
	default:
		return fmt.Errorf("seguridad inválida: usa starttls, tls o none")
	}
	return nil
}

func (e *Email) Send(ctx context.Context, ev Event) error {
	addr := net.JoinHostPort(e.Host, strconv.Itoa(e.Port))
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	var conn net.Conn
	var err error
	if e.Security == "tls" {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: e.Host})
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("conectando con %s: %w", addr, err)
	}
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	c, err := smtp.NewClient(conn, e.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if e.Security == "starttls" {
		if err := c.StartTLS(&tls.Config{ServerName: e.Host}); err != nil {
			return fmt.Errorf("STARTTLS: %w", err)
		}
	}
	if e.Username != "" {
		// net/smtp se niega a enviar la contraseña sin cifrar salvo a localhost.
		if err := c.Auth(smtp.PlainAuth("", e.Username, e.Password, e.Host)); err != nil {
			return fmt.Errorf("autenticación: %w", err)
		}
	}
	if err := c.Mail(e.From); err != nil {
		return err
	}
	for _, to := range e.To {
		if err := c.Rcpt(to); err != nil {
			return fmt.Errorf("destinatario %s: %w", to, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(e.message(ev)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func (e *Email) message(ev Event) []byte {
	var b bytes.Buffer
	subject := mime.QEncoding.Encode("utf-8", "[Dockfly] "+ev.Title)
	fmt.Fprintf(&b, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\n", e.From, strings.Join(e.To, ", "), subject, ev.Time.Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n")
	body := ev.Message
	if ev.URL != "" {
		body += "\n\n" + ev.URL
	}
	b.WriteString(strings.ReplaceAll(body, "\n", "\r\n"))
	b.WriteString("\r\n")
	return b.Bytes()
}

// Campos secretos de cada tipo: la API nunca los devuelve.
var SecretFields = map[string][]string{
	"email":    {"password"},
	"discord":  {"webhook_url"},
	"slack":    {"webhook_url"},
	"telegram": {"bot_token"},
	"webhook":  {"secret"},
}

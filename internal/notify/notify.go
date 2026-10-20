// Package notify envía avisos (build fallido, certificado por caducar,
// contenedor caído…) por email, Discord, Slack, Telegram o webhook.
package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/dockfly/dockfly/internal/secrets"
	"github.com/dockfly/dockfly/internal/store"
)

// Eventos a los que se puede suscribir un canal.
const (
	EventBuildFailed       = "build_failed"
	EventBuildSucceeded    = "build_succeeded"
	EventCertError         = "cert_error"
	EventCertExpiring      = "cert_expiring"
	EventContainerDown     = "container_down"
	EventContainerRecovers = "container_recovered"
	EventBackupFailed      = "backup_failed"
	EventServerDown        = "server_down"
	EventServerUp          = "server_up"
	EventTest              = "test" // el botón «Probar»: llega a cualquier canal
)

var AllEvents = []string{
	EventBuildFailed, EventBuildSucceeded, EventCertError, EventCertExpiring,
	EventContainerDown, EventContainerRecovers, EventBackupFailed, EventServerDown, EventServerUp,
}

type Severity string

const (
	Info    Severity = "info"
	Success Severity = "success"
	Warning Severity = "warning"
	Error   Severity = "error"
)

// Event es un aviso, independiente del canal por el que se envíe.
type Event struct {
	Kind     string    `json:"event"`
	Severity Severity  `json:"severity"`
	Title    string    `json:"title"`
	Message  string    `json:"message"`
	URL      string    `json:"url,omitempty"` // enlace al panel
	Time     time.Time `json:"timestamp"`
}

// Sender envía un evento por un tipo de canal.
type Sender interface {
	Send(ctx context.Context, ev Event) error
}

// Types son los tipos de canal disponibles.
var Types = []string{"email", "discord", "slack", "telegram", "webhook"}

// NewSender interpreta la configuración (JSON) de un canal.
func NewSender(typ string, config []byte) (Sender, error) {
	var s Sender
	switch typ {
	case "email":
		s = new(Email)
	case "discord":
		s = new(Discord)
	case "slack":
		s = new(Slack)
	case "telegram":
		s = new(Telegram)
	case "webhook":
		s = new(Webhook)
	default:
		return nil, fmt.Errorf("tipo de canal desconocido: %q", typ)
	}
	if err := json.Unmarshal(config, s); err != nil {
		return nil, fmt.Errorf("configuración inválida: %w", err)
	}
	if v, ok := s.(interface{ validate() error }); ok {
		if err := v.validate(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Dispatcher reparte cada evento entre los canales suscritos.
type Dispatcher struct {
	Store   *store.Store
	Secrets *secrets.Box
	Log     *slog.Logger

	mu   sync.Mutex
	sent map[string]time.Time // claves de Once
}

// Notify envía el evento en segundo plano a todos los canales activos que
// lo tengan suscrito. No bloquea: un SMTP lento no debe frenar un build.
func (d *Dispatcher) Notify(ev Event) {
	if ev.Time.IsZero() {
		ev.Time = time.Now().UTC()
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		channels, err := d.Store.ListNotificationChannels(ctx)
		if err != nil {
			d.Log.Error("leyendo canales de notificación", "error", err)
			return
		}
		var wg sync.WaitGroup
		for _, c := range channels {
			if !c.Enabled || !slices.Contains(c.Events, ev.Kind) {
				continue
			}
			wg.Go(func() {
				if err := d.SendTo(ctx, c, ev); err != nil {
					d.Log.Warn("no se pudo enviar la notificación", "canal", c.Name, "error", err)
				}
			})
		}
		wg.Wait()
	}()
}

// SendTo envía el evento a un canal concreto y guarda el resultado.
func (d *Dispatcher) SendTo(ctx context.Context, c *store.NotificationChannel, ev Event) error {
	if ev.Time.IsZero() {
		ev.Time = time.Now().UTC()
	}
	err := d.send(ctx, c, ev)
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	_ = d.Store.RecordNotificationResult(context.WithoutCancel(ctx), c.ID, msg)
	return err
}

func (d *Dispatcher) send(ctx context.Context, c *store.NotificationChannel, ev Event) error {
	config, err := d.Secrets.Open(c.ConfigEnc)
	if err != nil {
		return err
	}
	s, err := NewSender(c.Type, config)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return s.Send(ctx, ev)
}

// Once devuelve true la primera vez que se ve key en el periodo ttl. Evita
// avisar en bucle de algo que sigue igual (un certificado que Traefik
// reintenta, un contenedor que sigue caído).
func (d *Dispatcher) Once(key string, ttl time.Duration) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.sent == nil {
		d.sent = map[string]time.Time{}
	}
	if t, ok := d.sent[key]; ok && time.Since(t) < ttl {
		return false
	}
	d.sent[key] = time.Now()
	return true
}

// Forget olvida key, para que el próximo Once vuelva a avisar (p. ej. cuando
// un contenedor se recupera y vuelve a caer).
func (d *Dispatcher) Forget(key string) {
	d.mu.Lock()
	delete(d.sent, key)
	d.mu.Unlock()
}

var errMissing = errors.New("faltan campos obligatorios")

// Package notify sends alerts (failed build, certificate about to expire,
// container down…) over email, Discord, Slack, Telegram or a webhook.
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

// Events a channel can subscribe to.
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
	EventResourceHigh      = "resource_high"
	EventResourceNormal    = "resource_normal"
	EventJobFailed         = "job_failed"
	EventTest              = "test" // the "Test" button: reaches any channel
)

var AllEvents = []string{
	EventBuildFailed, EventBuildSucceeded, EventCertError, EventCertExpiring,
	EventContainerDown, EventContainerRecovers, EventBackupFailed, EventServerDown, EventServerUp,
	EventResourceHigh, EventResourceNormal, EventJobFailed,
}

type Severity string

const (
	Info    Severity = "info"
	Success Severity = "success"
	Warning Severity = "warning"
	Error   Severity = "error"
)

// Event is an alert, independent of the channel it is sent through.
type Event struct {
	Kind     string    `json:"event"`
	Severity Severity  `json:"severity"`
	Title    string    `json:"title"`
	Message  string    `json:"message"`
	URL      string    `json:"url,omitempty"` // link to the panel
	Time     time.Time `json:"timestamp"`
}

// Sender sends an event through one kind of channel.
type Sender interface {
	Send(ctx context.Context, ev Event) error
}

// Types are the available channel kinds.
var Types = []string{"email", "discord", "slack", "telegram", "webhook"}

// NewSender parses a channel's configuration (JSON).
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
		return nil, fmt.Errorf("unknown channel kind: %q", typ)
	}
	if err := json.Unmarshal(config, s); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}
	if v, ok := s.(interface{ validate() error }); ok {
		if err := v.validate(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Dispatcher fans each event out to the subscribed channels.
type Dispatcher struct {
	Store   *store.Store
	Secrets *secrets.Box
	Log     *slog.Logger

	mu   sync.Mutex
	sent map[string]time.Time // Once keys
}

// Notify sends the event in the background to every enabled channel
// subscribed to it. It does not block: a slow SMTP must not hold up a build.
func (d *Dispatcher) Notify(ev Event) {
	if ev.Time.IsZero() {
		ev.Time = time.Now().UTC()
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		channels, err := d.Store.ListNotificationChannels(ctx)
		if err != nil {
			d.Log.Error("reading notification channels", "error", err)
			return
		}
		var wg sync.WaitGroup
		for _, c := range channels {
			if !c.Enabled || !slices.Contains(c.Events, ev.Kind) {
				continue
			}
			wg.Go(func() {
				if err := d.SendTo(ctx, c, ev); err != nil {
					d.Log.Warn("could not send the notification", "channel", c.Name, "error", err)
				}
			})
		}
		wg.Wait()
	}()
}

// SendTo sends the event to one specific channel and stores the result.
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

// Once returns true the first time key is seen within the ttl window. It
// avoids alerting in a loop about something that has not changed (a
// certificate Traefik keeps retrying, a container that is still down).
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

// Forget drops key, so the next Once alerts again (for example when a
// container recovers and then goes down once more).
func (d *Dispatcher) Forget(key string) {
	d.mu.Lock()
	delete(d.sent, key)
	d.mu.Unlock()
}

var errMissing = errors.New("required fields are missing")

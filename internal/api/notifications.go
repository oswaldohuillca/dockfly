package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/dockfly/dockfly/internal/notify"
	"github.com/dockfly/dockfly/internal/store"
)

// secretMask sustituye a los campos secretos en las respuestas. Si vuelve
// tal cual al editar, se conserva el valor guardado.
const secretMask = "••••••••"

type channelView struct {
	*store.NotificationChannel
	Config map[string]any `json:"config"`
}

func (s *Server) channelView(c *store.NotificationChannel) channelView {
	cfg := map[string]any{}
	if raw, err := s.Secrets.Open(c.ConfigEnc); err == nil {
		_ = json.Unmarshal(raw, &cfg)
	}
	for _, f := range notify.SecretFields[c.Type] {
		if v, ok := cfg[f].(string); ok && v != "" {
			cfg[f] = secretMask
		}
	}
	return channelView{NotificationChannel: c, Config: cfg}
}

func (s *Server) handleListChannels(w http.ResponseWriter, r *http.Request) {
	channels, err := s.Store.ListNotificationChannels(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	out := make([]channelView, len(channels))
	for i, c := range channels {
		out[i] = s.channelView(c)
	}
	writeJSON(w, http.StatusOK, out)
}

type channelInput struct {
	Type    string         `json:"type"`
	Name    string         `json:"name"`
	Events  []string       `json:"events"`
	Enabled *bool          `json:"enabled"`
	Config  map[string]any `json:"config"`
}

// build valida la entrada y devuelve el canal con su configuración cifrada.
// previous es la configuración guardada (al editar), para conservar los
// secretos que llegan enmascarados.
func (s *Server) buildChannel(in channelInput, previous map[string]any) (*store.NotificationChannel, error) {
	if !slices.Contains(notify.Types, in.Type) {
		return nil, errors.New("tipo de canal inválido")
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, errors.New("ponle un nombre al canal")
	}
	if len(in.Events) == 0 {
		return nil, errors.New("elige al menos un evento")
	}
	for _, e := range in.Events {
		if !slices.Contains(notify.AllEvents, e) {
			return nil, errors.New("evento desconocido: " + e)
		}
	}
	if in.Config == nil {
		in.Config = map[string]any{}
	}
	for _, f := range notify.SecretFields[in.Type] {
		if v, _ := in.Config[f].(string); v == secretMask {
			in.Config[f] = previous[f]
		}
	}
	raw, _ := json.Marshal(in.Config)
	if _, err := notify.NewSender(in.Type, raw); err != nil {
		return nil, err
	}
	return &store.NotificationChannel{
		Type: in.Type, Name: in.Name, Events: in.Events, Enabled: in.Enabled == nil || *in.Enabled,
		ConfigEnc: s.Secrets.Seal(raw),
	}, nil
}

func (s *Server) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	var in channelInput
	if !decode(w, r, &in) {
		return
	}
	c, err := s.buildChannel(in, nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Store.CreateNotificationChannel(r.Context(), c); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.channelView(c))
}

func (s *Server) loadChannel(w http.ResponseWriter, r *http.Request) (*store.NotificationChannel, bool) {
	id, ok := pathID(w, r)
	if !ok {
		return nil, false
	}
	c, err := s.Store.GetNotificationChannel(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "canal no encontrado")
		return nil, false
	} else if err != nil {
		s.internalError(w, err)
		return nil, false
	}
	return c, true
}

func (s *Server) handleUpdateChannel(w http.ResponseWriter, r *http.Request) {
	existing, ok := s.loadChannel(w, r)
	if !ok {
		return
	}
	var in channelInput
	if !decode(w, r, &in) {
		return
	}
	previous := map[string]any{}
	if raw, err := s.Secrets.Open(existing.ConfigEnc); err == nil && in.Type == existing.Type {
		_ = json.Unmarshal(raw, &previous)
	}
	c, err := s.buildChannel(in, previous)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	c.ID = existing.ID
	if err := s.Store.UpdateNotificationChannel(r.Context(), c); err != nil {
		s.internalError(w, err)
		return
	}
	updated, _ := s.Store.GetNotificationChannel(r.Context(), c.ID)
	writeJSON(w, http.StatusOK, s.channelView(updated))
}

func (s *Server) handleDeleteChannel(w http.ResponseWriter, r *http.Request) {
	c, ok := s.loadChannel(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeleteNotificationChannel(r.Context(), c.ID); err != nil {
		s.internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleTestChannel envía un aviso de prueba y devuelve el error exacto si
// falla (contraseña SMTP, webhook borrado…).
func (s *Server) handleTestChannel(w http.ResponseWriter, r *http.Request) {
	c, ok := s.loadChannel(w, r)
	if !ok {
		return
	}
	err := s.Notify.SendTo(r.Context(), c, notify.Event{
		Kind: notify.EventTest, Severity: notify.Info, Title: "Prueba de Dockfly",
		Message: "Si ves esto, el canal «" + c.Name + "» está bien configurado.", URL: s.panelLink(r.Context(), ""),
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// panelLink construye un enlace al panel para las notificaciones. Usa la URL
// pública si está configurada; si no, no hay enlace (sería inaccesible).
func (s *Server) panelLink(ctx context.Context, path string) string {
	public, _ := s.Store.GetSetting(ctx, store.SettingPublicURL)
	if public == "" {
		return ""
	}
	return public + path
}

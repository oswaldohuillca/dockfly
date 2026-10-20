package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/dockfly/dockfly/internal/apps"
	"github.com/dockfly/dockfly/internal/docker"
	"github.com/dockfly/dockfly/internal/gitprovider"
	"github.com/dockfly/dockfly/internal/store"
)

var (
	appNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,40}[a-z0-9])?$`)
	volNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,30}$`)
)

type appInput struct {
	Name         string            `json:"name"`
	Source       string            `json:"source"`
	Image        string            `json:"image"`
	ConnectionID int64             `json:"connection_id"`
	Repo         string            `json:"repo"`
	Branch       string            `json:"branch"`
	RootDir      string            `json:"root_dir"`
	Dockerfile   string            `json:"dockerfile"`
	ComposeFile  string            `json:"compose_file"`
	Service      string            `json:"compose_service"`
	Port         int               `json:"port"`
	Domains      []string          `json:"domains"`
	SSL          bool              `json:"ssl"`
	ForceHTTPS   bool              `json:"force_https"`
	HSTS         bool              `json:"hsts"`
	Env          map[string]string `json:"env"` // null = conservar las actuales
	Volumes      []store.AppVolume `json:"volumes"`
	Command      string            `json:"command"`
	MemoryMB     int               `json:"memory_mb"`
	CPUs         float64           `json:"cpus"`
	HealthPath   string            `json:"health_path"`
	Deploy       bool              `json:"deploy"` // desplegar nada más crearla
}

func (in *appInput) toApp() (*store.App, error) {
	a := &store.App{
		Name: strings.ToLower(strings.TrimSpace(in.Name)), Source: in.Source,
		Port: in.Port, SSL: in.SSL, ForceHTTPS: in.SSL && in.ForceHTTPS, HSTS: in.SSL && in.HSTS,
		Command: strings.TrimSpace(in.Command), MemoryMB: in.MemoryMB, CPUs: in.CPUs,
		HealthPath: strings.TrimSpace(in.HealthPath), Domains: []string{}, Volumes: []store.AppVolume{},
	}
	if !appNameRe.MatchString(a.Name) {
		return nil, errors.New("el nombre solo puede tener minúsculas, números y guiones (máx. 42)")
	}
	switch a.Source {
	case store.AppSourceImage:
		a.Image = strings.TrimSpace(in.Image)
		if !imageRe.MatchString(a.Image) {
			return nil, fmt.Errorf("imagen inválida: %q", in.Image)
		}
	case store.AppSourceGit, store.AppSourceCompose:
		if in.ConnectionID == 0 || !strings.Contains(in.Repo, "/") || strings.TrimSpace(in.Branch) == "" {
			return nil, errors.New("elige la cuenta, el repositorio y la rama")
		}
		a.GitConnectionID = &in.ConnectionID
		a.Repo, a.Branch = strings.Trim(strings.TrimSpace(in.Repo), "/"), strings.TrimSpace(in.Branch)
		var err error
		if a.RootDir, err = relDir("el directorio raíz", in.RootDir); err != nil {
			return nil, err
		}
		if a.Dockerfile, err = relDir("el Dockerfile", in.Dockerfile); err != nil {
			return nil, err
		}
		if a.Source == store.AppSourceCompose {
			if a.ComposeFile, err = relDir("el archivo de Compose", in.ComposeFile); err != nil {
				return nil, err
			}
			a.ComposeService = strings.TrimSpace(in.Service)
			if len(in.Volumes) > 0 {
				return nil, errors.New("con Compose, los volúmenes se definen en el docker-compose.yml")
			}
			if len(in.Domains) > 0 && a.ComposeService == "" {
				return nil, errors.New("indica qué servicio del docker-compose.yml recibe el tráfico web")
			}
		}
	default:
		return nil, errors.New("el origen debe ser una imagen o un repositorio")
	}
	if a.Port < 0 || a.Port > 65535 {
		return nil, errors.New("puerto inválido")
	}
	if len(in.Domains) > 0 {
		if a.Port == 0 {
			return nil, errors.New("para publicar la app en un dominio, indica el puerto en el que escucha")
		}
		domains, err := normalizeDomains(in.Domains)
		if err != nil {
			return nil, err
		}
		a.Domains = domains
	}
	if a.HealthPath != "" && !strings.HasPrefix(a.HealthPath, "/") {
		a.HealthPath = "/" + a.HealthPath
	}
	if a.MemoryMB < 0 || a.CPUs < 0 {
		return nil, errors.New("los límites de recursos no pueden ser negativos")
	}
	seen := map[string]bool{}
	for _, v := range in.Volumes {
		v.Name, v.Path = strings.TrimSpace(v.Name), strings.TrimSpace(v.Path)
		if !volNameRe.MatchString(v.Name) || seen[v.Name] {
			return nil, fmt.Errorf("nombre de volumen inválido o repetido: %q", v.Name)
		}
		if !strings.HasPrefix(v.Path, "/") || v.Path == "/" {
			return nil, fmt.Errorf("la ruta del volumen %s debe ser absoluta (p. ej. /data)", v.Name)
		}
		seen[v.Name] = true
		a.Volumes = append(a.Volumes, v)
	}
	for k := range in.Env {
		if !envNameRe.MatchString(k) {
			return nil, fmt.Errorf("nombre de variable inválido: %q", k)
		}
	}
	if _, err := apps.SplitCommand(a.Command); err != nil {
		return nil, err
	}
	return a, nil
}

// appView añade a la app el estado real de su contenedor.
type appView struct {
	*store.App
	State string `json:"state"` // running, exited, restarting… o "" si no se ha desplegado
}

func (s *Server) appState(ctx context.Context, a *store.App) string {
	if a.Current == nil || a.Current.Container == "" || s.Docker == nil {
		return ""
	}
	info, err := s.Docker.InspectContainer(ctx, a.Current.Container)
	if err != nil {
		return "missing"
	}
	if info.Restarting {
		return "restarting"
	}
	return info.Status
}

func (s *Server) handleListApps(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListApps(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	out := make([]appView, len(list))
	for i, a := range list {
		out[i] = appView{App: a, State: s.appState(r.Context(), a)}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) loadApp(w http.ResponseWriter, r *http.Request) (*store.App, bool) {
	id, ok := pathID(w, r)
	if !ok {
		return nil, false
	}
	a, err := s.Store.GetApp(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "app no encontrada")
		return nil, false
	} else if err != nil {
		s.internalError(w, err)
		return nil, false
	}
	return a, true
}

func (s *Server) handleGetApp(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	deps, err := s.Store.ListAppDeployments(r.Context(), a.ID, 20)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"app": appView{App: a, State: s.appState(r.Context(), a)}, "deployments": deps})
}

func (s *Server) handleCreateApp(w http.ResponseWriter, r *http.Request) {
	var in appInput
	if !decode(w, r, &in) {
		return
	}
	a, err := in.toApp()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.checkDomains(w, r, a.Domains, owner{kind: ownerApp}) {
		return
	}
	if in.Env != nil {
		raw, _ := json.Marshal(in.Env)
		a.EnvEnc = s.Secrets.Seal(raw)
	}
	if err := s.Store.CreateApp(r.Context(), a); err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "ya existe una app con ese nombre")
			return
		}
		s.internalError(w, err)
		return
	}
	warning := s.setupAppWebhook(r.Context(), a)
	var dep *store.AppDeployment
	if in.Deploy {
		if dep, err = s.Apps.Enqueue(r.Context(), a.ID, store.TriggerManual, gitprovider.Commit{}, ""); err != nil {
			warning = err.Error()
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{"app": a, "deployment": dep, "warning": warning})
}

func (s *Server) handleUpdateApp(w http.ResponseWriter, r *http.Request) {
	existing, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	var in appInput
	if !decode(w, r, &in) {
		return
	}
	in.Name = existing.Name // el nombre no cambia: de él dependen volúmenes y red
	a, err := in.toApp()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.checkDomains(w, r, a.Domains, owner{kind: ownerApp, id: existing.ID}) {
		return
	}
	a.ID, a.EnvEnc = existing.ID, existing.EnvEnc
	if in.Env != nil {
		raw, _ := json.Marshal(in.Env)
		a.EnvEnc = s.Secrets.Seal(raw)
	}
	if err := s.Store.UpdateApp(r.Context(), a); err != nil {
		s.internalError(w, err)
		return
	}
	updated, _ := s.Store.GetApp(r.Context(), a.ID)
	if updated.Repo != existing.Repo || !sameID(updated.GitConnectionID, existing.GitConnectionID) {
		s.deleteAppWebhook(r.Context(), existing)
		_ = s.Store.SetAppWebhook(r.Context(), a.ID, "", "")
		updated.WebhookID = ""
	}
	warning := s.setupAppWebhook(r.Context(), updated)
	// Los dominios y el SSL se aplican ya; lo demás (imagen, variables,
	// volúmenes…) en el próximo despliegue.
	if err := s.Apply(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"app": updated, "warning": warning})
}

func sameID(a, b *int64) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }

func (s *Server) handleDeleteApp(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	removeData := r.URL.Query().Get("data") == "1"
	if err := s.Apps.Remove(r.Context(), a, removeData); err != nil {
		s.internalError(w, err)
		return
	}
	s.deleteAppWebhook(r.Context(), a)
	if err := s.Store.DeleteApp(r.Context(), a.ID); err != nil {
		s.internalError(w, err)
		return
	}
	if err := s.Apply(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGetAppEnv(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	env, err := apps.Env(s.Secrets, a)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, env)
}

func (s *Server) handleDeployApp(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	d, err := s.Apps.Enqueue(r.Context(), a.ID, store.TriggerManual, gitprovider.Commit{}, "")
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, d)
}

// handleRedeploy vuelve a desplegar la imagen de un despliegue anterior
// (volver atrás) sin construir de nuevo.
func (s *Server) handleRedeploy(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	prev, ok := s.loadAppDeployment(w, r, "deploymentID")
	if !ok {
		return
	}
	if prev.AppID != a.ID || prev.Image == "" {
		writeError(w, http.StatusBadRequest, "ese despliegue no tiene una imagen reutilizable")
		return
	}
	if exists, _ := s.Docker.ImageExists(r.Context(), prev.Image); !exists {
		writeError(w, http.StatusConflict, "la imagen de ese despliegue ya no existe; despliega de nuevo")
		return
	}
	d, err := s.Apps.Enqueue(r.Context(), a.ID, "rollback",
		gitprovider.Commit{SHA: prev.CommitSHA, Message: prev.CommitMessage}, prev.Image)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, d)
}

func (s *Server) appAction(action func(context.Context, *store.App) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, ok := s.loadApp(w, r)
		if !ok {
			return
		}
		if err := action(r.Context(), a); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) loadAppDeployment(w http.ResponseWriter, r *http.Request, param string) (*store.AppDeployment, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, param), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "id de despliegue inválido")
		return nil, false
	}
	d, err := s.Store.GetAppDeployment(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "despliegue no encontrado")
		return nil, false
	} else if err != nil {
		s.internalError(w, err)
		return nil, false
	}
	return d, true
}

func (s *Server) handleCancelAppDeployment(w http.ResponseWriter, r *http.Request) {
	d, ok := s.loadAppDeployment(w, r, "deploymentID")
	if !ok {
		return
	}
	if err := s.Apps.Cancel(r.Context(), d.ID); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAppDeploymentLogs transmite el log de un despliegue por SSE, como
// el de los builds de sitios.
func (s *Server) handleAppDeploymentLogs(w http.ResponseWriter, r *http.Request) {
	d, ok := s.loadAppDeployment(w, r, "deploymentID")
	if !ok {
		return
	}
	sse := newSSE(w)
	if !sse.send("status", d) {
		return
	}
	var f *os.File
	defer func() {
		if f != nil {
			f.Close()
		}
	}()
	buf := make([]byte, 32<<10)
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	for {
		if f == nil {
			f, _ = os.Open(s.Apps.LogPath(d.ID))
		}
		if f != nil {
			for {
				n, err := f.Read(buf)
				if n > 0 && !sse.send("log", string(buf[:n])) {
					return
				}
				if n == 0 || err != nil {
					break
				}
			}
		}
		// "live" llega antes de terminar la limpieza: el final de verdad lo
		// marca el log, así que se sigue leyendo un poco más.
		if d.Finished() {
			if logDone(f) {
				sse.send("end", d)
				return
			}
		}
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		}
		cur, err := s.Store.GetAppDeployment(r.Context(), d.ID)
		if err != nil {
			return
		}
		if cur.Status != d.Status && !sse.send("status", cur) {
			return
		}
		d = cur
	}
}

// logDone indica si el log ya tiene la línea final (✔ o ✘).
func logDone(f *os.File) bool {
	if f == nil {
		return false
	}
	fi, err := f.Stat()
	if err != nil || fi.Size() == 0 {
		return false
	}
	tail := make([]byte, min(fi.Size(), 512))
	if _, err := f.ReadAt(tail, fi.Size()-int64(len(tail))); err != nil && err != io.EOF {
		return false
	}
	t := string(tail)
	return strings.Contains(t, "\n✔ ") || strings.Contains(t, "\n✘ ")
}

// handleAppLogs sigue en vivo los logs del contenedor activo.
func (s *Server) handleAppLogs(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	if a.Current == nil || a.Current.Container == "" {
		writeError(w, http.StatusConflict, "la app todavía no se ha desplegado")
		return
	}
	rc, err := s.Docker.FollowLogs(r.Context(), a.Current.Container, 300)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer rc.Close()
	sse := newSSE(w)
	_ = docker.Demux(rc, sseWriter{sse})
	sse.send("end", nil)
}

// --- SSE ---

type sse struct {
	w  http.ResponseWriter
	rc *http.ResponseController
}

func newSSE(w http.ResponseWriter) *sse {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	return &sse{w: w, rc: http.NewResponseController(w)}
}

func (s *sse) send(event string, data any) bool {
	payload, _ := json.Marshal(data)
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, payload); err != nil {
		return false
	}
	return s.rc.Flush() == nil
}

// sseWriter manda cada escritura como un evento "log".
type sseWriter struct{ s *sse }

func (w sseWriter) Write(p []byte) (int, error) {
	if !w.s.send("log", string(p)) {
		return 0, io.ErrClosedPipe
	}
	return len(p), nil
}

// --- Webhooks de apps desde Git ---

func (s *Server) setupAppWebhook(ctx context.Context, a *store.App) string {
	if a.Source == store.AppSourceImage || a.GitConnectionID == nil || a.WebhookID != "" {
		return ""
	}
	conn, prov, err := s.connectionProvider(ctx, *a.GitConnectionID)
	if err != nil {
		return err.Error()
	}
	if conn.AuthType == store.AuthGitHubApp {
		app, err := s.Store.GetGitHubApp(ctx, *conn.GitHubAppID)
		if err == nil && app.WebhookActive {
			_ = s.Store.SetAppWebhook(ctx, a.ID, appWebhook, "")
			return ""
		}
		return "configura la URL pública de Dockfly en Integraciones para desplegar en cada push"
	}
	publicURL, _ := s.Store.GetSetting(ctx, store.SettingPublicURL)
	if publicURL == "" {
		return "configura la URL pública de Dockfly en Integraciones para desplegar en cada push"
	}
	secret := randomHex(32)
	hookURL := fmt.Sprintf("%s/api/hooks/%s/apps/%d", publicURL, conn.Provider, a.ID)
	id, err := prov.CreateWebhook(ctx, a.Repo, hookURL, secret)
	if err != nil {
		return "no se pudo crear el webhook: " + err.Error()
	}
	_ = s.Store.SetAppWebhook(ctx, a.ID, id, secret)
	return ""
}

func (s *Server) deleteAppWebhook(ctx context.Context, a *store.App) {
	if a.WebhookID == "" || a.WebhookID == appWebhook || a.GitConnectionID == nil {
		return
	}
	if _, p, err := s.connectionProvider(ctx, *a.GitConnectionID); err == nil {
		_ = p.DeleteWebhook(ctx, a.Repo, a.WebhookID)
	}
}

// handleAppWebhook recibe los pushes de un repo con webhook propio.
func (s *Server) handleAppWebhook(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	body, err := readWebhookBody(w, r)
	if err != nil {
		return
	}
	if a.WebhookSecret == "" || a.GitConnectionID == nil {
		writeError(w, http.StatusNotFound, "esta app no tiene webhooks activos")
		return
	}
	var push gitprovider.Push
	var isPush bool
	switch chi.URLParam(r, "provider") {
	case gitprovider.GitHub:
		push, isPush, err = gitprovider.ParseGitHubWebhook(r.Header, body, a.WebhookSecret)
	case gitprovider.GitLab:
		push, isPush, err = gitprovider.ParseGitLabWebhook(r.Header, body, a.WebhookSecret)
	default:
		writeError(w, http.StatusNotFound, "proveedor desconocido")
		return
	}
	if errors.Is(err, gitprovider.ErrBadSignature) {
		writeError(w, http.StatusUnauthorized, "firma inválida")
		return
	} else if err != nil {
		writeError(w, http.StatusBadRequest, "payload inválido")
		return
	}
	if !isPush || push.Branch != a.Branch {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignorado"})
		return
	}
	d, err := s.Apps.Enqueue(r.Context(), a.ID, store.TriggerPush, push.Commit, "")
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "en cola", "deployment_id": d.ID})
}

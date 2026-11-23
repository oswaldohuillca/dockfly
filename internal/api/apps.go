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

var (
	errAppNameInvalid          = newError("app_name_invalid", "The name can only contain lowercase letters, numbers and hyphens (max. 42)")
	errAppImageInvalid         = newError("app_image_invalid", "Invalid image: {image}")
	errAppGitSourceIncomplete  = newError("app_git_source_incomplete", "Choose the account, the repository and the branch")
	errAppRootDirDotDot        = newError("app_root_dir_dotdot", "The root directory can't contain \"..\"")
	errAppDockerfileDotDot     = newError("app_dockerfile_dotdot", "The Dockerfile can't contain \"..\"")
	errAppComposeFileDotDot    = newError("app_compose_file_dotdot", "The Compose file can't contain \"..\"")
	errAppComposeVolumes       = newError("app_compose_volumes", "With Compose, volumes are defined in docker-compose.yml")
	errAppComposeServiceNeeded = newError("app_compose_service_required", "Specify which docker-compose.yml service receives the web traffic")
	errAppSourceInvalid        = newError("app_source_invalid", "The source must be an image or a repository")
	errAppPortInvalid          = newError("app_port_invalid", "Invalid port")
	errAppPortRequired         = newError("app_port_required", "To publish the app on a domain, specify the port it listens on")
	errAppResourcesNegative    = newError("app_resources_negative", "Resource limits can't be negative")
	errAppVolumeNameInvalid    = newError("app_volume_name_invalid", "Invalid or duplicate volume name: {name}")
	errAppVolumePathRelative   = newError("app_volume_path_not_absolute", "The path of volume {name} must be absolute (e.g. /data)")
	errAppEnvNameInvalid       = newError("app_env_name_invalid", "Invalid variable name: {name}")
	errAppCommandUnclosedQuote = newError("app_command_unclosed_quote", "The command has an unclosed quote")
	errAppNotFound             = newError("app_not_found", "App not found")
	errAppServerNotFound       = newError("app_server_not_found", "The server doesn't exist")
	errAppComposeRemote        = newError("app_compose_remote", "Compose apps can only be deployed on the main server, for now")
	errAppNameTaken            = newError("app_name_taken", "An app with that name already exists")
	errAppVolumeShared         = newError("app_volume_shared", "The volume {volume} would share its data with the app {app}: give it another name")
	errAppNoReusableImage      = newError("app_no_reusable_image", "That deployment has no reusable image")
	errAppImageGone            = newError("app_image_gone", "That deployment's image no longer exists; deploy again")
	errAppNotDeployed          = newError("app_not_deployed", "The app hasn't been deployed yet")
	errAppExecEmpty            = newError("app_exec_empty", "Type a command to run")
	errAppExecTimeout          = newError("app_exec_timeout", "The command took more than {minutes} minutes and was stopped")
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
	Env          map[string]string `json:"env"` // null = keep the current ones
	Volumes      []store.AppVolume `json:"volumes"`
	Command      string            `json:"command"`
	MemoryMB     int               `json:"memory_mb"`
	CPUs         float64           `json:"cpus"`
	HealthPath   string            `json:"health_path"`
	AccessListID *int64            `json:"access_list_id"`
	Deploy       bool              `json:"deploy"` // deploy right after creating it
	// ServerID: remote server to create it on (only on create; nil = this one).
	ServerID *int64 `json:"server_id"`
}

func (in *appInput) toApp() (*store.App, error) {
	a := &store.App{
		Name: strings.ToLower(strings.TrimSpace(in.Name)), Source: in.Source,
		Port: in.Port, SSL: in.SSL, ForceHTTPS: in.SSL && in.ForceHTTPS, HSTS: in.SSL && in.HSTS,
		Command: strings.TrimSpace(in.Command), MemoryMB: in.MemoryMB, CPUs: in.CPUs,
		HealthPath: strings.TrimSpace(in.HealthPath), Domains: []string{}, Volumes: []store.AppVolume{},
		AccessListID: in.AccessListID,
	}
	if !appNameRe.MatchString(a.Name) {
		return nil, errAppNameInvalid
	}
	switch a.Source {
	case store.AppSourceImage:
		a.Image = strings.TrimSpace(in.Image)
		if !imageRe.MatchString(a.Image) {
			return nil, errAppImageInvalid.With("image", in.Image)
		}
	case store.AppSourceGit, store.AppSourceCompose:
		if in.ConnectionID == 0 || !strings.Contains(in.Repo, "/") || strings.TrimSpace(in.Branch) == "" {
			return nil, errAppGitSourceIncomplete
		}
		a.GitConnectionID = &in.ConnectionID
		a.Repo, a.Branch = strings.Trim(strings.TrimSpace(in.Repo), "/"), strings.TrimSpace(in.Branch)
		var err error
		// relDir only fails if the directory contains "..".
		if a.RootDir, err = relDir("the root directory", in.RootDir); err != nil {
			return nil, errAppRootDirDotDot
		}
		if a.Dockerfile, err = relDir("the Dockerfile", in.Dockerfile); err != nil {
			return nil, errAppDockerfileDotDot
		}
		if a.Source == store.AppSourceCompose {
			if a.ComposeFile, err = relDir("the Compose file", in.ComposeFile); err != nil {
				return nil, errAppComposeFileDotDot
			}
			a.ComposeService = strings.TrimSpace(in.Service)
			if len(in.Volumes) > 0 {
				return nil, errAppComposeVolumes
			}
			if len(in.Domains) > 0 && a.ComposeService == "" {
				return nil, errAppComposeServiceNeeded
			}
		}
	default:
		return nil, errAppSourceInvalid
	}
	if a.Port < 0 || a.Port > 65535 {
		return nil, errAppPortInvalid
	}
	if len(in.Domains) > 0 {
		if a.Port == 0 {
			return nil, errAppPortRequired
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
		return nil, errAppResourcesNegative
	}
	seen := map[string]bool{}
	for _, v := range in.Volumes {
		v.Name, v.Path = strings.TrimSpace(v.Name), strings.TrimSpace(v.Path)
		if !volNameRe.MatchString(v.Name) || seen[v.Name] {
			return nil, errAppVolumeNameInvalid.With("name", v.Name)
		}
		if !strings.HasPrefix(v.Path, "/") || v.Path == "/" {
			return nil, errAppVolumePathRelative.With("name", v.Name)
		}
		seen[v.Name] = true
		a.Volumes = append(a.Volumes, v)
	}
	for k := range in.Env {
		if !envNameRe.MatchString(k) {
			return nil, errAppEnvNameInvalid.With("name", k)
		}
	}
	if _, err := apps.SplitCommand(a.Command); err != nil {
		return nil, errAppCommandUnclosedQuote // SplitCommand's only error
	}
	return a, nil
}

// appView adds the real state of its container to the app.
type appView struct {
	*store.App
	State string `json:"state"` // running, exited, restarting… or "" if not deployed yet
}

func (s *Server) appState(ctx context.Context, a *store.App) string {
	dc := s.Apps.Docker(a)
	if a.Current == nil || a.Current.Container == "" || dc == nil {
		return ""
	}
	if a.ServerID != nil {
		if _, online := s.Hub.Online(*a.ServerID); !online {
			return "offline" // its server isn't connected: no way to tell
		}
	}
	info, err := dc.InspectContainer(ctx, a.Current.Container)
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
		writeError(w, http.StatusNotFound, errAppNotFound)
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
		writeErrorFrom(w, http.StatusBadRequest, err, errUnexpected)
		return
	}
	if a.GitConnectionID != nil && !canLinkRepo(r) {
		writeError(w, http.StatusForbidden, errRepoLinkAdmin)
		return
	}
	if in.ServerID != nil && *in.ServerID != 0 {
		if _, err := s.Store.GetServer(r.Context(), *in.ServerID); err != nil {
			writeError(w, http.StatusBadRequest, errAppServerNotFound)
			return
		}
		if a.Source == store.AppSourceCompose {
			writeError(w, http.StatusBadRequest, errAppComposeRemote)
			return
		}
		a.ServerID = in.ServerID
	}
	if !s.checkDomains(w, r, a.Domains, owner{kind: ownerApp}) || !s.checkAccessList(w, r, a.AccessListID) ||
		!s.checkVolumes(w, r, a, 0) ||
		!s.checkSSL(w, r, a.Domains, a.SSL) {
		return
	}
	if in.Env != nil {
		raw, _ := json.Marshal(in.Env)
		a.EnvEnc = s.Secrets.Seal(raw)
	}
	if err := s.Store.CreateApp(r.Context(), a); err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, errAppNameTaken)
			return
		}
		s.internalError(w, err)
		return
	}
	warn := s.syncAppWebhook(r.Context(), a)
	var dep *store.AppDeployment
	var deployErr string
	if in.Deploy {
		if dep, err = s.Apps.Enqueue(r.Context(), a.ID, store.TriggerManual, gitprovider.Commit{}, ""); err != nil {
			deployErr = err.Error()
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"app": a, "deployment": dep, "deploy_error": deployErr, "webhook_warning": toProblem(warn),
	})
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
	in.Name = existing.Name // the name never changes: volumes and network depend on it
	a, err := in.toApp()
	if err != nil {
		writeErrorFrom(w, http.StatusBadRequest, err, errUnexpected)
		return
	}
	if a.GitConnectionID != nil && (a.Repo != existing.Repo || !sameID(a.GitConnectionID, existing.GitConnectionID)) &&
		!canLinkRepo(r) {
		writeError(w, http.StatusForbidden, errRepoLinkAdmin)
		return
	}
	if !s.checkDomains(w, r, a.Domains, owner{kind: ownerApp, id: existing.ID}) || !s.checkAccessList(w, r, a.AccessListID) ||
		!s.checkVolumes(w, r, a, existing.ID) ||
		!s.checkSSL(w, r, a.Domains, a.SSL) {
		return
	}
	// Changing the access list (it protects the app) is the admin's business,
	// as with sites: otherwise a deployer could drop it and leave the app public.
	if !sameID(existing.AccessListID, a.AccessListID) && !isAdmin(r) {
		writeError(w, http.StatusForbidden, errAppAccessAdmin)
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
		s.removeWebhook(r.Context(), appHook(existing))
		_ = s.Store.SetAppWebhook(r.Context(), a.ID, "", "")
		updated.WebhookID, updated.WebhookSecret = "", ""
	}
	warn := s.syncAppWebhook(r.Context(), updated)
	// The domains and SSL take effect now; everything else (image, variables,
	// volumes…) on the next deployment.
	if err := s.Apply(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"app": updated, "webhook_warning": toProblem(warn)})
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
	s.removeWebhook(r.Context(), appHook(a))
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
		writeError(w, http.StatusServiceUnavailable, errUnexpected.With("detail", err.Error()))
		return
	}
	writeJSON(w, http.StatusAccepted, d)
}

// handleRedeploy deploys the image of an earlier deployment again (a
// rollback) without building anything.
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
		writeError(w, http.StatusBadRequest, errAppNoReusableImage)
		return
	}
	if exists, _ := s.Apps.Docker(a).ImageExists(r.Context(), prev.Image); !exists {
		writeError(w, http.StatusConflict, errAppImageGone)
		return
	}
	d, err := s.Apps.Enqueue(r.Context(), a.ID, "rollback",
		gitprovider.Commit{SHA: prev.CommitSHA, Message: prev.CommitMessage}, prev.Image)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, errUnexpected.With("detail", err.Error()))
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
			writeError(w, http.StatusConflict, errUnexpected.With("detail", err.Error()))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) loadAppDeployment(w http.ResponseWriter, r *http.Request, param string) (*store.AppDeployment, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, param), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, errInvalidDeploymentID)
		return nil, false
	}
	d, err := s.Store.GetAppDeployment(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, errDeploymentNotFound)
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
		writeError(w, http.StatusConflict, errUnexpected.With("detail", err.Error()))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAppDeploymentLogs streams a deployment's log over SSE, just like the
// one for site builds.
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
		// "live" arrives before the cleanup is done: the log is what marks the
		// real end, so we keep reading a little longer.
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

// logDone reports whether the log already has its final line (✔ or ✘).
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

// handleAppLogs follows the active container's logs live.
func (s *Server) handleAppLogs(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	if a.Current == nil || a.Current.Container == "" {
		writeError(w, http.StatusConflict, errAppNotDeployed)
		return
	}
	rc, err := s.Apps.Docker(a).FollowLogs(r.Context(), a.Current.Container, 300)
	if err != nil {
		writeError(w, http.StatusBadGateway, errUnexpected.With("detail", err.Error()))
		return
	}
	defer rc.Close()
	sse := newSSE(w)
	_ = docker.Demux(rc, sseWriter{sse})
	sse.send("end", nil)
}

// handleAppStats returns the CPU and memory the app's container is using
// right now.
// shortcut: on a Compose app it only measures the service that takes the
// traffic; add up the rest when seeing the whole project becomes necessary.
func (s *Server) handleAppStats(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	if a.Current == nil || a.Current.Container == "" {
		writeError(w, http.StatusConflict, errAppNotDeployed)
		return
	}
	usage, err := s.Apps.Docker(a).ContainerUsage(r.Context(), a.Current.Container)
	if err != nil {
		writeError(w, http.StatusBadGateway, errUnexpected.With("detail", err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, usage)
}

// Limits for a command launched from the panel.
const (
	execTimeout   = 2 * time.Minute
	execMaxOutput = 256 << 10
)

// handleAppExec runs a command in the app's container (a migration, looking
// at a file) and returns its output and its exit code.
// shortcut: this isn't an interactive terminal (no TTY, no state between
// commands); move to websocket + emulator if a real shell becomes necessary.
func (s *Server) handleAppExec(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	var in struct {
		Command string `json:"command"`
	}
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Command) == "" {
		writeError(w, http.StatusBadRequest, errAppExecEmpty)
		return
	}
	res, err := s.Apps.Exec(r.Context(), a, in.Command, execTimeout, execMaxOutput)
	switch {
	case errors.Is(err, apps.ErrNotDeployed):
		writeError(w, http.StatusConflict, errAppNotDeployed)
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, errAppExecTimeout.With("minutes", int(execTimeout.Minutes())))
	case err != nil:
		writeError(w, http.StatusBadGateway, errUnexpected.With("detail", err.Error()))
	default:
		writeJSON(w, http.StatusOK, res)
	}
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

// sseWriter sends every write as a "log" event.
type sseWriter struct{ s *sse }

func (w sseWriter) Write(p []byte) (int, error) {
	if !w.s.send("log", string(p)) {
		return 0, io.ErrClosedPipe
	}
	return len(p), nil
}

// --- App webhooks from Git ---

// syncAppWebhook leaves the app's webhook as it should be (see ensureWebhook)
// and returns the warning, if there is one.
func (s *Server) syncAppWebhook(ctx context.Context, a *store.App) *apiError {
	t := appHook(a)
	hook, warn := s.ensureWebhook(ctx, t)
	if hook != t.hook {
		_ = s.Store.SetAppWebhook(ctx, a.ID, hook.ID, hook.Secret)
		a.WebhookID, a.WebhookSecret = hook.ID, hook.Secret
	}
	return warn
}

// handleAppWebhook receives the pushes of a repo with its own webhook.
func (s *Server) handleAppWebhook(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	body, err := readWebhookBody(w, r)
	if err != nil {
		return
	}
	a, err := s.Store.GetApp(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && (a.WebhookSecret == "" || a.GitConnectionID == nil)) {
		denyWebhook(w)
		return
	} else if err != nil {
		s.internalError(w, err)
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
		err = gitprovider.ErrBadSignature
	}
	if errors.Is(err, gitprovider.ErrBadSignature) {
		denyWebhook(w)
		return
	} else if err != nil {
		writeError(w, http.StatusBadRequest, errWebhookBadPayload)
		return
	}
	if !isPush || push.Deleted || push.Branch != a.Branch {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}
	d, err := s.Apps.Enqueue(r.Context(), a.ID, store.TriggerPush, push.Commit, "")
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, errUnexpected.With("detail", err.Error()))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "queued", "deployment_id": d.ID})
}

// checkVolumes keeps two apps from sharing data: the Docker volume is named
// dockfly-app-<app>-<volume>, so the app "shop" with "db-data" and the app
// "shop-db" with "data" would end up on the same one.
func (s *Server) checkVolumes(w http.ResponseWriter, r *http.Request, a *store.App, selfID int64) bool {
	if len(a.Volumes) == 0 {
		return true
	}
	others, err := s.Store.ListApps(r.Context())
	if err != nil {
		s.internalError(w, err)
		return false
	}
	taken := map[string]string{}
	for _, o := range others {
		if o.ID == selfID {
			continue
		}
		for _, v := range o.Volumes {
			taken[apps.VolumeName(o, v)] = o.Name
		}
	}
	for _, v := range a.Volumes {
		if other, ok := taken[apps.VolumeName(a, v)]; ok {
			writeError(w, http.StatusConflict, errAppVolumeShared.With("volume", v.Name, "app", other))
			return false
		}
	}
	return true
}

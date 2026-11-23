package api

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dockfly/dockfly/internal/notify"
	"github.com/dockfly/dockfly/internal/store"
	"github.com/dockfly/dockfly/internal/sysinfo"
	"github.com/dockfly/dockfly/internal/traefik"
)

// How often certificates and containers get checked.
const (
	certCheckEvery      = 12 * time.Hour
	containerCheckEvery = time.Minute
)

// OnBuildFinished reports a build's result (the builder calls it).
func (s *Server) OnBuildFinished(ctx context.Context, buildID int64) {
	if s.Notify == nil {
		return
	}
	b, err := s.Store.GetBuild(ctx, buildID)
	if err != nil {
		return
	}
	site, err := s.Store.GetSite(ctx, b.SiteID)
	if err != nil {
		return
	}
	commit := strings.TrimSpace(fmt.Sprintf("%s %s", short(b.CommitSHA), b.CommitMessage))
	link := s.panelLink(ctx, fmt.Sprintf("/sites/%d", site.ID))
	switch b.Status {
	case store.BuildFailed:
		s.Notify.Notify(notify.Event{
			Kind: notify.EventBuildFailed, Severity: notify.Error, URL: link,
			Title:   fmt.Sprintf("Build #%d of %s failed", b.ID, site.Name),
			Message: fmt.Sprintf("%s (%s)\n%s", commit, b.Branch, b.Error),
		})
	case store.BuildSuccess:
		title := fmt.Sprintf("%s published", site.Name)
		if b.Preview != "" {
			title = fmt.Sprintf("Preview of %s (%s) published", site.Name, b.Branch)
			if u := s.previewURL(ctx, site, b.Preview); u != "" {
				link = u
			}
		}
		s.Notify.Notify(notify.Event{
			Kind: notify.EventBuildSucceeded, Severity: notify.Success, URL: link,
			Title:   title,
			Message: fmt.Sprintf("Build #%d · %s (%s)", b.ID, commit, b.Branch),
		})
	}
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// OnAppDeployFinished reports the result of an app deployment.
func (s *Server) OnAppDeployFinished(ctx context.Context, deploymentID int64) {
	if s.Notify == nil {
		return
	}
	d, err := s.Store.GetAppDeployment(ctx, deploymentID)
	if err != nil {
		return
	}
	a, err := s.Store.GetApp(ctx, d.AppID)
	if err != nil {
		return
	}
	link := s.panelLink(ctx, fmt.Sprintf("/apps/%d", a.ID))
	what := strings.TrimSpace(short(d.CommitSHA) + " " + d.CommitMessage)
	if what == "" {
		what = d.Image
	}
	switch d.Status {
	case store.DeployFailed:
		s.Notify.Notify(notify.Event{Kind: notify.EventBuildFailed, Severity: notify.Error, URL: link,
			Title:   fmt.Sprintf("Deployment #%d of %s failed", d.ID, a.Name),
			Message: fmt.Sprintf("%s\n%s\nThe previous version is still running.", what, d.Error)})
	case store.DeployLive:
		s.Notify.Notify(notify.Event{Kind: notify.EventBuildSucceeded, Severity: notify.Success, URL: link,
			Title: a.Name + " deployed", Message: fmt.Sprintf("Deployment #%d · %s", d.ID, what)})
	}
}

// OnCertError reports a Let's Encrypt failure. Traefik retries often, so the
// same domain is only reported once a day.
func (s *Server) OnCertError(domains []string, msg string) {
	if s.Notify == nil {
		return
	}
	for _, d := range domains {
		if !s.Notify.Once("cert_error:"+d, 24*time.Hour) {
			continue
		}
		text := msg
		if hint := traefik.Hint(msg); hint != "" {
			text = hint + "\n\n" + msg
		}
		s.Notify.Notify(notify.Event{
			Kind: notify.EventCertError, Severity: notify.Error, URL: s.panelLink(context.Background(), "/proxy-hosts"),
			Title: "Couldn't obtain the certificate for " + d, Message: text,
		})
	}
}

// RunMonitors periodically checks certificates and containers until ctx is
// canceled.
func (s *Server) RunMonitors(ctx context.Context) {
	certs := time.NewTicker(certCheckEvery)
	containers := time.NewTicker(containerCheckEvery)
	defer certs.Stop()
	defer containers.Stop()
	down := map[string]bool{}
	resources := resourceWatch{}

	// First check shortly after startup (Traefik has loaded its config by then).
	select {
	case <-ctx.Done():
		return
	case <-time.After(30 * time.Second):
	}
	s.checkCertificates(ctx)
	s.checkContainers(ctx, down)
	for {
		select {
		case <-ctx.Done():
			return
		case <-certs.C:
			s.checkCertificates(ctx)
		case <-containers.C:
			s.checkContainers(ctx, down)
			s.checkResources(ctx, resources)
		}
	}
}

// checkCertificates reports certificates that expire soon (meaning automatic
// renewal is failing) or have already expired.
func (s *Server) checkCertificates(ctx context.Context) {
	if s.Notify == nil || s.Certs == nil {
		return
	}
	domains, err := s.sslDomainsCtx(ctx)
	if err != nil {
		return
	}
	status, err := s.Certs.Status(s.Traefik.ACMEFile(), domains, time.Now(), s.customCertMap(ctx))
	if err != nil {
		return
	}
	for _, c := range status {
		if c.Status != traefik.CertExpiring && c.Status != traefik.CertExpired {
			continue
		}
		if !s.Notify.Once("cert_expiring:"+c.Domain, 24*time.Hour) {
			continue
		}
		title := "The certificate for " + c.Domain + " has expired"
		if c.Status == traefik.CertExpiring {
			days := int(time.Until(*c.NotAfter).Hours() / 24)
			title = fmt.Sprintf("The certificate for %s expires in %d days", c.Domain, days)
		}
		msg := "Traefik should have renewed it: renewal is failing."
		if c.Hint != "" {
			msg += "\n" + c.Hint
		}
		s.Notify.Notify(notify.Event{Kind: notify.EventCertExpiring, Severity: notify.Warning, Title: title, Message: msg,
			URL: s.panelLink(ctx, "/proxy-hosts")})
	}
}

// checkContainers reports when a container a proxy host points at goes down
// (and when it comes back). down remembers which ones are down between checks,
// so each outage is reported only once.
func (s *Server) checkContainers(ctx context.Context, down map[string]bool) {
	if s.Notify == nil || s.Docker == nil {
		return
	}
	hosts, err := s.Store.ListProxyHosts(ctx)
	if err != nil {
		return
	}
	containers, err := s.Docker.ListContainers(ctx)
	if err != nil {
		return // Docker is not answering: that is not the containers' fault
	}
	state := map[string]string{}
	for _, c := range containers {
		state[c.Name] = c.State
	}
	watched := map[string][]string{} // container → domains that use it
	for _, h := range hosts {
		if h.Enabled && h.TargetType == store.TargetDocker {
			watched[h.ForwardHost] = append(watched[h.ForwardHost], h.Domains...)
		}
	}
	// Plus the current container of every running app. Apps mid-deployment are
	// skipped: with volumes, their container is stopped on purpose for a few
	// seconds.
	deploying := map[int64]bool{}
	if running, err := s.Store.ListAppDeploymentsByStatus(ctx, store.DeployRunning); err == nil {
		for _, d := range running {
			deploying[d.AppID] = true
		}
	}
	appList, _ := s.Store.ListApps(ctx)
	var remoteApps []*store.App
	if appList != nil {
		for _, a := range appList {
			if a.ServerID != nil {
				if !a.Stopped && !deploying[a.ID] && a.Current != nil && a.Current.Container != "" {
					remoteApps = append(remoteApps, a)
				}
				continue
			}
			if !a.Stopped && !deploying[a.ID] && a.Current != nil && a.Current.Container != "" {
				label := append([]string{"app " + a.Name}, a.Domains...)
				watched[a.Current.Container] = append(watched[a.Current.Container], label...)
			}
		}
	}
	for name, domains := range watched {
		st, exists := state[name]
		isDown := !exists || st != "running"
		switch {
		case isDown && !down[name]:
			down[name] = true
			what := "does not exist"
			if exists {
				what = "is \"" + st + "\""
			}
			s.Notify.Notify(notify.Event{
				Kind: notify.EventContainerDown, Severity: notify.Error, URL: s.panelLink(ctx, "/proxy-hosts"),
				Title:   "Container " + name + " is down",
				Message: fmt.Sprintf("Container %s %s. It affects: %s.", name, what, strings.Join(domains, ", ")),
			})
		case !isDown && down[name]:
			delete(down, name)
			s.Notify.Notify(notify.Event{
				Kind: notify.EventContainerRecovers, Severity: notify.Success, URL: s.panelLink(ctx, "/proxy-hosts"),
				Title:   "Container " + name + " is working again",
				Message: "It affects: " + strings.Join(domains, ", ") + ".",
			})
		}
	}
	// Containers no host watches any more stop counting as down (the ones on
	// remote servers are checkRemote's business: deleting them here would
	// report the outage every minute and the recovery never).
	for name := range down {
		if _, ok := watched[name]; !ok && !strings.HasPrefix(name, "server:") {
			delete(down, name)
		}
	}
	s.checkRemote(ctx, remoteApps, down)
}

// checkRemote watches the remote servers (is their agent connected?) and the
// containers of their apps.
func (s *Server) checkRemote(ctx context.Context, apps []*store.App, down map[string]bool) {
	if s.Hub == nil {
		return
	}
	servers, err := s.Store.ListServers(ctx)
	if err != nil {
		return
	}
	online := map[int64]bool{}
	for _, sv := range servers {
		_, ok := s.Hub.Online(sv.ID)
		online[sv.ID] = ok
		key := fmt.Sprintf("server:%d", sv.ID)
		switch {
		// A server that never connected is not "down": it isn't installed yet.
		case !ok && !down[key] && sv.LastSeenAt != nil:
			down[key] = true
			s.Notify.Notify(notify.Event{
				Kind: notify.EventServerDown, Severity: notify.Error, URL: s.panelLink(ctx, "/servers"),
				Title:   "Server " + sv.Name + " is disconnected",
				Message: "Its agent is not connected to the panel. Its apps keep serving with their last configuration, but they can't be deployed or monitored.",
			})
		case ok && down[key]:
			delete(down, key)
			s.Notify.Notify(notify.Event{
				Kind: notify.EventServerUp, Severity: notify.Success, URL: s.panelLink(ctx, "/servers"),
				Title: "Server " + sv.Name + " is connected again",
			})
		}
	}
	for _, a := range apps {
		if !online[*a.ServerID] {
			continue // with no connection we know nothing about its containers
		}
		info, err := s.Apps.Docker(a).InspectContainer(ctx, a.Current.Container)
		key := fmt.Sprintf("server:%d/%s", *a.ServerID, a.Current.Container)
		isDown := err != nil || !info.Running
		switch {
		case isDown && !down[key]:
			down[key] = true
			s.Notify.Notify(notify.Event{
				Kind: notify.EventContainerDown, Severity: notify.Error, URL: s.panelLink(ctx, fmt.Sprintf("/apps/%d", a.ID)),
				Title:   "App " + a.Name + " is down",
				Message: fmt.Sprintf("Its container %s is not running. It affects: %s.", a.Current.Container, strings.Join(a.Domains, ", ")),
			})
		case !isDown && down[key]:
			delete(down, key)
			s.Notify.Notify(notify.Event{
				Kind: notify.EventContainerRecovers, Severity: notify.Success, URL: s.panelLink(ctx, fmt.Sprintf("/apps/%d", a.ID)),
				Title: "App " + a.Name + " is working again",
			})
		}
	}
}

// --- Server resources ---

// resourceLimits are the resources being watched: an alert fires when usage
// reaches percent for checks consecutive checks (one per minute; disk has no
// spikes, memory and CPU do), and again once it drops resourceMargin points
// below, so sitting at the limit doesn't alert in a loop.
var resourceLimits = []struct {
	kind, label string
	percent     float64
	checks      int
	usage       func(sysinfo.Stats) float64
}{
	{"disk", "Disk", 90, 1, func(st sysinfo.Stats) float64 { return percentOf(st.DiskUsed, st.DiskTotal) }},
	{"memory", "Memory", 90, 5, func(st sysinfo.Stats) float64 { return percentOf(st.MemUsed, st.MemTotal) }},
	{"cpu", "CPU", 90, 10, func(st sysinfo.Stats) float64 { return st.CPUPercent }},
}

const resourceMargin = 5

func percentOf(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(used) / float64(total) * 100
}

// resourceWatch remembers, per server and resource, how many consecutive
// checks it has been over the limit; -1 = already alerted.
type resourceWatch map[string]int

// observe records a reading and returns the alerts it warrants.
func (w resourceWatch) observe(server string, st sysinfo.Stats) []notify.Event {
	var events []notify.Event
	for _, l := range resourceLimits {
		key, use := server+"/"+l.kind, l.usage(st)
		switch alerted := w[key] == -1; {
		case alerted && use < l.percent-resourceMargin:
			delete(w, key)
			events = append(events, notify.Event{Kind: notify.EventResourceNormal, Severity: notify.Success,
				Title: fmt.Sprintf("%s on %s is back to normal levels (%.0f %%)", l.label, server, use)})
		case alerted:
		case use < l.percent:
			delete(w, key)
		default:
			if w[key]++; w[key] >= l.checks {
				w[key] = -1
				events = append(events, notify.Event{Kind: notify.EventResourceHigh, Severity: notify.Warning,
					Title:   fmt.Sprintf("%s on %s is at %.0f %%", l.label, server, use),
					Message: fmt.Sprintf("That is over the %.0f %% considered safe. With the disk or the memory full, deployments and databases fail.", l.percent)})
			}
		}
	}
	return events
}

// checkResources alerts when a server's disk, memory or CPU (the panel's own
// and the connected remote ones) get close to the limit.
func (s *Server) checkResources(ctx context.Context, w resourceWatch) {
	if s.Notify == nil {
		return
	}
	link := s.panelLink(ctx, "/servers")
	send := func(server string, st sysinfo.Stats) {
		for _, ev := range w.observe(server, st) {
			ev.URL = link
			s.Notify.Notify(ev)
		}
	}
	if st, err := sysinfo.Read(ctx, s.DataDir); err == nil {
		send("this server", st)
	}
	if s.Hub == nil {
		return
	}
	servers, _ := s.Store.ListServers(ctx)
	for _, sv := range servers {
		// With no connection (or an old agent) there is no reading: nothing is
		// alerted and nothing is considered resolved.
		if st, err := s.remoteStats(ctx, sv.ID); err == nil {
			send(sv.Name, st)
		}
	}
}

// OnAppJobFailed reports a scheduled task that ended badly.
func (s *Server) OnAppJobFailed(ctx context.Context, app *store.App, job *store.AppJob, exitCode int) {
	if s.Notify == nil {
		return
	}
	what := fmt.Sprintf("exited with code %d", exitCode)
	if exitCode < 0 {
		what = "couldn't be run or went over the time limit"
	}
	s.Notify.Notify(notify.Event{
		Kind: notify.EventJobFailed, Severity: notify.Error, URL: s.panelLink(ctx, fmt.Sprintf("/apps/%d", app.ID)),
		Title:   "A scheduled task of " + app.Name + " failed",
		Message: fmt.Sprintf("%q %s.", job.Command, what),
	})
}

package api

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dockfly/dockfly/internal/notify"
	"github.com/dockfly/dockfly/internal/store"
	"github.com/dockfly/dockfly/internal/traefik"
)

// Cada cuánto se revisan certificados y contenedores.
const (
	certCheckEvery      = 12 * time.Hour
	containerCheckEvery = time.Minute
)

// OnBuildFinished avisa del resultado de un build (lo llama el builder).
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
			Title:   fmt.Sprintf("El build #%d de %s falló", b.ID, site.Name),
			Message: fmt.Sprintf("%s (%s)\n%s", commit, b.Branch, b.Error),
		})
	case store.BuildSuccess:
		s.Notify.Notify(notify.Event{
			Kind: notify.EventBuildSucceeded, Severity: notify.Success, URL: link,
			Title:   fmt.Sprintf("%s publicado", site.Name),
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

// OnAppDeployFinished avisa del resultado de un despliegue de app.
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
			Title:   fmt.Sprintf("El despliegue #%d de %s falló", d.ID, a.Name),
			Message: fmt.Sprintf("%s\n%s\nLa versión anterior sigue en marcha.", what, d.Error)})
	case store.DeployLive:
		s.Notify.Notify(notify.Event{Kind: notify.EventBuildSucceeded, Severity: notify.Success, URL: link,
			Title: a.Name + " desplegada", Message: fmt.Sprintf("Despliegue #%d · %s", d.ID, what)})
	}
}

// OnCertError avisa de un fallo de Let's Encrypt. Traefik reintenta cada
// poco: el mismo dominio solo se avisa una vez al día.
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
			Title: "No se pudo obtener el certificado de " + d, Message: text,
		})
	}
}

// RunMonitors revisa periódicamente certificados y contenedores hasta que
// ctx se cancela.
func (s *Server) RunMonitors(ctx context.Context) {
	certs := time.NewTicker(certCheckEvery)
	containers := time.NewTicker(containerCheckEvery)
	defer certs.Stop()
	defer containers.Stop()
	down := map[string]bool{}

	// Primera revisión al poco de arrancar (Traefik ya cargó la configuración).
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
		}
	}
}

// checkCertificates avisa de los certificados que caducan pronto (la
// renovación automática está fallando) o ya caducaron.
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
		title := "El certificado de " + c.Domain + " ha caducado"
		if c.Status == traefik.CertExpiring {
			days := int(time.Until(*c.NotAfter).Hours() / 24)
			title = fmt.Sprintf("El certificado de %s caduca en %d días", c.Domain, days)
		}
		msg := "Traefik debería haberlo renovado: la renovación está fallando."
		if c.Hint != "" {
			msg += "\n" + c.Hint
		}
		s.Notify.Notify(notify.Event{Kind: notify.EventCertExpiring, Severity: notify.Warning, Title: title, Message: msg,
			URL: s.panelLink(ctx, "/proxy-hosts")})
	}
}

// checkContainers avisa cuando cae (y cuando se recupera) un contenedor al
// que apunta un proxy host. down recuerda los caídos entre revisiones, para
// avisar una sola vez por caída.
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
		return // Docker no responde: no es culpa de los contenedores
	}
	state := map[string]string{}
	for _, c := range containers {
		state[c.Name] = c.State
	}
	watched := map[string][]string{} // contenedor → dominios que lo usan
	for _, h := range hosts {
		if h.Enabled && h.TargetType == store.TargetDocker {
			watched[h.ForwardHost] = append(watched[h.ForwardHost], h.Domains...)
		}
	}
	// Y el contenedor actual de cada app en marcha. Las que se están
	// desplegando se saltan: con volúmenes, su contenedor se detiene a
	// propósito unos segundos.
	deploying := map[int64]bool{}
	if running, err := s.Store.ListAppDeploymentsByStatus(ctx, store.DeployRunning); err == nil {
		for _, d := range running {
			deploying[d.AppID] = true
		}
	}
	if appList, err := s.Store.ListApps(ctx); err == nil {
		for _, a := range appList {
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
			what := "no existe"
			if exists {
				what = "está «" + st + "»"
			}
			s.Notify.Notify(notify.Event{
				Kind: notify.EventContainerDown, Severity: notify.Error, URL: s.panelLink(ctx, "/proxy-hosts"),
				Title:   "El contenedor " + name + " está caído",
				Message: fmt.Sprintf("El contenedor %s %s. Afecta a: %s.", name, what, strings.Join(domains, ", ")),
			})
		case !isDown && down[name]:
			delete(down, name)
			s.Notify.Notify(notify.Event{
				Kind: notify.EventContainerRecovers, Severity: notify.Success, URL: s.panelLink(ctx, "/proxy-hosts"),
				Title:   "El contenedor " + name + " vuelve a funcionar",
				Message: "Afecta a: " + strings.Join(domains, ", ") + ".",
			})
		}
	}
	// Los que ya no vigila ningún host dejan de contar como caídos.
	for name := range down {
		if _, ok := watched[name]; !ok {
			delete(down, name)
		}
	}
}

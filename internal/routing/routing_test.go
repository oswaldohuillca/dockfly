package routing

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dockfly/dockfly/internal/store"
	"github.com/dockfly/dockfly/internal/traefik"
)

func id(n int64) *int64 { return &n }

func list(n int64) *store.AccessList {
	return &store.AccessList{ID: n, AllowIPs: []string{"10.0.0.0/8"}}
}

// base es un snapshot sin nada publicado y sin certificados especiales.
func base() Snapshot {
	return Snapshot{
		StaticURL: "http://127.0.0.1:8099",
		SiteDir: func(site, dep int64) string {
			return "/sites/" + strconv.FormatInt(site, 10) + "/" + strconv.FormatInt(dep, 10)
		},
		Resolver: func([]string) string { return "" },
	}
}

func route(t *testing.T, p Plan, name string) traefik.Route {
	t.Helper()
	for _, r := range p.Dynamic.Routes {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("no hay ruta %s en %v", name, names(p))
	return traefik.Route{}
}

func names(p Plan) []string {
	var out []string
	for _, r := range p.Dynamic.Routes {
		out = append(out, r.Name)
	}
	return out
}

// Todo lo que protege una lista en el plan cuenta como uso de esa lista: si
// no, se podría borrar una lista en uso y la ruta pasaría a denegarlo todo.
func TestEveryProtectedRouteCountsAsUsage(t *testing.T) {
	s := base()
	s.AccessLists = []*store.AccessList{list(1), list(2), list(3), list(4), list(5), list(6), list(7)}
	s.Hosts = []*store.ProxyHost{{
		ID: 1, Enabled: true, Domains: []string{"h.test"}, ForwardScheme: "http", ForwardHost: "x", ForwardPort: 80,
		AccessListID: id(1),
		Locations:    []store.ProxyLocation{{Path: "/api", ForwardScheme: "http", ForwardHost: "api", ForwardPort: 80, Access: store.LocationAccessList, AccessListID: id(2)}},
	}}
	s.Sites = []*store.Site{{
		ID: 1, Name: "web", Domains: []string{"web.test"}, AccessListID: id(3), Previews: true,
		Locations: []store.ProxyLocation{{Path: "/api", ForwardScheme: "http", ForwardHost: "api", ForwardPort: 80, Access: store.LocationAccessList, AccessListID: id(4)}},
	}}
	s.Apps = []*store.App{{ID: 1, Port: 80, Domains: []string{"app.test"}, AccessListID: id(5), Current: &store.AppDeployment{Container: "c"}}}
	s.Previews = Previews{Domain: "pv.test", AccessListID: id(6), BySite: map[int64][]*store.Deployment{1: {{ID: 9, Preview: "rama"}}}}

	plan := Build(s)
	used := AccessListUsage(s)
	protected := map[int64]bool{}
	for _, r := range plan.Dynamic.Routes {
		for _, mw := range r.Middlewares {
			if n, ok := strings.CutPrefix(mw, "acl-"); ok {
				v, _ := strconv.ParseInt(strings.Split(n, "-")[0], 10, 64)
				protected[v] = true
			}
		}
	}
	if len(protected) != 6 {
		t.Fatalf("listas en las rutas: %v", protected)
	}
	for v := range protected {
		if used[v] == 0 {
			t.Errorf("la lista %d protege una ruta pero no cuenta como usada: %v", v, used)
		}
	}
	if used[7] != 0 {
		t.Errorf("la lista 7 no la usa nadie: %v", used)
	}
}

// Una lista borrada no deja la ruta pública: la deniega.
func TestMissingAccessListDenies(t *testing.T) {
	s := base()
	s.Sites = []*store.Site{{ID: 1, Domains: []string{"web.test"}, AccessListID: id(42)}}
	if mw := route(t, Build(s), "site-1").Middlewares; !slices.Equal(mw, []string{traefik.MwDeny}) {
		t.Errorf("middlewares = %v", mw)
	}
}

// Las previews de un sitio protegido usan el certificado comodín, no uno
// por host.
func TestProtectedPreviewUsesWildcardCertificate(t *testing.T) {
	s := base()
	s.Resolver = func([]string) string { return traefik.DNSResolver("cloudflare") }
	s.AccessLists = []*store.AccessList{list(1)}
	s.Sites = []*store.Site{{ID: 1, Name: "web", Domains: []string{"web.test"}, AccessListID: id(1), Previews: true}}
	s.Previews = Previews{Domain: "pv.test", BySite: map[int64][]*store.Deployment{1: {{ID: 9, Preview: "rama"}}}}
	plan := Build(s)

	if w := route(t, plan, PreviewRouteName); !w.SSL || w.Resolver != traefik.DNSResolver("cloudflare") {
		t.Errorf("comodín: %+v", w)
	}
	p := route(t, plan, "preview-1-rama--web.pv.test")
	if !p.SSL || p.Resolver != traefik.NoResolver || !slices.Equal(p.Middlewares, []string{"acl-1-ip"}) {
		t.Errorf("preview protegida: %+v", p)
	}
	if r := plan.Static["rama--web.pv.test"]; r.DeploymentID != 9 || r.Dir != "/sites/1/9" {
		t.Errorf("tabla estática: %+v", r)
	}
}

// Las apps de un servidor remoto van a su Traefik, no al de este servidor;
// las paradas o sin desplegar no se publican.
func TestAppsRemoteAndStopped(t *testing.T) {
	s := base()
	s.Apps = []*store.App{
		{ID: 1, Port: 80, Domains: []string{"a.test"}, Current: &store.AppDeployment{Container: "a"}},
		{ID: 2, Port: 80, Domains: []string{"b.test"}, Current: &store.AppDeployment{Container: "b"}, ServerID: id(7)},
		{ID: 3, Port: 80, Domains: []string{"c.test"}, Current: &store.AppDeployment{Container: "c"}, Stopped: true},
		{ID: 4, Port: 80, Domains: []string{"d.test"}},
	}
	plan := Build(s)
	if got := names(plan); !slices.Equal(got, []string{"app-1"}) {
		t.Errorf("rutas locales: %v", got)
	}
	if r := plan.Remote[7]; len(r) != 1 || r[0].Name != "app-2" || r[0].URL != "http://b:80" {
		t.Errorf("rutas remotas: %+v", plan.Remote)
	}
}

// Las ubicaciones de un sitio van a su backend, sin la compresión del sitio.
func TestSiteLocations(t *testing.T) {
	s := base()
	s.Sites = []*store.Site{{ID: 1, Domains: []string{"web.test"}, Locations: []store.ProxyLocation{
		{Path: "/api", StripPrefix: true, ForwardScheme: "http", ForwardHost: "api", ForwardPort: 3000, Access: store.LocationAccessPublic},
	}}}
	loc := route(t, Build(s), "site-1-loc0")
	if loc.PathPrefix != "/api" || !loc.StripPrefix || loc.URL != "http://api:3000" || loc.Compress || len(loc.Middlewares) != 0 {
		t.Errorf("ubicación: %+v", loc)
	}
}

func TestPreviewHost(t *testing.T) {
	if h := PreviewHost("previews.test", "web", "feature-x"); h != "feature-x--web.previews.test" {
		t.Errorf("host: %s", h)
	}
	// La etiqueta nunca pasa de 63 caracteres, aunque el sitio tenga un nombre largo.
	h := PreviewHost("p.test", strings.Repeat("s", 60), strings.Repeat("b", 40))
	if label := strings.TrimSuffix(h, ".p.test"); len(label) > 63 || strings.HasSuffix(label, "-") {
		t.Errorf("etiqueta de %d caracteres: %s", len(label), label)
	}
}

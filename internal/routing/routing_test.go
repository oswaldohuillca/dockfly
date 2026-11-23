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

// base is a snapshot with nothing published and no special certificates.
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
	t.Fatalf("no route %s in %v", name, names(p))
	return traefik.Route{}
}

func names(p Plan) []string {
	var out []string
	for _, r := range p.Dynamic.Routes {
		out = append(out, r.Name)
	}
	return out
}

// Everything a list protects in the plan counts as a use of that list;
// otherwise a list in use could be deleted and its route would start denying
// everything.
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
		t.Fatalf("lists on the routes: %v", protected)
	}
	for v := range protected {
		if used[v] == 0 {
			t.Errorf("list %d protects a route but does not count as used: %v", v, used)
		}
	}
	if used[7] != 0 {
		t.Errorf("nobody uses list 7: %v", used)
	}
}

// A deleted list does not leave the route public: it denies it.
func TestMissingAccessListDenies(t *testing.T) {
	s := base()
	s.Sites = []*store.Site{{ID: 1, Domains: []string{"web.test"}, AccessListID: id(42)}}
	if mw := route(t, Build(s), "site-1").Middlewares; !slices.Equal(mw, []string{traefik.MwDeny}) {
		t.Errorf("middlewares = %v", mw)
	}
}

// The previews of a protected site use the wildcard certificate, not a
// per-host one.
func TestProtectedPreviewUsesWildcardCertificate(t *testing.T) {
	s := base()
	s.Resolver = func([]string) string { return traefik.DNSResolver("cloudflare") }
	s.AccessLists = []*store.AccessList{list(1)}
	s.Sites = []*store.Site{{ID: 1, Name: "web", Domains: []string{"web.test"}, AccessListID: id(1), Previews: true}}
	s.Previews = Previews{Domain: "pv.test", BySite: map[int64][]*store.Deployment{1: {{ID: 9, Preview: "rama"}}}}
	plan := Build(s)

	if w := route(t, plan, PreviewRouteName); !w.SSL || w.Resolver != traefik.DNSResolver("cloudflare") {
		t.Errorf("wildcard: %+v", w)
	}
	p := route(t, plan, "preview-1-rama--web.pv.test")
	if !p.SSL || p.Resolver != traefik.NoResolver || !slices.Equal(p.Middlewares, []string{"acl-1-ip"}) {
		t.Errorf("protected preview: %+v", p)
	}
	if r := plan.Static["rama--web.pv.test"]; r.DeploymentID != 9 || r.Dir != "/sites/1/9" {
		t.Errorf("static table: %+v", r)
	}
}

// When there is a global preview access list, the per-host router of a site
// with its own list must require both (otherwise the site's credentials would
// bypass the global gate).
func TestPreviewChainsGlobalAndSiteACL(t *testing.T) {
	s := base()
	s.AccessLists = []*store.AccessList{list(1), list(6)}
	s.Sites = []*store.Site{{ID: 1, Name: "web", Domains: []string{"web.test"}, AccessListID: id(1), Previews: true}}
	s.Previews = Previews{Domain: "pv.test", AccessListID: id(6), BySite: map[int64][]*store.Deployment{1: {{ID: 9, Preview: "rama"}}}}
	plan := Build(s)

	if w := route(t, plan, PreviewRouteName).Middlewares; !slices.Equal(w, []string{"acl-6-ip"}) {
		t.Errorf("the wildcard uses the global list: %v", w)
	}
	if p := route(t, plan, "preview-1-rama--web.pv.test").Middlewares; !slices.Equal(p, []string{"acl-6-ip", "acl-1-ip"}) {
		t.Errorf("the per-host router requires global + site: %v", p)
	}
}

// Apps on a remote server go to its Traefik, not to this server's; stopped or
// undeployed ones are not published.
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
		t.Errorf("local routes: %v", got)
	}
	if r := plan.Remote[7]; len(r) != 1 || r[0].Name != "app-2" || r[0].URL != "http://b:80" {
		t.Errorf("remote routes: %+v", plan.Remote)
	}
}

// A site's locations go to its backend, without the site's compression.
func TestSiteLocations(t *testing.T) {
	s := base()
	s.Sites = []*store.Site{{ID: 1, Domains: []string{"web.test"}, Locations: []store.ProxyLocation{
		{Path: "/api", StripPrefix: true, ForwardScheme: "http", ForwardHost: "api", ForwardPort: 3000, Access: store.LocationAccessPublic},
	}}}
	loc := route(t, Build(s), "site-1-loc0")
	if loc.PathPrefix != "/api" || !loc.StripPrefix || loc.URL != "http://api:3000" || loc.Compress || len(loc.Middlewares) != 0 {
		t.Errorf("location: %+v", loc)
	}
}

func TestPreviewHost(t *testing.T) {
	if h := PreviewHost("previews.test", "web", "feature-x"); h != "feature-x--web.previews.test" {
		t.Errorf("host: %s", h)
	}
	// The label never goes over 63 characters, even if the site has a long name.
	h := PreviewHost("p.test", strings.Repeat("s", 60), strings.Repeat("b", 40))
	if label := strings.TrimSuffix(h, ".p.test"); len(label) > 63 || strings.HasSuffix(label, "-") {
		t.Errorf("label of %d characters: %s", len(label), label)
	}
}

// Package routing decides what Dockfly publishes: from the configuration
// (proxy hosts, sites, apps, redirects, streams, access lists and previews)
// it computes the Traefik routes and the static server's table.
//
// It is a pure function: it neither reads the database nor writes files. The
// caller gathers a Snapshot and applies the Plan.
package routing

import (
	"fmt"
	"net"
	"slices"
	"strconv"

	"github.com/dockfly/dockfly/internal/pages"
	"github.com/dockfly/dockfly/internal/store"
	"github.com/dockfly/dockfly/internal/traefik"
)

// PreviewRouteName is the Traefik route of the preview wildcard.
const PreviewRouteName = "previews"

// Snapshot is everything that affects routing.
type Snapshot struct {
	Hosts       []*store.ProxyHost
	Sites       []*store.Site
	Apps        []*store.App
	Redirects   []*store.RedirectHost
	Streams     []*store.Stream
	AccessLists []*store.AccessList
	Previews    Previews
	// Certificates are the user's own certificates, already on disk.
	Certificates []traefik.CertFile

	// StaticURL is the address of the internal static server.
	StaticURL string
	// SiteDir gives the directory of a site deployment.
	SiteDir func(siteID, deploymentID int64) string
	// Resolver picks the certificate for a set of domains (see traefik.Route.Resolver).
	Resolver func(domains []string) string
}

// Previews are the published branches of the sites that have previews on.
type Previews struct {
	// Domain is the preview domain ("" = previews disabled).
	Domain string
	// AccessListID protects every preview (on top of its site's own list).
	AccessListID *int64
	// BySite holds the current deployment of each preview, keyed by site.
	BySite map[int64][]*store.Deployment
}

// Plan is the result: what to apply in Traefik and in the static server.
type Plan struct {
	// Dynamic is this server's Traefik configuration.
	Dynamic traefik.Dynamic
	// Remote holds the app routes of each remote server (they use the same
	// middlewares as Dynamic).
	Remote map[int64][]traefik.Route
	// Static is the static server's table: domain → deployment.
	Static map[string]pages.Route
}

// PreviewHost is the preview domain of a site, kept within the 63 characters
// a DNS label allows.
func PreviewHost(domain, site, preview string) string {
	if room := 63 - len(site) - 2; len(preview) > room {
		preview = trimDash(preview[:max(room, 1)])
	}
	label := preview + "--" + site
	if len(label) > 63 {
		label = trimDash(label[:63])
	}
	return label + "." + domain
}

func trimDash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '-' {
		s = s[:len(s)-1]
	}
	return s
}

// RedirectRouteName is the Traefik route of a redirect.
func RedirectRouteName(id int64) string { return "rd-" + strconv.FormatInt(id, 10) }

// Build computes the plan.
func Build(s Snapshot) Plan {
	b := builder{snap: s, middlewares: map[string]any{}, acl: map[int64][]string{}}
	for _, l := range s.AccessLists {
		users := map[string]string{}
		for _, u := range l.Users {
			users[u.Username] = u.PasswordHash
		}
		b.acl[l.ID] = traefik.AccessListMiddlewares(l.ID, l.AllowIPs, users, b.middlewares)
	}
	plan := Plan{Remote: map[int64][]traefik.Route{}, Static: map[string]pages.Route{}}
	var routes []traefik.Route

	for _, h := range s.Hosts {
		rt, ok := traefik.ProxyHostRoute(h)
		if !ok {
			continue
		}
		routes = append(routes, b.withACL(rt, h.AccessListID))
		routes = append(routes, b.locations(rt, h.Locations, h.AccessListID, h.SkipTLSVerify)...)
	}

	// Apps that are deployed, running and have a port and domains: Traefik
	// sends the traffic to their current container (it changes on every
	// deployment). Apps on remote servers go to their server's Traefik.
	for _, a := range s.Apps {
		if a.Stopped || a.Port == 0 || len(a.Domains) == 0 || a.Current == nil || a.Current.Container == "" {
			continue
		}
		rt := b.withACL(traefik.Route{
			Name: "app-" + strconv.FormatInt(a.ID, 10), Domains: a.Domains,
			URL:          fmt.Sprintf("http://%s:%d", a.Current.Container, a.Port),
			PreserveHost: true, SSL: a.SSL, ForceHTTPS: a.ForceHTTPS, HSTS: a.HSTS,
		}, a.AccessListID)
		if a.ServerID != nil {
			plan.Remote[*a.ServerID] = append(plan.Remote[*a.ServerID], rt)
			continue
		}
		routes = append(routes, rt)
	}

	for _, rd := range s.Redirects {
		if rd.Enabled {
			routes = append(routes, traefik.Route{
				Name: RedirectRouteName(rd.ID), Domains: rd.Domains, SSL: rd.SSL,
				Redirect: &traefik.Redirect{Target: rd.Target, Permanent: rd.Permanent, PreservePath: rd.PreservePath},
			})
		}
	}

	for _, site := range s.Sites {
		if rt, ok := traefik.SiteRoute(site, s.StaticURL); ok {
			routes = append(routes, b.withACL(rt, site.AccessListID))
			// A location's target is a backend, not the static server: let it
			// compress if it wants to (and leave text/event-stream alone).
			rt.Compress = false
			routes = append(routes, b.locations(rt, site.Locations, site.AccessListID, false)...)
		}
		var pr pages.Route
		if id := site.ActiveDeploymentID; id != nil {
			pr = pages.Route{Dir: s.SiteDir(site.ID, *id), DeploymentID: *id, SPA: site.SPA}
		}
		for _, d := range site.Domains {
			plan.Static[d] = pr
		}
	}
	routes = append(routes, b.previews(plan.Static)...)

	// Every SSL route uses the certificate that fits it: a user-provided one,
	// a DNS one (from the configured zones) or an HTTP one, unless it already
	// comes with one chosen.
	for i := range routes {
		if routes[i].SSL && routes[i].Resolver == "" {
			routes[i].Resolver = s.Resolver(routes[i].Domains)
		}
	}

	var streams []traefik.Stream
	for _, st := range s.Streams {
		if st.Enabled {
			streams = append(streams, traefik.Stream{
				Name:    "st-" + strconv.FormatInt(st.ID, 10),
				Port:    traefik.StreamPort{Protocol: st.Protocol, Port: st.ListenPort},
				Address: net.JoinHostPort(st.ForwardHost, strconv.Itoa(st.ForwardPort)),
			})
		}
	}
	plan.Dynamic = traefik.Dynamic{Routes: routes, Middlewares: b.middlewares, Streams: streams, Certificates: s.Certificates}
	return plan
}

type builder struct {
	snap        Snapshot
	middlewares map[string]any
	acl         map[int64][]string
}

// withACL applies the access list id to a route. If the list no longer exists
// (it was deleted), deny everything instead of leaving the route public.
func (b *builder) withACL(rt traefik.Route, id *int64) traefik.Route {
	if id != nil {
		if mw, ok := b.acl[*id]; ok {
			rt.Middlewares = mw
		} else {
			rt.Middlewares = []string{traefik.MwDeny}
		}
	}
	return rt
}

// chainACL chains several access lists on one route: the request must pass
// all of them (for example the global preview gate and the site's own list).
// A deleted list denies everything. nil is ignored; duplicates are skipped.
func (b *builder) chainACL(ids ...*int64) []string {
	var mw []string
	for _, id := range ids {
		if id == nil {
			continue
		}
		m, ok := b.acl[*id]
		if !ok {
			return []string{traefik.MwDeny}
		}
		for _, name := range m {
			if !slices.Contains(mw, name) {
				mw = append(mw, name)
			}
		}
	}
	return mw
}

// locations translates the locations of a proxy host or a site (parent,
// already carrying its name and domains). Each one is one more route on the
// same domain, limited to its prefix, with its own target and access; Traefik
// prefers it over the domain's route because its rule is longer (or, with
// wildcards, because of the priority traefik.BuildDynamic gives it).
func (b *builder) locations(parent traefik.Route, locations []store.ProxyLocation, parentACL *int64, skipTLSVerify bool) []traefik.Route {
	routes := make([]traefik.Route, 0, len(locations))
	for i, l := range locations {
		loc := parent
		loc.Name = fmt.Sprintf("%s-loc%d", parent.Name, i)
		loc.PathPrefix, loc.StripPrefix = l.Path, l.StripPrefix
		loc.URL = traefik.LocationURL(l)
		loc.SkipTLSVerify = l.ForwardScheme == "https" && skipTLSVerify
		loc.Middlewares = nil
		switch l.Access {
		case store.LocationAccessPublic:
		case store.LocationAccessList:
			loc = b.withACL(loc, l.AccessListID)
		default:
			loc = b.withACL(loc, parentACL)
		}
		routes = append(routes, loc)
	}
	return routes
}

// previews adds each site's previews to the static table and returns their
// routes: the wildcard and, for sites with an access list, a per-host router
// carrying that list (it beats the wildcard on priority).
func (b *builder) previews(table map[string]pages.Route) []traefik.Route {
	p := b.snap.Previews
	if p.Domain == "" {
		return nil
	}
	wildcard := b.withACL(traefik.Route{
		Name: PreviewRouteName, Domains: []string{"*." + p.Domain}, URL: b.snap.StaticURL,
		PreserveHost: true, Compress: true,
	}, p.AccessListID)
	// With a wildcard certificate (DNS zone or user-provided), previews go
	// over HTTPS.
	if b.snap.Resolver(wildcard.Domains) != "" {
		wildcard.SSL, wildcard.ForceHTTPS = true, true
	}
	routes := []traefik.Route{wildcard}
	for _, site := range b.snap.Sites {
		if !site.Previews {
			continue
		}
		for _, d := range p.BySite[site.ID] {
			host := PreviewHost(p.Domain, site.Name, d.Preview)
			if _, taken := table[host]; !taken {
				table[host] = pages.Route{Dir: b.snap.SiteDir(site.ID, d.ID), DeploymentID: d.ID, SPA: site.SPA}
			}
			if site.AccessListID == nil {
				continue
			}
			// The per-host router beats the wildcard on priority, so it has to
			// repeat the global preview gate on top of the site's one; otherwise
			// the site's credentials would bypass the global list.
			rt := traefik.Route{
				Name: fmt.Sprintf("preview-%d-%s", site.ID, host), Domains: []string{host},
				URL: b.snap.StaticURL, PreserveHost: true, Compress: true,
				SSL: wildcard.SSL, ForceHTTPS: wildcard.SSL,
			}
			rt.Middlewares = b.chainACL(p.AccessListID, site.AccessListID)
			if rt.SSL {
				rt.Resolver = traefik.NoResolver // the wildcard already has the certificate
			}
			routes = append(routes, rt)
		}
	}
	return routes
}

// AccessListUsage counts how many things use each access list: proxy hosts,
// sites, apps and their locations, plus the preview list. It also counts what
// is not published right now (a stopped app still uses its list).
func AccessListUsage(s Snapshot) map[int64]int {
	used := map[int64]int{}
	ref := func(id *int64) {
		if id != nil {
			used[*id]++
		}
	}
	locations := func(ls []store.ProxyLocation) {
		for _, l := range ls {
			if l.Access == store.LocationAccessList {
				ref(l.AccessListID)
			}
		}
	}
	for _, h := range s.Hosts {
		ref(h.AccessListID)
		locations(h.Locations)
	}
	for _, site := range s.Sites {
		ref(site.AccessListID)
		locations(site.Locations)
	}
	for _, a := range s.Apps {
		ref(a.AccessListID)
	}
	ref(s.Previews.AccessListID)
	return used
}

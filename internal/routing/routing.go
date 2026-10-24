// Package routing decide qué publica Dockfly: a partir de lo configurado
// (proxy hosts, sitios, apps, redirecciones, streams, listas de acceso y
// previews) calcula las rutas de Traefik y la tabla del servidor estático.
//
// Es una función pura: no lee la base de datos ni escribe archivos. Quien la
// llama reúne un Snapshot y aplica el Plan.
package routing

import (
	"fmt"
	"net"
	"strconv"

	"github.com/dockfly/dockfly/internal/pages"
	"github.com/dockfly/dockfly/internal/store"
	"github.com/dockfly/dockfly/internal/traefik"
)

// PreviewRouteName es la ruta de Traefik del comodín de previews.
const PreviewRouteName = "previews"

// Snapshot es todo lo que influye en el enrutado.
type Snapshot struct {
	Hosts       []*store.ProxyHost
	Sites       []*store.Site
	Apps        []*store.App
	Redirects   []*store.RedirectHost
	Streams     []*store.Stream
	AccessLists []*store.AccessList
	Previews    Previews
	// Certificates son los certificados propios, ya en disco.
	Certificates []traefik.CertFile

	// StaticURL es la dirección del servidor estático interno.
	StaticURL string
	// SiteDir da la carpeta de un despliegue de un sitio.
	SiteDir func(siteID, deploymentID int64) string
	// Resolver elige el certificado de unos dominios (ver traefik.Route.Resolver).
	Resolver func(domains []string) string
}

// Previews son las ramas publicadas de los sitios con previews.
type Previews struct {
	// Domain es el dominio de previews ("" = desactivadas).
	Domain string
	// AccessListID protege todas las previews (además de la lista de su sitio).
	AccessListID *int64
	// BySite son los despliegues vigentes de cada preview, por sitio.
	BySite map[int64][]*store.Deployment
}

// Plan es el resultado: qué aplicar en Traefik y en el servidor estático.
type Plan struct {
	// Dynamic es la configuración de Traefik de este servidor.
	Dynamic traefik.Dynamic
	// Remote son las rutas de las apps de cada servidor remoto (usan los
	// mismos middlewares que Dynamic).
	Remote map[int64][]traefik.Route
	// Static es la tabla del servidor estático: dominio → despliegue.
	Static map[string]pages.Route
}

// PreviewHost es el dominio de la preview de un sitio, sin pasar de los 63
// caracteres que permite una etiqueta DNS.
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

// RedirectRouteName es la ruta de Traefik de una redirección.
func RedirectRouteName(id int64) string { return "rd-" + strconv.FormatInt(id, 10) }

// Build calcula el plan.
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

	// Apps desplegadas, en marcha y con puerto y dominios: Traefik envía el
	// tráfico a su contenedor actual (cambia en cada despliegue). Las de los
	// servidores remotos van al Traefik de su servidor.
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
			// El destino de una ubicación es un backend, no el servidor
			// estático: comprime él si quiere (y no se toca un text/event-stream).
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

	// Cada ruta con SSL usa el certificado que le corresponde: propio, por
	// DNS (zonas configuradas) o por HTTP, salvo que ya traiga uno elegido.
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

// withACL aplica la lista de acceso id a una ruta. Si la lista ya no existe
// (se borró), deniega todo en vez de dejar la ruta pública.
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

// locations traduce las ubicaciones de un proxy host o de un sitio (parent,
// ya con su nombre y sus dominios). Cada una es una ruta más del mismo
// dominio, limitada a su prefijo, con su destino y su acceso; Traefik la
// prefiere a la de su dominio porque su regla es más larga (o, con
// comodines, por la prioridad que le da traefik.BuildDynamic).
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

// previews añade a la tabla estática las previews de cada sitio y devuelve
// sus rutas: el comodín y, para los sitios con lista de acceso, un router
// por host con esa lista (gana al comodín por prioridad).
func (b *builder) previews(table map[string]pages.Route) []traefik.Route {
	p := b.snap.Previews
	if p.Domain == "" {
		return nil
	}
	wildcard := b.withACL(traefik.Route{
		Name: PreviewRouteName, Domains: []string{"*." + p.Domain}, URL: b.snap.StaticURL,
		PreserveHost: true, Compress: true,
	}, p.AccessListID)
	// Con certificado comodín (zona DNS o propio), las previews van por HTTPS.
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
			rt := b.withACL(traefik.Route{
				Name: fmt.Sprintf("preview-%d-%s", site.ID, host), Domains: []string{host},
				URL: b.snap.StaticURL, PreserveHost: true, Compress: true,
				SSL: wildcard.SSL, ForceHTTPS: wildcard.SSL,
			}, site.AccessListID)
			if rt.SSL {
				rt.Resolver = traefik.NoResolver // el comodín ya tiene el certificado
			}
			routes = append(routes, rt)
		}
	}
	return routes
}

// AccessListUsage cuenta cuántas cosas usan cada lista de acceso: proxy
// hosts, sitios, apps y sus ubicaciones, y la lista de las previews. Cuenta
// también lo que ahora no se publica (una app parada sigue usando su lista).
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

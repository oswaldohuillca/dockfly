package traefik

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/dockfly/dockfly/internal/store"
)

func TestBuildDynamicEmpty(t *testing.T) {
	httpCfg := BuildDynamic(nil, nil)["http"].(map[string]any)
	if _, ok := httpCfg["routers"]; ok {
		t.Error("with no hosts there must be no routers section (Traefik rejects an empty one)")
	}
}

func TestBuildDynamic(t *testing.T) {
	hosts := []*store.ProxyHost{
		{ID: 1, Domains: []string{"a.com", "www.a.com"}, ForwardScheme: "http", ForwardHost: "web", ForwardPort: 3000, PreserveHost: true,
			SSL: true, ForceHTTPS: true, HSTS: true, Enabled: true},
		{ID: 2, Domains: []string{"b.com"}, ForwardScheme: "https", ForwardHost: "10.0.0.5", ForwardPort: 8443,
			SkipTLSVerify: true, Enabled: true},
		{ID: 3, Domains: []string{"off.com"}, ForwardScheme: "http", ForwardHost: "x", ForwardPort: 80, Enabled: false},
		{ID: 4, Domains: []string{"v6.com"}, ForwardScheme: "http", ForwardHost: "fd00::1", ForwardPort: 80, Enabled: true},
	}
	var routes []Route
	for _, h := range hosts {
		if rt, ok := ProxyHostRoute(h); ok {
			routes = append(routes, rt)
		}
	}
	httpCfg := BuildDynamic(routes, nil)["http"].(map[string]any)
	routers := httpCfg["routers"].(map[string]any)
	services := httpCfg["services"].(map[string]any)

	if _, ok := routers["ph-3"]; ok {
		t.Error("a disabled host must not generate routers")
	}

	r1 := routers["ph-1"].(map[string]any)
	if r1["rule"] != "Host(`a.com`) || Host(`www.a.com`)" {
		t.Errorf("unexpected rule: %v", r1["rule"])
	}
	if mw := r1["middlewares"].([]string); mw[0] != mwRedirectHTTPS {
		t.Errorf("the HTTP router must redirect to HTTPS, it has %v", mw)
	}
	sec := routers["ph-1-secure"].(map[string]any)
	if sec["tls"].(map[string]any)["certResolver"] != CertResolver {
		t.Error("the secure router must use the ACME resolver")
	}

	if _, ok := routers["ph-2-secure"]; ok {
		t.Error("without SSL there must be no secure router")
	}
	lb2 := services["ph-2"].(map[string]any)["loadBalancer"].(map[string]any)
	if lb2["passHostHeader"] != false {
		t.Error("without preserve_host, Traefik must send the target's Host")
	}
	if lb2["serversTransport"] != transportNoCheck {
		t.Error("skip_tls_verify must use the insecure transport")
	}
	if _, ok := httpCfg["serversTransports"]; !ok {
		t.Error("serversTransports is not defined")
	}

	lb4 := services["ph-4"].(map[string]any)["loadBalancer"].(map[string]any)
	if url := lb4["servers"].([]map[string]any)[0]["url"]; url != "http://[fd00::1]:80" {
		t.Errorf("malformed IPv6 URL: %v", url)
	}
}

func TestSiteRoute(t *testing.T) {
	site := &store.Site{ID: 3, Domains: []string{"web.com"}, SSL: true, ForceHTTPS: true}
	rt, ok := SiteRoute(site, "http://127.0.0.1:8099")
	if !ok {
		t.Fatal("a site with domains must generate a route")
	}
	httpCfg := BuildDynamic([]Route{rt}, nil)["http"].(map[string]any)
	routers := httpCfg["routers"].(map[string]any)

	secure := routers["site-3-secure"].(map[string]any)
	if mw := secure["middlewares"].([]string); mw[0] != mwCompress {
		t.Errorf("sites must be compressed: %v", mw)
	}
	// With the HTTPS redirect, the HTTP router only redirects.
	if mw := routers["site-3"].(map[string]any)["middlewares"].([]string); len(mw) != 1 || mw[0] != mwRedirectHTTPS {
		t.Errorf("the HTTP router must only redirect: %v", mw)
	}
	lb := httpCfg["services"].(map[string]any)["site-3"].(map[string]any)["loadBalancer"].(map[string]any)
	if lb["passHostHeader"] != true {
		t.Error("the static server needs the original Host to pick the site")
	}

	if _, ok := SiteRoute(&store.Site{ID: 4}, "x"); ok {
		t.Error("a site without domains must not generate a route")
	}
}

func TestAccessListMiddlewares(t *testing.T) {
	extra := map[string]any{}
	names := AccessListMiddlewares(4, []string{"10.0.0.0/8"}, map[string]string{"oswa": "$2a$10$x"}, extra)
	if len(names) != 2 || names[0] != "acl-4-ip" || names[1] != "acl-4-auth" {
		t.Fatalf("names: %v", names)
	}
	rt := Route{Name: "ph-1", Domains: []string{"a.com"}, URL: "http://x", SSL: true, ForceHTTPS: true, Compress: true, Middlewares: names}
	httpCfg := BuildDynamic([]Route{rt}, extra)["http"].(map[string]any)
	secure := httpCfg["routers"].(map[string]any)["ph-1-secure"].(map[string]any)["middlewares"].([]string)
	if strings.Join(secure, ",") != "acl-4-ip,acl-4-auth,dockfly-compress" {
		t.Errorf("access must be checked before compressing: %v", secure)
	}
	auth := httpCfg["middlewares"].(map[string]any)["acl-4-auth"].(map[string]any)["basicAuth"].(map[string]any)
	if auth["users"].([]string)[0] != "oswa:$2a$10$x" || auth["removeHeader"] != true {
		t.Errorf("basicAuth: %v", auth)
	}
}

func TestRedirectRoute(t *testing.T) {
	rt := Route{Name: "rd-1", Domains: []string{"old.com"}, SSL: true, ForceHTTPS: true,
		Redirect: &Redirect{Target: "https://new.com", Permanent: true, PreservePath: true}}
	httpCfg := BuildDynamic([]Route{rt}, nil)["http"].(map[string]any)
	routers := httpCfg["routers"].(map[string]any)
	for _, name := range []string{"rd-1", "rd-1-secure"} {
		r := routers[name].(map[string]any)
		if r["service"] != "noop@internal" || !slices.Contains(r["middlewares"].([]string), "rd-1-redirect") {
			t.Errorf("%s must redirect with no backend: %v", name, r)
		}
	}
	mw := httpCfg["middlewares"].(map[string]any)["rd-1-redirect"].(map[string]any)["redirectRegex"].(map[string]any)
	if mw["replacement"] != "https://new.com${1}" || mw["permanent"] != true {
		t.Errorf("redirectRegex: %v", mw)
	}
	// Only redirects: no services section (Traefik rejects empty ones).
	if _, ok := httpCfg["services"]; ok {
		t.Error("with no real services there must be no services section")
	}
}

func TestWildcardAndResolvers(t *testing.T) {
	routes := []Route{
		{Name: "wildcard", Domains: []string{"*.example.com", "example.com"}, URL: "http://x", SSL: true, Resolver: DNSResolver("cloudflare")},
		{Name: "concrete", Domains: []string{"app.example.com"}, URL: "http://y", SSL: true, Resolver: NoResolver},
	}
	httpCfg := BuildDynamic(routes, nil)["http"].(map[string]any)
	routers := httpCfg["routers"].(map[string]any)
	w := routers["wildcard-secure"].(map[string]any)
	if w["rule"] != "HostRegexp(`^[a-z0-9-]+\\.example\\.com$`) || Host(`example.com`)" {
		t.Errorf("wildcard rule: %v", w["rule"])
	}
	if w["priority"] != 1 {
		t.Error("the wildcard must have the lowest priority so it does not swallow the concrete hosts")
	}
	tls := w["tls"].(map[string]any)
	if tls["certResolver"] != "dns-cloudflare" || tls["domains"].([]map[string]any)[0]["main"] != "*.example.com" {
		t.Errorf("wildcard tls: %v", tls)
	}
	c := routers["concrete-secure"].(map[string]any)
	if _, has := c["tls"].(map[string]any)["certResolver"]; has {
		t.Errorf("with a custom certificate nothing must be asked of Let's Encrypt: %v", c)
	}
}

// A long location of a wildcard does not beat the domain's concrete host.
func TestWildcardLocationBelowConcreteHost(t *testing.T) {
	routes := []Route{
		{Name: "wildcard", Domains: []string{"*.example.com"}, URL: "http://x", PathPrefix: "/api/v1/internal-service"},
		{Name: "concrete", Domains: []string{"app.example.com"}, URL: "http://y"},
		{Name: "concrete-loc", Domains: []string{"app.example.com"}, URL: "http://z", PathPrefix: "/api"},
	}
	routers := BuildDynamic(routes, nil)["http"].(map[string]any)["routers"].(map[string]any)
	prio := func(name string) int { return routers[name].(map[string]any)["priority"].(int) }
	if prio("wildcard") >= prio("concrete") {
		t.Errorf("the wildcard %d must stay below the concrete host %d", prio("wildcard"), prio("concrete"))
	}
	if prio("concrete-loc") <= prio("concrete") {
		t.Errorf("the location %d must beat its host %d", prio("concrete-loc"), prio("concrete"))
	}
}

// Traefik must not read container labels: routes are generated by Dockfly
// and nothing else.
func TestStaticWithoutDockerProvider(t *testing.T) {
	o := Options{DataDir: t.TempDir(), HTTPAddr: ":80", HTTPSAddr: ":443", DockerNet: "dockfly"}
	if err := WriteStatic(o); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(o.StaticFile())
	if strings.Contains(string(raw), "docker:") || strings.Contains(string(raw), "exposedByDefault") {
		t.Errorf("the static configuration must not have the Docker provider:\n%s", raw)
	}
}

// Behind Cloudflare or a load balancer, Traefik only accepts
// X-Forwarded-For from trusted proxies (and never on the metrics entrypoint).
func TestEntryPointsTrustedProxies(t *testing.T) {
	eps := entryPoints(Options{HTTPAddr: ":80", HTTPSAddr: ":443", TrustedProxies: []string{"173.245.48.0/20"}})
	for _, name := range []string{"web", "websecure"} {
		fh, _ := eps[name].(map[string]any)["forwardedHeaders"].(map[string]any)
		if ips, _ := fh["trustedIPs"].([]string); !slices.Equal(ips, []string{"173.245.48.0/20"}) {
			t.Errorf("%s: trustedIPs = %v", name, fh)
		}
	}
	if _, ok := eps[metricsEntryPoint].(map[string]any)["forwardedHeaders"]; ok {
		t.Error("the metrics entrypoint must not trust any proxy")
	}
	if _, ok := entryPoints(Options{})["web"].(map[string]any)["forwardedHeaders"]; ok {
		t.Error("without trusted proxies there must be no forwardedHeaders")
	}
}

package traefik

import (
	"slices"
	"strings"
	"testing"

	"github.com/dockfly/dockfly/internal/store"
)

func TestBuildDynamicEmpty(t *testing.T) {
	httpCfg := BuildDynamic(nil, nil)["http"].(map[string]any)
	if _, ok := httpCfg["routers"]; ok {
		t.Error("sin hosts no debe haber sección routers (Traefik la rechaza vacía)")
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
		t.Error("un host desactivado no debe generar routers")
	}

	r1 := routers["ph-1"].(map[string]any)
	if r1["rule"] != "Host(`a.com`) || Host(`www.a.com`)" {
		t.Errorf("regla inesperada: %v", r1["rule"])
	}
	if mw := r1["middlewares"].([]string); mw[0] != mwRedirectHTTPS {
		t.Errorf("el router HTTP debe redirigir a HTTPS, tiene %v", mw)
	}
	sec := routers["ph-1-secure"].(map[string]any)
	if sec["tls"].(map[string]any)["certResolver"] != CertResolver {
		t.Error("el router seguro debe usar el resolver ACME")
	}

	if _, ok := routers["ph-2-secure"]; ok {
		t.Error("sin SSL no debe haber router seguro")
	}
	lb2 := services["ph-2"].(map[string]any)["loadBalancer"].(map[string]any)
	if lb2["passHostHeader"] != false {
		t.Error("sin preserve_host, Traefik debe enviar el Host del destino")
	}
	if lb2["serversTransport"] != transportNoCheck {
		t.Error("skip_tls_verify debe usar el transporte inseguro")
	}
	if _, ok := httpCfg["serversTransports"]; !ok {
		t.Error("falta definir serversTransports")
	}

	lb4 := services["ph-4"].(map[string]any)["loadBalancer"].(map[string]any)
	if url := lb4["servers"].([]map[string]any)[0]["url"]; url != "http://[fd00::1]:80" {
		t.Errorf("URL IPv6 mal formada: %v", url)
	}
}

func TestSiteRoute(t *testing.T) {
	site := &store.Site{ID: 3, Domains: []string{"web.com"}, SSL: true, ForceHTTPS: true}
	rt, ok := SiteRoute(site, "http://127.0.0.1:8099")
	if !ok {
		t.Fatal("un sitio con dominios debe generar ruta")
	}
	httpCfg := BuildDynamic([]Route{rt}, nil)["http"].(map[string]any)
	routers := httpCfg["routers"].(map[string]any)

	secure := routers["site-3-secure"].(map[string]any)
	if mw := secure["middlewares"].([]string); mw[0] != mwCompress {
		t.Errorf("los sitios deben comprimirse: %v", mw)
	}
	// Con redirección a HTTPS, el router HTTP solo redirige.
	if mw := routers["site-3"].(map[string]any)["middlewares"].([]string); len(mw) != 1 || mw[0] != mwRedirectHTTPS {
		t.Errorf("el router HTTP debe solo redirigir: %v", mw)
	}
	lb := httpCfg["services"].(map[string]any)["site-3"].(map[string]any)["loadBalancer"].(map[string]any)
	if lb["passHostHeader"] != true {
		t.Error("el servidor estático necesita el Host original para elegir el sitio")
	}

	if _, ok := SiteRoute(&store.Site{ID: 4}, "x"); ok {
		t.Error("un sitio sin dominios no debe generar ruta")
	}
}

func TestAccessListMiddlewares(t *testing.T) {
	extra := map[string]any{}
	names := AccessListMiddlewares(4, []string{"10.0.0.0/8"}, map[string]string{"oswa": "$2a$10$x"}, extra)
	if len(names) != 2 || names[0] != "acl-4-ip" || names[1] != "acl-4-auth" {
		t.Fatalf("nombres: %v", names)
	}
	rt := Route{Name: "ph-1", Domains: []string{"a.com"}, URL: "http://x", SSL: true, ForceHTTPS: true, Compress: true, Middlewares: names}
	httpCfg := BuildDynamic([]Route{rt}, extra)["http"].(map[string]any)
	secure := httpCfg["routers"].(map[string]any)["ph-1-secure"].(map[string]any)["middlewares"].([]string)
	if strings.Join(secure, ",") != "acl-4-ip,acl-4-auth,dockfly-compress" {
		t.Errorf("el acceso debe comprobarse antes de comprimir: %v", secure)
	}
	auth := httpCfg["middlewares"].(map[string]any)["acl-4-auth"].(map[string]any)["basicAuth"].(map[string]any)
	if auth["users"].([]string)[0] != "oswa:$2a$10$x" || auth["removeHeader"] != true {
		t.Errorf("basicAuth: %v", auth)
	}
}

func TestRedirectRoute(t *testing.T) {
	rt := Route{Name: "rd-1", Domains: []string{"viejo.com"}, SSL: true, ForceHTTPS: true,
		Redirect: &Redirect{Target: "https://nuevo.com", Permanent: true, PreservePath: true}}
	httpCfg := BuildDynamic([]Route{rt}, nil)["http"].(map[string]any)
	routers := httpCfg["routers"].(map[string]any)
	for _, name := range []string{"rd-1", "rd-1-secure"} {
		r := routers[name].(map[string]any)
		if r["service"] != "noop@internal" || !slices.Contains(r["middlewares"].([]string), "rd-1-redirect") {
			t.Errorf("%s debe redirigir sin backend: %v", name, r)
		}
	}
	mw := httpCfg["middlewares"].(map[string]any)["rd-1-redirect"].(map[string]any)["redirectRegex"].(map[string]any)
	if mw["replacement"] != "https://nuevo.com${1}" || mw["permanent"] != true {
		t.Errorf("redirectRegex: %v", mw)
	}
	// Solo hay redirecciones: sin la sección services (Traefik rechaza las vacías).
	if _, ok := httpCfg["services"]; ok {
		t.Error("sin servicios reales no debe haber sección services")
	}
}

package traefik

import (
	"testing"

	"github.com/dockfly/dockfly/internal/store"
)

func TestBuildDynamicEmpty(t *testing.T) {
	httpCfg := BuildDynamic(nil)["http"].(map[string]any)
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
	httpCfg := BuildDynamic(hosts)["http"].(map[string]any)
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

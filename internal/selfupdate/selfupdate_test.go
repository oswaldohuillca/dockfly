package selfupdate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewer(t *testing.T) {
	for _, tc := range []struct {
		cur, latest string
		want        bool
	}{
		{"0.3.0", "0.3.1", true}, {"0.3.0", "0.10.0", true}, {"0.9.9", "1.0.0", true},
		{"0.3.0", "0.3.0", false}, {"0.4.0", "0.3.9", false},
		{"edge", "0.3.0", false}, {"dev", "0.3.0", false}, {"0.3.0", "rc1", false},
	} {
		if got := newer(tc.cur, tc.latest); got != tc.want {
			t.Errorf("newer(%s, %s) = %v", tc.cur, tc.latest, got)
		}
	}
}

func TestTargetImage(t *testing.T) {
	for ref, want := range map[string]string{
		"ghcr.io/oswaldohuillca/dockfly:latest":     "ghcr.io/oswaldohuillca/dockfly:latest",
		"ghcr.io/oswaldohuillca/dockfly":            "ghcr.io/oswaldohuillca/dockfly:latest",
		"ghcr.io/oswaldohuillca/dockfly:0.3.0":      "ghcr.io/oswaldohuillca/dockfly:0.4.1",
		"localhost:5000/dockfly:latest":             "localhost:5000/dockfly:latest",
		"localhost:5000/dockfly":                    "localhost:5000/dockfly:latest",
		"ghcr.io/oswaldohuillca/dockfly:edge":       "",
		"ghcr.io/oswaldohuillca/dockfly@sha256:abc": "",
	} {
		got, ok := targetImage(ref, "0.4.1")
		if got != want || ok != (want != "") {
			t.Errorf("targetImage(%s) = %q %v, quería %q", ref, got, ok, want)
		}
	}
}

func TestLatestFromGitHub(t *testing.T) {
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/"+Repo+"/releases/latest" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"tag_name":"v0.4.0","html_url":"https://github.com/x/releases/v0.4.0","body":"novedades"}`))
	}))
	defer gh.Close()
	u := &Updater{API: gh.URL, Version: "0.3.0"}
	info := u.Info(context.Background())
	if info.Latest == nil || info.Latest.Version != "0.4.0" || !info.Available {
		t.Fatalf("info: %+v", info)
	}
	// Sin Docker hay versión nueva, pero no se puede actualizar desde el panel.
	if info.CanUpdate || info.Reason != ReasonNotInDocker {
		t.Errorf("sin Docker: %+v", info)
	}
}

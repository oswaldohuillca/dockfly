package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestBackupStorageSettings(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	s3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if strings.Contains(r.Header.Get("Authorization"), "Credential=MALA/") {
			w.WriteHeader(403)
			fmt.Fprint(w, `<Error><Code>InvalidAccessKeyId</Code></Error>`)
			return
		}
		w.WriteHeader(200)
	}))
	defer s3.Close()

	ts, _ := testServer(t)
	c := newClient(t, ts)
	c.do("POST", "/api/setup", adminJSON)

	_, body := c.do("GET", "/api/backup-storage", "")
	if body["config"].(map[string]any)["type"] != "local" || !strings.HasSuffix(body["location"].(string), "/db-backups") {
		t.Errorf("por defecto, en disco: %v", body)
	}
	cfg := func(key, secret string) string {
		return fmt.Sprintf(`{"type":"s3","endpoint":%q,"region":"auto","bucket":"copias","prefix":"/dockfly/","access_key":%q,"secret_key":%q}`, s3.URL+"/", key, secret)
	}
	if code, body := c.do("PUT", "/api/backup-storage", cfg("MALA", "x")); code != 400 || body["code"] != "backup_storage_write_failed" ||
		!strings.Contains(body["params"].(map[string]any)["detail"].(string), "credenciales") {
		t.Errorf("credenciales malas: %d %v", code, body)
	}
	if code, _ := c.do("PUT", "/api/backup-storage", `{"type":"s3","endpoint":"ftp://x","bucket":"b","access_key":"a","secret_key":"s"}`); code != 400 {
		t.Errorf("endpoint inválido: %d", code)
	}
	code, body := c.do("PUT", "/api/backup-storage", cfg("AK", "secreto-s3"))
	if code != 200 || body["location"] != "s3://copias/dockfly" {
		t.Fatalf("guardar S3: %d %v", code, body)
	}
	mu.Lock()
	joined := strings.Join(seen, ",")
	mu.Unlock()
	if !strings.Contains(joined, "PUT /copias/dockfly/.dockfly-check") || !strings.Contains(joined, "DELETE /copias/dockfly/.dockfly-check") {
		t.Errorf("debe probar a escribir antes de guardar: %s", joined)
	}
	if body["config"].(map[string]any)["secret_key"] != secretMask {
		t.Error("la clave secreta no debe devolverse")
	}
	// Al editar sin cambiar la clave, se conserva la guardada.
	if code, _ := c.do("PUT", "/api/backup-storage", cfg("AK", secretMask)); code != 200 {
		t.Errorf("guardar con la clave enmascarada: %d", code)
	}
	s := serverOf(t, ts)
	if got, _ := s.backupStorageConfig(t.Context()); got.SecretKey != "secreto-s3" {
		t.Errorf("la clave guardada se perdió: %+v", got)
	}

	// Las copias son para bases de datos.
	c.do("POST", "/api/apps", `{"name":"web","source":"image","image":"nginx:alpine","port":80}`)
	c.do("POST", "/api/apps", `{"name":"pg","source":"image","image":"postgres:17","volumes":[{"name":"data","path":"/var/lib/postgresql/data"}]}`)
	if _, body := c.do("GET", "/api/apps/1/backups", ""); body["engine"] != "" {
		t.Errorf("nginx no es una base de datos: %v", body)
	}
	_, body = c.do("GET", "/api/apps/2/backups", "")
	if body["engine"] != "postgres" || body["can_restore"] != true || body["policy"].(map[string]any)["enabled"] != false {
		t.Errorf("postgres: %v", body)
	}
	if code, _ := c.do("PUT", "/api/apps/2/backups/policy", `{"enabled":true,"interval_hours":0,"keep":7}`); code != 400 && code != 503 {
		t.Errorf("intervalo 0: %d", code)
	}
}

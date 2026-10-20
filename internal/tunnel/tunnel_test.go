package tunnel

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// panel monta un Hub con la misma autenticación mínima que la API.
func panel(t *testing.T, h *Hub) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/agent/control", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ok" {
			http.Error(w, `{"error":"token inválido"}`, 401)
			return
		}
		h.ServeControl(w, r, 7, Info{Version: r.Header.Get("X-Dockfly-Agent-Version")})
	})
	mux.HandleFunc("POST /api/agent/data/{id}", func(w http.ResponseWriter, r *http.Request) {
		h.ServeData(w, r, 7, r.PathValue("id"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// localHTTP sirve un handler en una conexión en memoria, como el agente con su API.
func localHTTP(handler http.Handler) func(context.Context) (net.Conn, error) {
	return func(context.Context) (net.Conn, error) {
		a, b := net.Pipe()
		go func() {
			srv := &http.Server{Handler: handler}
			_ = srv.Serve(&oneConnListener{conn: b})
		}()
		return a, nil
	}
}

type oneConnListener struct {
	conn net.Conn
	once sync.Once
	done chan struct{}
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	var c net.Conn
	l.once.Do(func() { c, l.done = l.conn, make(chan struct{}) })
	if c != nil {
		return c, nil
	}
	<-l.done
	return nil, net.ErrClosed
}
func (l *oneConnListener) Close() error   { return nil }
func (l *oneConnListener) Addr() net.Addr { return l.conn.LocalAddr() }

func TestTunnel(t *testing.T) {
	connected := make(chan int64, 1)
	h := &Hub{OnConnect: func(id int64) { connected <- id }}
	srv := panel(t, h)

	if _, err := h.Dial(context.Background(), 7, TargetAgent); err != ErrOffline {
		t.Fatalf("sin agente: %v", err)
	}

	// Un destino TCP de eco con semicierre, como el stdin de una restauración.
	echo, _ := net.Listen("tcp", "127.0.0.1:0")
	defer echo.Close()
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go func() {
				data, _ := io.ReadAll(c)
				c.Write([]byte(strings.ToUpper(string(data))))
				c.Close()
			}()
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &Agent{
		PanelURL: srv.URL, Token: "ok", Version: "1.2.3", Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Targets: map[string]func(context.Context) (net.Conn, error){
			TargetAgent: localHTTP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, "hola desde el agente: %s", r.URL.Path)
			})),
			TargetDocker: func(ctx context.Context) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "tcp", echo.Addr().String())
			},
		},
	}
	go a.Run(ctx)
	select {
	case id := <-connected:
		if id != 7 {
			t.Fatalf("servidor %d", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("el agente no se conectó")
	}
	if info, ok := h.Online(7); !ok || info.Version != "1.2.3" {
		t.Fatalf("online: %+v %v", info, ok)
	}

	// HTTP a través del túnel, con un cliente que marca por el Hub.
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return h.Dial(ctx, 7, TargetAgent) },
	}}
	var wg sync.WaitGroup
	for i := range 5 {
		wg.Go(func() {
			resp, err := client.Get(fmt.Sprintf("http://agente/info/%d", i))
			if err != nil {
				t.Error(err)
				return
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if string(body) != fmt.Sprintf("hola desde el agente: /info/%d", i) {
				t.Errorf("respuesta %q", body)
			}
		})
	}
	wg.Wait()

	// Conexión en bruto con semicierre.
	c, err := h.Dial(context.Background(), 7, TargetDocker)
	if err != nil {
		t.Fatal(err)
	}
	c.Write([]byte("datos del volcado"))
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		cw.CloseWrite()
	} else {
		t.Fatalf("la conexión del túnel debe permitir semicierre (%T)", c)
	}
	got, _ := io.ReadAll(bufio.NewReader(c))
	c.Close()
	if string(got) != "DATOS DEL VOLCADO" {
		t.Errorf("eco: %q", got)
	}

	// Un agente con un token malo no se conecta.
	bad := &Agent{PanelURL: srv.URL, Token: "malo", Log: a.Log}
	if err := bad.control(context.Background()); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("token malo: %v", err)
	}

	cancel()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok := h.Online(7); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("el agente sigue apareciendo conectado")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

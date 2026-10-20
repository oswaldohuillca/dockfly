// Package docker es un cliente mínimo de la API de Docker sobre el socket unix.
// Evitamos el SDK oficial porque añade decenas de MB de dependencias para
// las pocas llamadas que necesitamos.
package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"
)

const apiVersion = "v1.44"

type Client struct {
	Socket string
	http   *http.Client
	// stream no tiene timeout global: lo usan las operaciones largas (pull,
	// logs, wait, copiar archivos), cuyo límite lo marca el contexto.
	stream *http.Client
}

func New(socket string) *Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}
	return &Client{
		Socket: socket,
		http:   &http.Client{Timeout: 15 * time.Second, Transport: transport},
		stream: &http.Client{Transport: transport},
	}
}

// Available indica si el socket existe y responde.
func (c *Client) Available(ctx context.Context) bool {
	if _, err := os.Stat(c.Socket); err != nil {
		return false
	}
	return c.do(ctx, http.MethodGet, "/_ping", nil, nil) == nil
}

type Container struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Image    string   `json:"image"`
	State    string   `json:"state"`
	Status   string   `json:"status"`
	Networks []string `json:"networks"`
	Ports    []int    `json:"ports"` // puertos internos expuestos
	// Self marca el contenedor del propio Dockfly (útil para publicar el
	// panel con un dominio, pero conviene distinguirlo).
	Self bool `json:"self"`
}

func (c *Client) ListContainers(ctx context.Context) ([]Container, error) {
	var raw []struct {
		ID     string   `json:"Id"`
		Names  []string `json:"Names"`
		Image  string   `json:"Image"`
		State  string   `json:"State"`
		Status string   `json:"Status"`
		Ports  []struct {
			PrivatePort int    `json:"PrivatePort"`
			Type        string `json:"Type"`
		} `json:"Ports"`
		NetworkSettings struct {
			Networks map[string]json.RawMessage `json:"Networks"`
		} `json:"NetworkSettings"`
	}
	if err := c.do(ctx, http.MethodGet, "/containers/json?all=1", nil, &raw); err != nil {
		return nil, err
	}

	// Dentro de Docker, el hostname por defecto es el ID corto del contenedor.
	self, _ := os.Hostname()
	out := make([]Container, 0, len(raw))
	for _, r := range raw {
		ct := Container{
			ID:     r.ID[:12],
			Image:  r.Image,
			State:  r.State,
			Status: r.Status,
			Self:   self != "" && strings.HasPrefix(r.ID, self),
		}
		if len(r.Names) > 0 {
			ct.Name = strings.TrimPrefix(r.Names[0], "/")
		}
		for n := range r.NetworkSettings.Networks {
			ct.Networks = append(ct.Networks, n)
		}
		slices.Sort(ct.Networks)
		for _, p := range r.Ports {
			if p.Type == "tcp" && !slices.Contains(ct.Ports, p.PrivatePort) {
				ct.Ports = append(ct.Ports, p.PrivatePort)
			}
		}
		slices.Sort(ct.Ports)
		out = append(out, ct)
	}
	slices.SortFunc(out, func(a, b Container) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// SelfNetworks devuelve las redes del contenedor de Dockfly. Dentro de Docker
// el hostname por defecto es el ID corto del contenedor.
func (c *Client) SelfNetworks(ctx context.Context) ([]string, error) {
	host, err := os.Hostname()
	if err != nil {
		return nil, err
	}
	var raw struct {
		NetworkSettings struct {
			Networks map[string]json.RawMessage `json:"Networks"`
		} `json:"NetworkSettings"`
	}
	if err := c.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(host)+"/json", nil, &raw); err != nil {
		return nil, err
	}
	var nets []string
	for n := range raw.NetworkSettings.Networks {
		nets = append(nets, n)
	}
	slices.Sort(nets)
	return nets, nil
}

// ConnectNetwork conecta un contenedor a una red. Si ya está conectado no es error.
func (c *Client) ConnectNetwork(ctx context.Context, network, container string) error {
	body := map[string]string{"Container": container}
	err := c.do(ctx, http.MethodPost, "/networks/"+url.PathEscape(network)+"/connect", body, nil)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusForbidden &&
		strings.Contains(apiErr.Message, "already exists") {
		return nil
	}
	return err
}

type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("docker: %d %s", e.Status, e.Message) }

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://docker/"+apiVersion+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return &APIError{Status: resp.StatusCode, Message: e.Message}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

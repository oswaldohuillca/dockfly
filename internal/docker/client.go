// Package docker is a minimal client for the Docker API over the unix socket
// (or over TCP, e.g. behind a socket proxy).
// We avoid the official SDK because it drags in tens of MB of dependencies
// for the handful of calls we need.
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
	// dial, when not nil, replaces the connection to Socket (the Docker of a
	// remote server, reached through its agent's tunnel).
	dial func(ctx context.Context) (net.Conn, error)
	http *http.Client
	// stream has no global timeout: it is used by the long operations (pull,
	// logs, wait, copying files), whose limit comes from the context.
	stream *http.Client
}

// New connects to the Docker unix socket, or to "tcp://host:port".
func New(socket string) *Client {
	c := &Client{Socket: socket}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return c.Dial(ctx) },
	}
	c.http = &http.Client{Timeout: 15 * time.Second, Transport: transport}
	c.stream = &http.Client{Transport: transport}
	return c
}

// NewRemote creates a client that reaches Docker through dial. name only
// serves to identify it.
func NewRemote(name string, dial func(ctx context.Context) (net.Conn, error)) *Client {
	c := New(name)
	c.dial = dial
	return c
}

// Dial opens a connection to the Docker API.
func (c *Client) Dial(ctx context.Context) (net.Conn, error) {
	if c.dial != nil {
		return c.dial(ctx)
	}
	var d net.Dialer
	if addr, ok := strings.CutPrefix(c.Socket, "tcp://"); ok {
		return d.DialContext(ctx, "tcp", addr)
	}
	return d.DialContext(ctx, "unix", strings.TrimPrefix(c.Socket, "unix://"))
}

// Available reports whether Docker answers.
func (c *Client) Available(ctx context.Context) bool {
	if c.dial == nil && !strings.HasPrefix(c.Socket, "tcp://") {
		if _, err := os.Stat(strings.TrimPrefix(c.Socket, "unix://")); err != nil {
			return false
		}
	}
	return c.do(ctx, http.MethodGet, "/_ping", nil, nil) == nil
}

// SelfInfo is what Dockfly needs to know about its own container.
type SelfInfo struct {
	// Image is the image the container was created from.
	Image string
	// IPs per network.
	IPs map[string]string
	// Mounts are the host sources of its mounts (paths or volumes).
	Mounts []string
}

// Self inspects Dockfly's container (ok=false when it does not run in Docker).
func (c *Client) Self(ctx context.Context) (SelfInfo, bool) {
	host, err := os.Hostname()
	if err != nil {
		return SelfInfo{}, false
	}
	var raw struct {
		Config struct {
			Image string `json:"Image"`
		} `json:"Config"`
		Mounts []struct {
			Type   string `json:"Type"`
			Name   string `json:"Name"`
			Source string `json:"Source"`
		} `json:"Mounts"`
		NetworkSettings struct {
			Networks map[string]struct {
				IPAddress string `json:"IPAddress"`
			} `json:"Networks"`
		} `json:"NetworkSettings"`
	}
	if err := c.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(host)+"/json", nil, &raw); err != nil {
		return SelfInfo{}, false
	}
	info := SelfInfo{IPs: map[string]string{}, Image: raw.Config.Image}
	for n, s := range raw.NetworkSettings.Networks {
		info.IPs[n] = s.IPAddress
	}
	for _, m := range raw.Mounts {
		if m.Source != "" {
			info.Mounts = append(info.Mounts, m.Source)
		}
	}
	return info, true
}

type Container struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Image    string   `json:"image"`
	State    string   `json:"state"`
	Status   string   `json:"status"`
	Networks []string `json:"networks"`
	Ports    []int    `json:"ports"` // exposed internal ports
	// Self marks Dockfly's own container (useful to publish the panel under
	// a domain, but worth telling apart from the rest).
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

	// Inside Docker, the default hostname is the container's short ID.
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

// SelfNetworks returns the networks of Dockfly's container. Inside Docker
// the default hostname is the container's short ID.
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

// ConnectNetwork attaches a container to a network. Already attached is not an error.
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

// SelfPublishedPorts returns the ports that Dockfly's container publishes on
// the host ("5432/tcp"…). ok=false when Dockfly does not run in Docker.
func (c *Client) SelfPublishedPorts(ctx context.Context) (ports map[string]bool, ok bool) {
	host, err := os.Hostname()
	if err != nil {
		return nil, false
	}
	var raw struct {
		HostConfig struct {
			PortBindings map[string][]json.RawMessage `json:"PortBindings"`
		} `json:"HostConfig"`
	}
	if err := c.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(host)+"/json", nil, &raw); err != nil {
		return nil, false
	}
	ports = map[string]bool{}
	for p, b := range raw.HostConfig.PortBindings {
		if len(b) > 0 {
			ports[p] = true
		}
	}
	return ports, true
}

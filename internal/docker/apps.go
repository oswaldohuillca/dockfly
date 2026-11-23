package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

// AppSpec is the configuration of an app container. Unlike builds, these are
// long-running services the user trusts (a database needs its capabilities
// just to start up).
type AppSpec struct {
	Name     string
	Image    string
	Cmd      []string // empty = the image's own
	Env      []string
	Labels   map[string]string
	Network  string
	Aliases  []string          // names other apps use to find it
	Volumes  map[string]string // volume → path
	MemoryMB int64
	CPUs     float64
}

// CreateAppContainer creates an app's container with no restart policy: while
// we are checking whether it starts up fine, a crash must stay crashed (with
// its exit code). Once it is ready, KeepRunning sets unless-stopped.
func (c *Client) CreateAppContainer(ctx context.Context, spec AppSpec) (string, error) {
	host := map[string]any{
		"RestartPolicy": map[string]any{"Name": "no"},
		"Init":          true,
		// Log rotation: a chatty service must not fill up the disk.
		"LogConfig": map[string]any{"Type": "json-file", "Config": map[string]string{"max-size": "10m", "max-file": "3"}},
	}
	if spec.MemoryMB > 0 {
		host["Memory"] = spec.MemoryMB << 20
	}
	if spec.CPUs > 0 {
		host["NanoCpus"] = int64(spec.CPUs * 1e9)
	}
	var mounts []map[string]any
	for name, target := range spec.Volumes {
		mounts = append(mounts, map[string]any{"Type": "volume", "Source": name, "Target": target})
	}
	if len(mounts) > 0 {
		host["Mounts"] = mounts
	}
	body := map[string]any{
		"Image":      spec.Image,
		"Env":        spec.Env,
		"Labels":     spec.Labels,
		"HostConfig": host,
	}
	if len(spec.Cmd) > 0 {
		body["Cmd"] = spec.Cmd
	}
	if spec.Network != "" {
		body["NetworkingConfig"] = map[string]any{
			"EndpointsConfig": map[string]any{spec.Network: map[string]any{"Aliases": spec.Aliases}},
		}
	}
	var out struct {
		ID string `json:"Id"`
	}
	if err := c.do(ctx, http.MethodPost, "/containers/create?name="+url.QueryEscape(spec.Name), body, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// ContainerInfo is the state of a container.
type ContainerInfo struct {
	ID         string
	Name       string
	Status     string // created, running, restarting, exited…
	Running    bool
	Restarting bool
	ExitCode   int
	OOMKilled  bool
	Health     string // "" when the image defines no HEALTHCHECK; starting, healthy, unhealthy
	StartedAt  time.Time
	IPs        map[string]string // network → IP
	Labels     map[string]string
}

func (c *Client) InspectContainer(ctx context.Context, id string) (*ContainerInfo, error) {
	var raw struct {
		ID    string `json:"Id"`
		Name  string `json:"Name"`
		State struct {
			Status     string    `json:"Status"`
			Running    bool      `json:"Running"`
			Restarting bool      `json:"Restarting"`
			ExitCode   int       `json:"ExitCode"`
			OOMKilled  bool      `json:"OOMKilled"`
			StartedAt  time.Time `json:"StartedAt"`
			Health     *struct {
				Status string `json:"Status"`
			} `json:"Health"`
		} `json:"State"`
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
		NetworkSettings struct {
			Networks map[string]struct {
				IPAddress string `json:"IPAddress"`
			} `json:"Networks"`
		} `json:"NetworkSettings"`
	}
	if err := c.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/json", nil, &raw); err != nil {
		return nil, err
	}
	info := &ContainerInfo{
		ID: raw.ID, Name: strings.TrimPrefix(raw.Name, "/"), Status: raw.State.Status,
		Running: raw.State.Running, Restarting: raw.State.Restarting, ExitCode: raw.State.ExitCode,
		OOMKilled: raw.State.OOMKilled, StartedAt: raw.State.StartedAt, Labels: raw.Config.Labels,
		IPs: map[string]string{},
	}
	if raw.State.Health != nil {
		info.Health = raw.State.Health.Status
	}
	for n, e := range raw.NetworkSettings.Networks {
		info.IPs[n] = e.IPAddress
	}
	return info, nil
}

// StopContainer stops the container, giving it timeout to shut down cleanly
// (a database needs a few seconds to close its files).
func (c *Client) StopContainer(ctx context.Context, id string, timeout time.Duration) error {
	err := c.postLong(ctx, "/containers/"+url.PathEscape(id)+"/stop?t="+strconv.Itoa(int(timeout.Seconds())))
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotModified {
		return nil // it was already stopped
	}
	return err
}

func (c *Client) RestartContainer(ctx context.Context, id string, timeout time.Duration) error {
	return c.postLong(ctx, "/containers/"+url.PathEscape(id)+"/restart?t="+strconv.Itoa(int(timeout.Seconds())))
}

// postLong does a POST that can take longer than c.http's 15 s timeout:
// stopping a container waits up to its own timeout (20 s) for it to finish,
// and cutting that short leaves the deploy half done with the container
// still shutting down.
func (c *Client) postLong(ctx context.Context, path string) error {
	resp, err := c.raw(ctx, c.stream, http.MethodPost, path, nil, "")
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// FollowLogs follows a container's logs (multiplexed: use Demux), starting
// from its last tail lines.
func (c *Client) FollowLogs(ctx context.Context, id string, tail int) (io.ReadCloser, error) {
	path := fmt.Sprintf("/containers/%s/logs?follow=1&stdout=1&stderr=1&tail=%d", url.PathEscape(id), tail)
	resp, err := c.raw(ctx, c.stream, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// BuildImage builds an image from a context in tar form (the repository) and
// writes the build output to log.
func (c *Client) BuildImage(ctx context.Context, tag, dockerfile string, buildContext io.Reader, memoryMB int64, log io.Writer) error {
	q := url.Values{"t": {tag}, "rm": {"1"}, "forcerm": {"1"}}
	if dockerfile != "" {
		q.Set("dockerfile", dockerfile)
	}
	if memoryMB > 0 {
		q.Set("memory", strconv.FormatInt(memoryMB<<20, 10))
	}
	resp, err := c.raw(ctx, c.stream, http.MethodPost, "/build?"+q.Encode(), buildContext, "application/x-tar")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// As with the pull, an error midway through the build arrives as {"error": ...}.
	dec := json.NewDecoder(resp.Body)
	for {
		var msg struct {
			Stream string `json:"stream"`
			Status string `json:"status"`
			Error  string `json:"error"`
		}
		if err := dec.Decode(&msg); err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
		if msg.Error != "" {
			return errors.New(strings.TrimSpace(msg.Error))
		}
		if msg.Stream != "" {
			io.WriteString(log, msg.Stream)
		}
	}
}

// RemoveImage deletes an image (the ones built for older deploys).
func (c *Client) RemoveImage(ctx context.Context, ref string) error {
	err := c.do(ctx, http.MethodDelete, "/images/"+ref, nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// CreateNetwork creates a bridge network. It already existing is not an error.
func (c *Client) CreateNetwork(ctx context.Context, name string) error {
	err := c.do(ctx, http.MethodPost, "/networks/create", map[string]any{"Name": name, "CheckDuplicate": true}, nil)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusConflict {
		return nil
	}
	return err
}

// EnsureSelfNetwork creates the network if it does not exist and attaches
// Dockfly's container to it. connected reports whether it had to be attached
// now (false when it already was, or when Dockfly does not run in a container).
func (c *Client) EnsureSelfNetwork(ctx context.Context, network string) (connected bool, err error) {
	if err := c.CreateNetwork(ctx, network); err != nil {
		return false, err
	}
	nets, err := c.SelfNetworks(ctx)
	if err != nil || len(nets) == 0 {
		return false, nil // Dockfly does not run in a container (development)
	}
	if slices.Contains(nets, network) {
		return false, nil
	}
	self, _ := os.Hostname()
	if err := c.ConnectNetwork(ctx, network, self); err != nil {
		return false, err
	}
	return true, nil
}

// CreateInternalNetwork creates a labelled network with no route out to the
// internet or to the host. It returns an error if it already exists.
func (c *Client) CreateInternalNetwork(ctx context.Context, name string, labels map[string]string) error {
	return c.do(ctx, http.MethodPost, "/networks/create",
		map[string]any{"Name": name, "CheckDuplicate": true, "Internal": true, "Labels": labels}, nil)
}

// DisconnectNetwork takes a container out of a network (not being in it is
// not an error).
func (c *Client) DisconnectNetwork(ctx context.Context, network, container string) error {
	err := c.do(ctx, http.MethodPost, "/networks/"+url.PathEscape(network)+"/disconnect",
		map[string]any{"Container": container, "Force": true}, nil)
	var apiErr *APIError
	if errors.As(err, &apiErr) && (apiErr.Status == http.StatusNotFound || apiErr.Status == http.StatusForbidden) {
		return nil
	}
	return err
}

// RemoveNetwork deletes a network (it not existing is not an error).
func (c *Client) RemoveNetwork(ctx context.Context, network string) error {
	err := c.do(ctx, http.MethodDelete, "/networks/"+url.PathEscape(network), nil, nil)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
		return nil
	}
	return err
}

// NetworksWithLabel returns the names of the networks carrying a label ("key" or "key=value").
func (c *Client) NetworksWithLabel(ctx context.Context, label string) ([]string, error) {
	filters, _ := json.Marshal(map[string][]string{"label": {label}})
	var raw []struct {
		Name string `json:"Name"`
	}
	if err := c.do(ctx, http.MethodGet, "/networks?filters="+url.QueryEscape(string(filters)), nil, &raw); err != nil {
		return nil, err
	}
	names := make([]string, len(raw))
	for i, n := range raw {
		names[i] = n.Name
	}
	return names, nil
}

// ContainerIP returns a container's IP on a network.
func (c *Client) ContainerIP(ctx context.Context, container, network string) (string, error) {
	var raw struct {
		NetworkSettings struct {
			Networks map[string]struct {
				IPAddress string `json:"IPAddress"`
			} `json:"Networks"`
		} `json:"NetworkSettings"`
	}
	if err := c.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(container)+"/json", nil, &raw); err != nil {
		return "", err
	}
	ip := raw.NetworkSettings.Networks[network].IPAddress
	if ip == "" {
		return "", fmt.Errorf("container %s has no IP on network %s", container, network)
	}
	return ip, nil
}

// Exec runs a command inside a container and returns its output (stdout and
// stderr).
func (c *Client) Exec(ctx context.Context, container string, cmd []string) (string, error) {
	var created struct {
		ID string `json:"Id"`
	}
	body := map[string]any{"Cmd": cmd, "AttachStdout": true, "AttachStderr": true}
	if err := c.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(container)+"/exec", body, &created); err != nil {
		return "", err
	}
	start, _ := json.Marshal(map[string]any{"Detach": false, "Tty": false})
	resp, err := c.raw(ctx, c.stream, http.MethodPost, "/exec/"+created.ID+"/start", strings.NewReader(string(start)), "application/json")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out strings.Builder
	err = Demux(resp.Body, &out)
	return out.String(), err
}

// KeepRunning makes Docker restart the container if it crashes or if the
// server reboots (unless-stopped).
func (c *Client) KeepRunning(ctx context.Context, id string) error {
	body := map[string]any{"RestartPolicy": map[string]any{"Name": "unless-stopped"}}
	return c.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/update", body, nil)
}

// ContainerUsage is a container's consumption right now.
type ContainerUsage struct {
	CPUPercent float64 `json:"cpu_percent"` // over every core of the server: 100 = all of them busy
	MemUsed    uint64  `json:"mem_used"`
	MemLimit   uint64  `json:"mem_limit"` // its limit or, when it has none, the server's memory
}

// containerStats is the part of GET /containers/{id}/stats that we use.
type containerStats struct {
	CPU    cpuSample `json:"cpu_stats"`
	PreCPU cpuSample `json:"precpu_stats"`
	Memory struct {
		Usage uint64            `json:"usage"`
		Limit uint64            `json:"limit"`
		Stats map[string]uint64 `json:"stats"`
	} `json:"memory_stats"`
}

type cpuSample struct {
	Usage struct {
		Total uint64 `json:"total_usage"`
	} `json:"cpu_usage"`
	System uint64 `json:"system_cpu_usage"`
}

func (s *containerStats) usage() ContainerUsage {
	u := ContainerUsage{MemUsed: s.Memory.Usage, MemLimit: s.Memory.Limit}
	// Same as `docker stats`: the file cache does not count as used memory
	// (inactive_file on cgroup v2, total_inactive_file on v1).
	for _, k := range []string{"inactive_file", "total_inactive_file"} {
		if cache := s.Memory.Stats[k]; cache > 0 && cache < u.MemUsed {
			u.MemUsed -= cache
			break
		}
	}
	if system := s.CPU.System - s.PreCPU.System; s.CPU.System > s.PreCPU.System && s.CPU.Usage.Total >= s.PreCPU.Usage.Total {
		u.CPUPercent = float64(s.CPU.Usage.Total-s.PreCPU.Usage.Total) / float64(system) * 100
	}
	return u
}

// ContainerUsage measures a container's CPU and memory. Docker takes two CPU
// samples a second apart, so it takes that long to answer.
func (c *Client) ContainerUsage(ctx context.Context, id string) (ContainerUsage, error) {
	var raw containerStats
	if err := c.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/stats?stream=false", nil, &raw); err != nil {
		return ContainerUsage{}, err
	}
	return raw.usage(), nil
}

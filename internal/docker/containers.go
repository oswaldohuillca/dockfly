package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ContainerSpec is the subset of the container configuration that builds
// use.
type ContainerSpec struct {
	Name       string
	Image      string
	Cmd        []string
	Env        []string
	User       string
	WorkingDir string
	Labels     map[string]string
	MemoryMB   int64
	CPUs       float64
	PidsLimit  int64
	// Volumes mounts named volumes: name → path inside the container.
	Volumes map[string]string
	// KeepCaps keeps the default capabilities (only for Dockfly's own helper
	// containers, which never run user code).
	KeepCaps bool
	// Binds mounts host paths ("source:target"). Helpers only.
	Binds []string
	// Network, when not empty, is the container's network (instead of bridge).
	Network string
	// Restart is the restart policy ("" = do not restart).
	Restart string
	// ReadOnly mounts the root filesystem read-only.
	ReadOnly bool
	// NoHealthcheck turns off the image's healthcheck.
	NoHealthcheck bool
}

func (c *Client) raw(ctx context.Context, client *http.Client, method, path string, body io.Reader, contentType string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, "http://docker/"+apiVersion+path, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		var e struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&e)
		return nil, &APIError{Status: resp.StatusCode, Message: e.Message}
	}
	return resp, nil
}

func IsNotFound(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

// WithDefaultTag adds ":latest" when the reference carries no tag or digest.
// Without a tag, the Docker API would pull every tag of the image.
func WithDefaultTag(ref string) string {
	if strings.Contains(ref, "@") {
		return ref
	}
	last := ref[strings.LastIndex(ref, "/")+1:]
	if strings.Contains(last, ":") {
		return ref
	}
	return ref + ":latest"
}

func (c *Client) ImageExists(ctx context.Context, ref string) (bool, error) {
	err := c.do(ctx, http.MethodGet, "/images/"+ref+"/json", nil, nil)
	if IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

// PullImage downloads an image and writes a summary of the progress to log.
func (c *Client) PullImage(ctx context.Context, ref string, log io.Writer) error {
	resp, err := c.raw(ctx, c.stream, http.MethodPost, "/images/create?fromImage="+url.QueryEscape(WithDefaultTag(ref)), nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// The response is a stream of JSON objects; an error midway through the
	// download arrives as {"error": "..."} with status 200.
	dec := json.NewDecoder(resp.Body)
	for {
		var msg struct {
			Status string `json:"status"`
			ID     string `json:"id"`
			Error  string `json:"error"`
		}
		if err := dec.Decode(&msg); err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
		if msg.Error != "" {
			return errors.New(msg.Error)
		}
		// Only the overall status lines: per-layer progress is just noise.
		if msg.ID == "" || strings.HasPrefix(msg.Status, "Pull complete") || strings.HasPrefix(msg.Status, "Digest") {
			fmt.Fprintf(log, "%s %s\n", msg.ID, msg.Status)
		}
	}
}

func (c *Client) CreateContainer(ctx context.Context, spec ContainerSpec) (string, error) {
	host := map[string]any{
		"Memory":      spec.MemoryMB << 20,
		"MemorySwap":  spec.MemoryMB << 20, // no extra swap
		"NanoCpus":    int64(spec.CPUs * 1e9),
		"PidsLimit":   spec.PidsLimit,
		"SecurityOpt": []string{"no-new-privileges"},
		"Init":        true, // reaps the orphan processes left by scripts
	}
	if !spec.KeepCaps {
		host["CapDrop"] = []string{"ALL"}
	}
	var mounts []map[string]any
	for name, target := range spec.Volumes {
		mounts = append(mounts, map[string]any{"Type": "volume", "Source": name, "Target": target})
	}
	if len(mounts) > 0 {
		host["Mounts"] = mounts
	}
	if len(spec.Binds) > 0 {
		host["Binds"] = spec.Binds
	}
	if spec.Network != "" {
		host["NetworkMode"] = spec.Network
	}
	if spec.Restart != "" {
		host["RestartPolicy"] = map[string]string{"Name": spec.Restart}
	}
	if spec.ReadOnly {
		host["ReadonlyRootfs"] = true
	}
	body := map[string]any{
		"Image":      spec.Image,
		"Cmd":        spec.Cmd,
		"Env":        spec.Env,
		"User":       spec.User,
		"WorkingDir": spec.WorkingDir,
		"Labels":     spec.Labels,
		// No TTY: tools then skip the \r progress bars (unreadable in a log)
		// and the logs arrive multiplexed (stdout and stderr with an 8-byte
		// header per frame).
		"Tty":        false,
		"HostConfig": host,
	}
	if spec.NoHealthcheck {
		body["Healthcheck"] = map[string]any{"Test": []string{"NONE"}}
	}
	var out struct {
		ID string `json:"Id"`
	}
	path := "/containers/create"
	if spec.Name != "" {
		path += "?name=" + url.QueryEscape(spec.Name)
	}
	if err := c.do(ctx, http.MethodPost, path, body, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// PutArchive extracts a tar inside the container, into dir.
func (c *Client) PutArchive(ctx context.Context, id, dir string, tar io.Reader) error {
	resp, err := c.raw(ctx, c.stream, http.MethodPut,
		"/containers/"+id+"/archive?path="+url.QueryEscape(dir), tar, "application/x-tar")
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// GetArchive returns the contents of path as a tar. Entries start with the
// base name of path (e.g. "dist/index.html").
func (c *Client) GetArchive(ctx context.Context, id, path string) (io.ReadCloser, error) {
	resp, err := c.raw(ctx, c.stream, http.MethodGet, "/containers/"+id+"/archive?path="+url.QueryEscape(path), nil, "")
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (c *Client) StartContainer(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/containers/"+id+"/start", nil, nil)
}

// Logs follows the container's output until it finishes.
func (c *Client) Logs(ctx context.Context, id string) (io.ReadCloser, error) {
	resp, err := c.raw(ctx, c.stream, http.MethodGet, "/containers/"+id+"/logs?follow=1&stdout=1&stderr=1", nil, "")
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// Wait waits for the container to finish and returns its exit code.
func (c *Client) Wait(ctx context.Context, id string) (int, error) {
	resp, err := c.raw(ctx, c.stream, http.MethodPost, "/containers/"+id+"/wait?condition=not-running", nil, "")
	if err != nil {
		return -1, err
	}
	defer resp.Body.Close()
	var out struct {
		StatusCode int `json:"StatusCode"`
		Error      *struct {
			Message string `json:"Message"`
		} `json:"Error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return -1, err
	}
	if out.Error != nil && out.Error.Message != "" {
		return -1, errors.New(out.Error.Message)
	}
	return out.StatusCode, nil
}

// RemoveContainer deletes the container (stopping it if need be) and its
// anonymous volumes. It no longer existing is not an error.
func (c *Client) RemoveContainer(ctx context.Context, id string) error {
	err := c.do(ctx, http.MethodDelete, "/containers/"+id+"?force=1&v=1", nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// ContainersWithLabel returns the IDs of the containers (in any state) that
// carry the given label.
func (c *Client) ContainersWithLabel(ctx context.Context, label string) ([]string, error) {
	filters, _ := json.Marshal(map[string][]string{"label": {label}})
	var raw []struct {
		ID string `json:"Id"`
	}
	if err := c.do(ctx, http.MethodGet, "/containers/json?all=1&filters="+url.QueryEscape(string(filters)), nil, &raw); err != nil {
		return nil, err
	}
	ids := make([]string, len(raw))
	for i, r := range raw {
		ids[i] = r.ID
	}
	return ids, nil
}

// VolumeExists reports whether a volume with that name exists.
func (c *Client) VolumeExists(ctx context.Context, name string) (bool, error) {
	err := c.do(ctx, http.MethodGet, "/volumes/"+url.PathEscape(name), nil, nil)
	if IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

func (c *Client) CreateVolume(ctx context.Context, name string, labels map[string]string) error {
	return c.do(ctx, http.MethodPost, "/volumes/create", map[string]any{"Name": name, "Labels": labels}, nil)
}

// RemoveVolume deletes a volume. It not existing is not an error.
func (c *Client) RemoveVolume(ctx context.Context, name string) error {
	err := c.do(ctx, http.MethodDelete, "/volumes/"+url.PathEscape(name)+"?force=1", nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

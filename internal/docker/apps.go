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

// AppSpec es la configuración de un contenedor de app. A diferencia de los
// builds, son servicios de larga duración en los que el usuario confía (una
// base de datos necesita sus capabilities para arrancar).
type AppSpec struct {
	Name     string
	Image    string
	Cmd      []string // vacío = el de la imagen
	Env      []string
	Labels   map[string]string
	Network  string
	Aliases  []string          // nombres con los que lo encuentran otras apps
	Volumes  map[string]string // volumen → ruta
	MemoryMB int64
	CPUs     float64
}

func (c *Client) CreateAppContainer(ctx context.Context, spec AppSpec) (string, error) {
	host := map[string]any{
		"RestartPolicy": map[string]any{"Name": "unless-stopped"},
		"Init":          true,
		// Rotación de logs: un servicio que escribe mucho no llena el disco.
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

// ContainerInfo es el estado de un contenedor.
type ContainerInfo struct {
	ID         string
	Name       string
	Status     string // created, running, restarting, exited…
	Running    bool
	Restarting bool
	ExitCode   int
	OOMKilled  bool
	Health     string // "" si la imagen no define HEALTHCHECK; starting, healthy, unhealthy
	StartedAt  time.Time
	IPs        map[string]string // red → IP
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

// StopContainer detiene el contenedor dándole timeout para terminar bien
// (una base de datos necesita unos segundos para cerrar sus archivos).
func (c *Client) StopContainer(ctx context.Context, id string, timeout time.Duration) error {
	err := c.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/stop?t="+strconv.Itoa(int(timeout.Seconds())), nil, nil)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotModified {
		return nil // ya estaba parado
	}
	return err
}

func (c *Client) RestartContainer(ctx context.Context, id string, timeout time.Duration) error {
	return c.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/restart?t="+strconv.Itoa(int(timeout.Seconds())), nil, nil)
}

// FollowLogs sigue los logs de un contenedor (multiplexados: usar Demux),
// empezando por las últimas tail líneas.
func (c *Client) FollowLogs(ctx context.Context, id string, tail int) (io.ReadCloser, error) {
	path := fmt.Sprintf("/containers/%s/logs?follow=1&stdout=1&stderr=1&tail=%d", url.PathEscape(id), tail)
	resp, err := c.raw(ctx, c.stream, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// BuildImage construye una imagen a partir de un contexto en tar (el
// repositorio) y escribe en log la salida del build.
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
	// Como en el pull, un error a mitad del build llega como {"error": ...}.
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

// RemoveImage borra una imagen (las construidas para despliegues antiguos).
func (c *Client) RemoveImage(ctx context.Context, ref string) error {
	err := c.do(ctx, http.MethodDelete, "/images/"+ref, nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// CreateNetwork crea una red bridge. No es un error que ya exista.
func (c *Client) CreateNetwork(ctx context.Context, name string) error {
	err := c.do(ctx, http.MethodPost, "/networks/create", map[string]any{"Name": name, "CheckDuplicate": true}, nil)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusConflict {
		return nil
	}
	return err
}

// EnsureSelfNetwork crea la red si no existe y conecta a ella el contenedor
// de Dockfly. connected indica si hubo que conectarlo ahora (false si ya lo
// estaba o si Dockfly no corre en un contenedor).
func (c *Client) EnsureSelfNetwork(ctx context.Context, network string) (connected bool, err error) {
	if err := c.CreateNetwork(ctx, network); err != nil {
		return false, err
	}
	nets, err := c.SelfNetworks(ctx)
	if err != nil || len(nets) == 0 {
		return false, nil // Dockfly no corre en un contenedor (desarrollo)
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

// CreateInternalNetwork crea una red sin salida a internet ni al host, con
// etiquetas. Devuelve error si ya existe.
func (c *Client) CreateInternalNetwork(ctx context.Context, name string, labels map[string]string) error {
	return c.do(ctx, http.MethodPost, "/networks/create",
		map[string]any{"Name": name, "CheckDuplicate": true, "Internal": true, "Labels": labels}, nil)
}

// DisconnectNetwork saca un contenedor de una red (si no estaba, no es error).
func (c *Client) DisconnectNetwork(ctx context.Context, network, container string) error {
	err := c.do(ctx, http.MethodPost, "/networks/"+url.PathEscape(network)+"/disconnect",
		map[string]any{"Container": container, "Force": true}, nil)
	var apiErr *APIError
	if errors.As(err, &apiErr) && (apiErr.Status == http.StatusNotFound || apiErr.Status == http.StatusForbidden) {
		return nil
	}
	return err
}

// RemoveNetwork borra una red (si no existe, no es error).
func (c *Client) RemoveNetwork(ctx context.Context, network string) error {
	err := c.do(ctx, http.MethodDelete, "/networks/"+url.PathEscape(network), nil, nil)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
		return nil
	}
	return err
}

// NetworksWithLabel devuelve los nombres de las redes con una etiqueta ("clave" o "clave=valor").
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

// ContainerIP devuelve la IP de un contenedor en una red.
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
		return "", fmt.Errorf("el contenedor %s no tiene IP en la red %s", container, network)
	}
	return ip, nil
}

// Exec ejecuta un comando dentro de un contenedor y devuelve su salida
// (stdout y stderr).
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

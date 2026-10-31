package docker

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ContainerJSON es lo que hace falta de un contenedor para recrearlo.
type ContainerJSON struct {
	ID         string         `json:"Id"`
	Name       string         `json:"Name"`
	Config     map[string]any `json:"Config"`
	HostConfig map[string]any `json:"HostConfig"`
	Mounts     []struct {
		Type        string `json:"Type"`
		Name        string `json:"Name"`
		Source      string `json:"Source"`
		Destination string `json:"Destination"`
	} `json:"Mounts"`
	NetworkSettings struct {
		Networks map[string]map[string]any `json:"Networks"`
	} `json:"NetworkSettings"`
	State struct {
		Running  bool   `json:"Running"`
		Status   string `json:"Status"`
		ExitCode int    `json:"ExitCode"`
		Health   *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
}

// Inspect devuelve la configuración completa de un contenedor.
func (c *Client) Inspect(ctx context.Context, id string) (*ContainerJSON, error) {
	var out ContainerJSON
	if err := c.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/json", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RenameContainer cambia el nombre de un contenedor.
func (c *Client) RenameContainer(ctx context.Context, id, name string) error {
	return c.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/rename?name="+url.QueryEscape(name), nil, nil)
}

// RemoveContainerKeepVolumes borra un contenedor sin tocar sus volúmenes
// anónimos (los datos de Dockfly pueden estar en uno: VOLUME /data).
func (c *Client) RemoveContainerKeepVolumes(ctx context.Context, id string) error {
	err := c.do(ctx, http.MethodDelete, "/containers/"+url.PathEscape(id)+"?force=1", nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// Clone crea (sin arrancarlo) un contenedor llamado name con la misma
// configuración que old pero con otra imagen: volúmenes, puertos, redes,
// variables, política de reinicio y etiquetas.
func (c *Client) Clone(ctx context.Context, old *ContainerJSON, name, image string) (string, error) {
	shortID := old.ID[:min(12, len(old.ID))]
	cfg := map[string]any{}
	for k, v := range old.Config {
		cfg[k] = v
	}
	cfg["Image"] = image
	// Docker pone de hostname el ID corto: el nuevo debe tener el suyo
	// (Dockfly se reconoce a sí mismo por él).
	if h, _ := cfg["Hostname"].(string); h == shortID {
		delete(cfg, "Hostname")
	}
	// Las etiquetas de la imagen vieja (versión…) no deben pasar a la nueva.
	if labels, ok := cfg["Labels"].(map[string]any); ok {
		clean := map[string]any{}
		for k, v := range labels {
			if !strings.HasPrefix(k, "org.opencontainers.image.") {
				clean[k] = v
			}
		}
		cfg["Labels"] = clean
	}

	host := map[string]any{}
	for k, v := range old.HostConfig {
		host[k] = v
	}
	// Los volúmenes anónimos (VOLUME /data sin montar nada) no están en
	// HostConfig: sin esto el nuevo arrancaría con /data vacío.
	declared := map[string]bool{}
	if binds, ok := host["Binds"].([]any); ok {
		for _, b := range binds {
			if s, ok := b.(string); ok {
				if parts := strings.Split(s, ":"); len(parts) >= 2 {
					declared[parts[1]] = true
				}
			}
		}
	}
	mounts, _ := host["Mounts"].([]any)
	for _, m := range mounts {
		if mm, ok := m.(map[string]any); ok {
			if t, ok := mm["Target"].(string); ok {
				declared[t] = true
			}
		}
	}
	for _, m := range old.Mounts {
		if m.Type == "volume" && !declared[m.Destination] {
			mounts = append(mounts, map[string]any{"Type": "volume", "Source": m.Name, "Target": m.Destination})
		}
	}
	if len(mounts) > 0 {
		host["Mounts"] = mounts
	}

	endpoints := map[string]any{}
	for netName, ep := range old.NetworkSettings.Networks {
		e := map[string]any{}
		for _, k := range []string{"IPAMConfig", "Links", "DriverOpts"} {
			if v, ok := ep[k]; ok && v != nil {
				e[k] = v
			}
		}
		if aliases, ok := ep["Aliases"].([]any); ok {
			var keep []any
			for _, a := range aliases {
				if a != shortID {
					keep = append(keep, a)
				}
			}
			if len(keep) > 0 {
				e["Aliases"] = keep
			}
		}
		endpoints[netName] = e
	}
	cfg["HostConfig"] = host
	cfg["NetworkingConfig"] = map[string]any{"EndpointsConfig": endpoints}

	var out struct {
		ID string `json:"Id"`
	}
	if err := c.do(ctx, http.MethodPost, "/containers/create?name="+url.QueryEscape(name), cfg, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// WaitHealthy espera a que el contenedor esté en marcha y, si tiene
// healthcheck, sano. Falla si se para, se marca como enfermo o pasa timeout.
func (c *Client) WaitHealthy(ctx context.Context, id string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		info, err := c.Inspect(ctx, id)
		if err != nil {
			return err
		}
		switch {
		case !info.State.Running && info.State.Status != "created":
			return fmt.Errorf("el contenedor se paró (código %d)", info.State.ExitCode)
		case info.State.Health == nil && info.State.Running:
			return nil
		case info.State.Health != nil && info.State.Health.Status == "healthy":
			return nil
		case info.State.Health != nil && info.State.Health.Status == "unhealthy":
			return fmt.Errorf("el healthcheck falla")
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no estuvo sano en %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

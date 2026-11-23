package api

import (
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/dockfly/dockfly/internal/docker"
)

func (s *Server) handleListContainers(w http.ResponseWriter, r *http.Request) {
	if s.Docker == nil || !s.Docker.Available(r.Context()) {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "containers": []any{}})
		return
	}
	containers, err := s.Docker.ListContainers(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	// If Dockfly isn't running in Docker (development), it has no networks.
	selfNets, _ := s.Docker.SelfNetworks(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"available":     true,
		"self_networks": selfNets,
		"containers":    containers,
	})
}

// ensureReachable connects the target container to Dockfly's network if they
// don't share one, so that Traefik can resolve it by name.
func (s *Server) ensureReachable(r *http.Request, container string) error {
	if s.Docker == nil {
		return errors.New("Docker isn't available: couldn't check the container's network")
	}
	ctx := r.Context()
	selfNets, err := s.Docker.SelfNetworks(ctx)
	if err != nil || len(selfNets) == 0 {
		return nil // Dockfly isn't running in a container; nothing to connect.
	}
	containers, err := s.Docker.ListContainers(ctx)
	if err != nil {
		return fmt.Errorf("couldn't list containers: %w", err)
	}
	idx := slices.IndexFunc(containers, func(c docker.Container) bool { return c.Name == container })
	if idx < 0 {
		return fmt.Errorf("container %q doesn't exist yet; Traefik will route to it once it's on Dockfly's network", container)
	}
	for _, n := range containers[idx].Networks {
		if slices.Contains(selfNets, n) {
			return nil
		}
	}
	target := s.Traefik.DockerNet
	if target == "" || !slices.Contains(selfNets, target) {
		target = selfNets[0]
	}
	if err := s.Docker.ConnectNetwork(ctx, target, container); err != nil {
		return fmt.Errorf("couldn't connect %q to the %q network: %w", container, target, err)
	}
	s.Log.Info("container connected to Dockfly's network", "container", container, "network", target)
	return nil
}

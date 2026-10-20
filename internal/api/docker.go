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
	// Si Dockfly no corre en Docker (desarrollo), no hay redes propias.
	selfNets, _ := s.Docker.SelfNetworks(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"available":     true,
		"self_networks": selfNets,
		"containers":    containers,
	})
}

// ensureReachable conecta el contenedor destino a la red de Dockfly si no
// comparten ninguna, para que Traefik pueda resolverlo por nombre.
func (s *Server) ensureReachable(r *http.Request, container string) error {
	if s.Docker == nil {
		return errors.New("Docker no está disponible: no se pudo verificar la red del contenedor")
	}
	ctx := r.Context()
	selfNets, err := s.Docker.SelfNetworks(ctx)
	if err != nil || len(selfNets) == 0 {
		return nil // Dockfly no corre en un contenedor; nada que conectar.
	}
	containers, err := s.Docker.ListContainers(ctx)
	if err != nil {
		return fmt.Errorf("no se pudo listar contenedores: %w", err)
	}
	idx := slices.IndexFunc(containers, func(c docker.Container) bool { return c.Name == container })
	if idx < 0 {
		return fmt.Errorf("el contenedor %q no existe todavía; Traefik lo enrutará cuando esté en la red de Dockfly", container)
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
		return fmt.Errorf("no se pudo conectar %q a la red %q: %w", container, target, err)
	}
	s.Log.Info("contenedor conectado a la red de Dockfly", "container", container, "network", target)
	return nil
}

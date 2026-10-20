package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"

	"github.com/dockfly/dockfly/internal/auth"
	"github.com/dockfly/dockfly/internal/store"
	"github.com/dockfly/dockfly/internal/traefik"
)

type accessListView struct {
	ID       int64    `json:"id"`
	Name     string   `json:"name"`
	Users    []string `json:"users"` // solo nombres: los hashes no salen del servidor
	AllowIPs []string `json:"allow_ips"`
	UsedBy   int      `json:"used_by"`
}

func (s *Server) handleListAccessLists(w http.ResponseWriter, r *http.Request) {
	lists, err := s.Store.ListAccessLists(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	used, err := s.accessListUsage(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	out := make([]accessListView, len(lists))
	for i, l := range lists {
		v := accessListView{ID: l.ID, Name: l.Name, AllowIPs: l.AllowIPs, Users: []string{}, UsedBy: used[l.ID]}
		for _, u := range l.Users {
			v.Users = append(v.Users, u.Username)
		}
		out[i] = v
	}
	writeJSON(w, http.StatusOK, out)
}

// accessListUsage cuenta cuántos hosts, sitios y apps usan cada lista.
func (s *Server) accessListUsage(ctx context.Context) (map[int64]int, error) {
	used := map[int64]int{}
	hosts, err := s.Store.ListProxyHosts(ctx)
	if err != nil {
		return nil, err
	}
	for _, h := range hosts {
		if h.AccessListID != nil {
			used[*h.AccessListID]++
		}
	}
	sites, err := s.Store.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	for _, st := range sites {
		if st.AccessListID != nil {
			used[*st.AccessListID]++
		}
	}
	appList, err := s.Store.ListApps(ctx)
	if err != nil {
		return nil, err
	}
	for _, a := range appList {
		if a.AccessListID != nil {
			used[*a.AccessListID]++
		}
	}
	return used, nil
}

type accessListInput struct {
	Name  string `json:"name"`
	Users []struct {
		Username string `json:"username"`
		Password string `json:"password"` // vacío al editar = conservar la actual
	} `json:"users"`
	AllowIPs []string `json:"allow_ips"`
}

func (in *accessListInput) build(previous *store.AccessList) (*store.AccessList, error) {
	l := &store.AccessList{Name: strings.TrimSpace(in.Name), Users: []store.AccessUser{}, AllowIPs: []string{}}
	if l.Name == "" {
		return nil, errors.New("ponle un nombre a la lista")
	}
	old := map[string]string{}
	if previous != nil {
		for _, u := range previous.Users {
			old[u.Username] = u.PasswordHash
		}
	}
	seen := map[string]bool{}
	for _, u := range in.Users {
		name := strings.TrimSpace(u.Username)
		if name == "" || strings.ContainsAny(name, ": ") || seen[name] {
			return nil, fmt.Errorf("usuario inválido o repetido: %q", u.Username)
		}
		seen[name] = true
		hash := old[name]
		if u.Password != "" {
			if len(u.Password) < 6 {
				return nil, fmt.Errorf("la contraseña de %s debe tener al menos 6 caracteres", name)
			}
			var err error
			if hash, err = auth.HashPassword(u.Password); err != nil {
				return nil, err
			}
		}
		if hash == "" {
			return nil, fmt.Errorf("falta la contraseña de %s", name)
		}
		l.Users = append(l.Users, store.AccessUser{Username: name, PasswordHash: hash})
	}
	for _, ip := range in.AllowIPs {
		ip = strings.TrimSpace(ip)
		if ip == "" {
			continue
		}
		// Una IP suelta se convierte en /32 (o /128): Traefik exige rangos válidos
		// y uno mal escrito invalidaría toda la configuración.
		if addr, err := netip.ParseAddr(ip); err == nil {
			ip = netip.PrefixFrom(addr, addr.BitLen()).String()
		} else if p, err := netip.ParsePrefix(ip); err == nil {
			ip = p.Masked().String()
		} else {
			return nil, fmt.Errorf("IP o rango inválido: %q (p. ej. 203.0.113.7 o 192.168.1.0/24)", ip)
		}
		l.AllowIPs = append(l.AllowIPs, ip)
	}
	if len(l.Users) == 0 && len(l.AllowIPs) == 0 {
		return nil, errors.New("añade al menos un usuario o una IP permitida")
	}
	return l, nil
}

func (s *Server) handleCreateAccessList(w http.ResponseWriter, r *http.Request) {
	var in accessListInput
	if !decode(w, r, &in) {
		return
	}
	l, err := in.build(nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Store.CreateAccessList(r.Context(), l); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": l.ID})
}

func (s *Server) handleUpdateAccessList(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	prev, err := s.Store.GetAccessList(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "lista no encontrada")
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	var in accessListInput
	if !decode(w, r, &in) {
		return
	}
	l, err := in.build(prev)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	l.ID = id
	if err := s.Store.UpdateAccessList(r.Context(), l); err != nil {
		s.internalError(w, err)
		return
	}
	s.applyAndRespond(w, r, http.StatusOK, map[string]any{"id": id})
}

func (s *Server) handleDeleteAccessList(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	used, err := s.accessListUsage(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	// Borrarla dejaría públicos de golpe los hosts que protege: mejor que el
	// usuario lo decida uno a uno.
	if used[id] > 0 {
		writeError(w, http.StatusConflict, fmt.Sprintf("la usan %d hosts, sitios o apps: quítasela primero", used[id]))
		return
	}
	if err := s.Store.DeleteAccessList(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "lista no encontrada")
		return
	} else if err != nil {
		s.internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// checkAccessList responde 400 si id apunta a una lista que no existe.
func (s *Server) checkAccessList(w http.ResponseWriter, r *http.Request, id *int64) bool {
	if id == nil {
		return true
	}
	if _, err := s.Store.GetAccessList(r.Context(), *id); err != nil {
		writeError(w, http.StatusBadRequest, "la lista de acceso elegida no existe")
		return false
	}
	return true
}

// accessMiddlewares genera los middlewares de todas las listas y devuelve,
// por lista, los nombres que deben usar sus rutas.
func (s *Server) accessMiddlewares(ctx context.Context, into map[string]any) (map[int64][]string, error) {
	lists, err := s.Store.ListAccessLists(ctx)
	if err != nil {
		return nil, err
	}
	out := map[int64][]string{}
	for _, l := range lists {
		users := map[string]string{}
		for _, u := range l.Users {
			users[u.Username] = u.PasswordHash
		}
		out[l.ID] = traefik.AccessListMiddlewares(l.ID, l.AllowIPs, users, into)
	}
	return out, nil
}

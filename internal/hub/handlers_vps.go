package hub

import (
	"net/http"
	"strings"
	"time"

	"vpspulse/internal/protocol"
	"vpspulse/internal/store"
)

// VPSView enriches a store.VPS record with its grouped containers, systemd units, and probes.
type VPSView struct {
	store.VPS
	Containers   []store.DockerContainerView `json:"containers"`
	SystemdUnits []store.SystemdUnitView     `json:"systemd_units"`
	Probes       []store.Probe               `json:"probes"`
}

func (s *Server) buildVPSView(v store.VPS) VPSView {
	containers, _ := s.store.ListContainers(v.ID)
	units, _ := s.store.ListSystemdUnits(v.ID)
	idCopy := v.ID
	probes, _ := s.store.ListProbes(&idCopy)
	if containers == nil {
		containers = []store.DockerContainerView{}
	}
	if units == nil {
		units = []store.SystemdUnitView{}
	}
	if probes == nil {
		probes = []store.Probe{}
	}
	return VPSView{
		VPS:          v,
		Containers:   containers,
		SystemdUnits: units,
		Probes:       probes,
	}
}

func (s *Server) handleListVPS(w http.ResponseWriter, _ *http.Request) {
	list, err := s.store.ListVPS()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	views := make([]VPSView, 0, len(list))
	for _, v := range list {
		views = append(views, s.buildVPSView(v))
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *Server) handleCreateVPS(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
		Tag  string `json:"tag"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	v, rawToken, err := s.store.CreateVPS(body.Name, body.Tag)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	view := s.buildVPSView(*v)
	if s.broker != nil {
		s.broker.Publish("vps_update", view)
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"vps":   view,
		"token": rawToken,
	})
}

func (s *Server) handleGetVPS(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.GetVPS(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "vps not found")
		return
	}
	writeJSON(w, http.StatusOK, s.buildVPSView(*v))
}

func (s *Server) handleUpdateVPS(w http.ResponseWriter, r *http.Request) {
	var patch store.VPSPatch
	if err := decodeJSON(r, &patch); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	v, err := s.store.UpdateVPS(r.PathValue("id"), patch)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	view := s.buildVPSView(*v)
	if s.broker != nil {
		s.broker.Publish("vps_update", view)
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleDeleteVPS(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.store.DeleteVPS(id); err != nil {
		writeError(w, http.StatusNotFound, "vps not found")
		return
	}
	if s.broker != nil {
		s.broker.Publish("vps_deleted", map[string]string{"id": id})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleRotateVPSToken(w http.ResponseWriter, r *http.Request) {
	tok, err := s.store.RotateVPSToken(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "vps not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": tok})
}

func (s *Server) handleVPSMetrics(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.store.GetVPS(id); err != nil {
		writeError(w, http.StatusNotFound, "vps not found")
		return
	}
	rng := r.URL.Query().Get("range")
	if rng == "" {
		rng = "24h"
	}
	pts, err := s.store.QueryMetrics(id, rng)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, pts)
}

func (s *Server) handleSetWatchedUnits(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.store.GetVPS(id); err != nil {
		writeError(w, http.StatusNotFound, "vps not found")
		return
	}
	var body struct {
		Units []string `json:"units"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := s.store.SetWatchedUnits(id, body.Units); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	v, _ := s.store.GetVPS(id)
	if v != nil {
		view := s.buildVPSView(*v)
		if s.broker != nil {
			s.broker.Publish("vps_update", view)
		}
		writeJSON(w, http.StatusOK, view)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleContainerPolicy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	name := r.PathValue("name")
	var body struct {
		Muted  *bool `json:"muted"`
		Pinned *bool `json:"pinned"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := s.store.UpdateContainerPolicy(id, name, body.Muted, body.Pinned); err != nil {
		writeError(w, http.StatusNotFound, "container not found")
		return
	}
	v, err := s.store.GetVPS(id)
	if err == nil {
		view := s.buildVPSView(*v)
		if s.broker != nil {
			s.broker.Publish("vps_update", view)
		}
		writeJSON(w, http.StatusOK, view)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAgentHeartbeat(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get("Authorization")
	rawToken := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer"))
	if !strings.HasPrefix(authHeader, "Bearer ") || rawToken == "" {
		writeError(w, http.StatusUnauthorized, "missing bearer token")
		return
	}
	vps, err := s.store.AuthenticateAgent(rawToken)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid agent token")
		return
	}

	var payload protocol.HeartbeatPayload
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid heartbeat json")
		return
	}
	if payload.Timestamp.IsZero() {
		payload.Timestamp = time.Now().UTC()
	}

	if err := s.store.RecordHeartbeat(vps.ID, payload); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	updated, err := s.store.GetVPS(vps.ID)
	if err == nil {
		if s.alerts != nil {
			s.alerts.EvaluateHeartbeat(*updated, payload, time.Now())
		}
		if s.broker != nil {
			s.broker.Publish("vps_update", s.buildVPSView(*updated))
		}
	}

	watched, _ := s.store.GetWatchedUnits(vps.ID)
	interval := vps.IntervalSeconds
	if interval < 5 {
		interval = 10
	}
	writeJSON(w, http.StatusOK, protocol.HeartbeatResponse{
		IntervalSeconds: interval,
		WatchedUnits:    watched,
		ServerTime:      time.Now().UTC().Format(time.RFC3339),
	})
}

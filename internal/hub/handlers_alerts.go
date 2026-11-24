package hub

import (
	"net/http"

	"vpspulse/internal/store"
)

func (s *Server) handleListIncidents(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	list, err := s.store.ListIncidents(status, 150)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleAckIncident(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.store.AcknowledgeIncident(id); err != nil {
		writeError(w, http.StatusNotFound, "open incident not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleListChannels(w http.ResponseWriter, _ *http.Request) {
	list, err := s.store.ListChannels()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	var ch store.NotificationChannel
	ch.Enabled = true
	if err := decodeJSON(r, &ch); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	created, err := s.store.CreateChannel(ch)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleUpdateChannel(w http.ResponseWriter, r *http.Request) {
	var ch store.NotificationChannel
	if err := decodeJSON(r, &ch); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	updated, err := s.store.UpdateChannel(r.PathValue("id"), ch)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteChannel(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteChannel(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleTestChannel(w http.ResponseWriter, r *http.Request) {
	ch, err := s.store.GetChannel(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}
	if err := s.alerts.SendTest(r.Context(), *ch); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	sizeBytes, counts := s.store.DBStats()
	writeJSON(w, http.StatusOK, map[string]any{
		"public_url":         s.resolvePublicURL(r),
		"demo_mode":          s.cfg.DemoMode,
		"default_cpu_warn":   s.store.GetSetting("default_cpu_warn", "90"),
		"default_ram_warn":   s.store.GetSetting("default_ram_warn", "90"),
		"default_disk_warn":  s.store.GetSetting("default_disk_warn", "85"),
		"db_size_bytes":      sizeBytes,
		"db_table_counts":    counts,
		"retention_policy":   "Raw 10s (24h) · 1m rollups (7d) · 5m rollups (30d) · Incidents (90d)",
	})
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var body map[string]string
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	allowed := map[string]bool{
		"default_cpu_warn":  true,
		"default_ram_warn":  true,
		"default_disk_warn": true,
	}
	for k, v := range body {
		if allowed[k] {
			_ = s.store.SetSetting(k, v)
		}
	}
	s.handleGetSettings(w, r)
}

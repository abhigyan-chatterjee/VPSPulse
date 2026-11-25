package hub

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"

	"vpspulse/internal/store"
	"vpspulse/internal/version"
)

func maskSecretURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return store.RedactedSentinel
	}
	suffix := ""
	if len(u.Path) >= 4 {
		suffix = "••••" + u.Path[len(u.Path)-4:]
	} else {
		suffix = "••••"
	}
	return u.Scheme + "://" + u.Host + "/" + suffix
}

func redactChannel(ch store.NotificationChannel) store.NotificationChannel {
	out := ch
	out.Config = make(map[string]string, len(ch.Config))
	for k, v := range ch.Config {
		switch k {
		case "password", "bot_token", "secret":
			if v != "" {
				out.Config[k] = store.RedactedSentinel
			}
		case "webhook_url", "url":
			if v != "" {
				out.Config[k] = maskSecretURL(v)
			}
		default:
			out.Config[k] = v
		}
	}
	return out
}

func (s *Server) handleListIncidents(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	list, err := s.store.ListIncidents(status, 150)
	if err != nil {
		writeInternalError(w, "list incidents", err)
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
		writeInternalError(w, "list channels", err)
		return
	}
	redacted := make([]store.NotificationChannel, len(list))
	for i, ch := range list {
		redacted[i] = redactChannel(ch)
	}
	writeJSON(w, http.StatusOK, redacted)
}

func (s *Server) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	var ch store.NotificationChannel
	ch.Enabled = true
	if code, err := decodeJSON(w, r, &ch, 32<<10); err != nil {
		writeError(w, code, err.Error())
		return
	}
	created, err := s.store.CreateChannel(ch)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, redactChannel(*created))
}

func (s *Server) handleUpdateChannel(w http.ResponseWriter, r *http.Request) {
	var patch store.ChannelPatch
	if code, err := decodeJSON(w, r, &patch, 32<<10); err != nil {
		writeError(w, code, err.Error())
		return
	}
	updated, err := s.store.UpdateChannel(r.PathValue("id"), patch)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "channel not found")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, redactChannel(*updated))
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
		log.Printf("test channel %q (%s) failed: %v", ch.Name, ch.Type, err)
		writeError(w, http.StatusBadGateway, "delivery failed; check channel settings and server logs")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	sizeBytes, counts := s.store.DBStats()
	writeJSON(w, http.StatusOK, map[string]any{
		"public_url":        s.resolvePublicURL(r),
		"demo_mode":         s.cfg.DemoMode,
		"version":           version.String(),
		"default_cpu_warn":  s.store.GetSetting("default_cpu_warn", "90"),
		"default_ram_warn":  s.store.GetSetting("default_ram_warn", "90"),
		"default_disk_warn": s.store.GetSetting("default_disk_warn", "85"),
		"db_size_bytes":     sizeBytes,
		"db_table_counts":   counts,
		"retention_policy":  store.DefaultRetentionPolicy().Summary(),
	})
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var body map[string]string
	if code, err := decodeJSON(w, r, &body, 16<<10); err != nil {
		writeError(w, code, err.Error())
		return
	}
	allowed := map[string]bool{
		"default_cpu_warn":  true,
		"default_ram_warn":  true,
		"default_disk_warn": true,
	}
	for k, v := range body {
		if !allowed[k] {
			continue
		}
		n, err := strconv.ParseFloat(v, 64)
		if err != nil || n < 1 || n > 100 {
			writeError(w, http.StatusBadRequest, k+" must be between 1 and 100")
			return
		}
		_ = s.store.SetSetting(k, v)
	}
	s.handleGetSettings(w, r)
}

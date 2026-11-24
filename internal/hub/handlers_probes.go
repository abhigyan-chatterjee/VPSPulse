package hub

import (
	"net/http"

	"vpspulse/internal/store"
)

func (s *Server) handleListProbes(w http.ResponseWriter, r *http.Request) {
	var filter *string
	if r.URL.Query().Has("vps_id") {
		v := r.URL.Query().Get("vps_id")
		filter = &v
	}
	probes, err := s.store.ListProbes(filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, probes)
}

func (s *Server) handleCreateProbe(w http.ResponseWriter, r *http.Request) {
	var p store.Probe
	p.Enabled = true
	if err := decodeJSON(r, &p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	created, err := s.store.CreateProbe(p)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if created.Enabled && s.prober != nil {
		go func(prb store.Probe) {
			_, _ = s.prober.CheckProbeNow(r.Context(), prb)
		}(*created)
	}
	if s.broker != nil {
		s.broker.Publish("probe_update", created)
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleUpdateProbe(w http.ResponseWriter, r *http.Request) {
	var p store.Probe
	if err := decodeJSON(r, &p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	updated, err := s.store.UpdateProbe(r.PathValue("id"), p)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.broker != nil {
		s.broker.Publish("probe_update", updated)
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteProbe(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.store.DeleteProbe(id); err != nil {
		writeError(w, http.StatusNotFound, "probe not found")
		return
	}
	if s.broker != nil {
		s.broker.Publish("probe_deleted", map[string]string{"id": id})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleCheckProbeNow(w http.ResponseWriter, r *http.Request) {
	probe, err := s.store.GetProbe(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "probe not found")
		return
	}
	if s.prober == nil {
		writeError(w, http.StatusServiceUnavailable, "prober unavailable")
		return
	}
	res, err := s.prober.CheckProbeNow(r.Context(), *probe)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, _ := s.store.GetProbe(probe.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"result": res,
		"probe":  updated,
	})
}

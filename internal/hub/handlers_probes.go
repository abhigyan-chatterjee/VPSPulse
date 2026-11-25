package hub

import (
	"database/sql"
	"errors"
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
		writeInternalError(w, "list probes", err)
		return
	}
	writeJSON(w, http.StatusOK, probes)
}

func (s *Server) handleCreateProbe(w http.ResponseWriter, r *http.Request) {
	var p store.Probe
	p.Enabled = true
	if code, err := decodeJSON(w, r, &p, 32<<10); err != nil {
		writeError(w, code, err.Error())
		return
	}
	created, err := s.store.CreateProbe(p)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if created.Enabled && s.prober != nil {
		go func(prb store.Probe) {
			_, _ = s.prober.CheckProbeNow(s.ctx, prb)
		}(*created)
	}
	if s.broker != nil {
		s.broker.Publish("probe_update", created)
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleUpdateProbe(w http.ResponseWriter, r *http.Request) {
	var patch store.ProbePatch
	if code, err := decodeJSON(w, r, &patch, 32<<10); err != nil {
		writeError(w, code, err.Error())
		return
	}
	updated, err := s.store.UpdateProbe(r.PathValue("id"), patch)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "probe not found")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !updated.Enabled && s.alerts != nil {
		s.alerts.ForgetProbe(updated.ID)
	}
	if s.broker != nil {
		s.broker.Publish("probe_update", updated)
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteProbe(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if resolved, err := s.store.ResolveIncidentsByProbe(id, "Probe deleted"); err == nil && s.broker != nil {
		for i := range resolved {
			s.broker.Publish("incident_update", &resolved[i])
		}
	}
	if err := s.store.DeleteProbe(id); err != nil {
		writeError(w, http.StatusNotFound, "probe not found")
		return
	}
	if s.alerts != nil {
		s.alerts.ForgetProbe(id)
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
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	updated, _ := s.store.GetProbe(probe.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"result": res,
		"probe":  updated,
	})
}

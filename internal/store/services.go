package store

import (
	"time"
)

// ServiceTick represents a single status sample for a container, unit, or probe.
type ServiceTick struct {
	Timestamp int64  `json:"ts"`
	Status    string `json:"status"`
	LatencyMs int    `json:"latency_ms,omitempty"`
}

// DockerContainerView combines latest container state with policy flags and uptime history.
type DockerContainerView struct {
	VPSID        string        `json:"vps_id"`
	Name         string        `json:"name"`
	ContainerID  string        `json:"container_id"`
	Image        string        `json:"image"`
	State        string        `json:"state"`
	Health       string        `json:"health"`
	ExitCode     int           `json:"exit_code"`
	OOMKilled    bool          `json:"oom_killed"`
	StartedAt    string        `json:"started_at"`
	RestartCount int           `json:"restart_count"`
	CPUPercent   float64       `json:"cpu_percent"`
	MemUsedBytes uint64        `json:"mem_used_bytes"`
	MemLimitBytes uint64       `json:"mem_limit_bytes"`
	Muted        bool          `json:"muted"`
	Pinned       bool          `json:"pinned"`
	UpdatedAt    int64         `json:"updated_at"`
	Uptime24h    float64       `json:"uptime_24h"`
	Ticks        []ServiceTick `json:"ticks"`
}

// SystemdUnitView combines latest systemd unit state with uptime history.
type SystemdUnitView struct {
	VPSID       string        `json:"vps_id"`
	Name        string        `json:"name"`
	ActiveState string        `json:"active_state"`
	SubState    string        `json:"sub_state"`
	MemBytes    uint64        `json:"mem_bytes"`
	ActiveSince string        `json:"active_since"`
	AutoFailed  bool          `json:"auto_failed"`
	Watched     bool          `json:"watched"`
	UpdatedAt   int64         `json:"updated_at"`
	Uptime24h   float64       `json:"uptime_24h"`
	Ticks       []ServiceTick `json:"ticks"`
}

// ListContainers returns Docker containers for a VPS with 30-tick history and 24h uptime.
func (s *Store) ListContainers(vpsID string) ([]DockerContainerView, error) {
	rows, err := s.db.Query(
		`SELECT vps_id, name, container_id, image, state, health, exit_code, oom_killed,
		        started_at, restart_count, cpu_percent, mem_used, mem_limit, muted, pinned, updated_at
		 FROM vps_containers WHERE vps_id = ? ORDER BY pinned DESC, name ASC`,
		vpsID,
	)
	if err != nil {
		return nil, err
	}
	var list []DockerContainerView
	for rows.Next() {
		var c DockerContainerView
		var oom, muted, pinned int
		if err := rows.Scan(
			&c.VPSID, &c.Name, &c.ContainerID, &c.Image, &c.State, &c.Health, &c.ExitCode, &oom,
			&c.StartedAt, &c.RestartCount, &c.CPUPercent, &c.MemUsedBytes, &c.MemLimitBytes,
			&muted, &pinned, &c.UpdatedAt,
		); err != nil {
			_ = rows.Close()
			return nil, err
		}
		c.OOMKilled = oom == 1
		c.Muted = muted == 1
		c.Pinned = pinned == 1
		list = append(list, c)
	}
	_ = rows.Close()

	for i := range list {
		list[i].Ticks, _ = s.getServiceTicks(vpsID, "docker", list[i].Name, 30)
		list[i].Uptime24h = s.computeServiceUptime24h(vpsID, "docker", list[i].Name)
	}
	if list == nil {
		list = []DockerContainerView{}
	}
	return list, nil
}

// ListSystemdUnits returns systemd units for a VPS with 30-tick history and 24h uptime.
func (s *Store) ListSystemdUnits(vpsID string) ([]SystemdUnitView, error) {
	watched, _ := s.GetWatchedUnits(vpsID)
	watchedSet := map[string]bool{}
	for _, w := range watched {
		watchedSet[w] = true
	}

	rows, err := s.db.Query(
		`SELECT vps_id, name, active_state, sub_state, mem_bytes, active_since, auto_failed, updated_at
		 FROM vps_systemd_units WHERE vps_id = ? ORDER BY auto_failed DESC, name ASC`,
		vpsID,
	)
	if err != nil {
		return nil, err
	}
	var list []SystemdUnitView
	for rows.Next() {
		var u SystemdUnitView
		var autoFailed int
		if err := rows.Scan(&u.VPSID, &u.Name, &u.ActiveState, &u.SubState, &u.MemBytes, &u.ActiveSince, &autoFailed, &u.UpdatedAt); err != nil {
			_ = rows.Close()
			return nil, err
		}
		u.AutoFailed = autoFailed == 1
		u.Watched = watchedSet[u.Name]
		list = append(list, u)
	}
	_ = rows.Close()

	for i := range list {
		list[i].Ticks, _ = s.getServiceTicks(vpsID, "systemd", list[i].Name, 30)
		list[i].Uptime24h = s.computeServiceUptime24h(vpsID, "systemd", list[i].Name)
	}
	if list == nil {
		list = []SystemdUnitView{}
	}
	return list, nil
}

func (s *Store) getServiceTicks(vpsID, kind, name string, limit int) ([]ServiceTick, error) {
	rows, err := s.db.Query(
		`SELECT ts, status FROM service_ticks
		 WHERE vps_id = ? AND kind = ? AND name = ?
		 ORDER BY ts DESC LIMIT ?`,
		vpsID, kind, name, limit,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	rev := []ServiceTick{}
	for rows.Next() {
		var t ServiceTick
		if err := rows.Scan(&t.Timestamp, &t.Status); err != nil {
			return nil, err
		}
		rev = append(rev, t)
	}
	out := make([]ServiceTick, len(rev))
	for i := range rev {
		out[i] = rev[len(rev)-1-i]
	}
	return out, nil
}

func (s *Store) computeServiceUptime24h(vpsID, kind, name string) float64 {
	since := time.Now().Add(-24 * time.Hour).Unix()
	var total, up int
	err := s.db.QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(CASE WHEN status = 'up' THEN 1 ELSE 0 END), 0)
		 FROM service_ticks WHERE vps_id = ? AND kind = ? AND name = ? AND ts >= ?`,
		vpsID, kind, name, since,
	).Scan(&total, &up)
	if err != nil || total == 0 {
		return 100.0
	}
	return float64(up) * 100 / float64(total)
}

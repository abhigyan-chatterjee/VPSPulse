package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"vpspulse/internal/protocol"
)

var validSystemdUnitRe = regexp.MustCompile(`^[A-Za-z0-9:_@.-]{1,256}$`)

// VPS represents a monitored virtual private server.
type VPS struct {
	ID                string                `json:"id"`
	Name              string                `json:"name"`
	Tag               string                `json:"tag"`
	Status            string                `json:"status"`
	LastSeenAt        int64                 `json:"last_seen_at"`
	Hostname          string                `json:"hostname"`
	OS                string                `json:"os"`
	Kernel            string                `json:"kernel"`
	Arch              string                `json:"arch"`
	IP                string                `json:"ip"`
	UptimeSec         uint64                `json:"uptime_sec"`
	CPUCores          int                   `json:"cpu_cores"`
	AgentVersion      string                `json:"agent_version"`
	DockerAvailable   bool                  `json:"docker_available"`
	SystemdAvailable  bool                  `json:"systemd_available"`
	DiscoverableUnits []string              `json:"discoverable_units"`
	WatchedUnits      []string              `json:"watched_units"`
	Disks             []protocol.DiskReport `json:"disks"`
	CPUWarn           float64               `json:"cpu_warn"`
	RAMWarn           float64               `json:"ram_warn"`
	DiskWarn          float64               `json:"disk_warn"`
	IntervalSeconds   int                   `json:"interval_seconds"`
	CreatedAt         int64                 `json:"created_at"`
	LatestMetric      *HostMetricPoint      `json:"latest_metric,omitempty"`
	Sparkline         []float64             `json:"sparkline,omitempty"`
}

// VPSPatch updates mutable VPS fields when non-nil.
type VPSPatch struct {
	Name            *string  `json:"name"`
	Tag             *string  `json:"tag"`
	CPUWarn         *float64 `json:"cpu_warn"`
	RAMWarn         *float64 `json:"ram_warn"`
	DiskWarn        *float64 `json:"disk_warn"`
	IntervalSeconds *int     `json:"interval_seconds"`
}

func (s *Store) defaultThreshold(key string, fallback float64) float64 {
	raw := strings.TrimSpace(s.GetSetting(key, ""))
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v < 1 || v > 100 {
		return fallback
	}
	return v
}

// CreateVPS registers a new VPS and returns it alongside its one-time raw agent token.
func (s *Store) CreateVPS(name, tag string) (*VPS, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, "", errors.New("vps name is required")
	}
	tag = strings.TrimSpace(tag)
	id := newID("vps")
	rawToken := randomToken("vpsp_", 24)
	now := time.Now().Unix()

	cpuWarn := s.defaultThreshold("default_cpu_warn", 90)
	ramWarn := s.defaultThreshold("default_ram_warn", 90)
	diskWarn := s.defaultThreshold("default_disk_warn", 85)

	_, err := s.db.Exec(
		`INSERT INTO vps (id, name, tag, token_hash, status, cpu_warn, ram_warn, disk_warn, created_at)
		 VALUES (?, ?, ?, ?, 'pending', ?, ?, ?, ?)`,
		id, name, tag, hashToken(rawToken), cpuWarn, ramWarn, diskWarn, now,
	)
	if err != nil {
		return nil, "", err
	}
	v, err := s.GetVPS(id)
	if err != nil {
		return nil, "", err
	}
	return v, rawToken, nil
}

// GetVPS returns a single VPS with latest metric and watched units.
func (s *Store) GetVPS(id string) (*VPS, error) {
	var v VPS
	var dockerAvail, systemdAvail int
	var discJSON, disksJSON string
	err := s.db.QueryRow(
		`SELECT id, name, tag, status, last_seen_at, hostname, os, kernel, arch, ip,
		        uptime_sec, cpu_cores, agent_version, docker_available, systemd_available,
		        discoverable_units_json, disks_json, cpu_warn, ram_warn, disk_warn, interval_seconds, created_at
		 FROM vps WHERE id = ?`,
		id,
	).Scan(
		&v.ID, &v.Name, &v.Tag, &v.Status, &v.LastSeenAt, &v.Hostname, &v.OS, &v.Kernel, &v.Arch, &v.IP,
		&v.UptimeSec, &v.CPUCores, &v.AgentVersion, &dockerAvail, &systemdAvail,
		&discJSON, &disksJSON, &v.CPUWarn, &v.RAMWarn, &v.DiskWarn, &v.IntervalSeconds, &v.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	v.DockerAvailable = dockerAvail == 1
	v.SystemdAvailable = systemdAvail == 1
	_ = json.Unmarshal([]byte(discJSON), &v.DiscoverableUnits)
	if v.DiscoverableUnits == nil {
		v.DiscoverableUnits = []string{}
	}
	_ = json.Unmarshal([]byte(disksJSON), &v.Disks)
	if v.Disks == nil {
		v.Disks = []protocol.DiskReport{}
	}
	watched, err := s.GetWatchedUnits(v.ID)
	if err == nil {
		v.WatchedUnits = watched
	} else {
		v.WatchedUnits = []string{}
	}
	latest, err := s.GetLatestMetric(v.ID)
	if err == nil {
		v.LatestMetric = latest
	}
	spark, err := s.GetRecentCPUSparkline(v.ID, 16)
	if err == nil {
		v.Sparkline = spark
	}
	return &v, nil
}

// ListVPS returns all configured VPSes ordered by creation time.
func (s *Store) ListVPS() ([]VPS, error) {
	rows, err := s.db.Query(`SELECT id FROM vps ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()

	out := make([]VPS, 0, len(ids))
	for _, id := range ids {
		v, err := s.GetVPS(id)
		if err == nil {
			out = append(out, *v)
		}
	}
	return out, nil
}

// UpdateVPS modifies editable VPS metadata and alert thresholds.
func (s *Store) UpdateVPS(id string, patch VPSPatch) (*VPS, error) {
	cur, err := s.GetVPS(id)
	if err != nil {
		return nil, err
	}
	if patch.Name != nil {
		trimmed := strings.TrimSpace(*patch.Name)
		if trimmed == "" {
			return nil, errors.New("vps name cannot be empty")
		}
		cur.Name = trimmed
	}
	if patch.Tag != nil {
		cur.Tag = strings.TrimSpace(*patch.Tag)
	}
	if patch.CPUWarn != nil {
		if *patch.CPUWarn < 1 || *patch.CPUWarn > 100 {
			return nil, errors.New("cpu_warn must be between 1 and 100")
		}
		cur.CPUWarn = *patch.CPUWarn
	}
	if patch.RAMWarn != nil {
		if *patch.RAMWarn < 1 || *patch.RAMWarn > 100 {
			return nil, errors.New("ram_warn must be between 1 and 100")
		}
		cur.RAMWarn = *patch.RAMWarn
	}
	if patch.DiskWarn != nil {
		if *patch.DiskWarn < 1 || *patch.DiskWarn > 100 {
			return nil, errors.New("disk_warn must be between 1 and 100")
		}
		cur.DiskWarn = *patch.DiskWarn
	}
	if patch.IntervalSeconds != nil {
		if *patch.IntervalSeconds < 5 || *patch.IntervalSeconds > 300 {
			return nil, errors.New("interval_seconds must be between 5 and 300")
		}
		cur.IntervalSeconds = *patch.IntervalSeconds
	}
	_, err = s.db.Exec(
		`UPDATE vps SET name=?, tag=?, cpu_warn=?, ram_warn=?, disk_warn=?, interval_seconds=? WHERE id=?`,
		cur.Name, cur.Tag, cur.CPUWarn, cur.RAMWarn, cur.DiskWarn, cur.IntervalSeconds, id,
	)
	if err != nil {
		return nil, err
	}
	return s.GetVPS(id)
}

// DeleteVPS deletes a VPS and cascades its metrics/service state.
func (s *Store) DeleteVPS(id string) error {
	res, err := s.db.Exec(`DELETE FROM vps WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// RotateVPSToken replaces the VPS token hash and returns the new raw token.
func (s *Store) RotateVPSToken(id string) (string, error) {
	rawToken := randomToken("vpsp_", 24)
	res, err := s.db.Exec(`UPDATE vps SET token_hash = ? WHERE id = ?`, hashToken(rawToken), id)
	if err != nil {
		return "", err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return "", sql.ErrNoRows
	}
	return rawToken, nil
}

// AuthenticateAgent resolves a raw bearer token to its VPS.
func (s *Store) AuthenticateAgent(rawToken string) (*VPS, error) {
	if rawToken == "" {
		return nil, errors.New("missing agent token")
	}
	var id string
	err := s.db.QueryRow(`SELECT id FROM vps WHERE token_hash = ?`, hashToken(rawToken)).Scan(&id)
	if err != nil {
		return nil, err
	}
	return s.GetVPS(id)
}

// SetVPSStatus updates a VPS's online/offline status.
func (s *Store) SetVPSStatus(id, status string) error {
	_, err := s.db.Exec(`UPDATE vps SET status = ? WHERE id = ?`, status, id)
	return err
}

// ValidateUnitName validates a systemd unit name to prevent CLI flag injection.
func ValidateUnitName(name string) error {
	if name == "" || strings.HasPrefix(name, "-") || !validSystemdUnitRe.MatchString(name) {
		return fmt.Errorf("invalid systemd unit name %q", name)
	}
	return nil
}

// SetWatchedUnits replaces the watched systemd unit list for a VPS.
func (s *Store) SetWatchedUnits(vpsID string, units []string) error {
	cleaned := make([]string, 0, len(units))
	seen := map[string]bool{}
	for _, u := range units {
		u = strings.TrimSpace(u)
		if u == "" || seen[u] {
			continue
		}
		if err := ValidateUnitName(u); err != nil {
			return err
		}
		seen[u] = true
		cleaned = append(cleaned, u)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`DELETE FROM vps_watched_units WHERE vps_id = ?`, vpsID); err != nil {
		return err
	}
	for _, u := range cleaned {
		if _, err := tx.Exec(`INSERT INTO vps_watched_units (vps_id, unit_name) VALUES (?, ?)`, vpsID, u); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetWatchedUnits lists watched systemd unit names for a VPS.
func (s *Store) GetWatchedUnits(vpsID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT unit_name FROM vps_watched_units WHERE vps_id = ? ORDER BY unit_name ASC`, vpsID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []string{}
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UpdateContainerPolicy updates muted/pinned flags for a container identified by (vpsID, containerName).
func (s *Store) UpdateContainerPolicy(vpsID, containerName string, muted, pinned *bool) error {
	var curMuted, curPinned int
	err := s.db.QueryRow(
		`SELECT muted, pinned FROM vps_containers WHERE vps_id = ? AND name = ?`,
		vpsID, containerName,
	).Scan(&curMuted, &curPinned)
	if err != nil {
		return err
	}
	if muted != nil {
		if *muted {
			curMuted = 1
		} else {
			curMuted = 0
		}
	}
	if pinned != nil {
		if *pinned {
			curPinned = 1
		} else {
			curPinned = 0
		}
	}
	_, err = s.db.Exec(
		`UPDATE vps_containers SET muted = ?, pinned = ? WHERE vps_id = ? AND name = ?`,
		curMuted, curPinned, vpsID, containerName,
	)
	return err
}

func clampInt64(v uint64) int64 {
	if v > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(v)
}

// RecordHeartbeat persists a heartbeat payload from an agent and updates container/systemd history.
func (s *Store) RecordHeartbeat(vpsID string, payload protocol.HeartbeatPayload) error {
	now := payload.Timestamp.Unix()
	if now <= 0 {
		now = time.Now().Unix()
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	discJSON, _ := json.Marshal(payload.DiscoverableUnits)
	disksJSON, _ := json.Marshal(payload.Metrics.Disks)
	dockerAvail := 0
	if payload.DockerAvailable {
		dockerAvail = 1
	}
	systemdAvail := 0
	if payload.SystemdAvailable {
		systemdAvail = 1
	}

	if _, err := tx.Exec(
		`UPDATE vps SET
			status = 'online',
			last_seen_at = ?,
			hostname = ?,
			os = ?,
			kernel = ?,
			arch = ?,
			ip = ?,
			uptime_sec = ?,
			cpu_cores = ?,
			agent_version = ?,
			docker_available = ?,
			systemd_available = ?,
			discoverable_units_json = CASE WHEN ? != 'null' AND ? != '[]' THEN ? ELSE discoverable_units_json END,
			disks_json = ?
		 WHERE id = ?`,
		now,
		payload.Host.Hostname, payload.Host.OS, payload.Host.Kernel, payload.Host.Arch, payload.Host.IP,
		clampInt64(payload.Host.UptimeSec), payload.Host.CPUCores, payload.AgentVersion,
		dockerAvail, systemdAvail,
		string(discJSON), string(discJSON), string(discJSON),
		string(disksJSON),
		vpsID,
	); err != nil {
		return err
	}

	m := payload.Metrics
	if _, err := tx.Exec(
		`INSERT INTO host_metrics_raw (
			vps_id, ts, cpu_percent, load1, load5, load15,
			ram_used, ram_total, swap_used, swap_total,
			disk_used, disk_total, net_rx, net_tx
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		vpsID, now, m.CPUPercent, m.Load1, m.Load5, m.Load15,
		clampInt64(m.RAMUsedBytes), clampInt64(m.RAMTotalBytes), clampInt64(m.SwapUsedBytes), clampInt64(m.SwapTotalBytes),
		clampInt64(m.DiskUsedBytes), clampInt64(m.DiskTotalBytes), m.NetRxBytesPerSec, m.NetTxBytesPerSec,
	); err != nil {
		return err
	}

	reportedContainers := make(map[string]bool, len(payload.Containers))
	for _, c := range payload.Containers {
		reportedContainers[c.Name] = true
		oom := 0
		if c.OOMKilled {
			oom = 1
		}
		if _, err := tx.Exec(
			`INSERT INTO vps_containers (
				vps_id, name, container_id, image, state, health, exit_code, oom_killed,
				started_at, restart_count, cpu_percent, mem_used, mem_limit, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(vps_id, name) DO UPDATE SET
				container_id = excluded.container_id,
				image = excluded.image,
				state = excluded.state,
				health = excluded.health,
				exit_code = excluded.exit_code,
				oom_killed = excluded.oom_killed,
				started_at = excluded.started_at,
				restart_count = excluded.restart_count,
				cpu_percent = excluded.cpu_percent,
				mem_used = excluded.mem_used,
				mem_limit = excluded.mem_limit,
				updated_at = excluded.updated_at`,
			vpsID, c.Name, c.ID, c.Image, c.State, c.Health, c.ExitCode, oom,
			c.StartedAt, c.RestartCount, c.CPUPercent, clampInt64(c.MemUsedBytes), clampInt64(c.MemLimitBytes), now,
		); err != nil {
			return err
		}

		tickStatus := classifyContainerTick(c)
		if _, err := tx.Exec(
			`INSERT INTO service_ticks (vps_id, kind, name, ts, status) VALUES (?, 'docker', ?, ?, ?)`,
			vpsID, c.Name, now, tickStatus,
		); err != nil {
			return err
		}
	}

	if payload.DockerAvailable {
		rows, err := tx.Query(`SELECT name, pinned FROM vps_containers WHERE vps_id = ?`, vpsID)
		if err != nil {
			return err
		}
		var toDelete []string
		var missingPinned []string
		for rows.Next() {
			var cname string
			var pinned int
			if err := rows.Scan(&cname, &pinned); err != nil {
				_ = rows.Close()
				return err
			}
			if !reportedContainers[cname] {
				if pinned == 1 {
					missingPinned = append(missingPinned, cname)
				} else {
					toDelete = append(toDelete, cname)
				}
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		_ = rows.Close()
		for _, cname := range toDelete {
			if _, err := tx.Exec(`DELETE FROM vps_containers WHERE vps_id = ? AND name = ?`, vpsID, cname); err != nil {
				return err
			}
		}
		for _, cname := range missingPinned {
			if _, err := tx.Exec(`UPDATE vps_containers SET state = 'missing', health = 'none', cpu_percent = 0, mem_used = 0, updated_at = ? WHERE vps_id = ? AND name = ?`, now, vpsID, cname); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO service_ticks (vps_id, kind, name, ts, status) VALUES (?, 'docker', ?, ?, 'down')`, vpsID, cname, now); err != nil {
				return err
			}
		}
	}

	reportedUnits := make(map[string]bool, len(payload.SystemdUnits))
	for _, u := range payload.SystemdUnits {
		reportedUnits[u.Name] = true
		autoFailed := 0
		if u.AutoFailed {
			autoFailed = 1
		}
		if _, err := tx.Exec(
			`INSERT INTO vps_systemd_units (vps_id, name, active_state, sub_state, mem_bytes, active_since, auto_failed, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(vps_id, name) DO UPDATE SET
				active_state = excluded.active_state,
				sub_state = excluded.sub_state,
				mem_bytes = excluded.mem_bytes,
				active_since = excluded.active_since,
				auto_failed = excluded.auto_failed,
				updated_at = excluded.updated_at`,
			vpsID, u.Name, u.ActiveState, u.SubState, clampInt64(u.MemBytes), u.ActiveSince, autoFailed, now,
		); err != nil {
			return err
		}
		tickStatus := "up"
		if u.ActiveState == "failed" || u.ActiveState == "inactive" {
			tickStatus = "down"
		} else if u.ActiveState != "active" {
			tickStatus = "warn"
		}
		if _, err := tx.Exec(
			`INSERT INTO service_ticks (vps_id, kind, name, ts, status) VALUES (?, 'systemd', ?, ?, ?)`,
			vpsID, u.Name, now, tickStatus,
		); err != nil {
			return err
		}
	}

	if payload.SystemdAvailable {
		rows, err := tx.Query(`SELECT name FROM vps_systemd_units WHERE vps_id = ?`, vpsID)
		if err != nil {
			return err
		}
		var staleUnits []string
		for rows.Next() {
			var uname string
			if err := rows.Scan(&uname); err != nil {
				_ = rows.Close()
				return err
			}
			if !reportedUnits[uname] {
				staleUnits = append(staleUnits, uname)
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		_ = rows.Close()
		for _, uname := range staleUnits {
			if _, err := tx.Exec(`DELETE FROM vps_systemd_units WHERE vps_id = ? AND name = ?`, vpsID, uname); err != nil {
				return err
			}
		}
	}

	return tx.Commit()
}

func classifyContainerTick(c protocol.DockerContainerReport) string {
	state := strings.ToLower(c.State)
	health := strings.ToLower(c.Health)
	if state == "restarting" || health == "unhealthy" || c.OOMKilled {
		return "warn"
	}
	if state == "running" {
		if health == "starting" {
			return "warn"
		}
		return "up"
	}
	if state == "exited" && c.ExitCode == 0 {
		return "up"
	}
	return "down"
}

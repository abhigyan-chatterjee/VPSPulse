package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var validHostnameRe = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9.-]{0,251}[a-zA-Z0-9])?$`)

// Probe defines an external health check managed by the central hub.
type Probe struct {
	ID               string        `json:"id"`
	VPSID            *string       `json:"vps_id"`
	Name             string        `json:"name"`
	Type             string        `json:"type"` // "http", "tcp", "icmp"
	Target           string        `json:"target"`
	Method           string        `json:"method"`
	ExpectedMin      int           `json:"expected_min"`
	ExpectedMax      int           `json:"expected_max"`
	Keyword          string        `json:"keyword"`
	TimeoutSec       int           `json:"timeout_sec"`
	IntervalSec      int           `json:"interval_sec"`
	FailureThreshold int           `json:"failure_threshold"`
	Enabled          bool          `json:"enabled"`
	CreatedAt        int64         `json:"created_at"`
	Status           string        `json:"status"`
	LatencyMs        int           `json:"latency_ms"`
	TLSExpiryDays    *int          `json:"tls_expiry_days"`
	LastError        string        `json:"last_error"`
	LastCheckedAt    int64         `json:"last_checked_at"`
	Uptime24h        float64       `json:"uptime_24h"`
	Uptime7d         float64       `json:"uptime_7d"`
	Uptime30d        float64       `json:"uptime_30d"`
	Ticks            []ServiceTick `json:"ticks"`
}

// ProbePatch represents partial updates for PATCH /api/v1/probes/{id}.
type ProbePatch struct {
	VPSID            *string `json:"vps_id"`
	Name             *string `json:"name"`
	Type             *string `json:"type"`
	Target           *string `json:"target"`
	Method           *string `json:"method"`
	ExpectedMin      *int    `json:"expected_min"`
	ExpectedMax      *int    `json:"expected_max"`
	Keyword          *string `json:"keyword"`
	TimeoutSec       *int    `json:"timeout_sec"`
	IntervalSec      *int    `json:"interval_sec"`
	FailureThreshold *int    `json:"failure_threshold"`
	Enabled          *bool   `json:"enabled"`
}

// ProbeCheckResult is a single execution result of a probe.
type ProbeCheckResult struct {
	Timestamp     int64  `json:"ts"`
	Status        string `json:"status"` // "up", "down", "degraded"
	LatencyMs     int    `json:"latency_ms"`
	StatusCode    int    `json:"status_code"`
	TLSExpiryDays *int   `json:"tls_expiry_days,omitempty"`
	ErrorMsg      string `json:"error_msg"`
}

// ValidateProbeTarget checks probe target format by probe type.
func ValidateProbeTarget(typ, target string) error {
	target = strings.TrimSpace(target)
	if target == "" {
		return errors.New("probe target is required")
	}
	switch strings.ToLower(strings.TrimSpace(typ)) {
	case "http", "https":
		u, err := url.Parse(target)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("http probe target must be a valid http:// or https:// URL")
		}
		return nil
	case "tcp":
		host, portStr, err := net.SplitHostPort(target)
		if err != nil || host == "" {
			return errors.New("tcp probe target must be host:port")
		}
		port, err := strconv.Atoi(portStr)
		if err != nil || port < 1 || port > 65535 {
			return errors.New("tcp probe port must be between 1 and 65535")
		}
		return nil
	case "icmp", "ping":
		if strings.HasPrefix(target, "-") {
			return errors.New("icmp target cannot start with '-'")
		}
		if net.ParseIP(target) != nil {
			return nil
		}
		if !validHostnameRe.MatchString(target) {
			return fmt.Errorf("invalid icmp hostname or IP %q", target)
		}
		return nil
	default:
		return fmt.Errorf("unsupported probe type %q", typ)
	}
}

// CreateProbe inserts a new external probe.
func (s *Store) CreateProbe(p Probe) (*Probe, error) {
	p.Name = strings.TrimSpace(p.Name)
	p.Target = strings.TrimSpace(p.Target)
	p.Type = strings.ToLower(strings.TrimSpace(p.Type))
	if p.Name == "" {
		return nil, errors.New("probe name is required")
	}
	if p.Type == "" {
		p.Type = "http"
	}
	if err := ValidateProbeTarget(p.Type, p.Target); err != nil {
		return nil, err
	}
	if p.Method == "" {
		p.Method = "GET"
	}
	if p.ExpectedMin == 0 {
		p.ExpectedMin = 200
	}
	if p.ExpectedMax == 0 {
		p.ExpectedMax = 299
	}
	if p.TimeoutSec <= 0 {
		p.TimeoutSec = 10
	} else if p.TimeoutSec > 30 {
		p.TimeoutSec = 30
	}
	if p.IntervalSec < 10 {
		p.IntervalSec = 30
	} else if p.IntervalSec > 3600 {
		p.IntervalSec = 3600
	}
	if p.FailureThreshold <= 0 {
		p.FailureThreshold = 2
	}
	p.ID = newID("prb")
	p.CreatedAt = time.Now().Unix()
	enabledInt := 1
	if !p.Enabled {
		enabledInt = 0
	}
	var vpsArg any
	if p.VPSID != nil && *p.VPSID != "" {
		vpsArg = *p.VPSID
	}

	_, err := s.db.Exec(
		`INSERT INTO probes (
			id, vps_id, name, type, target, method, expected_min, expected_max,
			keyword, timeout_sec, interval_sec, failure_threshold, enabled, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, vpsArg, p.Name, p.Type, p.Target, strings.ToUpper(p.Method),
		p.ExpectedMin, p.ExpectedMax, p.Keyword, p.TimeoutSec, p.IntervalSec,
		p.FailureThreshold, enabledInt, p.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return s.GetProbe(p.ID)
}

// GetProbe fetches a probe by ID along with its history and uptime percentages.
func (s *Store) GetProbe(id string) (*Probe, error) {
	var p Probe
	var vpsID sql.NullString
	var enabled int
	err := s.db.QueryRow(
		`SELECT id, vps_id, name, type, target, method, expected_min, expected_max,
		        keyword, timeout_sec, interval_sec, failure_threshold, enabled, created_at
		 FROM probes WHERE id = ?`,
		id,
	).Scan(
		&p.ID, &vpsID, &p.Name, &p.Type, &p.Target, &p.Method, &p.ExpectedMin, &p.ExpectedMax,
		&p.Keyword, &p.TimeoutSec, &p.IntervalSec, &p.FailureThreshold, &enabled, &p.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	if vpsID.Valid {
		v := vpsID.String
		p.VPSID = &v
	}
	p.Enabled = enabled == 1
	s.hydrateProbeStats(&p)
	return &p, nil
}

// ListProbes returns probes optionally filtered by VPS ID (or standalone if empty string pointer).
func (s *Store) ListProbes(vpsIDFilter *string) ([]Probe, error) {
	query := `SELECT id FROM probes`
	var args []any
	if vpsIDFilter != nil {
		if *vpsIDFilter == "" {
			query += ` WHERE vps_id IS NULL`
		} else {
			query += ` WHERE vps_id = ?`
			args = append(args, *vpsIDFilter)
		}
	}
	query += ` ORDER BY created_at ASC`

	rows, err := s.db.Query(query, args...)
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

	out := make([]Probe, 0, len(ids))
	for _, id := range ids {
		p, err := s.GetProbe(id)
		if err == nil {
			out = append(out, *p)
		}
	}
	return out, nil
}

// UpdateProbe applies a partial ProbePatch to an existing probe.
func (s *Store) UpdateProbe(id string, p ProbePatch) (*Probe, error) {
	cur, err := s.GetProbe(id)
	if err != nil {
		return nil, err
	}
	if p.Name != nil {
		trimmed := strings.TrimSpace(*p.Name)
		if trimmed == "" {
			return nil, errors.New("probe name cannot be empty")
		}
		cur.Name = trimmed
	}
	if p.Type != nil {
		cur.Type = strings.ToLower(strings.TrimSpace(*p.Type))
	}
	if p.Target != nil {
		cur.Target = strings.TrimSpace(*p.Target)
	}
	if err := ValidateProbeTarget(cur.Type, cur.Target); err != nil {
		return nil, err
	}
	if p.Method != nil && strings.TrimSpace(*p.Method) != "" {
		cur.Method = strings.ToUpper(strings.TrimSpace(*p.Method))
	}
	if p.ExpectedMin != nil && *p.ExpectedMin > 0 {
		cur.ExpectedMin = *p.ExpectedMin
	}
	if p.ExpectedMax != nil && *p.ExpectedMax > 0 {
		cur.ExpectedMax = *p.ExpectedMax
	}
	if p.Keyword != nil {
		cur.Keyword = *p.Keyword
	}
	if p.TimeoutSec != nil {
		if *p.TimeoutSec < 1 || *p.TimeoutSec > 30 {
			return nil, errors.New("timeout_sec must be between 1 and 30")
		}
		cur.TimeoutSec = *p.TimeoutSec
	}
	if p.IntervalSec != nil {
		if *p.IntervalSec < 10 || *p.IntervalSec > 3600 {
			return nil, errors.New("interval_sec must be between 10 and 3600")
		}
		cur.IntervalSec = *p.IntervalSec
	}
	if p.FailureThreshold != nil && *p.FailureThreshold > 0 {
		cur.FailureThreshold = *p.FailureThreshold
	}
	if p.Enabled != nil {
		cur.Enabled = *p.Enabled
	}
	if p.VPSID != nil {
		if *p.VPSID == "" {
			cur.VPSID = nil
		} else {
			v := *p.VPSID
			cur.VPSID = &v
		}
	}
	enabledInt := 0
	if cur.Enabled {
		enabledInt = 1
	}
	var vpsArg any
	if cur.VPSID != nil {
		vpsArg = *cur.VPSID
	}

	_, err = s.db.Exec(
		`UPDATE probes SET vps_id=?, name=?, type=?, target=?, method=?, expected_min=?, expected_max=?,
		                   keyword=?, timeout_sec=?, interval_sec=?, failure_threshold=?, enabled=?
		 WHERE id=?`,
		vpsArg, cur.Name, cur.Type, cur.Target, cur.Method, cur.ExpectedMin, cur.ExpectedMax,
		cur.Keyword, cur.TimeoutSec, cur.IntervalSec, cur.FailureThreshold, enabledInt, id,
	)
	if err != nil {
		return nil, err
	}
	return s.GetProbe(id)
}

// DeleteProbe removes a probe and its check history.
func (s *Store) DeleteProbe(id string) error {
	res, err := s.db.Exec(`DELETE FROM probes WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// RecordProbeResult persists a check result and updates the hourly uptime rollup.
func (s *Store) RecordProbeResult(probeID string, res ProbeCheckResult) error {
	if res.Timestamp <= 0 {
		res.Timestamp = time.Now().Unix()
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var tlsArg any
	if res.TLSExpiryDays != nil {
		tlsArg = *res.TLSExpiryDays
	}

	if _, err := tx.Exec(
		`INSERT INTO probe_checks (probe_id, ts, status, latency_ms, status_code, tls_expiry_days, error_msg)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		probeID, res.Timestamp, res.Status, res.LatencyMs, res.StatusCode, tlsArg, res.ErrorMsg,
	); err != nil {
		return err
	}

	hourTS := (res.Timestamp / 3600) * 3600
	upInc := 0
	if res.Status == "up" {
		upInc = 1
	}
	if _, err := tx.Exec(
		`INSERT INTO probe_hourly_rollups (probe_id, hour_ts, total_checks, up_checks, avg_latency_ms)
		 VALUES (?, ?, 1, ?, ?)
		 ON CONFLICT(probe_id, hour_ts) DO UPDATE SET
			avg_latency_ms = ((probe_hourly_rollups.avg_latency_ms * probe_hourly_rollups.total_checks) + excluded.avg_latency_ms) / (probe_hourly_rollups.total_checks + 1),
			total_checks = probe_hourly_rollups.total_checks + 1,
			up_checks = probe_hourly_rollups.up_checks + excluded.up_checks`,
		probeID, hourTS, upInc, float64(res.LatencyMs),
	); err != nil {
		return err
	}
	return tx.Commit()
}

// GetProbeHistory returns the latest N check ticks plus 24h, 7d, 30d uptime percentages.
func (s *Store) GetProbeHistory(probeID string, limit int) ([]ServiceTick, float64, float64, float64, error) {
	if limit <= 0 {
		limit = 60
	}
	rows, err := s.db.Query(
		`SELECT ts, status, latency_ms FROM probe_checks WHERE probe_id = ? ORDER BY ts DESC LIMIT ?`,
		probeID, limit,
	)
	if err != nil {
		return nil, 100, 100, 100, err
	}
	defer func() { _ = rows.Close() }()
	rev := []ServiceTick{}
	for rows.Next() {
		var t ServiceTick
		if err := rows.Scan(&t.Timestamp, &t.Status, &t.LatencyMs); err != nil {
			return nil, 100, 100, 100, err
		}
		rev = append(rev, t)
	}
	if err := rows.Err(); err != nil {
		return nil, 100, 100, 100, err
	}
	ticks := make([]ServiceTick, len(rev))
	for i := range rev {
		ticks[i] = rev[len(rev)-1-i]
	}

	now := time.Now().Unix()
	u24 := s.probeUptimeSince(probeID, now-86400)
	u7 := s.probeUptimeSince(probeID, now-7*86400)
	u30 := s.probeUptimeSince(probeID, now-30*86400)
	return ticks, u24, u7, u30, nil
}

func (s *Store) probeUptimeSince(probeID string, since int64) float64 {
	var total, up sql.NullInt64
	err := s.db.QueryRow(
		`SELECT SUM(total_checks), SUM(up_checks) FROM probe_hourly_rollups WHERE probe_id = ? AND hour_ts >= ?`,
		probeID, (since/3600)*3600,
	).Scan(&total, &up)
	if err != nil || !total.Valid || total.Int64 == 0 {
		return 100.0
	}
	return float64(up.Int64) * 100 / float64(total.Int64)
}

func (s *Store) hydrateProbeStats(p *Probe) {
	var ts int64
	var status, errMsg string
	var latency int
	var tlsDays sql.NullInt64
	err := s.db.QueryRow(
		`SELECT ts, status, latency_ms, tls_expiry_days, error_msg
		 FROM probe_checks WHERE probe_id = ? ORDER BY ts DESC LIMIT 1`,
		p.ID,
	).Scan(&ts, &status, &latency, &tlsDays, &errMsg)
	if err == nil {
		p.LastCheckedAt = ts
		p.Status = status
		p.LatencyMs = latency
		p.LastError = errMsg
		if tlsDays.Valid {
			d := int(tlsDays.Int64)
			p.TLSExpiryDays = &d
		}
	} else {
		p.Status = "pending"
	}

	ticks, u24, u7, u30, err := s.GetProbeHistory(p.ID, 60)
	if err == nil {
		p.Ticks = ticks
		p.Uptime24h = u24
		p.Uptime7d = u7
		p.Uptime30d = u30
	} else {
		p.Ticks = []ServiceTick{}
		p.Uptime24h = 100
		p.Uptime7d = 100
		p.Uptime30d = 100
	}
}

package store

import (
	"database/sql"
	"os"
	"time"
)

// HostMetricPoint is a single metric data point returned for charts and summaries.
type HostMetricPoint struct {
	Timestamp     int64   `json:"ts"`
	CPUPercent    float64 `json:"cpu_percent"`
	CPUMax        float64 `json:"cpu_max,omitempty"`
	Load1         float64 `json:"load_1"`
	RAMPercent    float64 `json:"ram_percent"`
	RAMUsedBytes  uint64  `json:"ram_used_bytes,omitempty"`
	RAMTotalBytes uint64  `json:"ram_total_bytes,omitempty"`
	SwapUsedBytes uint64  `json:"swap_used_bytes,omitempty"`
	SwapTotalBytes uint64 `json:"swap_total_bytes,omitempty"`
	DiskPercent   float64 `json:"disk_percent"`
	DiskUsedBytes uint64  `json:"disk_used_bytes,omitempty"`
	DiskTotalBytes uint64 `json:"disk_total_bytes,omitempty"`
	NetRx         float64 `json:"net_rx"`
	NetTx         float64 `json:"net_tx"`
}

// GetLatestMetric returns the most recent raw host metric for a VPS.
func (s *Store) GetLatestMetric(vpsID string) (*HostMetricPoint, error) {
	var p HostMetricPoint
	var ramUsed, ramTotal, swapUsed, swapTotal, diskUsed, diskTotal uint64
	err := s.db.QueryRow(
		`SELECT ts, cpu_percent, load1, ram_used, ram_total, swap_used, swap_total, disk_used, disk_total, net_rx, net_tx
		 FROM host_metrics_raw WHERE vps_id = ? ORDER BY ts DESC LIMIT 1`,
		vpsID,
	).Scan(&p.Timestamp, &p.CPUPercent, &p.Load1, &ramUsed, &ramTotal, &swapUsed, &swapTotal, &diskUsed, &diskTotal, &p.NetRx, &p.NetTx)
	if err != nil {
		return nil, err
	}
	p.RAMUsedBytes = ramUsed
	p.RAMTotalBytes = ramTotal
	p.SwapUsedBytes = swapUsed
	p.SwapTotalBytes = swapTotal
	p.DiskUsedBytes = diskUsed
	p.DiskTotalBytes = diskTotal
	if ramTotal > 0 {
		p.RAMPercent = float64(ramUsed) * 100 / float64(ramTotal)
	}
	if diskTotal > 0 {
		p.DiskPercent = float64(diskUsed) * 100 / float64(diskTotal)
	}
	return &p, nil
}

// GetRecentCPUSparkline returns the last N CPU percentages ordered oldest-to-newest.
func (s *Store) GetRecentCPUSparkline(vpsID string, limit int) ([]float64, error) {
	if limit <= 0 {
		limit = 16
	}
	rows, err := s.db.Query(
		`SELECT cpu_percent FROM host_metrics_raw WHERE vps_id = ? ORDER BY ts DESC LIMIT ?`,
		vpsID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	rev := []float64{}
	for rows.Next() {
		var cpu float64
		if err := rows.Scan(&cpu); err != nil {
			return nil, err
		}
		rev = append(rev, cpu)
	}
	out := make([]float64, len(rev))
	for i := range rev {
		out[i] = rev[len(rev)-1-i]
	}
	return out, nil
}

// QueryMetrics returns time-series points using the appropriate retention table for rangeStr (1h, 24h, 7d, 30d).
func (s *Store) QueryMetrics(vpsID, rangeStr string) ([]HostMetricPoint, error) {
	now := time.Now().Unix()
	switch rangeStr {
	case "1h":
		return s.queryRawMetrics(vpsID, now-3600)
	case "24h", "":
		pts, err := s.queryRollupMetrics("host_metrics_1m", vpsID, now-86400)
		if err != nil || len(pts) >= 12 {
			return pts, err
		}
		return s.queryRawMetrics(vpsID, now-86400)
	case "7d":
		return s.queryRollupMetrics("host_metrics_1m", vpsID, now-7*86400)
	case "30d":
		return s.queryRollupMetrics("host_metrics_5m", vpsID, now-30*86400)
	default:
		return s.queryRawMetrics(vpsID, now-86400)
	}
}

func (s *Store) queryRawMetrics(vpsID string, since int64) ([]HostMetricPoint, error) {
	rows, err := s.db.Query(
		`SELECT ts, cpu_percent, load1, ram_used, ram_total, disk_used, disk_total, net_rx, net_tx
		 FROM host_metrics_raw WHERE vps_id = ? AND ts >= ? ORDER BY ts ASC`,
		vpsID, since,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []HostMetricPoint{}
	for rows.Next() {
		var p HostMetricPoint
		var ramU, ramT, diskU, diskT uint64
		if err := rows.Scan(&p.Timestamp, &p.CPUPercent, &p.Load1, &ramU, &ramT, &diskU, &diskT, &p.NetRx, &p.NetTx); err != nil {
			return nil, err
		}
		if ramT > 0 {
			p.RAMPercent = float64(ramU) * 100 / float64(ramT)
		}
		if diskT > 0 {
			p.DiskPercent = float64(diskU) * 100 / float64(diskT)
		}
		p.RAMUsedBytes = ramU
		p.RAMTotalBytes = ramT
		p.DiskUsedBytes = diskU
		p.DiskTotalBytes = diskT
		out = append(out, p)
	}
	return out, nil
}

func (s *Store) queryRollupMetrics(table, vpsID string, since int64) ([]HostMetricPoint, error) {
	rows, err := s.db.Query(
		`SELECT bucket_ts, cpu_avg, cpu_max, ram_pct_avg, disk_pct_avg, net_rx_avg, net_tx_avg, load1_avg
		 FROM `+table+` WHERE vps_id = ? AND bucket_ts >= ? ORDER BY bucket_ts ASC`,
		vpsID, since,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []HostMetricPoint{}
	for rows.Next() {
		var p HostMetricPoint
		if err := rows.Scan(&p.Timestamp, &p.CPUPercent, &p.CPUMax, &p.RAMPercent, &p.DiskPercent, &p.NetRx, &p.NetTx, &p.Load1); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// RunRollupsAndPrune aggregates raw metrics into 1m and 5m tables and enforces retention windows.
func (s *Store) RunRollupsAndPrune(now time.Time) error {
	nowUnix := now.Unix()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Roll up raw metrics into 1m buckets (looking back 48h so late or backfilled data rolls up cleanly).
	if _, err := tx.Exec(`
		INSERT INTO host_metrics_1m (
			vps_id, bucket_ts, cpu_avg, cpu_max, ram_pct_avg, ram_pct_max,
			disk_pct_avg, disk_pct_max, net_rx_avg, net_tx_avg, load1_avg
		)
		SELECT
			vps_id,
			(ts / 60) * 60 AS bucket_ts,
			AVG(cpu_percent),
			MAX(cpu_percent),
			AVG(CASE WHEN ram_total > 0 THEN (ram_used * 100.0 / ram_total) ELSE 0 END),
			MAX(CASE WHEN ram_total > 0 THEN (ram_used * 100.0 / ram_total) ELSE 0 END),
			AVG(CASE WHEN disk_total > 0 THEN (disk_used * 100.0 / disk_total) ELSE 0 END),
			MAX(CASE WHEN disk_total > 0 THEN (disk_used * 100.0 / disk_total) ELSE 0 END),
			AVG(net_rx),
			AVG(net_tx),
			AVG(load1)
		FROM host_metrics_raw
		WHERE ts >= ?
		GROUP BY vps_id, (ts / 60) * 60
		ON CONFLICT(vps_id, bucket_ts) DO UPDATE SET
			cpu_avg = excluded.cpu_avg,
			cpu_max = excluded.cpu_max,
			ram_pct_avg = excluded.ram_pct_avg,
			ram_pct_max = excluded.ram_pct_max,
			disk_pct_avg = excluded.disk_pct_avg,
			disk_pct_max = excluded.disk_pct_max,
			net_rx_avg = excluded.net_rx_avg,
			net_tx_avg = excluded.net_tx_avg,
			load1_avg = excluded.load1_avg
	`, nowUnix-48*3600); err != nil {
		return err
	}

	// Roll up 1m metrics into 5m buckets.
	if _, err := tx.Exec(`
		INSERT INTO host_metrics_5m (
			vps_id, bucket_ts, cpu_avg, cpu_max, ram_pct_avg, ram_pct_max,
			disk_pct_avg, disk_pct_max, net_rx_avg, net_tx_avg, load1_avg
		)
		SELECT
			vps_id,
			(bucket_ts / 300) * 300 AS b5,
			AVG(cpu_avg),
			MAX(cpu_max),
			AVG(ram_pct_avg),
			MAX(ram_pct_max),
			AVG(disk_pct_avg),
			MAX(disk_pct_max),
			AVG(net_rx_avg),
			AVG(net_tx_avg),
			AVG(load1_avg)
		FROM host_metrics_1m
		WHERE bucket_ts >= ?
		GROUP BY vps_id, (bucket_ts / 300) * 300
		ON CONFLICT(vps_id, bucket_ts) DO UPDATE SET
			cpu_avg = excluded.cpu_avg,
			cpu_max = excluded.cpu_max,
			ram_pct_avg = excluded.ram_pct_avg,
			ram_pct_max = excluded.ram_pct_max,
			disk_pct_avg = excluded.disk_pct_avg,
			disk_pct_max = excluded.disk_pct_max,
			net_rx_avg = excluded.net_rx_avg,
			net_tx_avg = excluded.net_tx_avg,
			load1_avg = excluded.load1_avg
	`, nowUnix-10*86400); err != nil {
		return err
	}

	// Enforce retention tiers:
	// - raw metrics: 24h
	// - 1m rollups: 7d
	// - 5m rollups: 30d
	// - service ticks: 7d
	// - probe checks: 7d
	// - probe hourly rollups: 90d
	// - incidents: 90d
	prunes := []struct {
		query  string
		cutoff int64
	}{
		{`DELETE FROM host_metrics_raw WHERE ts < ?`, nowUnix - 24*3600},
		{`DELETE FROM host_metrics_1m WHERE bucket_ts < ?`, nowUnix - 7*86400},
		{`DELETE FROM host_metrics_5m WHERE bucket_ts < ?`, nowUnix - 30*86400},
		{`DELETE FROM service_ticks WHERE ts < ?`, nowUnix - 7*86400},
		{`DELETE FROM probe_checks WHERE ts < ?`, nowUnix - 7*86400},
		{`DELETE FROM probe_hourly_rollups WHERE hour_ts < ?`, nowUnix - 90*86400},
		{`DELETE FROM incidents WHERE created_at < ?`, nowUnix - 90*86400},
		{`DELETE FROM sessions WHERE expires_at <= ?`, nowUnix},
	}
	for _, p := range prunes {
		if _, err := tx.Exec(p.query, p.cutoff); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// InsertRollupSample allows tests and demo seeding to insert historical 1m/5m points directly.
func (s *Store) InsertRollupSample(table, vpsID string, bucketTS int64, cpuAvg, cpuMax, ramPct, diskPct, netRx, netTx, load1 float64) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO `+table+` (
			vps_id, bucket_ts, cpu_avg, cpu_max, ram_pct_avg, ram_pct_max,
			disk_pct_avg, disk_pct_max, net_rx_avg, net_tx_avg, load1_avg
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		vpsID, bucketTS, cpuAvg, cpuMax, ramPct, ramPct, diskPct, diskPct, netRx, netTx, load1,
	)
	return err
}

// DBStats reports SQLite file size and row counts for the Settings page.
func (s *Store) DBStats() (int64, map[string]int64) {
	var size int64
	if s.dbPath != "" && s.dbPath != ":memory:" {
		if fi, err := os.Stat(s.dbPath); err == nil {
			size = fi.Size()
		}
	}
	counts := map[string]int64{}
	tables := []string{"vps", "host_metrics_raw", "host_metrics_1m", "host_metrics_5m", "probes", "probe_checks", "incidents"}
	for _, t := range tables {
		var c sql.NullInt64
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + t).Scan(&c); err == nil {
			counts[t] = c.Int64
		}
	}
	return size, counts
}

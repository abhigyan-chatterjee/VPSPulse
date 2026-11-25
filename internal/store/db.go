package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Store wraps the SQLite database handle and provides typed persistence methods.
type Store struct {
	db     *sql.DB
	dbPath string
}

// Open initializes SQLite at path (or ":memory:"), configures pragmas, and runs migrations.
func Open(path string) (*Store, error) {
	if path != ":memory:" {
		dir := filepath.Dir(path)
		if dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return nil, fmt.Errorf("create db directory: %w", err)
			}
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)

	if path != ":memory:" {
		_ = os.Chmod(path, 0o600)
	}

	pragmas := []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA busy_timeout=5000;",
		"PRAGMA foreign_keys=ON;",
		"PRAGMA synchronous=NORMAL;",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("apply pragma %q: %w", p, err)
		}
	}

	s := &Store{db: db, dbPath: path}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate schema: %w", err)
	}
	return s, nil
}

// Close closes the underlying SQLite connection.
func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			created_at INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS sessions (
			token TEXT PRIMARY KEY,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			expires_at INTEGER NOT NULL,
			created_at INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS vps (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			tag TEXT NOT NULL DEFAULT '',
			token_hash TEXT NOT NULL UNIQUE,
			status TEXT NOT NULL DEFAULT 'pending',
			last_seen_at INTEGER NOT NULL DEFAULT 0,
			hostname TEXT NOT NULL DEFAULT '',
			os TEXT NOT NULL DEFAULT '',
			kernel TEXT NOT NULL DEFAULT '',
			arch TEXT NOT NULL DEFAULT '',
			ip TEXT NOT NULL DEFAULT '',
			uptime_sec INTEGER NOT NULL DEFAULT 0,
			cpu_cores INTEGER NOT NULL DEFAULT 0,
			agent_version TEXT NOT NULL DEFAULT '',
			docker_available INTEGER NOT NULL DEFAULT 0,
			systemd_available INTEGER NOT NULL DEFAULT 0,
			discoverable_units_json TEXT NOT NULL DEFAULT '[]',
			disks_json TEXT NOT NULL DEFAULT '[]',
			cpu_warn REAL NOT NULL DEFAULT 90,
			ram_warn REAL NOT NULL DEFAULT 90,
			disk_warn REAL NOT NULL DEFAULT 85,
			interval_seconds INTEGER NOT NULL DEFAULT 10,
			created_at INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS host_metrics_raw (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			vps_id TEXT NOT NULL REFERENCES vps(id) ON DELETE CASCADE,
			ts INTEGER NOT NULL,
			cpu_percent REAL NOT NULL,
			load1 REAL NOT NULL,
			load5 REAL NOT NULL,
			load15 REAL NOT NULL,
			ram_used INTEGER NOT NULL,
			ram_total INTEGER NOT NULL,
			swap_used INTEGER NOT NULL,
			swap_total INTEGER NOT NULL,
			disk_used INTEGER NOT NULL,
			disk_total INTEGER NOT NULL,
			net_rx REAL NOT NULL,
			net_tx REAL NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_host_metrics_raw_vps_ts ON host_metrics_raw(vps_id, ts);`,
		`CREATE TABLE IF NOT EXISTS host_metrics_1m (
			vps_id TEXT NOT NULL REFERENCES vps(id) ON DELETE CASCADE,
			bucket_ts INTEGER NOT NULL,
			cpu_avg REAL NOT NULL,
			cpu_max REAL NOT NULL,
			ram_pct_avg REAL NOT NULL,
			ram_pct_max REAL NOT NULL,
			disk_pct_avg REAL NOT NULL,
			disk_pct_max REAL NOT NULL,
			net_rx_avg REAL NOT NULL,
			net_tx_avg REAL NOT NULL,
			load1_avg REAL NOT NULL,
			PRIMARY KEY (vps_id, bucket_ts)
		);`,
		`CREATE TABLE IF NOT EXISTS host_metrics_5m (
			vps_id TEXT NOT NULL REFERENCES vps(id) ON DELETE CASCADE,
			bucket_ts INTEGER NOT NULL,
			cpu_avg REAL NOT NULL,
			cpu_max REAL NOT NULL,
			ram_pct_avg REAL NOT NULL,
			ram_pct_max REAL NOT NULL,
			disk_pct_avg REAL NOT NULL,
			disk_pct_max REAL NOT NULL,
			net_rx_avg REAL NOT NULL,
			net_tx_avg REAL NOT NULL,
			load1_avg REAL NOT NULL,
			PRIMARY KEY (vps_id, bucket_ts)
		);`,
		`CREATE TABLE IF NOT EXISTS vps_containers (
			vps_id TEXT NOT NULL REFERENCES vps(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			container_id TEXT NOT NULL,
			image TEXT NOT NULL,
			state TEXT NOT NULL,
			health TEXT NOT NULL,
			exit_code INTEGER NOT NULL DEFAULT 0,
			oom_killed INTEGER NOT NULL DEFAULT 0,
			started_at TEXT NOT NULL DEFAULT '',
			restart_count INTEGER NOT NULL DEFAULT 0,
			cpu_percent REAL NOT NULL DEFAULT 0,
			mem_used INTEGER NOT NULL DEFAULT 0,
			mem_limit INTEGER NOT NULL DEFAULT 0,
			muted INTEGER NOT NULL DEFAULT 0,
			pinned INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (vps_id, name)
		);`,
		`CREATE TABLE IF NOT EXISTS vps_watched_units (
			vps_id TEXT NOT NULL REFERENCES vps(id) ON DELETE CASCADE,
			unit_name TEXT NOT NULL,
			PRIMARY KEY (vps_id, unit_name)
		);`,
		`CREATE TABLE IF NOT EXISTS vps_systemd_units (
			vps_id TEXT NOT NULL REFERENCES vps(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			active_state TEXT NOT NULL,
			sub_state TEXT NOT NULL,
			mem_bytes INTEGER NOT NULL DEFAULT 0,
			active_since TEXT NOT NULL DEFAULT '',
			auto_failed INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (vps_id, name)
		);`,
		`CREATE TABLE IF NOT EXISTS service_ticks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			vps_id TEXT NOT NULL REFERENCES vps(id) ON DELETE CASCADE,
			kind TEXT NOT NULL,
			name TEXT NOT NULL,
			ts INTEGER NOT NULL,
			status TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_service_ticks_lookup ON service_ticks(vps_id, kind, name, ts);`,
		`CREATE TABLE IF NOT EXISTS probes (
			id TEXT PRIMARY KEY,
			vps_id TEXT NULL REFERENCES vps(id) ON DELETE SET NULL,
			name TEXT NOT NULL,
			type TEXT NOT NULL,
			target TEXT NOT NULL,
			method TEXT NOT NULL DEFAULT 'GET',
			expected_min INTEGER NOT NULL DEFAULT 200,
			expected_max INTEGER NOT NULL DEFAULT 299,
			keyword TEXT NOT NULL DEFAULT '',
			timeout_sec INTEGER NOT NULL DEFAULT 10,
			interval_sec INTEGER NOT NULL DEFAULT 30,
			failure_threshold INTEGER NOT NULL DEFAULT 2,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS probe_checks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			probe_id TEXT NOT NULL REFERENCES probes(id) ON DELETE CASCADE,
			ts INTEGER NOT NULL,
			status TEXT NOT NULL,
			latency_ms INTEGER NOT NULL DEFAULT 0,
			status_code INTEGER NOT NULL DEFAULT 0,
			tls_expiry_days INTEGER NULL,
			error_msg TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE INDEX IF NOT EXISTS idx_probe_checks_probe_ts ON probe_checks(probe_id, ts);`,
		`CREATE TABLE IF NOT EXISTS probe_hourly_rollups (
			probe_id TEXT NOT NULL REFERENCES probes(id) ON DELETE CASCADE,
			hour_ts INTEGER NOT NULL,
			total_checks INTEGER NOT NULL DEFAULT 0,
			up_checks INTEGER NOT NULL DEFAULT 0,
			avg_latency_ms REAL NOT NULL DEFAULT 0,
			PRIMARY KEY (probe_id, hour_ts)
		);`,
		`CREATE TABLE IF NOT EXISTS notification_channels (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			type TEXT NOT NULL,
			config_json TEXT NOT NULL,
			events_json TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS incidents (
			id TEXT PRIMARY KEY,
			incident_key TEXT NOT NULL,
			vps_id TEXT NULL REFERENCES vps(id) ON DELETE SET NULL,
			severity TEXT NOT NULL,
			title TEXT NOT NULL,
			detail TEXT NOT NULL,
			status TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			resolved_at INTEGER NOT NULL DEFAULT 0
		);`,
		`CREATE INDEX IF NOT EXISTS idx_incidents_key_status ON incidents(incident_key, status);`,
		`CREATE INDEX IF NOT EXISTS idx_incidents_created ON incidents(created_at DESC);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_incidents_open_key ON incidents(incident_key) WHERE status IN ('open', 'acknowledged');`,
		`CREATE INDEX IF NOT EXISTS idx_host_metrics_raw_ts ON host_metrics_raw(ts);`,
		`CREATE INDEX IF NOT EXISTS idx_host_metrics_1m_ts ON host_metrics_1m(bucket_ts);`,
		`CREATE INDEX IF NOT EXISTS idx_host_metrics_5m_ts ON host_metrics_5m(bucket_ts);`,
		`CREATE INDEX IF NOT EXISTS idx_service_ticks_ts ON service_ticks(ts);`,
		`CREATE INDEX IF NOT EXISTS idx_probe_checks_ts ON probe_checks(ts);`,
		`CREATE INDEX IF NOT EXISTS idx_probe_hourly_ts ON probe_hourly_rollups(hour_ts);`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);`,
	}

	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("exec migration: %w\nSQL: %s", err, stmt)
		}
	}
	return nil
}

func newID(prefix string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return prefix + "_" + hex.EncodeToString(b[:])
}

func randomToken(prefix string, numBytes int) string {
	b := make([]byte, numBytes)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

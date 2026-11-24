package store

import (
	"testing"
	"time"

	"vpspulse/internal/protocol"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:): %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestAuthAndSessions(t *testing.T) {
	s := openTestStore(t)

	has, err := s.HasAdmin()
	if err != nil || has {
		t.Fatalf("expected HasAdmin=false initially, got %v, err=%v", has, err)
	}

	u, err := s.CreateAdmin("admin", "secret123")
	if err != nil {
		t.Fatalf("CreateAdmin: %v", err)
	}
	has, err = s.HasAdmin()
	if err != nil || !has {
		t.Fatalf("expected HasAdmin=true")
	}

	if _, err := s.VerifyAdmin("admin", "wrong"); err == nil {
		t.Fatalf("expected wrong password to fail")
	}
	if _, err := s.VerifyAdmin("admin", "secret123"); err != nil {
		t.Fatalf("expected valid password to pass: %v", err)
	}

	tok, err := s.CreateSession(u.ID, time.Hour)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sess, err := s.GetSession(tok)
	if err != nil || sess.Username != "admin" {
		t.Fatalf("GetSession failed: %v", err)
	}

	if err := s.DeleteSession(tok); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := s.GetSession(tok); err == nil {
		t.Fatalf("expected deleted session to fail")
	}
}

func TestVPSTokenHashingAndRotation(t *testing.T) {
	s := openTestStore(t)

	v, tok1, err := s.CreateVPS("prod-eu-1", "eu-central")
	if err != nil {
		t.Fatalf("CreateVPS: %v", err)
	}
	if len(tok1) < 20 {
		t.Fatalf("expected non-empty raw token, got %q", tok1)
	}

	// Ensure raw token is not stored in plaintext in the DB.
	var storedHash string
	if err := s.db.QueryRow(`SELECT token_hash FROM vps WHERE id = ?`, v.ID).Scan(&storedHash); err != nil {
		t.Fatalf("query token_hash: %v", err)
	}
	if storedHash == tok1 {
		t.Fatalf("raw token must not be stored directly")
	}

	authed, err := s.AuthenticateAgent(tok1)
	if err != nil || authed.ID != v.ID {
		t.Fatalf("AuthenticateAgent(tok1) failed: %v", err)
	}

	tok2, err := s.RotateVPSToken(v.ID)
	if err != nil {
		t.Fatalf("RotateVPSToken: %v", err)
	}
	if tok2 == tok1 {
		t.Fatalf("rotated token must differ")
	}
	if _, err := s.AuthenticateAgent(tok1); err == nil {
		t.Fatalf("old token must fail immediately after rotation")
	}
	if authed2, err := s.AuthenticateAgent(tok2); err != nil || authed2.ID != v.ID {
		t.Fatalf("new token must succeed: %v", err)
	}
}

func TestDockerContainerIdentityAndPolicyAcrossRecreations(t *testing.T) {
	s := openTestStore(t)
	v, _, err := s.CreateVPS("docker-host", "eu")
	if err != nil {
		t.Fatalf("CreateVPS: %v", err)
	}

	now := time.Now()
	hb1 := protocol.HeartbeatPayload{
		AgentVersion:    "0.1.0",
		Timestamp:       now.Add(-20 * time.Second),
		Host:            protocol.HostMeta{Hostname: "docker-host", OS: "Ubuntu 24.04", CPUCores: 2},
		Metrics:         protocol.HostMetrics{CPUPercent: 20, RAMUsedBytes: 1000, RAMTotalBytes: 4000},
		DockerAvailable: true,
		Containers: []protocol.DockerContainerReport{
			{ID: "container-id-111", Name: "api", Image: "app:v1", State: "running", Health: "healthy"},
		},
	}
	if err := s.RecordHeartbeat(v.ID, hb1); err != nil {
		t.Fatalf("RecordHeartbeat 1: %v", err)
	}

	muted := true
	pinned := true
	if err := s.UpdateContainerPolicy(v.ID, "api", &muted, &pinned); err != nil {
		t.Fatalf("UpdateContainerPolicy: %v", err)
	}

	// Simulate container recreation with a brand-new container ID under the same container name.
	hb2 := hb1
	hb2.Timestamp = now
	hb2.Containers = []protocol.DockerContainerReport{
		{ID: "container-id-222", Name: "api", Image: "app:v2", State: "running", Health: "healthy"},
	}
	if err := s.RecordHeartbeat(v.ID, hb2); err != nil {
		t.Fatalf("RecordHeartbeat 2: %v", err)
	}

	list, err := s.ListContainers(v.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("expected 1 container, got %d, err=%v", len(list), err)
	}
	if list[0].ContainerID != "container-id-222" || !list[0].Muted || !list[0].Pinned {
		t.Fatalf("expected policy preserved across container recreation, got %+v", list[0])
	}
	if len(list[0].Ticks) != 2 {
		t.Fatalf("expected 2 continuous history ticks across recreation, got %d", len(list[0].Ticks))
	}
}

func TestRollupsAndRetentionPruning(t *testing.T) {
	s := openTestStore(t)
	v, _, err := s.CreateVPS("rollup-host", "us")
	if err != nil {
		t.Fatalf("CreateVPS: %v", err)
	}

	now := time.Unix(1764000000, 0)
	// Insert recent raw points (within 24h) and expired raw point (>24h old).
	recent1 := now.Add(-10 * time.Minute)
	recent2 := recent1.Add(15 * time.Second)
	expiredRaw := now.Add(-25 * time.Hour)

	for _, item := range []struct {
		ts  time.Time
		cpu float64
	}{
		{expiredRaw, 99},
		{recent1, 20},
		{recent2, 40},
	} {
		if err := s.RecordHeartbeat(v.ID, protocol.HeartbeatPayload{
			Timestamp: item.ts,
			Host:      protocol.HostMeta{Hostname: "rollup-host"},
			Metrics:   protocol.HostMetrics{CPUPercent: item.cpu, RAMUsedBytes: 500, RAMTotalBytes: 1000, DiskUsedBytes: 100, DiskTotalBytes: 1000},
		}); err != nil {
			t.Fatalf("RecordHeartbeat: %v", err)
		}
	}

	// Insert an old 1m (>7d), old 5m (>30d), and old incident (>90d).
	if err := s.InsertRollupSample("host_metrics_1m", v.ID, now.Add(-8*24*time.Hour).Unix(), 10, 10, 10, 10, 1, 1, 0.1); err != nil {
		t.Fatalf("insert old 1m: %v", err)
	}
	if err := s.InsertRollupSample("host_metrics_5m", v.ID, now.Add(-31*24*time.Hour).Unix(), 10, 10, 10, 10, 1, 1, 0.1); err != nil {
		t.Fatalf("insert old 5m: %v", err)
	}
	if _, err := s.db.Exec(
		`INSERT INTO incidents (id, incident_key, vps_id, severity, title, detail, status, created_at, updated_at, resolved_at)
		 VALUES ('inc_old', 'old', ?, 'info', 'old', 'old', 'resolved', ?, ?, ?)`,
		v.ID, now.Add(-91*24*time.Hour).Unix(), now.Unix(), now.Unix(),
	); err != nil {
		t.Fatalf("insert old incident: %v", err)
	}

	if err := s.RunRollupsAndPrune(now); err != nil {
		t.Fatalf("RunRollupsAndPrune: %v", err)
	}

	var rawCount, m1Count, m5Count, incCount int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM host_metrics_raw WHERE vps_id = ?`, v.ID).Scan(&rawCount)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM host_metrics_1m WHERE vps_id = ?`, v.ID).Scan(&m1Count)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM host_metrics_5m WHERE vps_id = ?`, v.ID).Scan(&m5Count)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM incidents WHERE id = 'inc_old'`).Scan(&incCount)

	if rawCount != 2 {
		t.Fatalf("expected 2 recent raw metrics after pruning >24h, got %d", rawCount)
	}
	if m1Count != 2 {
		t.Fatalf("expected 2 recent 1m rollups (-25h and -10m, with -8d pruned), got %d", m1Count)
	}
	var old1m int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM host_metrics_1m WHERE vps_id = ? AND bucket_ts < ?`, v.ID, now.Add(-7*24*time.Hour).Unix()).Scan(&old1m)
	if old1m != 0 {
		t.Fatalf("expected 0 1m rollups older than 7d, got %d", old1m)
	}
	if m5Count != 3 {
		t.Fatalf("expected 3 recent 5m rollups (-8d, -25h, -10m; with -31d pruned), got %d", m5Count)
	}
	var old5m int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM host_metrics_5m WHERE vps_id = ? AND bucket_ts < ?`, v.ID, now.Add(-30*24*time.Hour).Unix()).Scan(&old5m)
	if old5m != 0 {
		t.Fatalf("expected 0 5m rollups older than 30d, got %d", old5m)
	}
	if incCount != 0 {
		t.Fatalf("expected >90d incident pruned, got %d", incCount)
	}

	// Verify 1m rollup values for the recent bucket averaged 20 and 40 -> 30, max -> 40.
	var cpuAvg, cpuMax float64
	recentBucket := (recent1.Unix() / 60) * 60
	if err := s.db.QueryRow(`SELECT cpu_avg, cpu_max FROM host_metrics_1m WHERE vps_id = ? AND bucket_ts = ?`, v.ID, recentBucket).Scan(&cpuAvg, &cpuMax); err != nil {
		t.Fatalf("query 1m rollup: %v", err)
	}
	if cpuAvg != 30 || cpuMax != 40 {
		t.Fatalf("unexpected 1m rollup values: avg=%v max=%v", cpuAvg, cpuMax)
	}
}

func TestProbeHistoryAndUptime(t *testing.T) {
	s := openTestStore(t)
	p, err := s.CreateProbe(Probe{
		Name:    "api",
		Type:    "http",
		Target:  "https://example.com",
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateProbe: %v", err)
	}

	now := time.Now().Unix()
	tlsDays := 45
	results := []ProbeCheckResult{
		{Timestamp: now - 90, Status: "up", LatencyMs: 30, StatusCode: 200, TLSExpiryDays: &tlsDays},
		{Timestamp: now - 60, Status: "down", LatencyMs: 0, StatusCode: 502, ErrorMsg: "bad gateway"},
		{Timestamp: now - 30, Status: "up", LatencyMs: 25, StatusCode: 200, TLSExpiryDays: &tlsDays},
		{Timestamp: now, Status: "up", LatencyMs: 20, StatusCode: 200, TLSExpiryDays: &tlsDays},
	}
	for _, r := range results {
		if err := s.RecordProbeResult(p.ID, r); err != nil {
			t.Fatalf("RecordProbeResult: %v", err)
		}
	}

	got, err := s.GetProbe(p.ID)
	if err != nil {
		t.Fatalf("GetProbe: %v", err)
	}
	if got.Uptime24h != 75.0 {
		t.Fatalf("expected 75.0%% uptime, got %v", got.Uptime24h)
	}
	if got.TLSExpiryDays == nil || *got.TLSExpiryDays != 45 {
		t.Fatalf("expected TLSExpiryDays=45, got %v", got.TLSExpiryDays)
	}
	if len(got.Ticks) != 4 {
		t.Fatalf("expected 4 ticks, got %d", len(got.Ticks))
	}
}

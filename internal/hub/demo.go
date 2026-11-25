package hub

import (
	"context"
	"math"
	"math/rand"
	"time"

	"vpspulse/internal/protocol"
	"vpspulse/internal/store"
	"vpspulse/internal/version"
)

type demoNode struct {
	ID         string
	Name       string
	Tag        string
	OS         string
	Kernel     string
	Arch       string
	IP         string
	Cores      int
	Uptime     uint64
	BaseCPU    float64
	RAMTotal   uint64
	RAMRatio   float64
	DiskTotal  uint64
	DiskRatio  float64
	NetBaseRx  float64
	NetBaseTx  float64
	Containers []protocol.DockerContainerReport
	Units      []protocol.SystemdUnitReport
	DiscUnits  []string
}

// SeedAndStartDemo populates realistic VPS, container, systemd, probe, and incident data and runs a 5s simulator.
func SeedAndStartDemo(ctx context.Context, st *store.Store, broker *Broker, alerts *AlertManager) error {
	if has, _ := st.HasAdmin(); !has {
		if _, err := st.CreateAdmin("admin", "demo1234"); err != nil {
			return err
		}
	}

	existing, _ := st.ListVPS()
	if len(existing) > 0 {
		return nil
	}

	nodes := []demoNode{
		{
			Name: "prod-eu-1", Tag: "eu-central", OS: "Ubuntu 24.04.1 LTS", Kernel: "6.8.0-45-generic", Arch: "amd64", IP: "116.203.42.19",
			Cores: 4, Uptime: 42*86400 + 6*3600, BaseCPU: 28, RAMTotal: 8 * 1024 * 1024 * 1024, RAMRatio: 0.52,
			DiskTotal: 80 * 1024 * 1024 * 1024, DiskRatio: 0.41, NetBaseRx: 1.8 * 1024 * 1024, NetBaseTx: 0.6 * 1024 * 1024,
			Containers: []protocol.DockerContainerReport{
				{ID: "a1b2c3d4e5f6", Name: "postgres", Image: "postgres:16-alpine", State: "running", Health: "healthy", CPUPercent: 6.4, MemUsedBytes: 612 * 1024 * 1024, MemLimitBytes: 2048 * 1024 * 1024},
				{ID: "b2c3d4e5f6a1", Name: "api-server", Image: "ghcr.io/demo/api:1.18", State: "running", Health: "healthy", CPUPercent: 14.2, MemUsedBytes: 420 * 1024 * 1024, MemLimitBytes: 1024 * 1024 * 1024},
				{ID: "c3d4e5f6a1b2", Name: "certbot-cron", Image: "certbot/certbot:latest", State: "exited", ExitCode: 0, Health: "none"},
			},
			Units: []protocol.SystemdUnitReport{
				{Name: "caddy.service", ActiveState: "active", SubState: "running", MemBytes: 64 * 1024 * 1024, ActiveSince: "42d ago"},
				{Name: "docker.service", ActiveState: "active", SubState: "running", MemBytes: 148 * 1024 * 1024, ActiveSince: "42d ago"},
			},
			DiscUnits: []string{"ssh.service", "ufw.service", "fail2ban.service", "unattended-upgrades.service"},
		},
		{
			Name: "worker-us-1", Tag: "us-east", OS: "Debian GNU/Linux 12", Kernel: "6.1.0-26-amd64", Arch: "amd64", IP: "5.78.91.104",
			Cores: 4, Uptime: 18*86400 + 2*3600, BaseCPU: 82, RAMTotal: 8 * 1024 * 1024 * 1024, RAMRatio: 0.85,
			DiskTotal: 80 * 1024 * 1024 * 1024, DiskRatio: 0.62, NetBaseRx: 4.2 * 1024 * 1024, NetBaseTx: 3.1 * 1024 * 1024,
			Containers: []protocol.DockerContainerReport{
				{ID: "d4e5f6a1b2c3", Name: "queue-worker", Image: "ghcr.io/demo/worker:2.4", State: "restarting", Health: "unhealthy", RestartCount: 4, CPUPercent: 41.0, MemUsedBytes: 1840 * 1024 * 1024, MemLimitBytes: 2048 * 1024 * 1024},
				{ID: "e5f6a1b2c3d4", Name: "redis", Image: "redis:7.4-alpine", State: "running", Health: "healthy", CPUPercent: 4.1, MemUsedBytes: 290 * 1024 * 1024, MemLimitBytes: 1024 * 1024 * 1024},
			},
			Units: []protocol.SystemdUnitReport{
				{Name: "worker.service", ActiveState: "active", SubState: "running", MemBytes: 312 * 1024 * 1024, ActiveSince: "18d ago"},
				{Name: "logrotate.service", ActiveState: "failed", SubState: "failed", AutoFailed: true},
			},
			DiscUnits: []string{"ssh.service", "cron.service", "rsyslog.service"},
		},
		{
			Name: "db-eu-2", Tag: "eu-central", OS: "Ubuntu 24.04.1 LTS", Kernel: "6.8.0-45-generic", Arch: "arm64", IP: "49.13.112.77",
			Cores: 8, Uptime: 91*86400 + 14*3600, BaseCPU: 14, RAMTotal: 16 * 1024 * 1024 * 1024, RAMRatio: 0.70,
			DiskTotal: 240 * 1024 * 1024 * 1024, DiskRatio: 0.78, NetBaseRx: 0.9 * 1024 * 1024, NetBaseTx: 1.4 * 1024 * 1024,
			Containers: []protocol.DockerContainerReport{
				{ID: "f6a1b2c3d4e5", Name: "pg-primary", Image: "postgres:16", State: "running", Health: "healthy", CPUPercent: 9.2, MemUsedBytes: 8420 * 1024 * 1024, MemLimitBytes: 12288 * 1024 * 1024},
				{ID: "123456abcdef", Name: "pgbouncer", Image: "edoburu/pgbouncer:1.23", State: "running", Health: "healthy", CPUPercent: 1.1, MemUsedBytes: 48 * 1024 * 1024, MemLimitBytes: 256 * 1024 * 1024},
			},
			Units: []protocol.SystemdUnitReport{
				{Name: "backup.timer", ActiveState: "active", SubState: "waiting", ActiveSince: "91d ago"},
			},
			DiscUnits: []string{"postgresql.service", "ssh.service", "chrony.service"},
		},
		{
			Name: "edge-sg-1", Tag: "ap-south", OS: "Debian GNU/Linux 12", Kernel: "6.1.0-26-arm64", Arch: "arm64", IP: "139.59.210.8",
			Cores: 2, Uptime: 12*86400 + 9*3600, BaseCPU: 19, RAMTotal: 4 * 1024 * 1024 * 1024, RAMRatio: 0.35,
			DiskTotal: 40 * 1024 * 1024 * 1024, DiskRatio: 0.29, NetBaseRx: 3.4 * 1024 * 1024, NetBaseTx: 2.8 * 1024 * 1024,
			Containers: []protocol.DockerContainerReport{
				{ID: "fedcba654321", Name: "nginx-edge", Image: "nginx:1.27-alpine", State: "running", Health: "healthy", CPUPercent: 8.5, MemUsedBytes: 128 * 1024 * 1024, MemLimitBytes: 512 * 1024 * 1024},
			},
			Units: []protocol.SystemdUnitReport{
				{Name: "wg-quick@wg0.service", ActiveState: "active", SubState: "exited", MemBytes: 4 * 1024 * 1024, ActiveSince: "12d ago"},
			},
			DiscUnits: []string{"ssh.service", "nftables.service", "docker.service"},
		},
	}

	now := time.Now()
	for i := range nodes {
		v, _, err := st.CreateVPS(nodes[i].Name, nodes[i].Tag)
		if err != nil {
			return err
		}
		nodes[i].ID = v.ID

		watched := make([]string, 0, len(nodes[i].Units))
		for _, u := range nodes[i].Units {
			if !u.AutoFailed {
				watched = append(watched, u.Name)
			}
		}
		_ = st.SetWatchedUnits(v.ID, watched)

		// Backfill 24h of 1m rollup points + 18 recent raw points.
		for m := 120; m >= 1; m-- {
			ts := now.Add(-time.Duration(m*12) * time.Minute).Unix()
			wave := math.Sin(float64(m)*0.35) * 6
			cpu := math.Max(4, math.Min(95, nodes[i].BaseCPU+wave))
			_ = st.InsertRollupSample("host_metrics_1m", v.ID, ts, cpu, cpu+4, nodes[i].RAMRatio*100, nodes[i].DiskRatio*100, nodes[i].NetBaseRx, nodes[i].NetBaseTx, cpu/30)
		}
		for r := 18; r >= 0; r-- {
			ts := now.Add(-time.Duration(r*10) * time.Second)
			hb := buildDemoHeartbeat(nodes[i], ts, float64(r))
			_ = st.RecordHeartbeat(v.ID, hb)
		}

		// Seed realistic probes attached to VPSes.
		switch nodes[i].Name {
		case "prod-eu-1":
			seedDemoProbe(st, &v.ID, "api.myapp.dev", "http", "https://api.myapp.dev/health", 38, 68, false)
			seedDemoProbe(st, &v.ID, "auth.myapp.dev", "http", "https://auth.myapp.dev/ready", 44, 68, false)
		case "worker-us-1":
			seedDemoProbe(st, &v.ID, "jobs.myapp.dev", "tcp", "5.78.91.104:6379", 19, 0, true)
		case "db-eu-2":
			seedDemoProbe(st, &v.ID, "pg-replica-tcp", "tcp", "49.13.112.77:5432", 11, 0, false)
		case "edge-sg-1":
			seedDemoProbe(st, &v.ID, "cdn.myapp.dev", "http", "https://cdn.myapp.dev/ping", 24, 11, false)
		}
	}

	// Standalone external probes (not bound to one VPS).
	seedDemoProbe(st, nil, "status.myapp.dev", "http", "https://status.myapp.dev", 31, 82, false)
	seedDemoProbe(st, nil, "smtp.myapp.dev:587", "tcp", "smtp.myapp.dev:587", 57, 0, true)

	// Trigger initial alert evaluation on worker-us-1 & edge-sg-1 TLS so realistic incidents appear.
	if v2, err := st.GetVPS(nodes[1].ID); err == nil && alerts != nil {
		alerts.EvaluateHeartbeat(*v2, buildDemoHeartbeat(nodes[1], now, 0), now)
	}
	if _, _, err := st.CreateOrUpdateIncident("probe-tls:demo", "warning", &nodes[3].ID, "TLS certificate expiring soon for cdn.myapp.dev (11d)", "Certificate expires in 11 days (<14d threshold)."); err != nil {
		return err
	}

	// Sample disabled notification channels so Settings looks populated out-of-the-box.
	_, _ = st.CreateChannel(store.NotificationChannel{
		Name:    "Discord #infra-alerts",
		Type:    "discord",
		Config:  map[string]string{"webhook_url": "https://discord.com/api/webhooks/example"},
		Events:  store.ChannelEvents{Offline: true, Service: true, Resource: true, TLS: true},
		Enabled: false,
	})
	_, _ = st.CreateChannel(store.NotificationChannel{
		Name:    "Telegram Ops Bot",
		Type:    "telegram",
		Config:  map[string]string{"bot_token": "123456:EXAMPLE", "chat_id": "-100123456789"},
		Events:  store.ChannelEvents{Offline: true, Service: true, Resource: false, TLS: true},
		Enabled: false,
	})

	// Live demo ticker.
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		step := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				step++
				t := time.Now()
				for i := range nodes {
					nodes[i].Uptime += 5
					hb := buildDemoHeartbeat(nodes[i], t, float64(step))
					if err := st.RecordHeartbeat(nodes[i].ID, hb); err == nil {
						if updated, err := st.GetVPS(nodes[i].ID); err == nil && broker != nil {
							broker.Publish("vps_update", BuildVPSView(st, *updated))
						}
					}
				}
			}
		}
	}()

	return nil
}

func buildDemoHeartbeat(n demoNode, ts time.Time, phase float64) protocol.HeartbeatPayload {
	jitter := math.Sin(phase*0.8)*4 + (rand.Float64()-0.5)*3
	cpu := math.Max(3, math.Min(96, n.BaseCPU+jitter))
	ramUsed := uint64(float64(n.RAMTotal) * math.Max(0.1, math.Min(0.95, n.RAMRatio+(rand.Float64()-0.5)*0.02)))
	diskUsed := uint64(float64(n.DiskTotal) * n.DiskRatio)
	return protocol.HeartbeatPayload{
		AgentVersion: version.String() + "-demo",
		Timestamp:    ts.UTC(),
		Host: protocol.HostMeta{
			Hostname:  n.Name,
			OS:        n.OS,
			Kernel:    n.Kernel,
			Arch:      n.Arch,
			IP:        n.IP,
			UptimeSec: n.Uptime,
			CPUCores:  n.Cores,
		},
		Metrics: protocol.HostMetrics{
			CPUPercent:       float64(int(cpu*10)) / 10,
			Load1:            float64(int((cpu/30)*100)) / 100,
			Load5:            float64(int((cpu/32)*100)) / 100,
			Load15:           float64(int((cpu/35)*100)) / 100,
			RAMUsedBytes:     ramUsed,
			RAMTotalBytes:    n.RAMTotal,
			SwapUsedBytes:    128 * 1024 * 1024,
			SwapTotalBytes:   2 * 1024 * 1024 * 1024,
			DiskUsedBytes:    diskUsed,
			DiskTotalBytes:   n.DiskTotal,
			Disks:            []protocol.DiskReport{{Mount: "/", FS: "ext4", UsedBytes: diskUsed, TotalBytes: n.DiskTotal, UsedPercent: n.DiskRatio * 100}},
			NetRxBytesPerSec: math.Max(10000, n.NetBaseRx*(0.85+rand.Float64()*0.3)),
			NetTxBytesPerSec: math.Max(10000, n.NetBaseTx*(0.85+rand.Float64()*0.3)),
		},
		DockerAvailable:   true,
		Containers:        n.Containers,
		SystemdAvailable:  true,
		SystemdUnits:      n.Units,
		DiscoverableUnits: n.DiscUnits,
	}
}

func seedDemoProbe(st *store.Store, vpsID *string, name, typ, target string, baseLatency int, tlsDays int, includeBlip bool) {
	// Keep Enabled=false on demo-only unreachable example.dev targets so the real prober doesn't mark fake domains down.
	p, err := st.CreateProbe(store.Probe{
		VPSID:       vpsID,
		Name:        name,
		Type:        typ,
		Target:      target,
		IntervalSec: 30,
		Enabled:     false,
	})
	if err != nil {
		return
	}
	now := time.Now().Unix()
	for i := 30; i >= 0; i-- {
		status := "up"
		lat := baseLatency + (i%5)*3
		if includeBlip && (i == 9 || i == 10) {
			status = "down"
			lat = 0
		}
		var tlsArg *int
		if tlsDays > 0 {
			d := tlsDays
			tlsArg = &d
		}
		_ = st.RecordProbeResult(p.ID, store.ProbeCheckResult{
			Timestamp:     now - int64(i*30),
			Status:        status,
			LatencyMs:     lat,
			StatusCode:    200,
			TLSExpiryDays: tlsArg,
		})
	}
}

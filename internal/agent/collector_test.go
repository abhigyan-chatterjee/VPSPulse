package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"vpspulse/internal/protocol"
)

func TestProcParsers(t *testing.T) {
	stat1 := "cpu  100 0 50 850 0 0 0 0 25 0\ncpu0 50 0 25 425 0 0 0 0 0 0\n"
	stat2 := "cpu  140 0 60 900 0 0 0 0 40 0\n"
	c1 := ParseProcStat(stat1)
	c2 := ParseProcStat(stat2)
	pct := computeCPUPercent(c1, c2)
	// Total delta = 100, idle delta = 50 => 50%
	if pct < 49.9 || pct > 50.1 {
		t.Fatalf("expected ~50%% CPU, got %v", pct)
	}

	meminfo := "MemTotal:        8000000 kB\nMemFree:         1000000 kB\nMemAvailable:    3000000 kB\nSwapTotal:       2000000 kB\nSwapFree:        1500000 kB\n"
	ramU, ramT, swapU, swapT := ParseProcMeminfo(meminfo)
	if ramT != 8000000*1024 || ramU != 5000000*1024 {
		t.Fatalf("unexpected RAM parse: used=%d total=%d", ramU, ramT)
	}
	if swapT != 2000000*1024 || swapU != 500000*1024 {
		t.Fatalf("unexpected Swap parse: used=%d total=%d", swapU, swapT)
	}

	netdev := `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 1000000       10    0    0    0     0          0         0  1000000       10    0    0    0     0       0          0
vethab: 5000000       10    0    0    0     0          0         0  5000000       10    0    0    0     0       0          0
  eth0:    2048        5    0    0    0     0          0         0     4096        8    0    0    0     0       0          0
`
	rx, tx := ParseProcNetDev(netdev)
	if rx != 2048 || tx != 4096 {
		t.Fatalf("expected rx=2048 tx=4096 (ignoring lo/veth), got rx=%d tx=%d", rx, tx)
	}

	mounts := `/dev/sda1 / ext4 rw,relatime 0 0
tmpfs /run tmpfs rw,nosuid 0 0
overlay /var/lib/docker/overlay2/abc/merged overlay rw 0 0
/dev/sdb1 /mnt/data xfs rw 0 0
/dev/sda1 /var/snap/bind ext4 rw 0 0
`
	mList := ParseProcMounts(mounts)
	if len(mList) != 2 || mList[0].Mount != "/" || mList[1].Mount != "/mnt/data" {
		t.Fatalf("unexpected mount list: %+v", mList)
	}
}

func TestDockerCollectorMockAndUnavailable(t *testing.T) {
	// Missing socket should gracefully report available=false.
	missing := NewDockerCollector("/nonexistent/docker-test.sock")
	avail, list := missing.Collect(context.Background())
	if avail || len(list) != 0 {
		t.Fatalf("expected unavailable for missing socket, got avail=%v len=%d", avail, len(list))
	}

	// Mock Docker Engine HTTP responses.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/containers/json":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"Id": "abcdef1234567890", "Names": []string{"/postgres"}, "Image": "postgres:16", "State": "running"},
			})
		case "/containers/abcdef1234567890/json":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"RestartCount": 1,
				"State": map[string]any{
					"Status":    "running",
					"ExitCode":  0,
					"OOMKilled": false,
					"StartedAt": "2026-11-24T10:00:00Z",
					"Health":    map[string]any{"Status": "healthy"},
				},
			})
		case "/containers/abcdef1234567890/stats":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"cpu_stats": map[string]any{
					"cpu_usage":        map[string]any{"total_usage": 300000000},
					"system_cpu_usage": 1000000000,
					"online_cpus":      2,
				},
				"precpu_stats": map[string]any{
					"cpu_usage":        map[string]any{"total_usage": 100000000},
					"system_cpu_usage": 500000000,
				},
				"memory_stats": map[string]any{
					"usage": 50000000,
					"limit": 100000000,
					"stats": map[string]any{"inactive_file": 10000000},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dc := &DockerCollector{baseURL: srv.URL, client: srv.Client()}
	avail, containers := dc.Collect(context.Background())
	if !avail || len(containers) != 1 {
		t.Fatalf("expected 1 container, avail=%v len=%d", avail, len(containers))
	}
	c := containers[0]
	if c.Name != "postgres" || c.Health != "healthy" || c.RestartCount != 1 || c.MemUsedBytes != 40000000 {
		t.Fatalf("unexpected container report: %+v", c)
	}
	if c.CPUPercent < 79.9 || c.CPUPercent > 80.1 {
		t.Fatalf("expected ~80%% CPU, got %v", c.CPUPercent)
	}
}

func TestSystemdCollectorParsersAndCollect(t *testing.T) {
	sc := &SystemdCollector{
		lookPath: func(string) (string, error) { return "/bin/systemctl", nil },
		run: func(_ context.Context, _ string, args ...string) (string, error) {
			if len(args) > 0 && args[0] == "list-units" {
				return "logrotate.service loaded failed failed Rotate log files\n", nil
			}
			if len(args) > 0 && args[0] == "list-unit-files" {
				return "caddy.service enabled enabled\ngetty@.service indirect enabled\nssh.service enabled enabled\n", nil
			}
			if len(args) > 0 && args[0] == "show" {
				return strings.Join([]string{
					"Id=caddy.service",
					"ActiveState=active",
					"SubState=running",
					"MemoryCurrent=12582912",
					"ActiveEnterTimestamp=Mon 2026-11-24 10:00:00 UTC",
					"",
					"Id=logrotate.service",
					"ActiveState=failed",
					"SubState=failed",
					"MemoryCurrent=18446744073709551615",
					"ActiveEnterTimestamp=",
				}, "\n"), nil
			}
			return "", nil
		},
	}

	avail, units, discoverable := sc.Collect(context.Background(), []string{"caddy.service"})
	if !avail || len(units) != 2 {
		t.Fatalf("expected 2 systemd units, avail=%v units=%+v", avail, units)
	}
	if units[0].Name != "caddy.service" || units[0].AutoFailed {
		t.Fatalf("unexpected watched unit: %+v", units[0])
	}
	if units[1].Name != "logrotate.service" || !units[1].AutoFailed || units[1].MemBytes != 0 {
		t.Fatalf("expected logrotate.service flagged AutoFailed with MemBytes=0 (MaxUint64 ignored): %+v", units[1])
	}
	if len(discoverable) != 2 || discoverable[0] != "caddy.service" || discoverable[1] != "ssh.service" {
		t.Fatalf("unexpected discoverable units: %+v", discoverable)
	}
}

func TestRunnerSendOnceAndDynamicConfigSync(t *testing.T) {
	var receivedAuth string
	var receivedPayload protocol.HeartbeatPayload

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&receivedPayload)
		_ = json.NewEncoder(w).Encode(protocol.HeartbeatResponse{
			IntervalSeconds: 15,
			WatchedUnits:    []string{"caddy.service", "postgresql.service"},
			ServerTime:      time.Now().UTC().Format(time.RFC3339),
		})
	}))
	defer srv.Close()

	runner := NewRunner(srv.URL, "vpsp_test_tok", "/nonexistent.sock", "0.1.0", 10*time.Second)
	runner.Systemd = &SystemdCollector{
		lookPath: func(string) (string, error) { return "", context.Canceled },
	}
	if err := runner.SendOnce(context.Background()); err != nil {
		t.Fatalf("SendOnce: %v", err)
	}
	if receivedAuth != "Bearer vpsp_test_tok" {
		t.Fatalf("unexpected Authorization header: %q", receivedAuth)
	}
	if receivedPayload.Host.Hostname == "" {
		t.Fatalf("expected hostname in payload")
	}
	watched := runner.WatchedUnits()
	if len(watched) != 2 || watched[0] != "caddy.service" || runner.Interval != 15*time.Second {
		t.Fatalf("expected dynamic config applied, got watched=%v interval=%v", watched, runner.Interval)
	}
}

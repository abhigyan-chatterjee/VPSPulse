package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"vpspulse/internal/protocol"
	"vpspulse/internal/store"
)

func newTestHub(t *testing.T) (*store.Store, *Broker, *Prober, *AlertManager, *Server) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	broker := NewBroker()
	alerts := NewAlertManager(st, broker)
	prober := NewProber(st, broker, alerts)
	staticFS := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html><body>VPSPulse</body></html>")},
	}
	srv := NewServer(Config{PublicURL: "https://monitor.example.com", TrustProxy: true}, st, broker, prober, alerts, staticFS)
	return st, broker, prober, alerts, srv
}

func TestProberHTTPAndTLSExpiryAndTCP(t *testing.T) {
	st, _, prober, _, _ := newTestHub(t)

	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"healthy"}`))
	}))
	defer tlsSrv.Close()
	prober.httpClient = tlsSrv.Client()

	pHTTP, err := st.CreateProbe(store.Probe{
		Name: "api-tls", Type: "http", Target: tlsSrv.URL, Keyword: "healthy", Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateProbe HTTP: %v", err)
	}
	res, err := prober.CheckProbeNow(context.Background(), *pHTTP)
	if err != nil {
		t.Fatalf("CheckProbeNow HTTP: %v", err)
	}
	if res.Status != "up" || res.TLSExpiryDays == nil {
		t.Fatalf("expected UP with TLSExpiryDays, got %+v", res)
	}

	// Keyword mismatch should mark probe down.
	pMismatch, _ := st.CreateProbe(store.Probe{
		Name: "api-bad-kw", Type: "http", Target: tlsSrv.URL, Keyword: "missing-string", Enabled: true,
	})
	resBad, _ := prober.CheckProbeNow(context.Background(), *pMismatch)
	if resBad.Status != "down" {
		t.Fatalf("expected down on keyword mismatch, got %+v", resBad)
	}

	// TCP check against httptest listener address.
	addr := strings.TrimPrefix(tlsSrv.URL, "https://")
	pTCP, _ := st.CreateProbe(store.Probe{Name: "tcp-check", Type: "tcp", Target: addr, Enabled: true})
	resTCP, _ := prober.CheckProbeNow(context.Background(), *pTCP)
	if resTCP.Status != "up" {
		t.Fatalf("expected TCP check UP, got %+v", resTCP)
	}
}

func TestAlertManagerProbeHysteresisAndNoSpam(t *testing.T) {
	st, _, _, alerts, _ := newTestHub(t)

	var mu sync.Mutex
	var received []map[string]any
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		received = append(received, body)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer hook.Close()

	_, err := st.CreateChannel(store.NotificationChannel{
		Name:    "generic",
		Type:    "webhook",
		Config:  map[string]string{"url": hook.URL, "secret": "s3cr3t"},
		Events:  store.ChannelEvents{Offline: true, Service: true, Resource: true, TLS: true},
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}

	p, _ := st.CreateProbe(store.Probe{
		Name: "web", Type: "http", Target: "https://web.example.com", FailureThreshold: 2, Enabled: true,
	})

	// 1st failure: below threshold -> 0 alerts
	alerts.EvaluateProbe(*p, store.ProbeCheckResult{Status: "down", ErrorMsg: "timeout"})
	time.Sleep(80 * time.Millisecond)
	mu.Lock()
	c1 := len(received)
	mu.Unlock()
	if c1 != 0 {
		t.Fatalf("expected 0 alerts after 1st failure, got %d", c1)
	}

	// 2nd failure: crosses threshold -> 1 alert
	alerts.EvaluateProbe(*p, store.ProbeCheckResult{Status: "down", ErrorMsg: "timeout"})
	time.Sleep(120 * time.Millisecond)
	mu.Lock()
	c2 := len(received)
	mu.Unlock()
	if c2 != 1 {
		t.Fatalf("expected 1 alert after 2nd failure, got %d", c2)
	}

	// 3rd failure: already open -> still 1 alert (no duplicate spam)
	alerts.EvaluateProbe(*p, store.ProbeCheckResult{Status: "down", ErrorMsg: "timeout"})
	time.Sleep(80 * time.Millisecond)
	mu.Lock()
	c3 := len(received)
	mu.Unlock()
	if c3 != 1 {
		t.Fatalf("expected no duplicate alert on 3rd failure, got %d", c3)
	}

	// Recovery -> 1 recovery alert (total 2)
	alerts.EvaluateProbe(*p, store.ProbeCheckResult{Status: "up", LatencyMs: 25})
	time.Sleep(120 * time.Millisecond)
	mu.Lock()
	c4 := len(received)
	mu.Unlock()
	if c4 != 2 {
		t.Fatalf("expected 1 recovery alert (total 2), got %d", c4)
	}
}

func TestDockerPolicyAndVPSOfflineTransitions(t *testing.T) {
	st, _, _, alerts, _ := newTestHub(t)
	v, _, _ := st.CreateVPS("node-1", "eu")

	now := time.Now()
	hb := protocol.HeartbeatPayload{
		Timestamp:       now.Add(-40 * time.Second),
		Host:            protocol.HostMeta{Hostname: "node-1"},
		Metrics:         protocol.HostMetrics{CPUPercent: 15, RAMUsedBytes: 100, RAMTotalBytes: 1000},
		DockerAvailable: true,
		Containers: []protocol.DockerContainerReport{
			{ID: "c1", Name: "oneshot", Image: "alpine", State: "exited", ExitCode: 0},
			{ID: "c2", Name: "worker", Image: "worker:1", State: "running", Health: "unhealthy"},
		},
	}
	if err := st.RecordHeartbeat(v.ID, hb); err != nil {
		t.Fatalf("RecordHeartbeat: %v", err)
	}
	vLoaded, _ := st.GetVPS(v.ID)
	alerts.EvaluateHeartbeat(*vLoaded, hb, now)

	incs, _ := st.ListIncidents("open", 50)
	if len(incs) != 1 || !strings.Contains(incs[0].Title, "worker") {
		t.Fatalf("expected only unhealthy 'worker' incident (not Exited 0 oneshot), got %+v", incs)
	}

	// Muting 'worker' should resolve its open incident on next heartbeat.
	muted := true
	_ = st.UpdateContainerPolicy(v.ID, "worker", &muted, nil)
	alerts.EvaluateHeartbeat(*vLoaded, hb, now)
	incsOpen, _ := st.ListIncidents("open", 50)
	if len(incsOpen) != 0 {
		t.Fatalf("expected 0 open incidents after muting worker, got %+v", incsOpen)
	}

	// Silence >35s marks VPS offline and opens incident; new heartbeat recovers it.
	alerts.CheckOfflineVPS(now)
	vAfterOffline, _ := st.GetVPS(v.ID)
	if vAfterOffline.Status != "offline" {
		t.Fatalf("expected VPS status offline after >35s silence, got %s", vAfterOffline.Status)
	}
	incsOffline, _ := st.ListIncidents("open", 50)
	if len(incsOffline) != 1 || !strings.Contains(incsOffline[0].Title, "offline") {
		t.Fatalf("expected offline incident, got %+v", incsOffline)
	}

	hbNow := hb
	hbNow.Timestamp = now
	_ = st.RecordHeartbeat(v.ID, hbNow)
	vOnline, _ := st.GetVPS(v.ID)
	alerts.EvaluateHeartbeat(*vOnline, hbNow, now)
	if vOnline.Status != "online" {
		t.Fatalf("expected VPS status online after heartbeat, got %s", vOnline.Status)
	}
	incsAfterRec, _ := st.ListIncidents("open", 50)
	if len(incsAfterRec) != 0 {
		t.Fatalf("expected offline incident resolved on heartbeat recovery, got %+v", incsAfterRec)
	}
}

func TestAutoResolveRemovedContainerAndClearedSystemdIncident(t *testing.T) {
	st, _, _, alerts, _ := newTestHub(t)
	v, _, _ := st.CreateVPS("cleanup-node", "in")

	now := time.Now()
	hbBad := protocol.HeartbeatPayload{
		Timestamp:        now,
		Host:             protocol.HostMeta{Hostname: "cleanup-node"},
		Metrics:          protocol.HostMetrics{CPUPercent: 10, RAMUsedBytes: 100, RAMTotalBytes: 1000},
		DockerAvailable:  true,
		Containers:       []protocol.DockerContainerReport{{ID: "dead1", Name: "nostalgic_bhabha", Image: "node", State: "exited", ExitCode: 1}},
		SystemdAvailable: true,
		SystemdUnits:     []protocol.SystemdUnitReport{{Name: "certbot.service", ActiveState: "failed", SubState: "failed", AutoFailed: true}},
	}
	if err := st.RecordHeartbeat(v.ID, hbBad); err != nil {
		t.Fatalf("RecordHeartbeat bad: %v", err)
	}
	vBad, _ := st.GetVPS(v.ID)
	alerts.EvaluateHeartbeat(*vBad, hbBad, now)

	openIncs, _ := st.ListIncidents("open", 50)
	if len(openIncs) != 2 {
		t.Fatalf("expected 2 open incidents (container + systemd), got %d: %+v", len(openIncs), openIncs)
	}

	// Next heartbeat after container prune and certbot reset-failed reports neither.
	hbClean := hbBad
	hbClean.Timestamp = now.Add(10 * time.Second)
	hbClean.Containers = nil
	hbClean.SystemdUnits = nil
	if err := st.RecordHeartbeat(v.ID, hbClean); err != nil {
		t.Fatalf("RecordHeartbeat clean: %v", err)
	}
	vClean, _ := st.GetVPS(v.ID)
	alerts.EvaluateHeartbeat(*vClean, hbClean, now.Add(10*time.Second))

	openAfter, _ := st.ListIncidents("open", 50)
	if len(openAfter) != 0 {
		t.Fatalf("expected 0 open incidents after container removal & systemd reset, got %+v", openAfter)
	}
}

func TestServerAuthHeartbeatAndInstallScript(t *testing.T) {
	_, _, _, _, srv := newTestHub(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 1. Unauthenticated requests must return 401.
	for _, path := range []string{"/api/v1/vps", "/api/v1/events", "/api/v1/incidents"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401 for unauthenticated %s, got %d", path, resp.StatusCode)
		}
	}

	// 2. Setup admin and preserve session cookie.
	client := &http.Client{Timeout: 5 * time.Second}
	setupBody := bytes.NewBufferString(`{"username":"admin","password":"strongpassword"}`)
	resp, err := client.Post(ts.URL+"/api/v1/auth/setup", "application/json", setupBody)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("setup failed: status=%v err=%v", resp, err)
	}
	cookies := resp.Cookies()
	_ = resp.Body.Close()

	authedReq := func(method, path, body string) *http.Response {
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for _, c := range cookies {
			req.AddCookie(c)
		}
		r, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		return r
	}

	// 3. Create VPS -> capture raw token.
	rCreate := authedReq(http.MethodPost, "/api/v1/vps", `{"name":"prod-1","tag":"eu"}`)
	if rCreate.StatusCode != http.StatusCreated {
		t.Fatalf("create VPS status %d", rCreate.StatusCode)
	}
	var createRes struct {
		VPS   VPSView `json:"vps"`
		Token string  `json:"token"`
	}
	_ = json.NewDecoder(rCreate.Body).Decode(&createRes)
	_ = rCreate.Body.Close()

	// 4. Configure watched systemd units and send agent heartbeat.
	rUnits := authedReq(http.MethodPut, "/api/v1/vps/"+createRes.VPS.ID+"/systemd", `{"units":["caddy.service"]}`)
	_ = rUnits.Body.Close()
	if rUnits.StatusCode != http.StatusOK {
		t.Fatalf("set watched units status %d", rUnits.StatusCode)
	}

	hbJSON, _ := json.Marshal(protocol.HeartbeatPayload{
		AgentVersion: "0.4.2",
		Timestamp:    time.Now().UTC(),
		Host:         protocol.HostMeta{Hostname: "prod-1", OS: "Ubuntu 24.04", CPUCores: 4},
		Metrics:      protocol.HostMetrics{CPUPercent: 22.5, RAMUsedBytes: 1024, RAMTotalBytes: 4096},
	})
	hbReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/agent/heartbeat", bytes.NewReader(hbJSON))
	hbReq.Header.Set("Authorization", "Bearer "+createRes.Token)
	hbResp, err := client.Do(hbReq)
	if err != nil || hbResp.StatusCode != http.StatusOK {
		t.Fatalf("heartbeat failed: resp=%v err=%v", hbResp, err)
	}
	var hbParsed protocol.HeartbeatResponse
	_ = json.NewDecoder(hbResp.Body).Decode(&hbParsed)
	_ = hbResp.Body.Close()
	if len(hbParsed.WatchedUnits) != 1 || hbParsed.WatchedUnits[0] != "caddy.service" {
		t.Fatalf("expected watched unit synced back to agent, got %+v", hbParsed.WatchedUnits)
	}

	// 5. Rotate token -> old token must return 401.
	rRot := authedReq(http.MethodPost, "/api/v1/vps/"+createRes.VPS.ID+"/rotate-token", `{}`)
	_ = rRot.Body.Close()
	if rRot.StatusCode != http.StatusOK {
		t.Fatalf("rotate token status %d", rRot.StatusCode)
	}
	hbReqOld, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/agent/heartbeat", bytes.NewReader(hbJSON))
	hbReqOld.Header.Set("Authorization", "Bearer "+createRes.Token)
	hbRespOld, _ := client.Do(hbReqOld)
	_ = hbRespOld.Body.Close()
	if hbRespOld.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for rotated old agent token, got %d", hbRespOld.StatusCode)
	}

	// 6. Verify /install.sh is valid POSIX shell syntax.
	rInst, err := http.Get(ts.URL + "/install.sh")
	if err != nil || rInst.StatusCode != http.StatusOK {
		t.Fatalf("GET /install.sh failed: %v", err)
	}
	var instBuf bytes.Buffer
	_, _ = instBuf.ReadFrom(rInst.Body)
	_ = rInst.Body.Close()
	if !strings.Contains(instBuf.String(), "https://monitor.example.com") {
		t.Fatalf("install.sh missing configured public URL")
	}
	cmd := exec.Command("sh", "-n")
	cmd.Stdin = strings.NewReader(instBuf.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("install.sh syntax check failed: %v\n%s", err, string(out))
	}

	// 7. Verify /robots.txt and X-Robots-Tag anti-indexing headers.
	rRobots, err := http.Get(ts.URL + "/robots.txt")
	if err != nil || rRobots.StatusCode != http.StatusOK {
		t.Fatalf("GET /robots.txt failed: %v", err)
	}
	var robotsBuf bytes.Buffer
	_, _ = robotsBuf.ReadFrom(rRobots.Body)
	_ = rRobots.Body.Close()
	if !strings.Contains(robotsBuf.String(), "Disallow: /") {
		t.Fatalf("unexpected /robots.txt body: %q", robotsBuf.String())
	}
	if xrt := rRobots.Header.Get("X-Robots-Tag"); !strings.Contains(xrt, "noindex") {
		t.Fatalf("expected X-Robots-Tag noindex, got %q", xrt)
	}
}

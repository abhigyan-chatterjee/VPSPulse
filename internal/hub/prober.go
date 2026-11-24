package hub

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"vpspulse/internal/store"
)

// Prober executes scheduled HTTP/HTTPS, TCP, and ICMP external health checks.
type Prober struct {
	store      *store.Store
	broker     *Broker
	alerts     *AlertManager
	httpClient *http.Client
	mu         sync.Mutex
	inFlight   map[string]bool
}

// NewProber creates a Prober.
func NewProber(st *store.Store, broker *Broker, alerts *AlertManager) *Prober {
	return &Prober{
		store:      st,
		broker:     broker,
		alerts:     alerts,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		inFlight:   map[string]bool{},
	}
}

// Start runs the background probe scheduler until ctx is cancelled.
func (p *Prober) Start(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	p.RunDue(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.RunDue(ctx)
		}
	}
}

// RunDue finds all enabled probes that are due and executes them concurrently.
func (p *Prober) RunDue(ctx context.Context) {
	probes, err := p.store.ListProbes(nil)
	if err != nil {
		return
	}
	now := time.Now().Unix()
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup

	for _, prb := range probes {
		if !prb.Enabled {
			continue
		}
		if prb.LastCheckedAt > 0 && now-prb.LastCheckedAt < int64(prb.IntervalSec) {
			continue
		}

		p.mu.Lock()
		if p.inFlight[prb.ID] {
			p.mu.Unlock()
			continue
		}
		p.inFlight[prb.ID] = true
		p.mu.Unlock()

		wg.Add(1)
		go func(target store.Probe) {
			defer wg.Done()
			defer func() {
				p.mu.Lock()
				delete(p.inFlight, target.ID)
				p.mu.Unlock()
			}()
			sem <- struct{}{}
			defer func() { <-sem }()

			_, _ = p.CheckProbeNow(ctx, target)
		}(prb)
	}
	wg.Wait()
}

// CheckProbeNow executes one probe immediately, records the result, evaluates alerts, and publishes SSE.
func (p *Prober) CheckProbeNow(ctx context.Context, probe store.Probe) (store.ProbeCheckResult, error) {
	res := p.execute(ctx, probe)
	if err := p.store.RecordProbeResult(probe.ID, res); err != nil {
		return res, err
	}
	if p.alerts != nil {
		p.alerts.EvaluateProbe(probe, res)
	}
	if p.broker != nil {
		updated, err := p.store.GetProbe(probe.ID)
		if err == nil {
			p.broker.Publish("probe_update", updated)
		}
	}
	return res, nil
}

func (p *Prober) execute(ctx context.Context, probe store.Probe) store.ProbeCheckResult {
	timeout := time.Duration(probe.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	switch strings.ToLower(probe.Type) {
	case "tcp":
		return p.checkTCP(cctx, probe.Target)
	case "icmp", "ping":
		return p.checkICMP(cctx, probe.Target, timeout)
	default:
		return p.checkHTTP(cctx, probe, timeout)
	}
}

func (p *Prober) checkHTTP(ctx context.Context, probe store.Probe, timeout time.Duration) store.ProbeCheckResult {
	method := strings.ToUpper(probe.Method)
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, probe.Target, nil)
	if err != nil {
		return store.ProbeCheckResult{Timestamp: time.Now().Unix(), Status: "down", ErrorMsg: err.Error()}
	}
	req.Header.Set("User-Agent", "VPSPulse-Prober/1.0")

	client := *p.httpClient
	client.Timeout = timeout

	start := time.Now()
	resp, err := client.Do(req)
	latency := int(time.Since(start).Milliseconds())
	if err != nil {
		return store.ProbeCheckResult{Timestamp: time.Now().Unix(), Status: "down", LatencyMs: latency, ErrorMsg: err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()

	res := store.ProbeCheckResult{
		Timestamp:  time.Now().Unix(),
		Status:     "up",
		LatencyMs:  latency,
		StatusCode: resp.StatusCode,
	}

	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		days := int(time.Until(resp.TLS.PeerCertificates[0].NotAfter).Hours() / 24)
		res.TLSExpiryDays = &days
	}

	minCode := probe.ExpectedMin
	maxCode := probe.ExpectedMax
	if minCode == 0 {
		minCode = 200
	}
	if maxCode == 0 {
		maxCode = 299
	}
	if resp.StatusCode < minCode || resp.StatusCode > maxCode {
		res.Status = "down"
		res.ErrorMsg = fmt.Sprintf("unexpected status %d (expected %d-%d)", resp.StatusCode, minCode, maxCode)
		return res
	}

	if probe.Keyword != "" && method != http.MethodHead {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
		if !strings.Contains(string(body), probe.Keyword) {
			res.Status = "down"
			res.ErrorMsg = fmt.Sprintf("keyword %q not found in response", probe.Keyword)
			return res
		}
	}
	return res
}

func (p *Prober) checkTCP(ctx context.Context, target string) store.ProbeCheckResult {
	start := time.Now()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", target)
	latency := int(time.Since(start).Milliseconds())
	if err != nil {
		return store.ProbeCheckResult{Timestamp: time.Now().Unix(), Status: "down", LatencyMs: latency, ErrorMsg: err.Error()}
	}
	_ = conn.Close()
	return store.ProbeCheckResult{Timestamp: time.Now().Unix(), Status: "up", LatencyMs: latency}
}

func (p *Prober) checkICMP(ctx context.Context, target string, timeout time.Duration) store.ProbeCheckResult {
	secs := int(timeout.Seconds())
	if secs < 1 {
		secs = 1
	}
	start := time.Now()
	cmd := exec.CommandContext(ctx, "ping", "-c", "1", "-W", fmt.Sprintf("%d", secs), target)
	out, err := cmd.CombinedOutput()
	latency := int(time.Since(start).Milliseconds())
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return store.ProbeCheckResult{Timestamp: time.Now().Unix(), Status: "down", LatencyMs: latency, ErrorMsg: msg}
	}
	return store.ProbeCheckResult{Timestamp: time.Now().Unix(), Status: "up", LatencyMs: latency}
}

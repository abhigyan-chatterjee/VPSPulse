package hub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"vpspulse/internal/store"
	"vpspulse/internal/version"
)

// Prober executes scheduled HTTP/HTTPS, TCP, and ICMP external health checks.
type Prober struct {
	store      *store.Store
	broker     *Broker
	alerts     *AlertManager
	httpClient *http.Client
	sem        chan struct{}
	mu         sync.Mutex
	inFlight   map[string]bool
}

// NewProber creates a Prober with SSRF protections on outbound dials.
func NewProber(st *store.Store, broker *Broker, alerts *AlertManager) *Prober {
	dialer := &net.Dialer{
		Timeout: 8 * time.Second,
		Control: ssrfSafeDialControl,
	}
	tr := &http.Transport{
		DialContext:         dialer.DialContext,
		MaxIdleConns:        32,
		IdleConnTimeout:     60 * time.Second,
		TLSHandshakeTimeout: 8 * time.Second,
	}
	client := &http.Client{
		Transport: tr,
		Timeout:   10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("stopped after 5 redirects")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("disallowed redirect scheme %q", req.URL.Scheme)
			}
			return nil
		},
	}
	return &Prober{
		store:      st,
		broker:     broker,
		alerts:     alerts,
		httpClient: client,
		sem:        make(chan struct{}, 8),
		inFlight:   map[string]bool{},
	}
}

// ssrfSafeDialControl blocks cloud metadata / link-local / unspecified addresses to mitigate SSRF.
func ssrfSafeDialControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return fmt.Errorf("blocked target IP %s", ip.String())
	}
	return nil
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

// RunDue dispatches due enabled probes without blocking the scheduler loop.
func (p *Prober) RunDue(ctx context.Context) {
	probes, err := p.store.ListProbes(nil)
	if err != nil {
		return
	}
	now := time.Now().Unix()

	for _, prb := range probes {
		if !prb.Enabled {
			continue
		}
		if prb.LastCheckedAt > 0 && now-prb.LastCheckedAt < int64(prb.IntervalSec) {
			continue
		}

		select {
		case p.sem <- struct{}{}:
		default:
			continue
		}

		go func(target store.Probe) {
			defer func() { <-p.sem }()
			_, _ = p.CheckProbeNow(ctx, target)
		}(prb)
	}
}

// CheckProbeNow executes one probe immediately (deduplicating concurrent runs per probe ID), records the result, and emits SSE.
func (p *Prober) CheckProbeNow(ctx context.Context, probe store.Probe) (store.ProbeCheckResult, error) {
	p.mu.Lock()
	if p.inFlight[probe.ID] {
		p.mu.Unlock()
		return store.ProbeCheckResult{}, errors.New("probe check already in progress")
	}
	p.inFlight[probe.ID] = true
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		delete(p.inFlight, probe.ID)
		p.mu.Unlock()
	}()

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
	if timeout <= 0 || timeout > 30*time.Second {
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
	req.Header.Set("User-Agent", "VPSPulse-Prober/"+version.String())

	client := *p.httpClient
	client.Timeout = timeout

	start := time.Now()
	resp, err := client.Do(req)
	latency := int(time.Since(start).Milliseconds())
	if err != nil {
		return store.ProbeCheckResult{Timestamp: time.Now().Unix(), Status: "down", LatencyMs: latency, ErrorMsg: err.Error()}
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
		_ = resp.Body.Close()
	}()

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
	d := net.Dialer{Control: ssrfSafeDialControl}
	conn, err := d.DialContext(ctx, "tcp", target)
	latency := int(time.Since(start).Milliseconds())
	if err != nil {
		return store.ProbeCheckResult{Timestamp: time.Now().Unix(), Status: "down", LatencyMs: latency, ErrorMsg: err.Error()}
	}
	_ = conn.Close()
	return store.ProbeCheckResult{Timestamp: time.Now().Unix(), Status: "up", LatencyMs: latency}
}

func (p *Prober) checkICMP(ctx context.Context, target string, timeout time.Duration) store.ProbeCheckResult {
	if err := store.ValidateProbeTarget("icmp", target); err != nil {
		return store.ProbeCheckResult{Timestamp: time.Now().Unix(), Status: "down", ErrorMsg: err.Error()}
	}
	secs := int(timeout.Seconds())
	if secs < 1 {
		secs = 1
	}
	start := time.Now()
	cmd := exec.CommandContext(ctx, "ping", "-c", "1", "-W", fmt.Sprintf("%d", secs), "--", target)
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

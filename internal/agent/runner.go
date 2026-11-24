package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"vpspulse/internal/protocol"
)

// Runner coordinates periodic metric collection and pushes heartbeats to the hub.
type Runner struct {
	HubURL       string
	Token        string
	AgentVersion string
	Interval     time.Duration
	Host         *HostCollector
	Docker       *DockerCollector
	Systemd      *SystemdCollector
	HTTPClient   *http.Client

	mu           sync.RWMutex
	watchedUnits []string
}

// NewRunner creates an agent Runner with sane defaults.
func NewRunner(hubURL, token, dockerSocket, version string, interval time.Duration) *Runner {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	if version == "" {
		version = "0.1.0"
	}
	return &Runner{
		HubURL:       strings.TrimRight(hubURL, "/"),
		Token:        token,
		AgentVersion: version,
		Interval:     interval,
		Host:         NewHostCollector(),
		Docker:       NewDockerCollector(dockerSocket),
		Systemd:      NewSystemdCollector(),
		HTTPClient:   &http.Client{Timeout: 8 * time.Second},
	}
}

// WatchedUnits returns the current dynamically synced systemd watch list.
func (r *Runner) WatchedUnits() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]string(nil), r.watchedUnits...)
}

// SendOnce collects one full snapshot and pushes it to the hub.
func (r *Runner) SendOnce(ctx context.Context) error {
	r.mu.RLock()
	watched := append([]string(nil), r.watchedUnits...)
	r.mu.RUnlock()

	hostMeta, hostMetrics := r.Host.Collect()
	dockerAvail, containers := r.Docker.Collect(ctx)
	systemdAvail, units, discoverable := r.Systemd.Collect(ctx, watched)

	payload := protocol.HeartbeatPayload{
		AgentVersion:      r.AgentVersion,
		Timestamp:         time.Now().UTC(),
		Host:              hostMeta,
		Metrics:           hostMetrics,
		DockerAvailable:   dockerAvail,
		Containers:        containers,
		SystemdAvailable:  systemdAvail,
		SystemdUnits:      units,
		DiscoverableUnits: discoverable,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.HubURL+"/api/v1/agent/heartbeat", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.Token)

	resp, err := r.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("post heartbeat: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("heartbeat rejected (%d): %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	var hbResp protocol.HeartbeatResponse
	if err := json.NewDecoder(resp.Body).Decode(&hbResp); err == nil {
		r.mu.Lock()
		r.watchedUnits = hbResp.WatchedUnits
		if hbResp.IntervalSeconds >= 5 && hbResp.IntervalSeconds <= 300 {
			r.Interval = time.Duration(hbResp.IntervalSeconds) * time.Second
		}
		r.mu.Unlock()
	}
	return nil
}

// Run pushes heartbeats continuously until ctx is cancelled.
func (r *Runner) Run(ctx context.Context, onErr func(error)) {
	if err := r.SendOnce(ctx); err != nil && onErr != nil {
		onErr(err)
	}

	for {
		r.mu.RLock()
		wait := r.Interval
		r.mu.RUnlock()

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			if err := r.SendOnce(ctx); err != nil && onErr != nil {
				onErr(err)
			}
		}
	}
}

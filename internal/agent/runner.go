package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"vpspulse/internal/protocol"
	"vpspulse/internal/version"
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

// NewRunner creates an agent Runner with clamped interval [5s, 300s] and redirect-safe HTTP client.
func NewRunner(hubURL, token, dockerSocket, ver string, interval time.Duration) *Runner {
	if interval < 5*time.Second {
		interval = 10 * time.Second
	} else if interval > 300*time.Second {
		interval = 300 * time.Second
	}
	if ver == "" {
		ver = version.String()
	}
	return &Runner{
		HubURL:       strings.TrimRight(hubURL, "/"),
		Token:        token,
		AgentVersion: ver,
		Interval:     interval,
		Host:         NewHostCollector(),
		Docker:       NewDockerCollector(dockerSocket),
		Systemd:      NewSystemdCollector(),
		HTTPClient: &http.Client{
			Timeout: 8 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
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
	interval := r.Interval
	r.mu.RUnlock()

	tickTimeout := interval * 2
	if tickTimeout < 15*time.Second {
		tickTimeout = 15 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, tickTimeout)
	defer cancel()

	hostMeta, hostMetrics := r.Host.Collect(cctx)
	dockerAvail, containers := r.Docker.Collect(cctx)
	systemdAvail, units, discoverable := r.Systemd.Collect(cctx, watched)

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

	req, err := http.NewRequestWithContext(cctx, http.MethodPost, r.HubURL+"/api/v1/agent/heartbeat", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.Token)

	resp, err := r.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("post heartbeat: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("heartbeat rejected (%d): %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	var hbResp protocol.HeartbeatResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&hbResp); err == nil {
		r.mu.Lock()
		r.watchedUnits = hbResp.WatchedUnits
		if hbResp.IntervalSeconds >= 5 && hbResp.IntervalSeconds <= 300 {
			r.Interval = time.Duration(hbResp.IntervalSeconds) * time.Second
		}
		r.mu.Unlock()
	}
	return nil
}

// Run pushes heartbeats continuously with exponential backoff + jitter on errors until ctx is cancelled.
func (r *Runner) Run(ctx context.Context, onErr func(error)) {
	consecutiveFails := 0
	if err := r.SendOnce(ctx); err != nil {
		consecutiveFails++
		if onErr != nil {
			onErr(err)
		}
	}
	if ctx.Err() != nil {
		return
	}

	for {
		r.mu.RLock()
		wait := r.Interval
		r.mu.RUnlock()

		if consecutiveFails > 0 {
			shift := min(consecutiveFails-1, 4)
			wait = wait << shift
			if wait > 2*time.Minute {
				wait = 2 * time.Minute
			}
			// ±20% jitter to avoid thundering herd on hub recovery
			jitter := 0.8 + rand.Float64()*0.4
			wait = time.Duration(float64(wait) * jitter)
		}

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			if err := r.SendOnce(ctx); err != nil {
				consecutiveFails++
				if onErr != nil {
					onErr(err)
				}
			} else {
				consecutiveFails = 0
			}
		}
	}
}

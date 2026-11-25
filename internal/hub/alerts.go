package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"vpspulse/internal/protocol"
	"vpspulse/internal/store"
)

// AlertConfig centralizes alerting and rollup intervals/thresholds.
type AlertConfig struct {
	MinOfflineAfter time.Duration
	SustainWindow   time.Duration
	HysteresisPct   float64
	TLSWarnDays     int
	MaxWorkers      int
}

// DefaultAlertConfig returns the standard alerting configuration.
func DefaultAlertConfig() AlertConfig {
	return AlertConfig{
		MinOfflineAfter: 35 * time.Second,
		SustainWindow:   180 * time.Second,
		HysteresisPct:   5,
		TLSWarnDays:     14,
		MaxWorkers:      8,
	}
}

// AlertManager evaluates VPS offline timeouts, service states, resource thresholds, and probe failures.
type AlertManager struct {
	store      *store.Store
	broker     *Broker
	httpClient *http.Client
	cfg        AlertConfig
	sem        chan struct{}

	mu                 sync.Mutex
	probeFailureCounts map[string]int
	highCPUStart       map[string]int64
	highRAMStart       map[string]int64
}

// NewAlertManager creates an AlertManager with DefaultAlertConfig.
func NewAlertManager(st *store.Store, broker *Broker) *AlertManager {
	cfg := DefaultAlertConfig()
	return &AlertManager{
		store:              st,
		broker:             broker,
		httpClient:         &http.Client{Timeout: 8 * time.Second},
		cfg:                cfg,
		sem:                make(chan struct{}, cfg.MaxWorkers),
		probeFailureCounts: map[string]int{},
		highCPUStart:       map[string]int64{},
		highRAMStart:       map[string]int64{},
	}
}

// ForgetVPS cleans up in-memory threshold trackers when a VPS is deleted.
func (a *AlertManager) ForgetVPS(vpsID string) {
	a.mu.Lock()
	delete(a.highCPUStart, vpsID)
	delete(a.highRAMStart, vpsID)
	a.mu.Unlock()
}

// ForgetProbe cleans up in-memory failure counters when a probe is deleted or disabled.
func (a *AlertManager) ForgetProbe(probeID string) {
	a.mu.Lock()
	delete(a.probeFailureCounts, probeID)
	a.mu.Unlock()
}

// Start runs the periodic VPS offline detector, 5m rollup loop, and hourly retention pruning loop.
func (a *AlertManager) Start(ctx context.Context) {
	offlineTicker := time.NewTicker(10 * time.Second)
	rollupTicker := time.NewTicker(5 * time.Minute)
	pruneTicker := time.NewTicker(1 * time.Hour)
	defer offlineTicker.Stop()
	defer rollupTicker.Stop()
	defer pruneTicker.Stop()

	policy := store.DefaultRetentionPolicy()
	_ = a.store.RunRollups(time.Now(), policy)

	for {
		select {
		case <-ctx.Done():
			return
		case <-offlineTicker.C:
			a.CheckOfflineVPS(time.Now())
		case <-rollupTicker.C:
			_ = a.store.RunRollups(time.Now(), policy)
		case <-pruneTicker.C:
			_ = a.store.PruneExpired(time.Now(), policy)
		}
	}
}

func (a *AlertManager) offlineThresholdFor(v store.VPS) int64 {
	interval := v.IntervalSeconds
	if interval < 5 {
		interval = 10
	}
	dyn := int64(interval*3 + 5)
	minSec := int64(a.cfg.MinOfflineAfter.Seconds())
	if dyn > minSec {
		return dyn
	}
	return minSec
}

// CheckOfflineVPS marks VPSes offline when no heartbeat has arrived within their dynamic offline window.
func (a *AlertManager) CheckOfflineVPS(now time.Time) {
	list, err := a.store.ListVPS()
	if err != nil {
		return
	}
	nowUnix := now.Unix()
	for _, v := range list {
		threshold := a.offlineThresholdFor(v)
		if v.Status == "online" && v.LastSeenAt > 0 && nowUnix-v.LastSeenAt > threshold {
			_ = a.store.SetVPSStatus(v.ID, "offline")
			key := "vps-offline:" + v.ID
			title := fmt.Sprintf("VPS %s is offline", v.Name)
			detail := fmt.Sprintf("No heartbeat received from %s for >%d seconds.", v.Name, threshold)
			inc, isNew, err := a.store.CreateOrUpdateIncident(key, "critical", &v.ID, title, detail)
			if err == nil && isNew {
				a.dispatch("offline", "critical", title, detail, &v.ID)
				if a.broker != nil {
					a.broker.Publish("incident_update", inc)
				}
			}
			if a.broker != nil {
				if updated, err := a.store.GetVPS(v.ID); err == nil {
					a.broker.Publish("vps_update", BuildVPSView(a.store, *updated))
				}
			}
		}
	}
}

// EvaluateHeartbeat processes an incoming heartbeat for recovery, container/systemd failures, and resource thresholds.
func (a *AlertManager) EvaluateHeartbeat(vps store.VPS, payload protocol.HeartbeatPayload, now time.Time) {
	// 1. Offline recovery
	if inc, resolved, err := a.store.ResolveIncidentByKey("vps-offline:"+vps.ID, fmt.Sprintf("VPS %s resumed heartbeats.", vps.Name)); err == nil && resolved {
		a.dispatch("offline", "resolved", fmt.Sprintf("VPS %s is back online", vps.Name), inc.Detail, &vps.ID)
		if a.broker != nil {
			a.broker.Publish("incident_update", inc)
		}
	}

	// 2. Docker container policy evaluation
	if payload.DockerAvailable {
		containers, err := a.store.ListContainers(vps.ID)
		if err == nil {
			activeDockerKeys := make(map[string]bool, len(containers))
			for _, c := range containers {
				key := fmt.Sprintf("docker:%s:%s", vps.ID, c.Name)
				activeDockerKeys[key] = true
				if c.Muted {
					if inc, resolved, err := a.store.ResolveIncidentByKey(key, "Container muted"); err == nil && resolved && a.broker != nil {
						a.broker.Publish("incident_update", inc)
					}
					continue
				}

				bad, reason := isContainerAlerting(c)
				if bad {
					title := fmt.Sprintf("Container %s on %s is %s", c.Name, vps.Name, reason)
					detail := fmt.Sprintf("Image: %s · State: %s · Health: %s · ExitCode: %d", c.Image, c.State, c.Health, c.ExitCode)
					inc, isNew, err := a.store.CreateOrUpdateIncident(key, "warning", &vps.ID, title, detail)
					if err == nil && isNew {
						a.dispatch("service", "warning", title, detail, &vps.ID)
						if a.broker != nil {
							a.broker.Publish("incident_update", inc)
						}
					}
				} else {
					if inc, resolved, err := a.store.ResolveIncidentByKey(key, fmt.Sprintf("Container %s recovered (%s)", c.Name, c.State)); err == nil && resolved {
						a.dispatch("service", "resolved", fmt.Sprintf("Container %s on %s recovered", c.Name, vps.Name), inc.Detail, &vps.ID)
						if a.broker != nil {
							a.broker.Publish("incident_update", inc)
						}
					}
				}
			}
			if resolvedList, err := a.store.ResolveStaleIncidentsByPrefix("docker:"+vps.ID+":", activeDockerKeys, "Container removed from host"); err == nil && a.broker != nil {
				for i := range resolvedList {
					a.broker.Publish("incident_update", &resolvedList[i])
				}
			}
		}
	}

	// 3. systemd unit evaluation
	if payload.SystemdAvailable {
		units, err := a.store.ListSystemdUnits(vps.ID)
		if err == nil {
			activeSystemdKeys := make(map[string]bool, len(units))
			for _, u := range units {
				key := fmt.Sprintf("systemd:%s:%s", vps.ID, u.Name)
				activeSystemdKeys[key] = true
				if u.ActiveState == "failed" || (u.Watched && u.ActiveState != "active") {
					title := fmt.Sprintf("systemd unit %s on %s is %s", u.Name, vps.Name, u.ActiveState)
					detail := fmt.Sprintf("SubState: %s (auto_failed=%v)", u.SubState, u.AutoFailed)
					inc, isNew, err := a.store.CreateOrUpdateIncident(key, "warning", &vps.ID, title, detail)
					if err == nil && isNew {
						a.dispatch("service", "warning", title, detail, &vps.ID)
						if a.broker != nil {
							a.broker.Publish("incident_update", inc)
						}
					}
				} else {
					if inc, resolved, err := a.store.ResolveIncidentByKey(key, fmt.Sprintf("Unit %s is active", u.Name)); err == nil && resolved {
						a.dispatch("service", "resolved", fmt.Sprintf("systemd unit %s on %s recovered", u.Name, vps.Name), inc.Detail, &vps.ID)
						if a.broker != nil {
							a.broker.Publish("incident_update", inc)
						}
					}
				}
			}
			if resolvedList, err := a.store.ResolveStaleIncidentsByPrefix("systemd:"+vps.ID+":", activeSystemdKeys, "systemd unit cleared"); err == nil && a.broker != nil {
				for i := range resolvedList {
					a.broker.Publish("incident_update", &resolvedList[i])
				}
			}
		}
	}

	// 4. Resource thresholds (sustained SustainWindow for CPU/RAM, immediate for Disk).
	sustainSec := int64(a.cfg.SustainWindow.Seconds())
	a.evaluateSustainedThreshold(vps, "cpu", payload.Metrics.CPUPercent, vps.CPUWarn, sustainSec, now, a.highCPUStart)
	var ramPct float64
	if payload.Metrics.RAMTotalBytes > 0 {
		ramPct = float64(payload.Metrics.RAMUsedBytes) * 100 / float64(payload.Metrics.RAMTotalBytes)
	}
	a.evaluateSustainedThreshold(vps, "ram", ramPct, vps.RAMWarn, sustainSec, now, a.highRAMStart)

	var diskPct float64
	if payload.Metrics.DiskTotalBytes > 0 {
		diskPct = float64(payload.Metrics.DiskUsedBytes) * 100 / float64(payload.Metrics.DiskTotalBytes)
	}
	a.evaluateSustainedThreshold(vps, "disk", diskPct, vps.DiskWarn, 0, now, nil)
}

func (a *AlertManager) evaluateSustainedThreshold(vps store.VPS, kind string, current, threshold float64, sustainSec int64, now time.Time, tracker map[string]int64) {
	if threshold <= 0 {
		return
	}
	key := fmt.Sprintf("resource:%s:%s", kind, vps.ID)
	nowUnix := now.Unix()

	if current >= threshold {
		triggered := sustainSec == 0
		if tracker != nil {
			a.mu.Lock()
			start, ok := tracker[vps.ID]
			if !ok {
				tracker[vps.ID] = nowUnix
				start = nowUnix
			}
			if nowUnix-start >= sustainSec {
				triggered = true
			}
			a.mu.Unlock()
		}
		if triggered {
			title := fmt.Sprintf("High %s usage on %s (%.1f%%)", strings.ToUpper(kind), vps.Name, current)
			detail := fmt.Sprintf("%s reached %.1f%% (threshold %.0f%%).", strings.ToUpper(kind), current, threshold)
			inc, isNew, err := a.store.CreateOrUpdateIncident(key, "warning", &vps.ID, title, detail)
			if err == nil && isNew {
				a.dispatch("resource", "warning", title, detail, &vps.ID)
				if a.broker != nil {
					a.broker.Publish("incident_update", inc)
				}
			}
		}
		return
	}

	// Clear sustained breach timer whenever usage dips below threshold.
	if tracker != nil {
		a.mu.Lock()
		delete(a.highCPUStart, vps.ID) // no-op if tracker is highRAMStart; delete on tracker below
		delete(tracker, vps.ID)
		a.mu.Unlock()
	}

	if current < threshold-a.cfg.HysteresisPct {
		if inc, resolved, err := a.store.ResolveIncidentByKey(key, fmt.Sprintf("%s usage returned to %.1f%%", strings.ToUpper(kind), current)); err == nil && resolved {
			a.dispatch("resource", "resolved", fmt.Sprintf("%s usage normalized on %s", strings.ToUpper(kind), vps.Name), inc.Detail, &vps.ID)
			if a.broker != nil {
				a.broker.Publish("incident_update", inc)
			}
		}
	}
}

func isContainerAlerting(c store.DockerContainerView) (bool, string) {
	state := strings.ToLower(c.State)
	health := strings.ToLower(c.Health)
	if c.OOMKilled {
		return true, "OOM killed"
	}
	if health == "unhealthy" {
		return true, "unhealthy"
	}
	if state == "restarting" {
		return true, "restarting"
	}
	if state == "missing" && c.Pinned {
		return true, "missing"
	}
	if state == "exited" {
		if c.ExitCode != 0 {
			return true, fmt.Sprintf("exited (%d)", c.ExitCode)
		}
		if c.Pinned {
			return true, "stopped (pinned)"
		}
		return false, ""
	}
	if state != "running" && state != "" {
		return true, state
	}
	return false, ""
}

// EvaluateProbe checks consecutive failures against FailureThreshold and TLS expiry < TLSWarnDays.
func (a *AlertManager) EvaluateProbe(probe store.Probe, res store.ProbeCheckResult) {
	key := "probe-down:" + probe.ID
	threshold := probe.FailureThreshold
	if threshold <= 0 {
		threshold = 2
	}

	var fails int
	a.mu.Lock()
	if res.Status == "up" {
		delete(a.probeFailureCounts, probe.ID)
	} else {
		a.probeFailureCounts[probe.ID]++
		fails = a.probeFailureCounts[probe.ID]
	}
	a.mu.Unlock()

	if res.Status == "up" {
		if inc, resolved, err := a.store.ResolveIncidentByKey(key, fmt.Sprintf("Probe %s recovered (%dms)", probe.Name, res.LatencyMs)); err == nil && resolved {
			a.dispatch("service", "resolved", fmt.Sprintf("Probe %s is UP", probe.Name), inc.Detail, probe.VPSID)
			if a.broker != nil {
				a.broker.Publish("incident_update", inc)
			}
		}
	} else if fails >= threshold {
		title := fmt.Sprintf("Probe %s is DOWN", probe.Name)
		detail := fmt.Sprintf("Target %s failed %d consecutive checks: %s", probe.Target, fails, res.ErrorMsg)
		inc, isNew, err := a.store.CreateOrUpdateIncident(key, "critical", probe.VPSID, title, detail)
		if err == nil && isNew {
			a.dispatch("service", "critical", title, detail, probe.VPSID)
			if a.broker != nil {
				a.broker.Publish("incident_update", inc)
			}
		}
	}

	if res.TLSExpiryDays != nil {
		tlsKey := "probe-tls:" + probe.ID
		if *res.TLSExpiryDays < a.cfg.TLSWarnDays {
			title := fmt.Sprintf("TLS certificate expiring soon for %s (%dd)", probe.Name, *res.TLSExpiryDays)
			detail := fmt.Sprintf("Certificate for %s expires in %d days.", probe.Target, *res.TLSExpiryDays)
			inc, isNew, err := a.store.CreateOrUpdateIncident(tlsKey, "warning", probe.VPSID, title, detail)
			if err == nil && isNew {
				a.dispatch("tls", "warning", title, detail, probe.VPSID)
				if a.broker != nil {
					a.broker.Publish("incident_update", inc)
				}
			}
		} else {
			if inc, resolved, err := a.store.ResolveIncidentByKey(tlsKey, fmt.Sprintf("TLS renewed (%dd remaining)", *res.TLSExpiryDays)); err == nil && resolved && a.broker != nil {
				a.broker.Publish("incident_update", inc)
			}
		}
	}
}

func (a *AlertManager) dispatch(category, severity, title, detail string, vpsID *string) {
	channels, err := a.store.ListChannels()
	if err != nil {
		return
	}
	for _, ch := range channels {
		if !ch.Enabled || !channelWantsEvent(ch.Events, category) {
			continue
		}
		select {
		case a.sem <- struct{}{}:
			go func(channel store.NotificationChannel) {
				defer func() { <-a.sem }()
				if err := a.sendToChannel(context.Background(), channel, category, severity, title, detail, vpsID); err != nil {
					log.Printf("alert delivery failed for channel %q (%s): %v", channel.Name, channel.Type, err)
				}
			}(ch)
		default:
			log.Printf("alert dispatch queue full; dropping notification for %q", ch.Name)
		}
	}
}

func channelWantsEvent(ev store.ChannelEvents, category string) bool {
	switch category {
	case "offline":
		return ev.Offline
	case "service":
		return ev.Service
	case "resource":
		return ev.Resource
	case "tls":
		return ev.TLS
	default:
		return true
	}
}

// SendTest sends a test notification synchronously and returns any delivery error.
func (a *AlertManager) SendTest(ctx context.Context, ch store.NotificationChannel) error {
	return a.sendToChannel(ctx, ch, "test", "info", "VPSPulse Test Alert", "Notifications are configured and working.", nil)
}

func (a *AlertManager) sendToChannel(ctx context.Context, ch store.NotificationChannel, category, severity, title, detail string, vpsID *string) error {
	var endpoint string
	var payload any
	headers := map[string]string{"Content-Type": "application/json"}

	prefix := "⚠️"
	if severity == "critical" {
		prefix = "🔴"
	} else if severity == "resolved" {
		prefix = "✅"
	} else if severity == "info" {
		prefix = "ℹ️"
	}
	text := fmt.Sprintf("%s **[VPSPulse]** %s\n%s", prefix, title, detail)

	switch ch.Type {
	case "smtp":
		return sendSMTP(ctx, ch, category, severity, title, detail)
	case "discord":
		endpoint = ch.Config["webhook_url"]
		payload = map[string]any{
			"content": fmt.Sprintf("%s **%s** — %s", prefix, title, detail),
		}
	case "slack":
		endpoint = ch.Config["webhook_url"]
		payload = map[string]any{
			"text": fmt.Sprintf("*%s*\n%s", title, detail),
		}
	case "telegram":
		botToken := ch.Config["bot_token"]
		chatID := ch.Config["chat_id"]
		if botToken == "" || chatID == "" {
			return fmt.Errorf("telegram requires bot_token and chat_id")
		}
		endpoint = fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", botToken)
		payload = map[string]any{
			"chat_id": chatID,
			"text":    text,
		}
	case "webhook":
		endpoint = ch.Config["url"]
		if endpoint == "" {
			endpoint = ch.Config["webhook_url"]
		}
		if sec := ch.Config["secret"]; sec != "" {
			headers["X-VPSPulse-Secret"] = sec
		}
		payload = map[string]any{
			"source":    "vpspulse",
			"category":  category,
			"severity":  severity,
			"title":     title,
			"detail":    detail,
			"vps_id":    vpsID,
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}
	default:
		return fmt.Errorf("unsupported channel type %q", ch.Type)
	}

	if endpoint == "" {
		return fmt.Errorf("missing webhook endpoint for channel %s", ch.Name)
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 16*1024))
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned HTTP %d", resp.StatusCode)
	}
	return nil
}

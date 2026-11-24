# VPSPulse — VPS & Service Monitoring Dashboard Design

**Date:** 2026-11-24  
**Status:** Approved  
**Target Directory:** `/home/abhi/.gemini/antigravity/scratch/vpspulse`

---

## 1. Intent & The Why

A small, self-hosted dashboard to monitor all VPSes and services from one place—similar in spirit to Uptime Kuma, but richer. Uptime Kuma excels at external uptime checks ("is this URL responding?"), while VPSPulse adds deep per-VPS visibility into host resources, Docker containers, and `systemd` units in one coherent VPS-by-VPS view without the weight of Prometheus + Grafana + cAdvisor.

---

## 2. Goals, Non-Goals & Anti-Goals

### Goals
- **VPS-first overview:** See every VPS at a glance with online/offline status, CPU, RAM, disk, network I/O, load average, uptime, and a summary of its services.
- **Detailed per-VPS drill-down:** Historical resource charts (`1h` / `24h` / `7d` / `30d`), Docker container health/stats, `systemd` unit status, and external service probes with Uptime Kuma-style heartbeat bars, uptime percentages, and response times.
- **Effortless VPS onboarding:** Add a VPS in the UI, get a per-VPS token and a copy-pasteable one-line install command, and see metrics flowing within seconds.
- **Zero-dependency footprint:** Single static Go binary for the central hub (with embedded web UI and pure-Go SQLite) and a tiny static Go binary for each VPS agent (`linux/amd64` and `linux/arm64`).
- **Actionable alerting:** Webhook notifications (Discord, Telegram, Slack, or generic JSON) when a VPS goes offline, a service fails, TLS nears expiry, or CPU/RAM/disk crosses thresholds.
- **Immediate local preview:** `--demo` mode seeding simulated VPSes and live heartbeats for instant exploration.

### Non-Goals (v1)
- Multi-user RBAC / teams / OAuth/SSO (single admin account).
- Remote execution / server management actions (restarting containers, web SSH). Strictly read-only observability.
- Full log aggregation or distributed tracing.
- Public unauthenticated status pages.
- Windows / macOS agent support.

### Anti-Goals
- Heavy agent overhead (>20 MB RAM or external runtime deps on VPSes).
- Open inbound firewall ports or centrally stored SSH keys.
- Unbounded SQLite disk growth.
- Alert fatigue from transient spikes or duplicate alerts.
- Cluttered enterprise UI.

---

## 3. Architecture & Product Design

### System Topology
- **Central Hub (`cmd/hub`):**
  - Embedded React + TypeScript + Tailwind SPA (`//go:embed`).
  - Authenticated REST API (`/api/v1/...`) + live SSE stream (`/api/v1/events`).
  - Agent ingestion endpoint (`POST /api/v1/agent/heartbeat`) authenticated via per-VPS bearer token.
  - Dynamic installer (`/install.sh`) and agent binary downloads (`/downloads/vpspulse-agent-linux-{amd64,arm64}`).
  - Central External Prober (HTTP/HTTPS, TCP, ICMP), Offline Detector, Alert Evaluator, and Metrics Rollup/Retention loop.
  - Pure-Go SQLite (`modernc.org/sqlite`) in `WAL` mode.
  - Runs behind a reverse proxy (`--public-url`, `--trust-proxy`) with optional `--tls-cert` / `--tls-key`.
- **VPS Agent (`cmd/agent`):**
  - Tiny single-binary Linux `systemd` service pushing JSON heartbeats every `10s`.
  - Reads dynamic watch config (watched `systemd` units, interval) from the hub's heartbeat HTTP response.

### Data & Policies
- **Host Resources:** CPU %, cores, load averages (`1m`/`5m`/`15m`), RAM & swap, mounted real filesystems, network RX/TX bytes/sec, OS/kernel/arch/IP/uptime/agent version.
- **Docker Containers:** Auto-detected via `/var/run/docker.sock` (no Docker CLI needed). Tracked by `(vps_id, container_name)` across recreations. Auto-monitored with per-container `muted` and `pinned` toggles; clean `Exited (0)` ignored unless `pinned`, alerts on `unhealthy`/`restarting`/`OOMKilled`/non-zero exit unless `muted`.
- **`systemd` Units:** Reports explicitly watched units + any auto-detected `failed` units, plus discoverable `.service` unit names for UI autocomplete.
- **External Probes:** HTTP/HTTPS (status range, optional keyword, latency, TLS expiry days), TCP, ICMP. Last 60 heartbeat ticks + `24h`/`7d`/`30d` uptime %.
- **Offline & Retention:** Offline after `35s` silence. Raw `10s` metrics kept `24h`, `1m` rollups `7d`, `5m` rollups `30d`, raw probe ticks `7d`, hourly probe summaries `90d`, incidents `90d`.
- **Alerting:** Global broadcast to enabled Discord/Telegram/Slack/Generic JSON channels with per-channel event toggles (`offline`, `service`, `resource`, `tls`) and state-transition hysteresis.

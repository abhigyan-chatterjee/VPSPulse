# VPSPulse

Lightweight, self-hosted **VPS-by-VPS** resource and service health monitoring dashboard inspired by Uptime Kuma — combining external HTTP/HTTPS/TCP/ICMP uptime checks with live per-VPS host metrics, Docker container health, and `systemd` unit monitoring in a single Go binary.

## Features

- **VPS-by-VPS Overview:** Live online/offline status, CPU %, load averages, RAM & swap, mounted disks, network RX/TX, CPU sparklines, and grouped service health ticks on one screen.
- **Detailed Drill-Down:** Interactive `1h` / `24h` / `7d` / `30d` charts with auto-scaled network throughput (`B/s`, `KB/s`, `MB/s`) and per-mount disk usage.
- **Zero-Inbound Push Agent (`~6 MB` static Go binary):** Outbound HTTPS heartbeats work behind NAT and strict firewalls with no open inbound ports or stored SSH keys.
- **Docker & `systemd` Visibility:**
  - Auto-discovers Docker containers via `/var/run/docker.sock`, tracks uptime by container name across recreations, and supports per-container **Pin** / **Mute** policies.
  - Monitors explicitly watched `systemd` units (`.service`, `.timer`, `.socket`) synced dynamically via heartbeat responses, plus auto-surfaces any `failed` unit.
- **External Probes:** HTTP/HTTPS (status range, body keyword match, TLS certificate expiry countdown), TCP port, and ICMP checks with Uptime Kuma-style heartbeat bars.
- **Actionable Alerting:** Discord, Telegram, Slack, **SMTP Email Relay** (STARTTLS / implicit TLS), and generic JSON webhooks with per-channel event toggles (`offline`, `service`, `resource`, `tls`) and anti-spam hysteresis.
- **Pure-Go SQLite (`WAL`):** Tiered downsampling (`10s` raw → `1m` → `5m`) and automated retention pruning keep disk usage bounded.
- **Real-Time SSE:** Live dashboard updates over `/api/v1/events` with dark/light mode support.

## Quick Preview (`--demo` mode)

Prerequisites: **Go 1.26+**, **Node.js 22+**, and **pnpm 10+**.

```bash
make demo
```

Open `http://localhost:8080` and sign in with **`admin` / `demo1234`** (or click **Fill** on the login card).

## Production Build & Run

```bash
make build-all
```

This builds the embedded React UI and outputs:
- `bin/vpspulse-hub` (central server with embedded UI + SQLite)
- `bin/vpspulse-agent-linux-amd64`
- `bin/vpspulse-agent-linux-arm64`

Run the hub behind a TLS reverse proxy:

```bash
./bin/vpspulse-hub \
  --listen 127.0.0.1:8080 \
  --db ./data/vpspulse.db \
  --public-url https://monitor.example.com \
  --trust-proxy \
  --downloads-dir ./bin
```

Or deploy via Docker Compose (`deploy/docker-compose.yml` binds `127.0.0.1:8080` by default):

```bash
docker compose -f deploy/docker-compose.yml up -d --build
```

## Connecting a VPS

1. Open the dashboard and click **+ Add VPS** to generate a per-VPS token.
2. Run the generated command on your Linux VPS:

```bash
curl -fsSL 'https://monitor.example.com/install.sh' | sudo sh -s -- \
  --url 'https://monitor.example.com' --token 'vpsp_...'
```

The installer downloads the matching `amd64`/`arm64` static agent binary, creates a hardened `vpspulse` system user and `systemd` service (`vpspulse-agent.service`), and starts pushing heartbeats every `10s`.

> **Security note:** If Docker is installed, the installer adds `vpspulse` to the `docker` group so it can read `/var/run/docker.sock`. Pass `--no-docker` if you do not need container monitoring on that host. To uninstall: `sudo systemctl disable --now vpspulse-agent && sudo rm -rf /etc/systemd/system/vpspulse-agent.service /etc/vpspulse /usr/local/bin/vpspulse-agent && sudo systemctl daemon-reload`.

## Reverse Proxy Examples

### Caddy
```caddy
monitor.example.com {
    reverse_proxy 127.0.0.1:8080 {
        flush_interval -1
    }
}
```

### Nginx
```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_set_header Connection "";
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_buffering off;
    proxy_read_timeout 3600s;
}
```

## Environment Variables (Hub)

| Variable | Default | Description |
|---|---|---|
| `VPSP_LISTEN` | `:8080` | HTTP listen address |
| `VPSP_DB_PATH` | `./data/vpspulse.db` | SQLite database path |
| `VPSP_PUBLIC_URL` | *(from Host header)* | Public base URL for `/install.sh` and links |
| `VPSP_TRUST_PROXY` | `false` | Trust `X-Forwarded-Proto` and proxy client IP headers |
| `VPSP_DOWNLOADS_DIR` | `./bin` | Directory containing `vpspulse-agent-linux-*` binaries |
| `VPSP_ADMIN_USER` / `VPSP_ADMIN_PASSWORD` | — | Optional first-boot admin credentials (password min 8 chars) |
| `VPSP_TLS_CERT` / `VPSP_TLS_KEY` | — | Optional direct TLS certificate & key paths |

## License

[MIT](LICENSE)

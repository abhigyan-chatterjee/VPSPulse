# VPSPulse

Lightweight, self-hosted **VPS-by-VPS** monitoring dashboard inspired by Uptime Kuma — combining external HTTP/HTTPS/TCP/ICMP service checks with live per-VPS host metrics, Docker container health, and `systemd` unit monitoring in a single Go binary.

## Quick Preview (`--demo` mode)

Build the web UI, agent binaries, and central hub, then launch demo mode:

```bash
make demo
```

Open `http://localhost:8080` and sign in with **`admin` / `demo1234`**.

## Production Build

```bash
make build-all
```

This produces:
- `bin/vpspulse-hub` (central server with embedded React UI + pure-Go SQLite)
- `bin/vpspulse-agent-linux-amd64`
- `bin/vpspulse-agent-linux-arm64`

Run the hub:

```bash
./bin/vpspulse-hub \
  --listen :8080 \
  --db ./data/vpspulse.db \
  --public-url https://monitor.example.com \
  --trust-proxy \
  --downloads-dir ./bin
```

Or via Docker Compose:

```bash
docker compose -f deploy/docker-compose.yml up -d --build
```

## Connecting a VPS

1. Open the dashboard and click **+ Add VPS**.
2. Copy the generated install snippet and run it on your Linux VPS:

```bash
curl -fsSL https://monitor.example.com/install.sh | sudo sh -s -- \
  --url https://monitor.example.com --token vpsp_...
```

The installer downloads the matching `amd64`/`arm64` static agent binary, creates a low-privilege `vpspulse` system user (added to the `docker` group if Docker is present), and enables `vpspulse-agent.service`.

## Reverse Proxy Examples

### Caddy
```caddy
monitor.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

### Nginx
```nginx
location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_buffering off;
}
```

## Environment Variables (Hub)
- `VPSP_LISTEN` (default `:8080`)
- `VPSP_DB_PATH` (default `./data/vpspulse.db`)
- `VPSP_PUBLIC_URL` (public base URL for `/install.sh` snippets)
- `VPSP_TRUST_PROXY` (`true`/`1` to trust `X-Forwarded-Proto`)
- `VPSP_DOWNLOADS_DIR` (default `./bin`)
- `VPSP_ADMIN_USER` / `VPSP_ADMIN_PASSWORD` (optional first-boot admin creation)
- `VPSP_TLS_CERT` / `VPSP_TLS_KEY` (optional direct TLS)

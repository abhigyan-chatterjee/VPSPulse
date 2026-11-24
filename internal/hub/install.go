package hub

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func (s *Server) handleInstallScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	baseURL := s.resolvePublicURL(r)
	script := fmt.Sprintf(`#!/usr/bin/env sh
set -eu

HUB_URL=%q
TOKEN=""
INTERVAL="10"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --url) HUB_URL="$2"; shift 2 ;;
    --token) TOKEN="$2"; shift 2 ;;
    --interval) INTERVAL="$2"; shift 2 ;;
    *) echo "Unknown flag: $1" >&2; exit 1 ;;
  esac
done

if [ -z "$TOKEN" ]; then
  echo "Error: --token is required" >&2
  exit 1
fi

ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64) GOARCH="amd64" ;;
  aarch64|arm64) GOARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

BIN_URL="${HUB_URL%%/}/downloads/vpspulse-agent-linux-${GOARCH}"
echo "Downloading vpspulse-agent (${GOARCH}) from ${BIN_URL}..."
curl -fsSL "$BIN_URL" -o /usr/local/bin/vpspulse-agent
chmod 0755 /usr/local/bin/vpspulse-agent

if ! id -u vpspulse >/dev/null 2>&1; then
  useradd --system --no-create-home --shell /usr/sbin/nologin vpspulse
fi

if getent group docker >/dev/null 2>&1; then
  usermod -aG docker vpspulse || true
fi

mkdir -p /etc/vpspulse
cat <<EOF > /etc/vpspulse/agent.env
VPSP_HUB_URL=${HUB_URL}
VPSP_AGENT_TOKEN=${TOKEN}
VPSP_INTERVAL=${INTERVAL}
EOF
chown root:vpspulse /etc/vpspulse/agent.env
chmod 0640 /etc/vpspulse/agent.env

cat <<EOF > /etc/systemd/system/vpspulse-agent.service
[Unit]
Description=VPSPulse Monitoring Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=vpspulse
EnvironmentFile=/etc/vpspulse/agent.env
ExecStart=/usr/local/bin/vpspulse-agent
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now vpspulse-agent
echo "vpspulse-agent installed and running."
`, baseURL)
	_, _ = w.Write([]byte(script))
}

func (s *Server) handleDownloads(w http.ResponseWriter, r *http.Request) {
	name := filepath.Base(r.PathValue("file"))
	if name == "." || name == "/" || strings.Contains(name, "..") {
		http.NotFound(w, r)
		return
	}
	full := filepath.Join(s.cfg.DownloadsDir, name)
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, full)
}

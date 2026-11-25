package hub

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var allowedDownloadRe = regexp.MustCompile(`^vpspulse-agent-linux-(amd64|arm64)$`)

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (s *Server) handleInstallScript(w http.ResponseWriter, r *http.Request) {
	baseURL := s.resolvePublicURL(r)
	if baseURL == "" {
		writeError(w, http.StatusBadRequest, "invalid Host header; configure --public-url")
		return
	}

	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	script := fmt.Sprintf(`#!/usr/bin/env sh
set -eu

if [ "$(id -u)" -ne 0 ]; then
  echo "Error: installer must be run as root (use sudo)" >&2
  exit 1
fi

HUB_URL=%s
TOKEN=""
INTERVAL="10"
WITH_DOCKER="1"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --url)
      [ "$#" -ge 2 ] || { echo "Error: --url requires a value" >&2; exit 1; }
      HUB_URL="$2"; shift 2 ;;
    --token)
      [ "$#" -ge 2 ] || { echo "Error: --token requires a value" >&2; exit 1; }
      TOKEN="$2"; shift 2 ;;
    --interval)
      [ "$#" -ge 2 ] || { echo "Error: --interval requires a value" >&2; exit 1; }
      INTERVAL="$2"; shift 2 ;;
    --no-docker)
      WITH_DOCKER="0"; shift ;;
    *)
      echo "Unknown flag: $1" >&2; exit 1 ;;
  esac
done

case "$HUB_URL" in
  http://*|https://*) ;;
  *) echo "Error: --url must start with http:// or https://" >&2; exit 1 ;;
esac

case "$HUB_URL$TOKEN" in
  *"
"*|*"	"*)
    echo "Error: invalid control character in arguments" >&2
    exit 1
    ;;
esac

if [ -z "$TOKEN" ]; then
  echo "Error: --token is required" >&2
  exit 1
fi

case "$INTERVAL" in
  ''|*[!0-9]*) echo "Error: --interval must be an integer (5-300)" >&2; exit 1 ;;
esac
if [ "$INTERVAL" -lt 5 ] || [ "$INTERVAL" -gt 300 ]; then
  echo "Error: --interval must be between 5 and 300" >&2
  exit 1
fi

if [ ! -d /run/systemd/system ]; then
  echo "Error: systemd is required for automatic service setup" >&2
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
TMP_BIN=$(mktemp /usr/local/bin/.vpspulse-agent.XXXXXX)
trap 'rm -f "$TMP_BIN"' EXIT INT TERM
if command -v curl >/dev/null 2>&1; then
  curl -fsSL "$BIN_URL" -o "$TMP_BIN"
elif command -v wget >/dev/null 2>&1; then
  wget -qO "$TMP_BIN" "$BIN_URL"
else
  echo "Error: curl or wget is required" >&2
  exit 1
fi
chmod 0755 "$TMP_BIN"
mv -f "$TMP_BIN" /usr/local/bin/vpspulse-agent

NOLOGIN="/bin/false"
if [ -x /usr/sbin/nologin ]; then
  NOLOGIN="/usr/sbin/nologin"
elif [ -x /sbin/nologin ]; then
  NOLOGIN="/sbin/nologin"
fi

if ! getent group vpspulse >/dev/null 2>&1; then
  groupadd --system vpspulse 2>/dev/null || addgroup -S vpspulse 2>/dev/null || true
fi
if ! id -u vpspulse >/dev/null 2>&1; then
  useradd --system --gid vpspulse --no-create-home --shell "$NOLOGIN" vpspulse 2>/dev/null || \
    adduser -S -D -H -G vpspulse -s "$NOLOGIN" vpspulse
fi

if [ "$WITH_DOCKER" = "1" ] && getent group docker >/dev/null 2>&1; then
  usermod -aG docker vpspulse 2>/dev/null || true
fi

install -d -m 0750 -o root -g vpspulse /etc/vpspulse
install -m 0640 -o root -g vpspulse /dev/null /etc/vpspulse/agent.env
printf 'VPSP_HUB_URL=%%s\nVPSP_AGENT_TOKEN=%%s\nVPSP_INTERVAL=%%s\n' "$HUB_URL" "$TOKEN" "$INTERVAL" > /etc/vpspulse/agent.env

cat <<'EOF' > /etc/systemd/system/vpspulse-agent.service
[Unit]
Description=VPSPulse Monitoring Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=vpspulse
Group=vpspulse
EnvironmentFile=/etc/vpspulse/agent.env
ExecStart=/usr/local/bin/vpspulse-agent
Restart=always
RestartSec=5
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=read-only
PrivateTmp=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
CapabilityBoundingSet=
AmbientCapabilities=
RestrictNamespaces=true
LockPersonality=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable vpspulse-agent
systemctl restart vpspulse-agent
echo "vpspulse-agent installed and running."
`, shellSingleQuote(baseURL))
	_, _ = w.Write([]byte(script))
}

func (s *Server) handleDownloads(w http.ResponseWriter, r *http.Request) {
	name := filepath.Base(r.PathValue("file"))
	if !allowedDownloadRe.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	full := filepath.Join(s.cfg.DownloadsDir, name)
	info, err := os.Lstat(full)
	if err != nil || info.IsDir() || (info.Mode()&os.ModeSymlink != 0) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	http.ServeFile(w, r, full)
}

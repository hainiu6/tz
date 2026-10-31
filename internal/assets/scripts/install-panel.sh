#!/usr/bin/env bash
set -euo pipefail

PORT=""
ADMIN_TOKEN=""
PANEL_URL="${TZ_PANEL_URL:-}"
BUNDLE_DIR="${TZ_BUNDLE_DIR:-}"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --port) PORT="$2"; shift 2 ;;
    --token) ADMIN_TOKEN="$2"; shift 2 ;;
    --url) PANEL_URL="$2"; shift 2 ;;
    --bundle-dir) BUNDLE_DIR="$2"; shift 2 ;;
    *) echo "未知参数: $1" >&2; exit 1 ;;
  esac
done

if [[ $EUID -ne 0 ]]; then
  echo "请使用 root 用户运行，或在命令前加 sudo。" >&2
  exit 1
fi
if [[ -z "$PORT" && -f /etc/tz-panel.env ]]; then
  PORT="$(sed -n 's/^TZ_LISTEN=:\([0-9]*\)$/\1/p' /etc/tz-panel.env | head -n1)"
fi
PORT="${PORT:-876}"
if ! [[ "$PORT" =~ ^[0-9]+$ ]] || (( PORT < 1 || PORT > 65535 )); then
  echo "端口必须是 1-65535 之间的数字。" >&2
  exit 1
fi
KEEP_TOKEN="false"
if [[ -z "$ADMIN_TOKEN" && -f /etc/tz-panel.env ]]; then
  KEEP_TOKEN="true"
elif [[ -z "$ADMIN_TOKEN" ]]; then
  ADMIN_TOKEN="tz-$(od -An -N18 -tx1 /dev/urandom | tr -d ' \n')"
fi

case "$(uname -m)" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "暂不支持的架构: $(uname -m)" >&2; exit 1 ;;
esac

for tool in curl sha256sum systemctl install; do
  command -v "$tool" >/dev/null || { echo "缺少必要命令: $tool" >&2; exit 1; }
done
if [[ -z "$BUNDLE_DIR" && -n "${BASH_SOURCE[0]:-}" ]]; then
  SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
  if [[ -f "$SCRIPT_DIR/tz-panel-linux-$ARCH" ]]; then BUNDLE_DIR="$SCRIPT_DIR"; fi
fi
RELEASE_BASE="${TZ_RELEASE_BASE:-}"
if [[ -z "$RELEASE_BASE" && -n "$PANEL_URL" ]]; then RELEASE_BASE="${PANEL_URL%/}/downloads"; fi
if [[ -z "$BUNDLE_DIR" && -z "$RELEASE_BASE" ]]; then
  echo "请解压离线安装包后运行本脚本，或通过 --url 指定已有面板地址；不会从 GitHub 自动下载。" >&2
  exit 1
fi
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT
fetch_package() {
  if [[ -n "$BUNDLE_DIR" ]]; then
    cp -- "$BUNDLE_DIR/$1" "$TMP_DIR/$1"
  else
    curl -fL --retry 3 --connect-timeout 15 --max-time 180 "${RELEASE_BASE%/}/$1" -o "$TMP_DIR/$1"
  fi
}
fetch_package checksums.txt
ARTIFACTS="tz-agent-linux-amd64 tz-agent-linux-arm64 tz-agent-linux-armv7 tz-panel-linux-amd64 tz-panel-linux-arm64"
for asset in $ARTIFACTS; do
  fetch_package "$asset"
  expected="$(awk -v asset="$asset" '$2 == asset {print $1}' "$TMP_DIR/checksums.txt")"
  if ! [[ "$expected" =~ ^[a-fA-F0-9]{64}$ ]]; then echo "安装包缺少有效校验值: $asset" >&2; exit 1; fi
  printf '%s  %s\n' "$expected" "$TMP_DIR/$asset" | sha256sum -c -
done
install -m 0755 "$TMP_DIR/tz-panel-linux-$ARCH" /usr/local/bin/.tz-panel.new
mv -f /usr/local/bin/.tz-panel.new /usr/local/bin/tz-panel

if ! id tz-panel >/dev/null 2>&1; then
  useradd --system --home /var/lib/tz --shell /usr/sbin/nologin tz-panel
fi
install -d -o tz-panel -g tz-panel -m 0750 /var/lib/tz
install -d -o tz-panel -g tz-panel -m 0755 /var/lib/tz/downloads
for asset in $ARTIFACTS; do
  install -o tz-panel -g tz-panel -m 0644 "$TMP_DIR/$asset" "/var/lib/tz/downloads/.$asset.new"
  mv -f "/var/lib/tz/downloads/.$asset.new" "/var/lib/tz/downloads/$asset"
done
if [[ "$KEEP_TOKEN" == "true" ]]; then
  sed -i "s/^TZ_LISTEN=.*/TZ_LISTEN=:${PORT}/" /etc/tz-panel.env
else
  ESCAPED_TOKEN="${ADMIN_TOKEN//\\/\\\\}"
  ESCAPED_TOKEN="${ESCAPED_TOKEN//\"/\\\"}"
  cat >/etc/tz-panel.env <<EOF
TZ_ADMIN_TOKEN="${ESCAPED_TOKEN}"
TZ_LISTEN=:${PORT}
TZ_DATA=/var/lib/tz/data.json
TZ_ENV_FILE=/etc/tz-panel.env
EOF
fi
if ! grep -q '^TZ_ENV_FILE=' /etc/tz-panel.env; then
  printf '%s\n' 'TZ_ENV_FILE=/etc/tz-panel.env' >>/etc/tz-panel.env
fi
if grep -q '^TZ_DOWNLOAD_DIR=' /etc/tz-panel.env; then
  sed -i 's|^TZ_DOWNLOAD_DIR=.*|TZ_DOWNLOAD_DIR=/var/lib/tz/downloads|' /etc/tz-panel.env
else
  printf '%s\n' 'TZ_DOWNLOAD_DIR=/var/lib/tz/downloads' >> /etc/tz-panel.env
fi
chown tz-panel:tz-panel /etc/tz-panel.env
chmod 0600 /etc/tz-panel.env

cat >/etc/systemd/system/tz-panel.service <<'EOF'
[Unit]
Description=TZ Linux Monitoring Panel
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=tz-panel
Group=tz-panel
EnvironmentFile=/etc/tz-panel.env
ExecStart=/usr/local/bin/tz-panel
Restart=always
RestartSec=5
NoNewPrivileges=true
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/var/lib/tz /etc/tz-panel.env

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable tz-panel
systemctl restart tz-panel

SERVER_IP="$(hostname -I 2>/dev/null | awk '{print $1}')"
if [[ -z "$SERVER_IP" ]]; then SERVER_IP="服务器IP"; fi
echo
echo "TZ Panel 安装完成"
echo "访问地址: http://${SERVER_IP}:${PORT}"
echo "查看当前管理令牌: grep '^TZ_ADMIN_TOKEN=' /etc/tz-panel.env"
echo
echo "请保存管理令牌，并在防火墙中放行 TCP ${PORT} 端口。"

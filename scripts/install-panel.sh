#!/usr/bin/env bash
set -euo pipefail

PORT=""
ADMIN_TOKEN=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --port) PORT="$2"; shift 2 ;;
    --token) ADMIN_TOKEN="$2"; shift 2 ;;
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

RELEASE_BASE="${TZ_RELEASE_BASE:-https://github.com/hainiu6/tz/releases/latest/download}"
TMP_FILE="$(mktemp)"
trap 'rm -f "$TMP_FILE"' EXIT
echo "正在下载 TZ Panel (${ARCH})..."
curl -fL --retry 3 "${RELEASE_BASE}/tz-panel-linux-${ARCH}" -o "$TMP_FILE"
install -m 0755 "$TMP_FILE" /usr/local/bin/.tz-panel.new
mv -f /usr/local/bin/.tz-panel.new /usr/local/bin/tz-panel

if ! id tz-panel >/dev/null 2>&1; then
  useradd --system --home /var/lib/tz --shell /usr/sbin/nologin tz-panel
fi
install -d -o tz-panel -g tz-panel -m 0750 /var/lib/tz
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

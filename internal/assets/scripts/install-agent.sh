#!/usr/bin/env bash
set -euo pipefail

PANEL_URL=""
AGENT_TOKEN=""
UPGRADE="false"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --url) PANEL_URL="$2"; shift 2 ;;
    --token) AGENT_TOKEN="$2"; shift 2 ;;
    --upgrade) UPGRADE="true"; shift ;;
    *) echo "未知参数: $1" >&2; exit 1 ;;
  esac
done

if [[ -z "$PANEL_URL" || -z "$AGENT_TOKEN" ]]; then
  echo "用法: install-agent.sh --url https://panel.example.com --token TOKEN" >&2
  exit 1
fi
if [[ $EUID -ne 0 ]]; then
  echo "请使用 root 用户运行，或在命令前加 sudo" >&2
  exit 1
fi
for tool in curl sha256sum systemctl install; do
  command -v "$tool" >/dev/null || { echo "缺少必要命令: $tool" >&2; exit 1; }
done

case "$(uname -m)" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  armv7l) ARCH="armv7" ;;
  *) echo "暂不支持的架构: $(uname -m)" >&2; exit 1 ;;
esac

RELEASE_BASE="${TZ_RELEASE_BASE:-${PANEL_URL%/}/downloads}"
TMP_DIR="$(mktemp -d)"
TMP_FILE="$TMP_DIR/agent"
trap 'rm -rf "$TMP_DIR"' EXIT
echo "正在下载 TZ Agent (${ARCH})..."
ASSET="tz-agent-linux-${ARCH}"
curl -fL --retry 3 --connect-timeout 15 --max-time 180 "${RELEASE_BASE%/}/$ASSET" -o "$TMP_FILE"
curl -fsSL --retry 3 --connect-timeout 15 --max-time 30 "${RELEASE_BASE%/}/checksums.txt" -o "$TMP_DIR/checksums.txt"
EXPECTED="$(awk -v asset="$ASSET" '$2 == asset {print $1}' "$TMP_DIR/checksums.txt")"
if ! [[ "$EXPECTED" =~ ^[a-fA-F0-9]{64}$ ]]; then
  echo "面板未提供有效的探针校验值，请先升级面板。" >&2; exit 1
fi
printf '%s  %s\n' "$EXPECTED" "$TMP_FILE" | sha256sum -c -
chmod 0755 "$TMP_FILE"
install -m 0755 "$TMP_FILE" /usr/local/bin/.tz-agent.new
mv -f /usr/local/bin/.tz-agent.new /usr/local/bin/tz-agent

NODE_ID=""
if [[ -f /etc/tz-agent.env ]]; then
  NODE_ID="$(sed -n 's/^TZ_NODE_ID=//p' /etc/tz-agent.env | head -n1)"
fi
if [[ -z "$NODE_ID" ]]; then
  NODE_ID="$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')"
fi
NODE_NAME="$(hostname)"

cat >/etc/tz-agent.env <<EOF
TZ_PANEL_URL=${PANEL_URL}
TZ_AGENT_TOKEN=${AGENT_TOKEN}
TZ_NODE_ID=${NODE_ID}
TZ_NODE_NAME=${NODE_NAME}
EOF
chmod 0600 /etc/tz-agent.env
cat >/etc/systemd/system/tz-agent.service <<'EOF'
[Unit]
Description=TZ Linux Monitoring Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=/etc/tz-agent.env
ExecStart=/usr/local/bin/tz-agent
Restart=always
RestartSec=5
NoNewPrivileges=true
ProtectHome=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable tz-agent
systemctl restart tz-agent
if [[ "$UPGRADE" == "true" ]]; then echo "TZ Agent 已升级并重启。"; else echo "TZ Agent 安装完成。"; fi

#!/usr/bin/env bash
set -euo pipefail

if [[ $EUID -ne 0 ]]; then
  echo "请使用 root 用户运行，或在命令前加 sudo" >&2
  exit 1
fi

if command -v systemctl >/dev/null 2>&1; then
  systemctl disable --now tz-agent 2>/dev/null || true
  rm -f /etc/systemd/system/tz-agent.service
  systemctl daemon-reload
  systemctl reset-failed tz-agent 2>/dev/null || true
fi
rm -f /usr/local/bin/tz-agent /etc/tz-agent.env
echo "TZ Agent 已卸载。面板中的离线记录可手动删除。"

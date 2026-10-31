#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
VERSION="${VERSION:-dev}"
mkdir -p internal/downloads/agents
cp scripts/install-panel.sh internal/assets/scripts/install-panel.sh
for arch in amd64 arm64 arm; do
  suffix="$arch"; goarm=""
  if [ "$arch" = arm ]; then suffix=armv7; goarm=7; fi
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" GOARM="$goarm" go build -trimpath \
    -ldflags="-s -w -X main.version=$VERSION" \
    -o "internal/downloads/agents/tz-agent-linux-$suffix" ./cmd/agent
done

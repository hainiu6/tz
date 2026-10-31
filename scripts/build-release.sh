#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
VERSION="${VERSION:-dev}"
export VERSION
sh scripts/build-agents.sh
mkdir -p dist
cp internal/downloads/agents/tz-agent-linux-* dist/
for arch in amd64 arm64; do
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath \
    -ldflags="-s -w -X main.version=$VERSION" -o "dist/tz-panel-linux-$arch" ./cmd/server
done
cp scripts/install-panel.sh dist/install-panel.sh
if command -v sha256sum >/dev/null 2>&1; then
  (cd dist && sha256sum tz-agent-linux-* tz-panel-linux-* install-panel.sh > checksums.txt)
else
  (cd dist && shasum -a 256 tz-agent-linux-* tz-panel-linux-* install-panel.sh > checksums.txt)
fi
COPYFILE_DISABLE=1 tar -czf dist/tz-offline-linux.tar.gz -C dist checksums.txt install-panel.sh \
  tz-agent-linux-amd64 tz-agent-linux-arm64 tz-agent-linux-armv7 \
  tz-panel-linux-amd64 tz-panel-linux-arm64

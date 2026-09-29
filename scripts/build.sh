#!/usr/bin/env bash
# Build the Windows programs (works on macOS, Linux and Windows with Git Bash).
# Output: dist/windows/*.exe
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION="${VERSION:-1.0.0}"
OUT=dist/windows
mkdir -p "$OUT"
export GOOS=windows GOARCH=amd64 CGO_ENABLED=0
LD="-s -w -X nextgaming/internal/server.Version=$VERSION -X nextgaming/internal/agent.Version=$VERSION"
go build -trimpath -ldflags "$LD" -o "$OUT/nextgaming-server.exe" ./cmd/nextgaming-server
go build -trimpath -ldflags "$LD" -o "$OUT/nextgaming-agent.exe"  ./cmd/nextgaming-agent
# -H windowsgui: the lock screen has no console window.
go build -trimpath -ldflags "$LD -H windowsgui" -o "$OUT/nextgaming-client.exe" ./cmd/nextgaming-client
cp scripts/firewall-server.ps1 scripts/harden-gamer-account.ps1 scripts/install-agent.cmd "$OUT/"
echo "Built $VERSION:"
ls -la "$OUT"

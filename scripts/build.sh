#!/usr/bin/env bash
# Builds the dashboard, embeds it, and builds bin/prox.
set -euo pipefail
cd "$(dirname "$0")/.."

# Go's linker can't read the macOS 27 SDK's stubs yet, so prefer the 26.5 SDK.
sdk=/Library/Developer/CommandLineTools/SDKs/MacOSX26.5.sdk
if [[ -z "${SDKROOT:-}" && -d "$sdk" ]]; then
  export SDKROOT="$sdk"
fi

(cd dashboard && bun install --frozen-lockfile && bun run build)

mkdir -p bin
version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)
commit=$(git rev-parse --short HEAD 2>/dev/null || echo none)
built=$(date -u +%Y-%m-%dT%H:%M:%SZ)
go build -ldflags "-X main.Version=$version -X main.Commit=$commit -X main.BuildDate=$built" -o bin/prox ./cmd/server

echo "Built bin/prox"

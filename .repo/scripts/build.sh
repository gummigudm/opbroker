#!/usr/bin/env bash
# Build the opbroker binary.
#
# Env vars (all optional):
#   VERSION   Version string baked into the binary via -ldflags (default: dev)
#   GOOS      Target OS (default: current)
#   GOARCH    Target arch (default: current)
#   OUT       Output path (default: dist/opbroker)
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"

: "${VERSION:=dev}"
: "${OUT:=dist/opbroker}"

mkdir -p "$(dirname "$OUT")"

LDFLAGS="-X 'github.com/gummigudm/opbroker/internal/version.Version=${VERSION}'"

echo "building: $OUT (version=$VERSION, GOOS=${GOOS:-$(go env GOOS)}, GOARCH=${GOARCH:-$(go env GOARCH)})"
go build -ldflags "$LDFLAGS" -o "$OUT" ./cmd/opbroker
echo "built: $OUT"

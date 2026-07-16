#!/usr/bin/env bash
# Universal test entry point for opbroker.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

echo "==> go vet"
go vet ./...

echo "==> go test"
go test ./...

if command -v golangci-lint >/dev/null 2>&1; then
  echo "==> golangci-lint"
  golangci-lint run --config .repo/.golangci.yml ./...
else
  echo "==> golangci-lint (skipped: not installed)"
fi

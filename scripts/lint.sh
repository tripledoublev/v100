#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION_FILE="$ROOT_DIR/.golangci-version"

if [[ ! -f "$VERSION_FILE" ]]; then
  echo "missing $VERSION_FILE" >&2
  exit 1
fi

VERSION="$(tr -d '[:space:]' < "$VERSION_FILE")"
if [[ -z "$VERSION" ]]; then
  echo "empty golangci-lint version in $VERSION_FILE" >&2
  exit 1
fi

export GOCACHE="${GOCACHE:-$ROOT_DIR/.gocache}"
export GOMODCACHE="${GOMODCACHE:-$ROOT_DIR/.gomodcache}"
export GOWORK=off
TARGETS=(./cmd/... ./internal/...)

if command -v golangci-lint >/dev/null 2>&1; then
  INSTALLED="$(golangci-lint version 2>/dev/null | awk '{print $4}')"
  if [[ "$INSTALLED" == "$VERSION" ]]; then
    exec golangci-lint run --timeout=5m "$@" "${TARGETS[@]}"
  fi
fi

exec go run "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${VERSION}" run --timeout=5m "$@" "${TARGETS[@]}"

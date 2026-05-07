#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

export GOCACHE="${GOCACHE:-$ROOT_DIR/.gocache}"
export GOMODCACHE="${GOMODCACHE:-$ROOT_DIR/.gomodcache}"
export GOWORK=off

cd "$ROOT_DIR"

read -r -a TARGETS <<< "${TEST_TARGETS:-./cmd/... ./internal/...}"

go test -race -coverprofile=coverage.out "$@" "${TARGETS[@]}"

#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE_TAG="${1:-v100-sandbox:local}"

exec docker build \
  -t "$IMAGE_TAG" \
  -f "$ROOT_DIR/docker/v100-sandbox.Dockerfile" \
  "$ROOT_DIR"

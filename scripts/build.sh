#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BINARY="${ROOT_DIR}/v100"

cd "${ROOT_DIR}"

echo "building ${BINARY}"
go build -o "${BINARY}" ./cmd/v100

if [ "${V100_SKIP_INSTALL:-}" = "1" ]; then
    echo "built ${BINARY}"
    exit 0
fi

echo "updating shell v100 link"
"${BINARY}" install

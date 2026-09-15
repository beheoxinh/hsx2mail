#!/bin/bash
# Run Email Hub in development mode (Vite hot-reload + Wails dev).
# Wraps `make dev` — OAuth ldflags loaded from .env/.env.local.
set -euo pipefail
cd "$(dirname "$0")"

if ! command -v wails >/dev/null 2>&1; then
  echo "wails CLI not found. Install it first:" >&2
  echo "  go install github.com/wailsapp/wails/v2/cmd/wails@v2.12.0" >&2
  echo "  export PATH=\"\$PATH:\$(go env GOPATH)/bin\"" >&2
  exit 1
fi

if [[ ! -d frontend/node_modules ]]; then
  echo "Frontend deps missing — installing..."
  (cd frontend && npm install --no-audit --no-fund)
fi

make dev

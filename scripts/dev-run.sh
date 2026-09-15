#!/bin/bash
# Dev run config: rebuild frontend (so go:embed picks up Svelte changes)
# then start the app in debug mode. Equivalent to `make dev` without the
# Wails dev-server overhead.
set -euo pipefail
cd "$(dirname "$0")"
(cd frontend && npm run build)
exec go run -tags webkit2_41,linux,production . --debug
#!/bin/bash
# Debug run for Hsx2Mail in GoLand.
# Wails requires the `production` tag (app_production.go) — without it the
# default stub returns "Wails applications will not build without the
# correct build tags." at runtime.
#   go run -tags webkit2_41,linux,production . --debug
set -euo pipefail
cd "$(dirname "$0")"
exec go run -tags webkit2_41,linux,production . --debug

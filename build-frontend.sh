#!/bin/bash
# Rebuild frontend so the go:embed dist picks up Svelte changes.
set -euo pipefail
cd "$(dirname "$0")"
cd frontend
npm run build
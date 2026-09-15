#!/bin/bash
# Build Email Hub for the current platform.
# Wraps `make build` (Wails build + OAuth ldflags loaded from .env/.env.local).
set -euo pipefail
cd "$(dirname "$0")"

make build

echo ""
echo "✅ Build complete."
case "$(uname -s)" in
  Darwin) echo "   App bundle: $(pwd)/build/bin/Hsx2Mail.app" ;;
  Linux)  echo "   Binary:     $(pwd)/build/bin/hsx2mail" ;;
  MINGW*|MSYS*|CYGWIN*) echo "   Binary:     $(pwd)/build/bin/hsx2mail.exe" ;;
esac

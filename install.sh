#!/bin/bash
# Build and install Email Hub on Linux (default prefix /usr/local).
# Installs binary + icon + desktop entry, mirroring `make install-linux`
# but WITHOUT the `build` dependency — so wails never runs as root (which
# would leave root-owned files in frontend/dist + frontend/wailsjs).
# Override the prefix with PREFIX=/usr ./install.sh if desired.
set -euo pipefail
cd "$(dirname "$0")"

if [[ "$(uname -s)" != "Linux" ]]; then
  echo "install.sh targets Linux only. Use the packaged installer for your OS." >&2
  exit 1
fi

PREFIX="${PREFIX:-/usr/local}"

if [[ ! -x build/bin/hsx2mail ]]; then
  echo "Binary not found — building first..."
  make build
fi

echo "Installing Email Hub to $PREFIX (sudo required)..."
sudo install -Dm755 build/bin/hsx2mail "$PREFIX/bin/hsx2mail"
sudo install -Dm644 build/appicon.png "$PREFIX/share/icons/hicolor/256x256/apps/io.github.beheoxinh.Hsx2Mail.png"
sudo install -Dm644 build/linux/hsx2mail.desktop "$PREFIX/share/applications/io.github.beheoxinh.Hsx2Mail.desktop"

echo "Updating icon cache + desktop database..."
sudo gtk-update-icon-cache -f -t "$PREFIX/share/icons/hicolor" 2>/dev/null || true
sudo update-desktop-database "$PREFIX/share/applications" 2>/dev/null || true

echo ""
echo "✅ Install complete."
echo "   Run 'hsx2mail' to start, or set it as default mail client:"
echo "   xdg-mime default io.github.beheoxinh.Hsx2Mail.desktop x-scheme-handler/mailto"

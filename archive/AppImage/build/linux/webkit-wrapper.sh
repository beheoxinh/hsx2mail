#!/bin/sh
# Wrapper script for webkit helper processes
# Ensures they can find bundled libraries

# Find the AppImage mount point by walking up from this script's location
SELF=$(readlink -f "$0")
HERE=${SELF%/*}

# Walk up to find the AppImage root (contains usr/bin, usr/lib, etc)
while [ "$HERE" != "/" ]; do
    if [ -d "$HERE/usr/lib" ] && [ -d "$HERE/usr/bin" ]; then
        APPDIR="$HERE"
        break
    fi
    HERE=$(dirname "$HERE")
done

if [ -z "$APPDIR" ]; then
    echo "ERROR: Could not find AppImage root directory" >&2
    exit 1
fi

# Detect architecture
ARCH=$(uname -m)
case "$ARCH" in
    x86_64)
        LIB_ARCH="x86_64-linux-gnu"
        ;;
    aarch64)
        LIB_ARCH="aarch64-linux-gnu"
        ;;
esac

# Set library paths
if [ -n "$LIB_ARCH" ]; then
    export LD_LIBRARY_PATH="${APPDIR}/usr/lib/${LIB_ARCH}:${APPDIR}/usr/lib:${LD_LIBRARY_PATH}"
else
    export LD_LIBRARY_PATH="${APPDIR}/usr/lib:${LD_LIBRARY_PATH}"
fi

# Get the binary name from script name
BINARY_NAME=$(basename "$0")

# Execute the actual binary
exec "${SELF}.bin" "$@"

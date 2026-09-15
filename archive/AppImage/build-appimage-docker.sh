#!/bin/bash
# Build Aerion AppImage using Docker with Ubuntu 22.04

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUTPUT_DIR="${SCRIPT_DIR}/dist"

echo "Building Aerion AppImage in Ubuntu 22.04 container..."
echo "Output directory: ${OUTPUT_DIR}"

# Create output directory
mkdir -p "${OUTPUT_DIR}"

# Build Docker image
echo "Building Docker image..."
docker build -f Dockerfile.ubuntu22.04 -t aerion-builder:ubuntu22.04 .

# Run build in container
echo "Running build..."
docker run --rm \
    -v "${SCRIPT_DIR}:/build" \
    -v "${OUTPUT_DIR}:/output" \
    aerion-builder:ubuntu22.04

echo "Build complete! AppImage is in ${OUTPUT_DIR}"
ls -lh "${OUTPUT_DIR}"/Aerion-*.AppImage

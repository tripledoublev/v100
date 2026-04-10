#!/usr/bin/env bash
set -euo pipefail

BINARY_PATH=$1
TARGET=$2

# Only build AppImage for linux/amd64
if [[ "$TARGET" != "linux_amd64" ]]; then
    echo "Skipping AppImage build for $TARGET"
    exit 0
fi

echo "Building AppImage for $TARGET from $BINARY_PATH..."

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ASSETS_DIR="$ROOT_DIR/assets"
DIST_DIR="$ROOT_DIR/dist"
APPDIR="$DIST_DIR/AppDir"

mkdir -p "$DIST_DIR"
rm -rf "$APPDIR"
mkdir -p "$APPDIR"

# Download linuxdeploy if not present
LINUXDEPLOY="$ROOT_DIR/linuxdeploy-x86_64.AppImage"
if [[ ! -f "$LINUXDEPLOY" ]]; then
    echo "Downloading linuxdeploy..."
    curl -fsSL https://github.com/linuxdeploy/linuxdeploy/releases/download/continuous/linuxdeploy-x86_64.AppImage -o "$LINUXDEPLOY"
    chmod +x "$LINUXDEPLOY"
fi

# Set environment variables for linuxdeploy
export OUTPUT="v100-x86_64.AppImage"
export VERSION="${VERSION:-v0.0.0}" # Fallback if not set by GoReleaser

# Run linuxdeploy
# --executable: path to the binary
# --appdir: target AppDir path
# --output: output format (appimage)
# --desktop-file: path to the .desktop file
# --icon-file: path to the icon file
"$LINUXDEPLOY" \
    --executable="$BINARY_PATH" \
    --appdir="$APPDIR" \
    --output=appimage \
    --desktop-file="$ASSETS_DIR/v100.desktop" \
    --icon-file="$ASSETS_DIR/v100.png"

# Move the resulting AppImage to the dist folder
# linuxdeploy names it based on the name in .desktop and ARCH
mv v100-x86_64.AppImage "$DIST_DIR/"
echo "AppImage built successfully: $DIST_DIR/v100-x86_64.AppImage"

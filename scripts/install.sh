#!/bin/bash
# v100 Installation Script
# Run: curl -fsSL https://raw.githubusercontent.com/tripledoublev/v100/main/scripts/install.sh | bash
# Or with version: curl -fsSL https://raw.githubusercontent.com/tripledoublev/v100/main/scripts/install.sh | bash -s -- v0.1.0

set -e

REPO="tripledoublev/v100"
INSTALL_DIR="${HOME}/.local/bin"
BINARY="v100"
TMP_DIR="$(mktemp -d)"

cleanup() {
    rm -rf "$TMP_DIR"
}
trap cleanup EXIT

# Parse args
VERSION="${1:-latest}"
if [ "$VERSION" = "latest" ]; then
    VERSION=$(curl -s "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')
fi

echo "Installing v100 ${VERSION}..."

# Detect OS and architecture
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
    x86_64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

case "$OS" in
    linux) FILENAME="v100-linux-${ARCH}" ;;
    darwin) FILENAME="v100-darwin-${ARCH}" ;;
    mingw*|msys*|cygwin*)
        OS="windows"
        case "$ARCH" in
            amd64) FILENAME="v100-windows-amd64.exe" ;;
            arm64) FILENAME="v100-windows-arm64.exe" ;;
            *) echo "Unsupported Windows architecture: $ARCH"; exit 1 ;;
        esac
        ;;
    *) echo "Unsupported OS: $OS"; exit 1 ;;
esac

URL="https://github.com/${REPO}/releases/download/${VERSION}/${FILENAME}"
CHECKSUMS_URL="https://github.com/${REPO}/releases/download/${VERSION}/checksums.txt"

# Create install dir
mkdir -p "$INSTALL_DIR"

# Download
echo "Downloading ${FILENAME}..."
curl -fsSL "$URL" -o "${TMP_DIR}/${FILENAME}" || {
    echo "Failed to download v100. Release may not be available for ${OS}/${ARCH} yet."
    exit 1
}

echo "Verifying checksum..."
curl -fsSL "$CHECKSUMS_URL" -o "${TMP_DIR}/checksums.txt"
CHECKSUM_LINE=$(awk -v file="$FILENAME" '$2 == file { print; exit }' "${TMP_DIR}/checksums.txt")
if [ -z "$CHECKSUM_LINE" ]; then
    echo "Failed to find checksum for ${FILENAME}"
    exit 1
fi
printf '%s\n' "$CHECKSUM_LINE" | (cd "${TMP_DIR}" && sha256sum -c -)

cp "${TMP_DIR}/${FILENAME}" "${INSTALL_DIR}/${BINARY}"

chmod +x "${INSTALL_DIR}/${BINARY}"

# Add to PATH
SHELL_RC="${HOME}/.bashrc"
if [ -f "${HOME}/.zshrc" ]; then SHELL_RC="${HOME}/.zshrc"; fi
if [ -f "${HOME}/.profile" ]; then SHELL_RC="${HOME}/.profile"; fi

if ! grep -q "${INSTALL_DIR}" "$SHELL_RC" 2>/dev/null; then
    echo "export PATH=\"\$PATH:${INSTALL_DIR}\"" >> "$SHELL_RC"
    echo "Added ${INSTALL_DIR} to PATH in ${SHELL_RC}"
fi

echo ""
echo "✅ v100 installed to ${INSTALL_DIR}/${BINARY}"
echo ""
echo "Run 'v100 --help' to get started!"

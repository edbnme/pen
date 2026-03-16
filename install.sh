#!/bin/sh
# PEN Installer — https://github.com/edbnme/pen
# Usage: curl -fsSL https://raw.githubusercontent.com/edbnme/pen/main/install.sh | sh
set -e

REPO="edbnme/pen"
INSTALL_DIR="/usr/local/bin"
BINARY="pen"

# ── Colors ───────────────────────────────────────────────────────────────────
CYAN='\033[0;36m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
RED='\033[0;31m'
DIM='\033[0;90m'
BOLD='\033[1m'
RESET='\033[0m'

error() {
    printf "  ${RED}✗ Error:${RESET} %s\n" "$1" >&2
    exit 1
}

# ── Banner ───────────────────────────────────────────────────────────────────
printf "${CYAN}${BOLD}"
cat << 'EOF'

  ██████╗ ███████╗███╗   ██╗
  ██╔══██╗██╔════╝████╗  ██║
  ██████╔╝█████╗  ██╔██╗ ██║
  ██╔═══╝ ██╔══╝  ██║╚██╗██║
  ██║     ███████╗██║ ╚████║
  ╚═╝     ╚══════╝╚═╝  ╚═══╝
EOF
printf "${RESET}"
echo ""
printf "  ${BOLD}AI-Powered Browser Performance Engineering${RESET}\n"
echo ""
printf "  ${DIM}────────────────────────────────────────────────────${RESET}\n"
echo ""

# ── Detect platform ─────────────────────────────────────────────────────────
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
    x86_64|amd64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) error "Unsupported architecture: $ARCH" ;;
esac

case "$OS" in
    linux|darwin) ;;
    mingw*|msys*|cygwin*)
        error "For Windows, use PowerShell:\n  irm https://raw.githubusercontent.com/$REPO/main/install.ps1 | iex" ;;
    *) error "Unsupported OS: $OS" ;;
esac

printf "  ${GREEN}✓${RESET} Platform: ${BOLD}%s/%s${RESET}\n" "$OS" "$ARCH"

# ── Fetch latest version ────────────────────────────────────────────────────
printf "  ${DIM}Fetching latest version...${RESET} "

if command -v curl > /dev/null 2>&1; then
    VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name"' | sed -E 's/.*"v([^"]+)".*/\1/')
elif command -v wget > /dev/null 2>&1; then
    VERSION=$(wget -qO- "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name"' | sed -E 's/.*"v([^"]+)".*/\1/')
else
    error "curl or wget is required"
fi

if [ -z "$VERSION" ]; then
    error "Could not determine latest version. Check https://github.com/$REPO/releases"
fi

printf "${GREEN}v%s${RESET}\n" "$VERSION"

# ── Download ─────────────────────────────────────────────────────────────────
FILENAME="${BINARY}_${VERSION}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/$REPO/releases/download/v${VERSION}/${FILENAME}"

printf "  ${DIM}Downloading${RESET} ${CYAN}%s${RESET}${DIM}...${RESET} " "$FILENAME"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

if command -v curl > /dev/null 2>&1; then
    curl -fsSL "$URL" -o "$TMP/$FILENAME" 2>/dev/null
elif command -v wget > /dev/null 2>&1; then
    wget -q "$URL" -O "$TMP/$FILENAME" 2>/dev/null
fi

if [ ! -f "$TMP/$FILENAME" ]; then
    error "Download failed. Check https://github.com/$REPO/releases/tag/v$VERSION"
fi

printf "${GREEN}done${RESET}\n"

# ── Verify checksum ─────────────────────────────────────────────────────────
printf "  ${DIM}Verifying checksum...${RESET} "

CHECKSUMS_URL="https://github.com/$REPO/releases/download/v${VERSION}/checksums.txt"
CHECKSUM_OK=false

if command -v curl > /dev/null 2>&1; then
    curl -fsSL "$CHECKSUMS_URL" -o "$TMP/checksums.txt" 2>/dev/null || true
elif command -v wget > /dev/null 2>&1; then
    wget -q "$CHECKSUMS_URL" -O "$TMP/checksums.txt" 2>/dev/null || true
fi

if [ -f "$TMP/checksums.txt" ]; then
    EXPECTED=$(grep "$FILENAME" "$TMP/checksums.txt" | awk '{print $1}')

    if command -v sha256sum > /dev/null 2>&1; then
        ACTUAL=$(sha256sum "$TMP/$FILENAME" | awk '{print $1}')
    elif command -v shasum > /dev/null 2>&1; then
        ACTUAL=$(shasum -a 256 "$TMP/$FILENAME" | awk '{print $1}')
    else
        ACTUAL=""
    fi

    if [ -n "$ACTUAL" ] && [ -n "$EXPECTED" ]; then
        if [ "$ACTUAL" = "$EXPECTED" ]; then
            CHECKSUM_OK=true
            printf "${GREEN}verified${RESET}\n"
        else
            error "Checksum mismatch!\n  Expected: $EXPECTED\n  Got:      $ACTUAL"
        fi
    fi
fi

if [ "$CHECKSUM_OK" = false ]; then
    printf "${YELLOW}skipped${RESET}\n"
fi

# ── Install ──────────────────────────────────────────────────────────────────
# Try user-local directory first, fall back to system directory with sudo.
if [ ! -w "$INSTALL_DIR" ] && [ "$(id -u)" != "0" ]; then
    LOCAL_BIN="$HOME/.local/bin"
    if [ -d "$LOCAL_BIN" ] || mkdir -p "$LOCAL_BIN" 2>/dev/null; then
        INSTALL_DIR="$LOCAL_BIN"
    fi
fi

printf "  ${DIM}Installing to${RESET} ${CYAN}%s${RESET}${DIM}...${RESET} " "$INSTALL_DIR"

tar xzf "$TMP/$FILENAME" -C "$TMP" "$BINARY" 2>/dev/null || tar xzf "$TMP/$FILENAME" -C "$TMP"

if [ -w "$INSTALL_DIR" ]; then
    mv "$TMP/$BINARY" "$INSTALL_DIR/$BINARY"
else
    sudo mv "$TMP/$BINARY" "$INSTALL_DIR/$BINARY"
fi

chmod +x "$INSTALL_DIR/$BINARY"
printf "${GREEN}done${RESET}\n"

echo ""
printf "  ${DIM}────────────────────────────────────────────────────${RESET}\n"
echo ""

# ── Verify ───────────────────────────────────────────────────────────────────
INSTALLED_VERSION=$("$INSTALL_DIR/$BINARY" --version 2>&1 || true)
printf "  ${GREEN}✓${RESET} Installed: ${BOLD}%s${RESET}\n" "$INSTALLED_VERSION"
echo ""

# ── Check PATH ───────────────────────────────────────────────────────────────
case ":$PATH:" in
    *":$INSTALL_DIR:"*) ;;
    *)
        printf "  ${YELLOW}!${RESET} %s is not in your PATH.\n" "$INSTALL_DIR"
        printf "  ${DIM}Add it with:${RESET}\n"
        echo ""
        SHELL_NAME=$(basename "$SHELL" 2>/dev/null || echo "sh")
        case "$SHELL_NAME" in
            zsh)  printf "  ${CYAN}echo 'export PATH=\"%s:\$PATH\"' >> ~/.zshrc && source ~/.zshrc${RESET}\n" "$INSTALL_DIR" ;;
            fish) printf "  ${CYAN}fish_add_path %s${RESET}\n" "$INSTALL_DIR" ;;
            *)    printf "  ${CYAN}echo 'export PATH=\"%s:\$PATH\"' >> ~/.bashrc && source ~/.bashrc${RESET}\n" "$INSTALL_DIR" ;;
        esac
        echo ""
        ;;
esac

# ── Next step ────────────────────────────────────────────────────────────────
printf "  Run ${CYAN}${BOLD}pen init${RESET} to set up your IDE and browser.\n"
echo ""

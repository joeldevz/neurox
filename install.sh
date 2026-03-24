#!/usr/bin/env bash
set -euo pipefail

# Neurox installer
# Downloads the prebuilt binary for the current platform from GitHub Releases.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/joeldevz/neurox/main/install.sh | bash
#   ./install.sh [--version v0.1.5] [--install-dir ~/.local/bin]

REPO="joeldevz/neurox"
BINARY="neurox"
DEFAULT_INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
VERSION="${NEUROX_VERSION:-latest}"

# ── Parse flags ───────────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)     VERSION="$2"; shift 2 ;;
    --install-dir) DEFAULT_INSTALL_DIR="$2"; shift 2 ;;
    *) shift ;;
  esac
done

# ── Detect platform ───────────────────────────────────────────────────────────
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64)   ARCH="amd64" ;;
  aarch64|arm64)  ARCH="arm64" ;;
  *)
    printf "Unsupported architecture: %s\n" "$ARCH" >&2; exit 1 ;;
esac
case "$OS" in
  linux|darwin) ;;
  *)
    printf "Unsupported OS: %s\n" "$OS" >&2; exit 1 ;;
esac

# ── Resolve latest version tag ────────────────────────────────────────────────
if [[ "$VERSION" == "latest" ]]; then
  printf "Fetching latest version...\n"
  VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name"' | head -1 \
    | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')"
  [[ -z "$VERSION" ]] && { printf "Could not resolve latest version.\n" >&2; exit 1; }
fi

VERSION_NUM="${VERSION#v}"
printf "Installing neurox %s (%s/%s)...\n" "$VERSION" "$OS" "$ARCH"

# ── Download prebuilt binary ──────────────────────────────────────────────────
TARBALL="neurox_${VERSION_NUM}_${OS}_${ARCH}.tar.gz"
DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${VERSION}/${TARBALL}"

TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

printf "Downloading %s...\n" "$DOWNLOAD_URL"
if ! curl -fsSL "$DOWNLOAD_URL" -o "$TMPDIR/$TARBALL"; then
  printf "Download failed: %s\n" "$DOWNLOAD_URL" >&2
  printf "Check available releases at https://github.com/%s/releases\n" "$REPO" >&2
  exit 1
fi

tar -xzf "$TMPDIR/$TARBALL" -C "$TMPDIR"

# The binary inside the tarball is named neurox_<os>_<arch>
EXTRACTED="$(find "$TMPDIR" -maxdepth 1 -type f -not -name "*.tar.gz" | head -1)"
[[ -z "$EXTRACTED" ]] && { printf "Could not find binary in tarball.\n" >&2; exit 1; }

# ── Install ───────────────────────────────────────────────────────────────────
mkdir -p "$DEFAULT_INSTALL_DIR"
install -m 755 "$EXTRACTED" "$DEFAULT_INSTALL_DIR/$BINARY"

# ── Ensure install dir is in PATH ─────────────────────────────────────────────
if [[ ":$PATH:" != *":$DEFAULT_INSTALL_DIR:"* ]]; then
  SHELL_RC=""
  if   [[ -f "$HOME/.zshrc" ]];          then SHELL_RC="$HOME/.zshrc"
  elif [[ -f "$HOME/.bashrc" ]];         then SHELL_RC="$HOME/.bashrc"
  elif [[ -f "$HOME/.bash_profile" ]];   then SHELL_RC="$HOME/.bash_profile"
  fi
  if [[ -n "$SHELL_RC" ]]; then
    printf '\nexport PATH="%s:$PATH"\n' "$DEFAULT_INSTALL_DIR" >> "$SHELL_RC"
    printf "Added %s to PATH in %s\nRun: source %s\n" \
      "$DEFAULT_INSTALL_DIR" "$SHELL_RC" "$SHELL_RC"
  fi
fi

printf "\n✓ neurox %s installed to %s/%s\n" "$VERSION" "$DEFAULT_INSTALL_DIR" "$BINARY"
printf "\nRun the interactive setup to configure your AI clients:\n"
printf "  neurox install\n\n"

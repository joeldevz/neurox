#!/usr/bin/env bash
set -euo pipefail

# Neurox installer
# Always installs to ~/.local/bin/neurox (or --install-dir).
# Downloads prebuilt binary — no Go or gcc required.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/joeldevz/neurox/main/install.sh | bash
#   ./install.sh [--version v0.1.9] [--install-dir /usr/local/bin]

REPO="joeldevz/neurox"
BINARY="neurox"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
VERSION="${NEUROX_VERSION:-latest}"

# ── Parse flags ───────────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)     VERSION="$2"; shift 2 ;;
    --install-dir) INSTALL_DIR="$2"; shift 2 ;;
    *) shift ;;
  esac
done

# ── Detect platform ───────────────────────────────────────────────────────────
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64)  ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) printf "Unsupported architecture: %s\n" "$ARCH" >&2; exit 1 ;;
esac
case "$OS" in
  linux|darwin) ;;
  *) printf "Unsupported OS: %s\n" "$OS" >&2; exit 1 ;;
esac

# ── Resolve latest version ────────────────────────────────────────────────────
if [[ "$VERSION" == "latest" ]]; then
  printf "Fetching latest version...\n"
  VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name"' | head -1 \
    | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')"
  [[ -z "$VERSION" ]] && { printf "Could not resolve latest version.\n" >&2; exit 1; }
fi

VERSION_NUM="${VERSION#v}"
printf "Installing neurox %s (%s/%s) → %s\n" "$VERSION" "$OS" "$ARCH" "$INSTALL_DIR"

# ── Download ──────────────────────────────────────────────────────────────────
TARBALL="neurox_${VERSION_NUM}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${TARBALL}"
TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

printf "Downloading %s...\n" "$URL"
if ! curl -fsSL "$URL" -o "$TMPDIR/$TARBALL"; then
  printf "Download failed: %s\n" "$URL" >&2; exit 1
fi
tar -xzf "$TMPDIR/$TARBALL" -C "$TMPDIR"
EXTRACTED="$(find "$TMPDIR" -maxdepth 1 -type f -not -name "*.tar.gz" | head -1)"
[[ -z "$EXTRACTED" ]] && { printf "Binary not found in tarball.\n" >&2; exit 1; }

# ── Install to INSTALL_DIR ────────────────────────────────────────────────────
mkdir -p "$INSTALL_DIR"
install -m 755 "$EXTRACTED" "$INSTALL_DIR/$BINARY"

# ── Remove stale copies from other locations ──────────────────────────────────
# Avoids version conflicts when the same binary exists in multiple PATH dirs.
while IFS= read -r stale; do
  [[ -z "$stale" || "$stale" == "$INSTALL_DIR/$BINARY" ]] && continue
  if install -m 755 "$EXTRACTED" "$stale" 2>/dev/null; then
    printf "  Updated stale copy at %s\n" "$stale"
  fi
done < <(which -a "$BINARY" 2>/dev/null || true)

# ── Ensure INSTALL_DIR is in PATH ─────────────────────────────────────────────
if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
  for RC in "$HOME/.zshrc" "$HOME/.bashrc" "$HOME/.bash_profile"; do
    if [[ -f "$RC" ]]; then
      printf '\nexport PATH="%s:$PATH"\n' "$INSTALL_DIR" >> "$RC"
      printf "Added %s to PATH in %s\n" "$INSTALL_DIR" "$RC"
      printf "Run: source %s\n" "$RC"
      break
    fi
  done
fi

# ── Done ──────────────────────────────────────────────────────────────────────
printf "\n✓ neurox %s installed to %s/%s\n" "$VERSION" "$INSTALL_DIR" "$BINARY"
printf "\nNext: configure your AI clients\n"
printf "  neurox install\n\n"

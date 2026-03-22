#!/usr/bin/env bash
set -euo pipefail

NEUROX_DIR="$(cd "$(dirname "$0")" && pwd)"

printf "\nLaunching Neurox installer...\n\n"

if ! command -v go >/dev/null 2>&1; then
  printf "Go is required to run the installer. Install it from https://go.dev/dl/\n" >&2
  exit 1
fi

if ! command -v gcc >/dev/null 2>&1 && ! command -v cc >/dev/null 2>&1; then
  printf "A C compiler is required for SQLite CGO support.\n" >&2
  exit 1
fi

exec go run -tags fts5 "$NEUROX_DIR" install "$@"

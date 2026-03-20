#!/usr/bin/env bash
set -euo pipefail

# ─────────────────────────────────────────────────────────────────────────────
# Neurox Installer
# Interactive setup: build, install, configure providers, set up editors
# ─────────────────────────────────────────────────────────────────────────────

NEUROX_DIR="$(cd "$(dirname "$0")" && pwd)"
INSTALL_DIR="${HOME}/.local/bin"
BINARY_NAME="neurox"
BINARY_PATH="${INSTALL_DIR}/${BINARY_NAME}"
CONFIG_DIR="${HOME}/.config/neurox"
CONFIG_FILE="${CONFIG_DIR}/config.yaml"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
MAGENTA='\033[0;35m'
BOLD='\033[1m'
DIM='\033[2m'
NC='\033[0m'

info()    { echo -e "  ${BLUE}[info]${NC}  $*"; }
ok()      { echo -e "  ${GREEN}[ok]${NC}    $*"; }
warn()    { echo -e "  ${YELLOW}[warn]${NC}  $*"; }
err()     { echo -e "  ${RED}[err]${NC}   $*"; }
step()    { echo -e "\n${CYAN}${BOLD}  $*${NC}"; echo -e "  ${DIM}$(printf '%.0s─' {1..60})${NC}"; }
ask()     { echo -en "  ${MAGENTA}?${NC} $* "; }
confirm() { echo -en "  ${MAGENTA}?${NC} $* ${DIM}[Y/n]${NC} "; read -r ans; [[ -z "$ans" || "$ans" =~ ^[Yy] ]]; }

# ─────────────────────────────────────────────────────────────────────────────
# Banner
# ─────────────────────────────────────────────────────────────────────────────

echo ""
echo -e "${BOLD}${CYAN}"
cat << 'BANNER'
    _   __
   / | / /__  __  ___________  _  __
  /  |/ / _ \/ / / / ___/ __ \| |/_/
 / /|  /  __/ /_/ / /  / /_/ />  <
/_/ |_/\___/\__,_/_/   \____/_/|_|
BANNER
echo -e "${NC}"
echo -e "  ${DIM}Brain-inspired memory engine for AI coding agents${NC}"
echo -e "  ${DIM}v0.1.0${NC}"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
# Step 1: Prerequisites
# ─────────────────────────────────────────────────────────────────────────────

step "1/6  Checking prerequisites"

MISSING=0

if command -v go &>/dev/null; then
    GO_VERSION=$(go version | grep -oP '\d+\.\d+' | head -1)
    ok "Go ${GO_VERSION}"
else
    err "Go not installed — https://go.dev/dl/"
    MISSING=1
fi

if command -v gcc &>/dev/null || command -v cc &>/dev/null; then
    ok "C compiler (required for SQLite CGO)"
else
    err "C compiler not found — sudo apt install build-essential"
    MISSING=1
fi

if [ "${MISSING}" -eq 1 ]; then
    echo ""
    err "Install missing prerequisites and try again."
    exit 1
fi

# Check optional services
OLLAMA_AVAILABLE=0
OLLAMA_EMBED_MODEL=""
OLLAMA_LLM_MODEL=""

if curl -sf http://localhost:11434/api/tags &>/dev/null; then
    OLLAMA_AVAILABLE=1
    OLLAMA_MODELS=$(curl -sf http://localhost:11434/api/tags | python3 -c "
import json,sys
models = json.load(sys.stdin).get('models',[])
for m in models: print(m['name'])
" 2>/dev/null || true)

    # Detect embed model
    for model in $OLLAMA_MODELS; do
        case "$model" in
            nomic-embed-text*|mxbai-embed*|all-minilm*|bge-*|snowflake-arctic-embed*)
                OLLAMA_EMBED_MODEL="$model"
                break
                ;;
        esac
    done

    # Detect LLM model
    for model in $OLLAMA_MODELS; do
        case "$model" in
            nomic-embed-text*|mxbai-embed*|all-minilm*|bge-*|snowflake-arctic-embed*)
                # Skip embedding models
                ;;
            *)
                OLLAMA_LLM_MODEL="$model"
                break
                ;;
        esac
    done

    ok "Ollama running"
    [ -n "$OLLAMA_EMBED_MODEL" ] && info "  Embedding model: ${OLLAMA_EMBED_MODEL}"
    [ -n "$OLLAMA_LLM_MODEL" ] && info "  LLM model: ${OLLAMA_LLM_MODEL}"
    [ -z "$OLLAMA_EMBED_MODEL" ] && warn "  No embedding model found (install with: ollama pull nomic-embed-text)"
    [ -z "$OLLAMA_LLM_MODEL" ] && warn "  No LLM model found (install with: ollama pull qwen2.5:3b)"
else
    info "Ollama not running (optional — neurox works without it)"
fi

# ─────────────────────────────────────────────────────────────────────────────
# Step 2: Build
# ─────────────────────────────────────────────────────────────────────────────

step "2/6  Building Neurox"

cd "${NEUROX_DIR}"
info "Source: ${NEUROX_DIR}"

CGO_ENABLED=1 go build -tags fts5 -o "${BINARY_NAME}" .
ok "Built successfully"

FTS5_COUNT=$(strings "${BINARY_NAME}" | grep -c fts5 || true)
if [ "${FTS5_COUNT}" -ge 10 ]; then
    ok "FTS5 full-text search enabled"
else
    warn "FTS5 symbols low (${FTS5_COUNT}) — search may not work"
fi

# ─────────────────────────────────────────────────────────────────────────────
# Step 3: Install binary
# ─────────────────────────────────────────────────────────────────────────────

step "3/6  Installing binary"

mkdir -p "${INSTALL_DIR}"
cp "${NEUROX_DIR}/${BINARY_NAME}" "${BINARY_PATH}"
chmod +x "${BINARY_PATH}"
ok "Installed to ${BINARY_PATH}"

# Add to PATH if needed
if ! echo "${PATH}" | tr ':' '\n' | grep -qx "${INSTALL_DIR}"; then
    SHELL_RC=""
    if [ -f "${HOME}/.zshrc" ]; then
        SHELL_RC="${HOME}/.zshrc"
    elif [ -f "${HOME}/.bashrc" ]; then
        SHELL_RC="${HOME}/.bashrc"
    fi

    if [ -n "${SHELL_RC}" ]; then
        if ! grep -q "/.local/bin" "${SHELL_RC}" 2>/dev/null; then
            echo "" >> "${SHELL_RC}"
            echo "# Neurox" >> "${SHELL_RC}"
            echo 'export PATH="${HOME}/.local/bin:${PATH}"' >> "${SHELL_RC}"
            ok "Added ~/.local/bin to PATH in ${SHELL_RC}"
        fi
    fi
fi

# ─────────────────────────────────────────────────────────────────────────────
# Step 4: Configure Neurox
# ─────────────────────────────────────────────────────────────────────────────

step "4/6  Configuring Neurox"

mkdir -p "${CONFIG_DIR}"
ok "Config directory: ${CONFIG_DIR}"

if [ -f "${CONFIG_FILE}" ]; then
    info "Config file already exists: ${CONFIG_FILE}"
    if confirm "Overwrite config?"; then
        WRITE_CONFIG=1
    else
        WRITE_CONFIG=0
    fi
else
    WRITE_CONFIG=1
fi

if [ "${WRITE_CONFIG}" -eq 1 ]; then
    echo ""
    echo -e "  ${BOLD}Provider setup${NC}"
    echo -e "  ${DIM}Neurox works without any providers (FTS5 only).${NC}"
    echo -e "  ${DIM}Providers unlock: hybrid search, quality gate, fact extraction, reflection.${NC}"
    echo ""

    # --- Embeddings ---
    EMBED_PROVIDER=""
    EMBED_REMOTE_URL=""
    EMBED_REMOTE_KEY=""
    EMBED_REMOTE_MODEL=""

    if [ "${OLLAMA_AVAILABLE}" -eq 1 ] && [ -n "${OLLAMA_EMBED_MODEL}" ]; then
        if confirm "Use Ollama for embeddings? (${OLLAMA_EMBED_MODEL})"; then
            EMBED_PROVIDER="ollama"
            ok "Embeddings: ollama/${OLLAMA_EMBED_MODEL}"
        fi
    fi

    if [ -z "${EMBED_PROVIDER}" ]; then
        if confirm "Configure a remote embedding API? (OpenAI-compatible)"; then
            EMBED_PROVIDER="remote"
            ask "Embeddings API URL (e.g. https://api.openai.com/v1):"; read -r EMBED_REMOTE_URL
            ask "API Key:"; read -rs EMBED_REMOTE_KEY; echo ""
            ask "Model name (e.g. text-embedding-3-small):"; read -r EMBED_REMOTE_MODEL
            ok "Embeddings: remote/${EMBED_REMOTE_MODEL}"
        else
            EMBED_PROVIDER=""
            info "Embeddings: disabled (FTS5-only search)"
        fi
    fi

    # --- LLM ---
    LLM_PROVIDER=""
    LLM_GATE_MODE="auto"
    LLM_OLLAMA_MODEL=""
    LLM_REMOTE_URL=""
    LLM_REMOTE_KEY=""
    LLM_REMOTE_MODEL=""

    if [ "${OLLAMA_AVAILABLE}" -eq 1 ] && [ -n "${OLLAMA_LLM_MODEL}" ]; then
        if confirm "Use Ollama for LLM? (${OLLAMA_LLM_MODEL})"; then
            LLM_PROVIDER="ollama"
            LLM_OLLAMA_MODEL="${OLLAMA_LLM_MODEL}"
            ok "LLM: ollama/${OLLAMA_LLM_MODEL}"
        fi
    fi

    if [ -z "${LLM_PROVIDER}" ]; then
        if confirm "Configure a remote LLM API? (OpenAI-compatible)"; then
            LLM_PROVIDER="remote"
            ask "LLM API URL (e.g. https://api.openai.com/v1):"; read -r LLM_REMOTE_URL
            ask "API Key:"; read -rs LLM_REMOTE_KEY; echo ""
            ask "Model name (e.g. gpt-4o-mini):"; read -r LLM_REMOTE_MODEL
            ok "LLM: remote/${LLM_REMOTE_MODEL}"
        else
            LLM_PROVIDER=""
            info "LLM: disabled (heuristic-only consolidation)"
        fi
    fi

    if [ -n "${LLM_PROVIDER}" ]; then
        echo ""
        echo -e "  ${BOLD}Quality gate mode${NC}"
        echo -e "  ${DIM}Controls how aggressively the LLM filters observations:${NC}"
        echo -e "  ${DIM}  auto — LLM decides on uncertain cases (recommended)${NC}"
        echo -e "  ${DIM}  full — LLM evaluates everything${NC}"
        echo -e "  ${DIM}  off  — pure heuristic, no LLM filtering${NC}"
        ask "Gate mode [auto/full/off] (default: auto):"; read -r LLM_GATE_MODE
        LLM_GATE_MODE="${LLM_GATE_MODE:-auto}"
    fi

    # --- Write config ---
    cat > "${CONFIG_FILE}" << YAML
# Neurox configuration
# Documentation: https://github.com/umibu/neurox#configuration

database:
  path: ${CONFIG_DIR}/neurox.db

llm:
  provider: "${LLM_PROVIDER}"          # "ollama", "remote", "" (auto-detect)
  gate_mode: "${LLM_GATE_MODE}"        # "auto", "full", "off"
  ollama_url: ""                        # default: http://localhost:11434
  ollama_model: "${LLM_OLLAMA_MODEL}"   # default: qwen2.5:3b
  remote_url: "${LLM_REMOTE_URL}"
  remote_api_key: "${LLM_REMOTE_KEY}"
  remote_model: "${LLM_REMOTE_MODEL}"

embeddings:
  provider: "${EMBED_PROVIDER}"         # "ollama", "remote", "" (auto-detect)
  remote_url: "${EMBED_REMOTE_URL}"
  remote_api_key: "${EMBED_REMOTE_KEY}"
  remote_model: "${EMBED_REMOTE_MODEL}"
YAML

    ok "Config written to ${CONFIG_FILE}"
fi

# ─────────────────────────────────────────────────────────────────────────────
# Step 5: Configure editors
# ─────────────────────────────────────────────────────────────────────────────

step "5/6  Configuring editors"

CONFIGURED_EDITORS=""

# --- Claude Code ---
if [ -d "${HOME}/.claude" ]; then
    CLAUDE_SETTINGS="${HOME}/.claude/settings.json"

    if confirm "Configure Claude Code?"; then
        if [ -f "${CLAUDE_SETTINGS}" ]; then
            TEMP=$(mktemp)
            python3 -c "
import json
with open('${CLAUDE_SETTINGS}') as f:
    cfg = json.load(f)
if 'mcpServers' not in cfg:
    cfg['mcpServers'] = {}
cfg['mcpServers']['neurox'] = {
    'command': '${BINARY_PATH}',
    'args': ['mcp']
}
with open('${TEMP}', 'w') as f:
    json.dump(cfg, f, indent=2)
" && mv "${TEMP}" "${CLAUDE_SETTINGS}" || rm -f "${TEMP}"
        else
            mkdir -p "$(dirname "${CLAUDE_SETTINGS}")"
            cat > "${CLAUDE_SETTINGS}" << JSONEOF
{
  "mcpServers": {
    "neurox": {
      "command": "${BINARY_PATH}",
      "args": ["mcp"]
    }
  }
}
JSONEOF
        fi
        ok "Claude Code configured"
        CONFIGURED_EDITORS="${CONFIGURED_EDITORS}claude "
    fi
fi

# --- OpenCode ---
OPENCODE_CONFIG="${HOME}/.config/opencode/opencode.json"

if [ -f "${OPENCODE_CONFIG}" ] || [ -d "${HOME}/.config/opencode" ]; then
    if confirm "Configure OpenCode?"; then
        if [ -f "${OPENCODE_CONFIG}" ]; then
            TEMP=$(mktemp)
            python3 -c "
import json
with open('${OPENCODE_CONFIG}') as f:
    cfg = json.load(f)
if 'mcp' not in cfg:
    cfg['mcp'] = {}
cfg['mcp']['neurox'] = {
    'type': 'local',
    'command': ['${BINARY_PATH}', 'mcp'],
    'enabled': True
}
with open('${TEMP}', 'w') as f:
    json.dump(cfg, f, indent=2)
" && mv "${TEMP}" "${OPENCODE_CONFIG}" || rm -f "${TEMP}"
        else
            mkdir -p "$(dirname "${OPENCODE_CONFIG}")"
            cat > "${OPENCODE_CONFIG}" << JSONEOF
{
  "mcp": {
    "neurox": {
      "type": "local",
      "command": ["${BINARY_PATH}", "mcp"],
      "enabled": true
    }
  }
}
JSONEOF
        fi
        ok "OpenCode configured"
        CONFIGURED_EDITORS="${CONFIGURED_EDITORS}opencode "
    fi
fi

# --- Cursor ---
if [ -d "${HOME}/.cursor" ]; then
    CURSOR_CONFIG="${HOME}/.cursor/mcp.json"

    if confirm "Configure Cursor?"; then
        if [ -f "${CURSOR_CONFIG}" ]; then
            TEMP=$(mktemp)
            python3 -c "
import json
with open('${CURSOR_CONFIG}') as f:
    cfg = json.load(f)
if 'mcpServers' not in cfg:
    cfg['mcpServers'] = {}
cfg['mcpServers']['neurox'] = {
    'command': '${BINARY_PATH}',
    'args': ['mcp']
}
with open('${TEMP}', 'w') as f:
    json.dump(cfg, f, indent=2)
" && mv "${TEMP}" "${CURSOR_CONFIG}" || rm -f "${TEMP}"
        else
            cat > "${CURSOR_CONFIG}" << JSONEOF
{
  "mcpServers": {
    "neurox": {
      "command": "${BINARY_PATH}",
      "args": ["mcp"]
    }
  }
}
JSONEOF
        fi
        ok "Cursor configured"
        CONFIGURED_EDITORS="${CONFIGURED_EDITORS}cursor "
    fi
fi

if [ -z "${CONFIGURED_EDITORS}" ]; then
    info "No editors configured. You can add neurox manually later."
fi

# ─────────────────────────────────────────────────────────────────────────────
# Step 6: Git hook (optional)
# ─────────────────────────────────────────────────────────────────────────────

step "6/6  Git integration (optional)"

echo -e "  ${DIM}The git hook marks memories as stale when linked files change.${NC}"
echo -e "  ${DIM}This keeps your memory fresh across commits.${NC}"
echo ""

if confirm "Install git post-commit hook in this repo?"; then
    "${BINARY_PATH}" install-hook 2>/dev/null && ok "Git hook installed" || warn "Could not install hook (not a git repo?)"
else
    info "Skipped. Run 'neurox install-hook' in any repo later."
fi

# ─────────────────────────────────────────────────────────────────────────────
# Verify
# ─────────────────────────────────────────────────────────────────────────────

echo ""
echo -e "  ${DIM}Verifying...${NC}"
if "${BINARY_PATH}" version &>/dev/null; then
    ok "neurox binary works"
else
    warn "Could not run neurox"
fi

# ─────────────────────────────────────────────────────────────────────────────
# Summary
# ─────────────────────────────────────────────────────────────────────────────

echo ""
echo -e "${GREEN}${BOLD}"
cat << 'DONE'
  ═══════════════════════════════════════════════════════
                Neurox installed successfully!
  ═══════════════════════════════════════════════════════
DONE
echo -e "${NC}"

echo -e "  ${BOLD}Paths${NC}"
echo -e "    Binary:    ${BINARY_PATH}"
echo -e "    Config:    ${CONFIG_FILE}"
echo -e "    Database:  ${CONFIG_DIR}/neurox.db"
echo ""

echo -e "  ${BOLD}Providers${NC}"
if [ -n "${EMBED_PROVIDER:-}" ]; then
    echo -e "    Embeddings: ${GREEN}${EMBED_PROVIDER}${NC}"
else
    echo -e "    Embeddings: ${DIM}disabled${NC}"
fi
if [ -n "${LLM_PROVIDER:-}" ]; then
    echo -e "    LLM:        ${GREEN}${LLM_PROVIDER}${NC}"
else
    echo -e "    LLM:        ${DIM}disabled${NC}"
fi
echo ""

if [ -n "${CONFIGURED_EDITORS}" ]; then
    echo -e "  ${BOLD}Editors${NC}"
    for editor in ${CONFIGURED_EDITORS}; do
        echo -e "    ${GREEN}${editor}${NC}: restart to connect"
    done
    echo ""
fi

echo -e "  ${BOLD}Quick test${NC}"
echo "    neurox status"
echo "    neurox save \"test\" --content \"Hello from neurox\""
echo "    neurox recall \"test\""
echo ""

if ! echo "${PATH}" | tr ':' '\n' | grep -qx "${INSTALL_DIR}"; then
    echo -e "  ${YELLOW}Run this first:${NC}  source ~/.bashrc"
    echo ""
fi

# Plan: Frictionless Installation — `neurox setup <agent>` + Improved Install Output

## Goal

Make Neurox installation a 2-command experience: install the binary, then configure your agent. No TUI wizard, no questions. Like Engram's `engram setup opencode` pattern.

## Business Context

- **Problem**: Current flow requires `curl | bash` → `neurox install` (interactive TUI with 6 steps, provider questions, manual selection). Engram's flow is `brew install engram` → `engram setup claude` — zero friction.
- **Strategic driver**: Installation friction is the #1 reason developers abandon a tool in the first 5 minutes. Every question is a dropout point.
- **Non-goals**: Homebrew tap (separate task), CGO removal (separate task), TUI redesign. The existing `neurox install` TUI stays as an advanced option.

## Technical Context

### Current state

**install.sh**:
- Downloads binary to `~/.local/bin/neurox` ✅
- Adds to PATH in shell RC files ✅
- BUT: final output says `neurox install` (TUI wizard) — should say `neurox setup <agent>` 
- BUT: when PATH is added, it tells user to `source ~/.zshrc` but doesn't show the actual export command to run immediately

**install.ps1** (Windows):
- Same pattern, same issues with final output

**CLI**:
- No `neurox setup` command exists
- `neurox install` launches the full TUI wizard (1320 lines of Bubble Tea code)
- Agent config paths are already known in `internal/installer/installer.go` (`detectEnvironment`)

**Agent config patterns** (from installer.go + docs):
- Claude Code: `~/.claude.json` → add to `mcpServers`
- Claude Desktop: platform-specific path → add to `mcpServers`
- OpenCode: `~/.config/opencode/opencode.json` → add to `mcp`
- Cursor: `~/.cursor/mcp.json` → add to `mcpServers`
- Antigravity/Gemini: `~/.gemini/antigravity/mcp_config.json` → add to `mcpServers`
- VS Code: `.vscode/mcp.json` or user-level → add to `servers`

### Key files

- `install.sh` — bash installer output
- `install.ps1` — PowerShell installer output  
- `main.go` — CLI dispatch, needs `case "setup":`
- `internal/installer/` — environment detection, config paths already defined
- `internal/installer/setup.go` — NEW file for setup logic

## Implementation Steps

### Step 1: Create `neurox setup <agent>` command
- **What**: Add a new `neurox setup <agent>` CLI command that auto-configures a specific AI agent's MCP config in one shot. No questions, no TUI. Supported agents: `claude-code`, `claude-desktop`, `opencode`, `cursor`, `vscode`, `antigravity`. Each agent writes the correct JSON config file with `{"command": "neurox", "args": ["mcp"]}` in the right location.
- **Why**: This is the core UX improvement. One command per agent, zero questions.
- **Where**: 
  - `internal/installer/setup.go` — NEW file with `Setup(agent string) error` function
  - `main.go` — add `case "setup":` dispatch
- **Acceptance**:
  - `neurox setup claude-code` writes MCP config to `~/.claude.json`
  - `neurox setup opencode` writes to `~/.config/opencode/opencode.json`
  - `neurox setup cursor` writes to `~/.cursor/mcp.json`
  - `neurox setup vscode` writes to `.vscode/mcp.json` in current directory
  - `neurox setup antigravity` writes to `~/.gemini/antigravity/mcp_config.json`
  - `neurox setup claude-desktop` writes to platform-specific Claude Desktop config
  - Each command prints what file was written and what was added
  - If config file already exists, merges neurox into existing `mcpServers` (doesn't overwrite)
  - If neurox is already configured, prints "already configured" and exits cleanly
  - `neurox setup` (no agent) prints usage with all supported agents
  - `neurox setup --list` lists all supported agents
  - `go build -tags fts5 ./...` passes
- **Status**: [x] done

### Step 2: Improve install.sh post-install output
- **What**: After successful install, show clear next-step instructions with:
  1. If PATH was modified, show the exact `export PATH=...` command to run NOW (don't just say "source ~/.zshrc")
  2. Show `neurox setup <agent>` as the next step (not `neurox install`)
  3. List all available agents in a table
  4. Show a quick verification command: `neurox version`
- **Why**: Users need to know exactly what to type next. "source ~/.zshrc" is vague. `neurox install` launches a 6-step wizard when they just want to configure one agent.
- **Where**: `install.sh` (lines 78-93, the post-install output section)
- **Acceptance**:
  - When PATH is added, output shows: `Run this now:  export PATH="~/.local/bin:$PATH"`
  - Shows available agents table with `neurox setup <agent>` examples
  - Shows `neurox version` as verification step
  - Does NOT mention `neurox install` (the TUI wizard is for advanced users)
- **Status**: [x] done

### Step 3: Improve install.ps1 post-install output
- **What**: Same improvements as Step 2 but for the Windows PowerShell installer. Show the PATH refresh command for PowerShell, list agents, show `neurox setup <agent>`.
- **Why**: Windows users get the same clear experience.
- **Where**: `install.ps1` (lines 78-83, the post-install output section)
- **Acceptance**:
  - Shows PowerShell PATH refresh: `$env:PATH = [Environment]::GetEnvironmentVariable("PATH", "User") + ";" + $env:PATH`
  - Shows available agents with `neurox setup <agent>`
  - Shows `neurox version` for verification
- **Status**: [x] done

### Step 4: Update printUsage and README
- **What**: 
  1. Add `setup` to `printUsage()` in `main.go`: `  setup <agent>     Configure an AI agent (claude-code, opencode, cursor, vscode, antigravity)`
  2. Update `README.md` and `README.es.md` Quick Start sections to show the 2-command flow
  3. Update the CLI reference table to include `setup`
- **Why**: Docs must match the new simplified flow.
- **Where**: `main.go`, `README.md`, `README.es.md`
- **Acceptance**:
  - `neurox --help` shows `setup` command
  - README Quick Start shows: `curl ... | bash` → `neurox setup claude-code`
  - CLI reference table includes `setup` with description
  - Both English and Spanish READMEs updated
- **Status**: [x] done

## Verification

```bash
# Build
CGO_ENABLED=1 go build -tags fts5 ./...
CGO_ENABLED=1 go vet -tags fts5 ./...
CGO_ENABLED=1 go test -tags fts5 ./...

# Test setup command
./neurox setup                    # Should show usage with all agents
./neurox setup --list             # Should list agents
./neurox setup claude-code        # Should configure (or show "already configured")
./neurox setup opencode           # Should configure

# Verify install.sh output
bash install.sh --help 2>&1       # Check output format
```

## Sequencing & Dependencies

```
Step 1 (setup command) ──> Step 4 (docs update)  [Step 4 needs setup to exist]
Step 2 (install.sh)    ──> independent
Step 3 (install.ps1)   ──> independent
```

Parallel groups:
- Steps 1, 2, 3 can run in parallel
- Step 4 depends on Step 1

## Risks / Notes

- **JSON merge strategy**: When the agent's config file already exists (e.g., `~/.claude.json` has other MCP servers), we must READ → MERGE → WRITE. Never overwrite the entire file. Use `json.Unmarshal` → add neurox entry → `json.MarshalIndent` → write back.
- **VS Code workspace vs user**: `neurox setup vscode` writes to `.vscode/mcp.json` in the current directory (workspace-level). This is the most common pattern. User-level config varies by OS and is harder to detect.
- **Claude Code has its own plugin system**: `claude plugin marketplace add` is an alternative, but the JSON config approach works universally and doesn't require the plugin marketplace.
- **The existing `neurox install` TUI stays**: It's an advanced option for users who want to configure providers, embeddings, LLM gate, etc. The `setup` command only configures the MCP connection — it doesn't set up Ollama or LLM providers.

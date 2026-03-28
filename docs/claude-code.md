# Neurox for Claude Code

Claude Code is the primary supported client for Neurox. The `SKILL.md` file in this repo is specifically designed for Claude Code's skills system, teaching it when and how to use Neurox proactively.

---

## Install

**Option A — Install via Go (recommended):**

Requires **Go 1.26+**. No C compiler required.

```bash
go install github.com/joeldevz/neurox@main
```

The binary is placed in `$(go env GOPATH)/bin/neurox`. Make sure `$(go env GOPATH)/bin` is in your `PATH`.

**Option B — Build from source:**

```bash
git clone https://github.com/joeldevz/neurox.git
cd neurox
go build -o neurox .
sudo mv neurox /usr/local/bin/
```

**Verify the binary:**

```bash
neurox status
```

---

## Configure

Edit `~/.claude.json` and add the `neurox` entry under `mcpServers`:

```json
{
  "mcpServers": {
    "neurox": {
      "command": "neurox",
      "args": ["mcp"]
    }
  }
}
```

If `~/.claude.json` doesn't exist yet, create it with exactly that content.

If you already have other MCP servers configured, add `neurox` alongside them:

```json
{
  "mcpServers": {
    "existing-server": {
      "command": "...",
      "args": ["..."]
    },
    "neurox": {
      "command": "neurox",
      "args": ["mcp"]
    }
  }
}
```

---

## Skill

The Neurox skill (`skills/neurox/SKILL.md`) teaches Claude Code when and how to use all 13 Neurox tools proactively — calling `session_start`, `recall`, `save`, and `session_end` without being asked.

**Automatic installation:** When you run `neurox install` and select Claude Code integration, the skill is copied automatically to `~/.claude/skills/neurox/SKILL.md`.

**Manual installation via npx:**

```bash
npx skills add joeldevz/neurox
```

This clones the repo and installs the skill to `~/.claude/skills/neurox/SKILL.md`.

**Manual copy:**

```bash
cp skills/neurox/SKILL.md ~/.claude/skills/neurox/SKILL.md
```

---

## Verify

Ask Claude Code:

> "Run neurox status"

Claude Code should respond with something like:

```
Buffer: 12 observations
Working: 45 observations
Core: 8 observations
Total: 65 observations
...
```

If it works, Neurox is connected. From this point forward, Claude Code will automatically save decisions, recall context, and manage your project memory.

---

## Troubleshooting

**Binary not found (`neurox: command not found`):**

```bash
# Check where Go installs binaries
go env GOPATH

# Add to PATH in ~/.bashrc or ~/.zshrc
export PATH="$PATH:$(go env GOPATH)/bin"

# Or use the absolute path in the config
{
  "mcpServers": {
    "neurox": {
      "command": "/usr/local/bin/neurox",
      "args": ["mcp"]
    }
  }
}
```

**Tools not appearing in Claude Code:**

Restart Claude Code completely (quit and reopen). The MCP server is initialized on startup — changes to `~/.claude.json` require a restart to take effect.

# Neurox for OpenCode

OpenCode is an open-source terminal AI coding assistant. Neurox connects as an MCP server, giving the agent persistent memory across all your coding sessions.

---

## Install

**Option A — Install via Go (recommended):**

Requires **Go 1.26+**. No C compiler required.

```bash
go install github.com/joeldevz/neurox@main
```

The binary is placed in `$(go env GOPATH)/bin/neurox`. Make sure this directory is in your `PATH`.

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

Edit `~/.config/opencode/config.json` (create it if it doesn't exist):

```json
{
  "mcp": {
    "neurox": {
      "command": "neurox",
      "args": ["mcp"],
      "enabled": true
    }
  }
}
```

If neurox is not on your `PATH`, use the absolute path:

```json
{
  "mcp": {
    "neurox": {
      "command": "/usr/local/bin/neurox",
      "args": ["mcp"],
      "enabled": true
    }
  }
}
```

If you have an existing OpenCode config, add the `mcp` block alongside your other settings:

```json
{
  "model": "claude-sonnet-4-5",
  "mcp": {
    "neurox": {
      "command": "neurox",
      "args": ["mcp"],
      "enabled": true
    }
  }
}
```

---

## Verify

Start a new OpenCode session and type:

> "run neurox status"

OpenCode should call the `status` tool and display your brain stats:

```
Buffer: 12 observations
Working: 45 observations
Core: 8 observations
...
```

---

## Troubleshooting

**Binary not found:**

OpenCode may launch without your full user `PATH`. Use the absolute binary path in the config:

```bash
# Find where neurox is installed
which neurox
```

Replace the `command` value with the full path shown.

**Tools not appearing in OpenCode:**

Exit and restart OpenCode. MCP servers are initialized at startup and config changes only take effect after a full restart.

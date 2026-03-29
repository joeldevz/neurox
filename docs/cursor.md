# Neurox for Cursor

Cursor is an AI-powered code editor that supports MCP servers. Connecting Neurox gives the Cursor agent persistent memory for your projects.

---

## Install

**Option A — Install via Go (recommended):**

Requires **Go 1.26+** and a **C compiler** (CGO).

```bash
CGO_ENABLED=1 go install -tags sqlite_fts5 github.com/joeldevz/neurox@main
```

The binary is placed in `$(go env GOPATH)/bin/neurox`. Ensure this directory is in your `PATH`.

**Option B — Build from source:**

```bash
git clone https://github.com/joeldevz/neurox.git
cd neurox
CGO_ENABLED=1 go build -tags sqlite_fts5 -o neurox .
sudo mv neurox /usr/local/bin/
```

**Verify the binary:**

```bash
neurox status
```

---

## Configure

**Option A — Cursor Settings UI:**

1. Open Cursor
2. Go to **Cursor Settings → Features → MCP**
3. Click **Add Server**
4. Enter:
   - **Name**: `neurox`
   - **Command**: `neurox`
   - **Args**: `mcp`

**Option B — Edit the config file directly:**

Edit `~/.cursor/mcp.json` (create it if it doesn't exist):

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

If neurox is not on your `PATH`, use the absolute path:

```json
{
  "mcpServers": {
    "neurox": {
      "command": "/usr/local/bin/neurox",
      "args": ["mcp"]
    }
  }
}
```

**Option C — Per-project config:**

Create `.cursor/mcp.json` in your project root:

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

---

## Verify

1. Restart Cursor
2. Open the Cursor chat (Cmd/Ctrl+L)
3. Type: `"Use neurox to check status"`
4. Cursor should call the neurox `status` tool and display your brain statistics

---

## Troubleshooting

**Binary not found:**

Cursor may launch with a restricted `PATH` that doesn't include your Go bin directory. Use the absolute path in `mcp.json`:

```bash
# Find the full path
which neurox
```

Then update the `command` field to the full path shown.

**Tools not appearing in Cursor chat:**

Restart Cursor completely. MCP servers are loaded when Cursor starts, so config file changes require a full restart.

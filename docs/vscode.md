# Neurox for VS Code

VS Code supports MCP servers through GitHub Copilot Chat. Connecting Neurox gives Copilot persistent memory for your codebase.

---

## Install

**Option A — Install via Go (recommended):**

Requires **Go 1.26+** and a **C compiler** (CGO).

```bash
CGO_ENABLED=1 go install -tags sqlite_fts5 github.com/joeldevz/neurox@main
```

The binary is placed in `$(go env GOPATH)/bin/neurox`. Make sure that directory is in your `PATH`.

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

**Option A — User settings (applies to all workspaces):**

Open `settings.json` (Cmd/Ctrl+Shift+P → "Open User Settings JSON") and add:

```json
{
  "mcp": {
    "servers": {
      "neurox": {
        "command": "neurox",
        "args": ["mcp"]
      }
    }
  }
}
```

**Option B — Workspace config (project-specific):**

Create `.vscode/mcp.json` in your project root:

```json
{
  "servers": {
    "neurox": {
      "command": "neurox",
      "args": ["mcp"]
    }
  }
}
```

Note: the workspace file uses `servers` at the root (no `mcp` wrapper), while `settings.json` nests it under `"mcp"`.

If neurox is not on your VS Code `PATH`, use the absolute path:

```json
{
  "mcp": {
    "servers": {
      "neurox": {
        "command": "/usr/local/bin/neurox",
        "args": ["mcp"]
      }
    }
  }
}
```

---

## Verify

1. Open Copilot Chat (Cmd/Ctrl+Shift+I or click the Copilot icon)
2. Type: `"check neurox status"`
3. Copilot should call the neurox `status` tool and display your brain stats

---

## Troubleshooting

**Binary not found:**

VS Code launches with a controlled environment that may not include your Go bin directory. Use the absolute path:

```bash
# Find the full path
which neurox
# or
echo "$(go env GOPATH)/bin/neurox"
```

Use that path in the `command` field of your config.

**Tools not appearing in Copilot Chat:**

Reload the VS Code window (Cmd/Ctrl+Shift+P → "Developer: Reload Window"). MCP servers in VS Code are initialized per-window and a reload picks up config changes without a full restart.

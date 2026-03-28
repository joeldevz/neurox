# Neurox for Claude Desktop

Claude Desktop is the graphical desktop application from Anthropic. Neurox connects to it as an MCP server, giving Claude access to your persistent project memory.

---

## Install

**Option A — Install via Go (recommended):**

Requires **Go 1.26+**. No C compiler required.

```bash
go install github.com/joeldevz/neurox@main
```

The binary is placed in `$(go env GOPATH)/bin/neurox`. Make sure it's on your `PATH`.

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

Edit the Claude Desktop config file for your platform:

**macOS:**
```
~/Library/Application Support/Claude/claude_desktop_config.json
```

**Windows:**
```
%APPDATA%\Claude\claude_desktop_config.json
```

Add the following content (create the file if it doesn't exist):

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

If the binary is not on your system `PATH`, use the absolute path instead:

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

On Windows, use the full path with backslashes:

```json
{
  "mcpServers": {
    "neurox": {
      "command": "C:\\Users\\YourName\\go\\bin\\neurox.exe",
      "args": ["mcp"]
    }
  }
}
```

---

## Verify

1. **Restart Claude Desktop** completely (quit from the system tray, then reopen)
2. Start a new conversation
3. Click the **`+`** menu (attachment/tools button) — you should see **neurox** listed as a connected tool
4. Type: `"neurox status"` — Claude should respond with your brain stats

---

## Troubleshooting

**Binary not found:**

Claude Desktop launches with a minimal `PATH`. If `neurox` is not found, use its absolute path in the config:

```bash
# Find the binary location
which neurox
# or
echo "$(go env GOPATH)/bin/neurox"
```

Then paste that full path into the `command` field of your config.

**Tools not appearing in the `+` menu:**

Quit Claude Desktop fully (including from the system tray on Windows), then reopen it. MCP servers are only loaded on startup.

# Neurox Quickstart

Neurox is a brain-inspired memory engine for AI coding agents. It gives your AI assistant persistent, structured memory that works across sessions — remembering decisions, patterns, preferences, and discoveries so your agent doesn't start from scratch every time.

## 60-Second Path

```bash
# 1. Install
go install github.com/joeldevz/neurox@main

# 2. Configure your AI client
# Add neurox to your MCP config — see the client guides below

# 3. Verify
# Ask your AI: "Run neurox status" — it should return brain stats
```

No C compiler required — Neurox uses a pure Go SQLite driver. Prerequisites: **Go 1.26+**.

## Client Setup Guides

| AI Client | Setup Guide |
|-----------|-------------|
| Claude Code | [claude-code.md](claude-code.md) |
| Claude Desktop | [claude-desktop.md](claude-desktop.md) |
| Cursor | [cursor.md](cursor.md) |
| VS Code + Copilot | [vscode.md](vscode.md) |
| OpenCode | [opencode.md](opencode.md) |

## What Neurox Gives Your Agent

Once connected, Neurox teaches your AI agent to:

- **Remember** architecture decisions, bug fixes, and project patterns across sessions
- **Recall** relevant context before answering questions or making changes
- **Track** what files were modified and why (via git hook integration)
- **Build** a growing knowledge base that gets smarter the more you use it

## The Three Memory Layers

| Layer | What it stores | Decay rate |
|-------|---------------|------------|
| Buffer | New observations | Fast |
| Working | Validated, frequently accessed | Moderate |
| Core | Long-term proven knowledge | Slow |

## After Setup

Once your client is configured, your agent will automatically:

1. Call `session_start` when you open a project
2. Call `recall` before answering technical questions
3. Call `save` after important decisions, bug fixes, and discoveries
4. Call `session_end` when you finish working

No manual commands needed — Neurox works in the background.

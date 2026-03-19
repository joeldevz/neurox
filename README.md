# Neurox

Brain-inspired memory engine for AI coding agents. Three-layer memory model (Buffer → Working → Core) with hybrid search, Ebbinghaus decay, consolidation pipelines, reflection, and proactive recall.

## Features

- **Three-layer memory**: Buffer (short-term) → Working (medium-term) → Core (long-term) with automatic promotion
- **Hybrid search**: FTS5 keyword search + semantic embeddings with tri-factor scoring (recency × importance × relevance)
- **Ebbinghaus decay**: Memories naturally fade unless reinforced by access
- **Consolidation pipeline**: Background process handles promotion, dedup, contradiction detection, reflection, and eviction
- **LLM quality gate**: Intelligent promotion decisions with 3-strike retry system
- **Contradiction detection**: Automatically finds and resolves conflicting memories
- **Reflection**: Synthesizes multiple observations into high-level insights (Stanford Generative Agents)
- **Proactive recall**: Session-aware context that returns relevant memories without being asked
- **Session lifecycle**: Auto-extracts atomic observations from session summaries
- **Fact graph**: Knowledge triples (subject-predicate-object) with temporal validity and multi-hop traversal
- **Git-linked staleness**: Observations linked to files are marked stale when those files change
- **Topic key upsert**: Same topic_key = update in place, no duplicates
- **Write gate**: Cosine similarity dedup with LLM-assisted decisions for gray zone
- **Graceful degradation**: Works without Ollama/LLM (FTS-only, heuristic-only mode)

## Quick Start

```bash
# Build
go build -o neurox .

# Start MCP server (for AI agents)
./neurox mcp

# Start HTTP API server
./neurox serve

# CLI usage
./neurox save "JWT auth pattern" --content "Using JWT with RS256 for API auth" --type decision --tags "auth,jwt"
./neurox recall "authentication" --namespace myproject --limit 5
./neurox context --namespace myproject --files "src/auth.go"
./neurox status
```

## Architecture

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│   Buffer     │ ──► │   Working   │ ──► │    Core     │
│  (Layer 0)   │     │  (Layer 1)  │     │  (Layer 2)  │
│  Short-term  │     │ Medium-term │     │  Long-term  │
└─────────────┘     └─────────────┘     └─────────────┘
      │                    │                    │
      └────────────────────┴────────────────────┘
                           │
                  ┌────────┴────────┐
                  │  Consolidation  │
                  │    Pipeline     │
                  │  (30 min loop)  │
                  └────────┬────────┘
                           │
           ┌───────────────┼───────────────┐
           │               │               │
      ┌────┴────┐   ┌─────┴─────┐   ┌────┴────┐
      │  Decay  │   │  Quality  │   │ Reflect │
      │         │   │   Gate    │   │         │
      └─────────┘   └───────────┘   └─────────┘
```

**Consolidation pipeline** (runs every 30 minutes):
1. Ebbinghaus decay on all observations
2. Retry previously rejected observations (3-strike system)
3. Promote Buffer → Working (importance threshold + quality gate)
4. Promote Working → Core (access count + age)
5. Semantic dedup (cosine similarity ≥ 0.85)
6. Contradiction detection + resolution
7. Reflection (synthesize insights from Working observations)
8. Buffer overflow eviction
9. Garbage collection

## MCP Tools

| Tool | Description |
|------|-------------|
| `save` | Save an observation to memory (Buffer layer) |
| `recall` | Search memories with FTS5 + tri-factor scoring |
| `context` | Get relevant context (file-linked + high-activation + reflections) |
| `update` | Update an existing observation |
| `forget` | Soft-delete an observation |
| `invalidate` | Mark observation as incorrect, optionally provide replacement |
| `status` | Brain statistics (layer counts, providers, health) |
| `session_start` | Start work session, returns proactive context |
| `session_end` | End session, LLM extracts atomic observations from summary |
| `git_hook` | Report changed files, marks linked observations stale |
| `reflect` | Trigger reflection synthesis |

## CLI Commands

```
neurox mcp              Start MCP server over stdio
neurox serve            Start HTTP API server
neurox save             Save an observation
neurox recall           Search memories
neurox context          Get relevant context
neurox invalidate       Mark observation as incorrect
neurox status           Show brain statistics
neurox config           Show current configuration
neurox install-hook     Install git post-commit hook
neurox version          Show version
```

## Agent Setup

### Claude Code

Add to your MCP settings (`~/.claude/settings.json` or project `.mcp.json`):

```json
{
  "mcpServers": {
    "neurox": {
      "command": "/path/to/neurox",
      "args": ["mcp"]
    }
  }
}
```

### Cursor

Settings → MCP Servers → Add:
- Name: `neurox`
- Command: `/path/to/neurox mcp`
- Transport: `stdio`

### OpenCode

Add to `opencode.json`:
```json
{
  "mcp": {
    "neurox": {
      "command": "/path/to/neurox",
      "args": ["mcp"]
    }
  }
}
```

### Windsurf / Copilot

Use the HTTP API mode:
```bash
neurox serve  # Starts on port 7438
```

API endpoint: `http://localhost:7438/api/v1/`

## Configuration

Config file: `~/.config/neurox/config.yaml`

```yaml
database:
  path: ~/.config/neurox/neurox.db

llm:
  provider: ""          # "ollama", "remote", or "" (auto-detect)
  gate_mode: "auto"     # "auto", "full", "off"
  ollama_url: ""        # default: http://localhost:11434
  ollama_model: ""      # default: qwen2.5:3b
  remote_url: ""        # OpenAI-compatible API URL
  remote_api_key: ""
  remote_model: ""
```

Environment overrides:
- `NEUROX_DATABASE_PATH`
- `NEUROX_CONFIG_DIR`
- `NEUROX_LLM_PROVIDER`
- `NEUROX_LLM_GATE_MODE`
- `NEUROX_LLM_OLLAMA_URL`
- `NEUROX_LLM_OLLAMA_MODEL`
- `NEUROX_LLM_REMOTE_URL`
- `NEUROX_LLM_REMOTE_API_KEY`
- `NEUROX_LLM_REMOTE_MODEL`

## Observation Types

| Type | When to use |
|------|-------------|
| `decision` | Architectural or design decisions |
| `bugfix` | What broke and why |
| `discovery` | Learned something about the codebase |
| `pattern` | Recurring patterns or conventions |
| `gotcha` | Traps, pitfalls, edge cases |
| `config` | Environment details, tool configurations |
| `preference` | User preferences or corrections |
| `question` | Open questions for review |

## Performance

- `save`: <1ms (SQLite insert + FTS5)
- `recall` FTS: <5ms
- `context`: <10ms
- Binary size: ~15MB
- RAM: <150MB with 10k observations

## Stack

- **Go** — single binary, goroutines for background work
- **SQLite** (WAL mode, FTS5) — via `mattn/go-sqlite3` (CGO)
- **Embeddings** — Ollama (nomic-embed-text) or OpenAI-compatible API
- **LLM** — Ollama or OpenAI-compatible API (optional, for quality gate + reflection)
- **MCP** — `mark3labs/mcp-go`
- **IDs** — ULID (`oklog/ulid`)

## License

MIT

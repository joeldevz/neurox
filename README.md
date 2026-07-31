<p align="center">
  <h1 align="center">Neurox</h1>
  <p align="center">
    <strong>Persistent memory for AI coding agents</strong>
  </p>
  <p align="center">
    One Go binary &bull; One SQLite file &bull; Native performance
  </p>
  <p align="center">
    <a href="#quick-start">Quick Start</a> &bull;
    <a href="#how-it-works">How It Works</a> &bull;
    <a href="#benchmark-results">98% Recall</a> &bull;
    <a href="#documentation">Docs</a> &bull;
    <a href="README.es.md">Leer en Espanol</a>
  </p>
</p>

---

Your AI coding agent forgets everything between sessions. Every conversation starts from scratch — no memory of the architecture decisions you made last week, the bug you fixed yesterday, or your preference for tabs over spaces.

Neurox gives your agent persistent, structured memory.

```bash
brew install joeldevz/tap/neurox           # macOS / Linux
neurox setup claude-code    # or: opencode, cursor, vscode, antigravity, claude-desktop
```

That's it. No Node.js, no Python, no Docker. One binary, one SQLite file, zero runtime dependencies.

---

## What It Remembers

Your agent saves observations as it works — decisions, bugs, patterns, preferences — and retrieves them when relevant.

```
Agent: "We decided to use SQLite instead of PostgreSQL for single-file deployment"
  → Neurox saves it as type: decision, links it to schema.sql
  → Parses "instead of PostgreSQL" as a knowledge update
  → Three months later, agent asks "what database do we use?"
  → Returns the SQLite decision first, PostgreSQL as history
```

**Nothing is hidden.** Every observation is a row in SQLite. You can query it directly, export it, delete it, or inspect how it was scored.

| Feature | Simple store | Neurox |
|---|---|---|
| Save and retrieve text | Yes | Yes |
| Full-text search (FTS5) | Maybe | Built-in |
| Understands time ("last week", "currently") | No | [Temporal reasoning](docs/concepts.md#temporal-intent) |
| Knows when facts change | No | Knowledge updates — old facts become history, not noise |
| Links memories to source files | No | Git integration — auto-marks stale when files change |
| Explains why a result ranked first | No | [Debug mode](docs/concepts.md#memory-debugging) — full score breakdown |
| Tracks where memories came from | No | [Provenance](docs/concepts.md#provenance) — which tool, session, and surface |
| Works without any external service | — | Yes — LLM and embeddings are [optional enhancements](#graceful-degradation) |

---

## Quick Start

### Install

```bash
# Homebrew (macOS / Linux)
brew install joeldevz/tap/neurox

# Script install (macOS / Linux)
curl -fsSL https://raw.githubusercontent.com/joeldevz/neurox/main/install.sh | bash

# Windows (PowerShell)
irm https://raw.githubusercontent.com/joeldevz/neurox/main/install.ps1 | iex

# Build from source (requires C compiler — CGO SQLite driver for native FTS5 performance)
CGO_ENABLED=1 go build -tags fts5 -o neurox .
```

### Configure your agent

```bash
neurox setup claude-code       # Claude Code
neurox setup opencode          # OpenCode
neurox setup cursor            # Cursor
neurox setup vscode            # VS Code (Copilot)
neurox setup antigravity       # Gemini CLI / Antigravity
neurox setup claude-desktop    # Claude Desktop
```

### Run

```bash
neurox mcp     # MCP server (stdio) — for Claude Code, Cursor, OpenCode, etc.
neurox serve   # HTTP API + web dashboard on localhost:7438
```

The web dashboard has four tabs: **Brain** (stats and activity), **Explorer** (browse and search observations), **Graph** (interactive force-directed visualization), and **Health** (brain power score with recommendations).

### Git integration

```bash
neurox install-hook   # post-commit hook — marks linked observations stale when files change
```

---

## How It Works

### Memory Layers

Observations move through three layers based on importance and access patterns:

```
 Buffer (new)          Working (validated)       Core (proven)
 ┌────────────────┐    ┌────────────────┐    ┌────────────────┐
 │ All new saves   │───>│ Passed quality  │───>│ Accessed 5+    │
 │ Capacity: 200   │    │ gate or high    │    │ times, 7+ days │
 │ Fast decay      │    │ importance      │    │ old, durable   │
 └────────────────┘    └────────────────┘    └────────────────┘
```

Decay reduces *accessibility* (how easy to find), not *value* (whether it exists). A decision made six months ago stays in Core — it just becomes less prominent unless accessed. Nothing is deleted without explicit action.

### Scoring

```
Score = (Recency × 0.3) + (Importance × 0.3) + (Relevance × 0.4)
        × Temporal multiplier (0.7x – 1.5x based on time intent)
        × Cross-signal boost (1.2x when FTS and semantic agree)
```

### Graceful Degradation

Neurox works without any external services. Features activate based on what's available:

| Available | Features enabled |
|---|---|
| **Nothing** (default) | FTS5 search, temporal parsing, decay, promotion |
| + Embeddings (Ollama or remote) | Hybrid search, semantic dedup, contradiction detection |
| + LLM (Ollama or remote) | Quality gate, fact extraction, reflection |
| + Curator LLM (remote) | Deep curation with importance recalibration |

The base configuration — zero runtime dependencies — already delivers [98% recall](#benchmark-results).

### Consolidation

A background pipeline runs every 30 minutes: decay → promote → dedup → contradict → reflect → evict. Every stage is deterministic and auditable. See [docs/concepts.md](docs/concepts.md#consolidation-epoch) for the full pipeline.

---

## Benchmark Results

Evaluated on [LongMemEval](https://github.com/xiaowu0162/LongMemEval) (ICLR 2025) — 500 questions across 6 categories, 48 distractor sessions per query.

| Category | N | Recall@10 | NDCG@10 |
|---|---|---|---|
| knowledge-update | 72 | **100.0%** | 96.9% |
| single-session-user | 64 | 98.4% | 97.0% |
| single-session-assistant | 56 | 98.2% | 95.1% |
| temporal-reasoning | 127 | 97.6% | 87.2% |
| multi-session | 121 | 98.4% | 87.0% |
| single-session-preference | 30 | 93.3% | 73.8% |
| **Overall** | **470** | **98.1%** | **90.0%** |

FTS5 + BM25 + temporal scoring. No LLM required. Reproducible in ~2 minutes.

A self-contained [Brain Benchmark](docs/concepts.md#brain-benchmark) (12 dimensions, 3 categories) is also included: `neurox benchmark`.

---

## Surface Parity

Neurox exposes three access surfaces. The core memory operations — `save`, `recall`, `context`, and `session` — use the **same shared pipeline** across all three, guaranteeing identical quality, provenance, and hooks regardless of how you connect.

| Capability | CLI | MCP | HTTP |
|---|:---:|:---:|:---:|
| **save** (shared pipeline, provenance, facts, embeddings) | ✓ | ✓ | ✓ |
| **recall** (FTS5 + semantic + temporal intent + provenance) | ✓ | ✓ | ✓ |
| **context** (proactive retrieval + reflections) | ✓ | ✓ | ✓ |
| **session_start / session_end** (observation extraction) | ✓ | ✓ | ✓ |
| **update** | — | ✓ | ✓ |
| **forget** (soft-delete) | — | ✓ | ✓ |
| **invalidate** (+ replacement) | ✓ | ✓ | ✓ |
| **status** | ✓ | ✓ | ✓ |
| **git_hook** | — | ✓ | ✓ |
| **reflect** | — | ✓ | ✓ |
| **consolidate** | ✓ | ✓ | ✓ |
| **health_check** | — | ✓ | ✓ |
| **curate** | ✓ | ✓ | ✓ |
| **backup** | ✓ | ✓ | ✓ |
| **audit** (full observation lifecycle) | ✓ | — | — |
| **graph** (interactive visualization) | ✓ | — | ✓ |
| **benchmark** | ✓ | — | — |
| **export / import** | ✓ | — | — |
| **reembed** | ✓ | — | — |
| **Web dashboard** (Brain, Explorer, Graph, Health) | — | — | ✓ |

**Concurrency model:** MCP and HTTP use an async `SaveQueue` with background workers. CLI uses the same pipeline synchronously (the process exits after each command). The quality gates, fact extraction, and embedding hooks are identical in all cases.

### MCP Tools

| Tool | Description |
|---|---|
| `save` | Save observation with FTS5 indexing and temporal extraction |
| `recall` | Search with hybrid scoring (FTS5 + semantic + temporal) |
| `context` | Proactive context: recent + important + file-linked |
| `update` | Update observation by ID |
| `forget` | Soft-delete |
| `invalidate` | Mark incorrect, optionally create replacement with supersedes link |
| `status` | Brain stats: layers, staleness, facts, providers |
| `session_start` | Start work session, return relevant context |
| `session_end` | End session with summary |
| `git_hook` | Report changed files, mark linked observations stale |
| `reflect` | Synthesize insights from Working-layer observations |
| `consolidate` | Force immediate consolidation cycle |
| `health_check` | Brain power score (0-100%) with recommendations |
| `curate` | Deep curation with external LLM |
| `backup` | Safe database backup while server is running |

Full tool inputs and parameters: [docs/reference.md](docs/reference.md#mcp-tool-inputs)

---

## Documentation

| Topic | Link |
|---|---|
| **Quickstart** | [docs/quickstart.md](docs/quickstart.md) |
| **Concepts & vocabulary** | [docs/concepts.md](docs/concepts.md) — memory layers, temporal intent, decay curves, knowledge graph, provenance, debug mode, brain power score |
| **Full reference** | [docs/reference.md](docs/reference.md) — CLI commands, REST API, MCP tool inputs, configuration, environment variables, architecture |
| **Claude Code** | [docs/claude-code.md](docs/claude-code.md) |
| **Claude Desktop** | [docs/claude-desktop.md](docs/claude-desktop.md) |
| **Cursor** | [docs/cursor.md](docs/cursor.md) |
| **VS Code** | [docs/vscode.md](docs/vscode.md) |
| **OpenCode** | [docs/opencode.md](docs/opencode.md) |

---

## Technology

- **Go 1.26+** — single binary, goroutines for background consolidation
- **SQLite 3** — WAL mode, FTS5 full-text search, via [mattn/go-sqlite3](https://github.com/mattn/go-sqlite3) (CGO, native SQLite performance)
- **MCP** — Model Context Protocol via [mark3labs/mcp-go](https://github.com/mark3labs/mcp-go)
- **Embeddings** — Ollama or any OpenAI-compatible API (optional)
- **LLM** — Ollama or OpenAI-compatible (optional)
- **IDs** — ULID (monotonic, sortable) via [oklog/ulid](https://github.com/oklog/ulid)

## Platform Support

| Platform | Architecture | Binary | Homebrew | Build from source |
|---|:---:|:---:|:---:|:---:|
| **macOS** | Apple Silicon (arm64) | ✓ | ✓ | ✓ |
| **macOS** | Intel (amd64) | ✓ | ✓ | ✓ |
| **Linux** | x86_64 (amd64) | ✓ | ✓ | ✓ |
| **Linux** | ARM64 (arm64) | ✓ | ✓ | ✓ |
| **Windows** | x86_64 (amd64) | ✓ | — | ✓ |
| **Windows** | ARM64 | — | — | untested |
| **FreeBSD** | any | — | — | untested |

**Prebuilt binaries** are attached to every [GitHub Release](https://github.com/joeldevz/neurox/releases). No Go, no C compiler, no dependencies — download and run.

**Homebrew** installs prebuilt binaries via the [joeldevz/tap](https://github.com/joeldevz/homebrew-tap).

**Build from source** requires Go 1.26+ and a C compiler (`gcc`, `clang`, or MinGW on Windows) with `CGO_ENABLED=1 -tags fts5`.

## License

[BSL 1.1](LICENSE) — You can use, modify, and distribute Neurox for any purpose **except** offering it as a commercial hosted service competing with the Licensor. On **2030-03-28**, it converts automatically to **Apache 2.0**.

Same model as [Sentry](https://blog.sentry.io/relicensing-sentry/), [CockroachDB](https://www.cockroachlabs.com/blog/oss-relicensing-cockroachdb/), [HashiCorp](https://www.hashicorp.com/blog/hashicorp-adopts-business-source-license), and [MariaDB](https://mariadb.com/bsl-faq-adopting/).

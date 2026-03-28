<p align="center">
  <h1 align="center">Neurox</h1>
  <p align="center">
    <strong>Persistent memory for AI coding agents</strong>
  </p>
  <p align="center">
    One binary &bull; One SQLite file &bull; Zero external dependencies
  </p>
  <p align="center">
    <a href="#quick-start">Quick Start</a> &bull;
    <a href="#what-it-remembers">What It Remembers</a> &bull;
    <a href="#benchmark-results">98% Recall</a> &bull;
    <a href="#common-questions">FAQ</a> &bull;
    <a href="README.es.md">Leer en Espanol</a>
  </p>
</p>

---

Your AI coding agent forgets everything between sessions. Every conversation starts from scratch — no memory of the architecture decisions you made last week, the bug you fixed yesterday, or your preference for tabs over spaces.

Neurox gives your agent persistent, structured memory.

```bash
# Install (macOS / Linux)
curl -fsSL https://raw.githubusercontent.com/joeldevz/neurox/main/install.sh | bash

# Configure your agent
neurox setup claude-code    # or: opencode, cursor, vscode, antigravity, claude-desktop
```

That's it. No Node.js, no Python, no Docker. **One binary, one SQLite file.**

---

## What It Remembers

Your agent saves observations as it works — decisions, bugs, patterns, preferences — and retrieves them when relevant. Every observation is a structured record in a local SQLite database, fully inspectable and auditable.

```
Agent: "We decided to use SQLite instead of PostgreSQL for single-file deployment"
  → Neurox saves it as type: decision, links it to schema.sql
  → Parses "instead of PostgreSQL" as a knowledge update
  → Three months later, agent asks "what database do we use?"
  → Neurox returns the SQLite decision first, PostgreSQL as history
```

**Nothing is hidden.** Every observation is a row in SQLite. You can query it directly, export it, delete it, or inspect how it was scored. There is no opaque consolidation that silently removes your data — everything is traceable through provenance metadata and audit logs.

### What makes this different from a key-value store

| Feature | Simple store | Neurox |
|---|---|---|
| Save and retrieve text | Yes | Yes |
| Full-text search (FTS5) | Maybe | Built-in |
| Understands time ("last week", "currently") | No | [Temporal reasoning](#temporal-reasoning) |
| Knows when facts change | No | [Knowledge updates](#temporal-reasoning) — old facts become history, not noise |
| Links memories to source files | No | [Git integration](#git-integration) — auto-marks stale when files change |
| Explains why a result ranked first | No | [Debug mode](#debug-mode) — full score breakdown per result |
| Tracks where memories came from | No | [Provenance](#provenance) — which tool, session, and surface created each memory |
| Works without any external service | — | Yes — [LLM and embeddings are optional enhancements](#graceful-degradation) |

## Quick Start

### Install

```bash
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/joeldevz/neurox/main/install.sh | bash

# Windows
irm https://raw.githubusercontent.com/joeldevz/neurox/main/install.ps1 | iex
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

### Interactive installer (advanced)

```bash
neurox install
```

Launches a Bubble Tea TUI where you can choose the install directory, config directory, provider setup, editor integrations, and git hook installation. Shows exactly what will be written before writing anything.

### Build from source

```bash
go build -o neurox .
```

No C compiler required — Neurox uses a pure Go SQLite driver.

### Use as MCP server

```bash
neurox mcp     # stdio — for Claude Code, Cursor, OpenCode, etc.
```

### Use as HTTP API + Web Dashboard

```bash
neurox serve   # localhost:7438 — opens REST API and web dashboard
```

Open `http://localhost:7438` in your browser to access the interactive dashboard:

| Tab | What it shows |
|---|---|
| **Brain** | Total observations, layer distribution, activity chart over time, stale count |
| **Explorer** | Browse and search all observations with filters (namespace, layer, kind), detail panel with full metadata |
| **Graph** | Interactive force-directed graph (vis-network) of observation relationships — filter by type, namespace, importance |
| **Health** | Brain power score (0-100%), dimension breakdown with recommendations, memory layer funnel, decay timeline chart |

The dashboard is a self-contained HTML page — no external frameworks, no build step, no separate install.

---

## How It Works

```
1. Agent saves a memory           →  neurox save "title" --content "..." --type decision
2. Neurox indexes it               →  FTS5 full-text + optional embeddings
3. Temporal info is extracted       →  "last week" → 2026-03-13
4. Files are linked                 →  --files "schema.sql" → tracked for git staleness
5. Agent searches later             →  neurox recall "what database" → ranked results
6. Background maintenance runs      →  every 30 min: decay, promote, dedup, reflect
```

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

**Important:** decay reduces *accessibility* (how easy it is to find), not *value* (whether it exists). A decision made six months ago stays in Core permanently — it just becomes less prominent in search results unless you access it. Nothing is deleted without explicit action or configurable eviction rules.

### Scoring

Every search result is ranked by a composite score:

```
Score = (Recency × 0.3) + (Importance × 0.3) + (Relevance × 0.4)
        × Temporal multiplier (0.7x – 1.5x based on time intent)
        × Cross-signal boost (1.2x when FTS and semantic agree)
```

FTS5 keyword matching is the primary search engine. Semantic embeddings are an optional reranker that improves results when configured — but Neurox works at full speed without them.

---

## Temporal Reasoning

Neurox understands *when* things happened. This is the difference between "What database do we use?" returning the right answer, and returning a deprecated fact from six months ago.

**On save** — temporal expressions are extracted and normalized:
```
"We migrated to SQLite last week"     → relative, 2026-03-13, confidence: 0.85
"Currently using PostgreSQL 16"       → current_state, confidence: 0.95
"Deployed on March 5, 2026"           → absolute, 2026-03-05, confidence: 0.95
```

Supports English and Spanish. Handles absolute dates, relative expressions (yesterday, 3 weeks ago, hace 2 meses), current-state markers, durations, and date ranges.

**On search** — temporal intent is detected and scoring adjusts:

| Query pattern | Effect |
|---|---|
| "currently", "now", "latest" | Boosts fresh results, penalizes stale |
| "before", "previously", "used to" | Includes historical results, boosts old |
| "when did", "what date" | Boosts results with dates |
| "how long", "since when" | Boosts duration mentions |
| "March 2026", "last week" | Boosts temporal proximity |

**On contradiction** — temporal sequences are preserved as history:
```
Old: "We use PostgreSQL"     →  marked stale (still findable as history)
New: "We migrated to SQLite" →  marked fresh (ranks first)
Link: new supersedes old
```

The old observation is *stale*, not *deleted*. "What did we use before?" still finds it.

---

## Git Integration

Install the post-commit hook:

```bash
neurox install-hook
```

When you commit, Neurox receives the list of changed files and marks linked observations as stale. If you saved "Auth uses JWT with RS256" linked to `auth/middleware.go`, and then you refactor that file, the observation is flagged for review — not silently trusted.

Git hook events are sent to the HTTP server (`neurox serve` must be running).

---

## Graceful Degradation

Neurox works without any external services. Features activate based on what's available:

| Available | Features enabled |
|---|---|
| **Nothing** (default) | FTS5 search, temporal parsing, heuristic promotion, decay |
| + Embeddings (Ollama or remote) | Hybrid search, semantic dedup, contradiction detection |
| + LLM (Ollama or remote) | Quality gate, fact extraction, reflection, session extraction |
| + Curator LLM (remote) | Deep curation with importance recalibration |

The base configuration — zero dependencies — already delivers 98% recall on LongMemEval. Everything else improves precision and maintenance.

---

## Debug Mode

Pass `debug: true` to any search to see exactly why each result ranked where it did:

```json
{
  "score_breakdown": {
    "recency": 0.85,
    "importance": 0.70,
    "relevance": 0.92,
    "semantic_score": 0.88,
    "temporal_multiplier": 1.2,
    "cross_signal_boost": 1.0,
    "final_score": 0.83
  }
}
```

Available via MCP (`debug: true`), CLI (`--debug`), and HTTP API (`?debug=true`).

---

## Provenance

Every observation records where it came from:

| Field | Description | Values |
|---|---|---|
| `source_surface` | Entry point | `mcp`, `http`, `cli`, `consolidator` |
| `source_session_id` | Session at creation | ULID or empty |
| `source_tool` | Operation | `save`, `invalidate`, `reflect`, `curate` |

Use `neurox audit <id>` to see the full lifecycle of any observation: creation, promotions, links, staleness transitions, temporal mentions, and current state.

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

FTS5 + BM25 + temporal scoring. No LLM required. 500 questions in ~2 minutes.

### Brain Benchmark

A self-contained suite that tests the full memory engine across 12 dimensions:

| Category | Weight | Dimensions |
|---|---|---|
| **Cognitive** | 45% | Knowledge Update, Signal vs Noise, Cross-Session, Temporal Reasoning, Memory Lifecycle |
| **Performance** | 20% | Write Throughput, Recall Latency, Concurrent Access, Context Retrieval |
| **Agent Simulation** | 35% | Lazy vs Perfect Agent, Realistic Workflows, Parameter Impact |

```bash
neurox benchmark                                            # Quick run (1k observations)
neurox benchmark --scale large --output-html report.html    # Full run with HTML report
```

All tests run against a fresh in-memory database — production data is never touched.

---

## Common Questions

### "Is the BSL license restrictive?"

The BSL 1.1 allows you to:

- ✅ Use Neurox in your company, team, or personal projects
- ✅ Modify the source code
- ✅ Distribute copies
- ✅ Build commercial products on top of it
- ✅ Use it in production, at any scale

The **only** restriction: you cannot offer Neurox itself as a commercial hosted service that directly competes with the Licensor's paid offerings.

On **2030-03-28**, the license automatically converts to **Apache 2.0** — fully permissive, no restrictions at all.

This is the same licensing model used by [Sentry](https://blog.sentry.io/relicensing-sentry/), [CockroachDB](https://www.cockroachlabs.com/blog/oss-relicensing-cockroachdb/), [HashiCorp](https://www.hashicorp.com/blog/hashicorp-adopts-business-source-license), and [MariaDB](https://mariadb.com/bsl-faq-adopting/). If your use case is anything other than reselling Neurox as a hosted memory service, the BSL has zero practical impact on you.

### "Is this only for advanced users or autonomous agent systems?"

No. The setup is the same as any MCP memory server:

```bash
curl -fsSL https://raw.githubusercontent.com/joeldevz/neurox/main/install.sh | bash
neurox setup claude-code
```

Two commands. No configuration files to edit, no API keys required, no external services needed. Works immediately with Claude Code, OpenCode, Cursor, VS Code, Gemini CLI, and Claude Desktop.

The base installation with zero dependencies already delivers [98% recall](#benchmark-results). Want to go further? Add an embeddings provider (Ollama or any OpenAI-compatible API) to unlock hybrid semantic search, automatic dedup, and contradiction detection. Add an LLM to enable quality gates, fact extraction, and reflection. These features activate automatically when a provider is detected — see [Graceful Degradation](#graceful-degradation) for the full breakdown.

### "How do you know it actually works?"

Published benchmarks, reproducible by anyone:

**[LongMemEval](https://github.com/xiaowu0162/LongMemEval)** (ICLR 2025) — 500 questions across 6 categories, 48 distractor sessions per query. Result: **98.1% Recall@10**, **90.0% NDCG@10**. No LLM required. Runs in ~2 minutes.

**Brain Benchmark** — a self-contained 12-dimension suite that tests cognitive fidelity (knowledge updates, temporal reasoning, signal vs noise, cross-session memory, lifecycle), performance (write throughput, recall latency, concurrent access), and agent simulation (lazy vs perfect usage, realistic workflows).

```bash
# Reproduce it yourself
neurox benchmark                                            # Quick run
neurox benchmark --scale large --output-html report.html    # Full run with HTML report
```

Every test runs against a fresh in-memory database. Results are deterministic and auditable. If a memory tool doesn't publish benchmarks, you have no way to know if it works at scale — you're trusting marketing, not data.

---

## Knowledge Graph

Observations are enriched into structured facts (subject-predicate-object triples):

```
migration  | happened_on | 2026-03-06
database   | current     | sqlite
auth       | changed_to  | jwt
project    | uses        | go
```

Facts have temporal validity — when superseded, the old fact keeps its history (`valid_until` set, `superseded_by` linked). You can query both current state and historical changes.

## Deep Curation

Over time, memory accumulates noise. The curator sends an entire namespace to a large language model for bulk review:

- **KEEP** with recalibrated importance (based on actual value, not just decay math)
- **DELETE** noise, duplicates, and observations that no longer provide signal

```bash
neurox curate --namespace myproject --dry-run   # Preview changes
neurox curate --namespace myproject             # Apply
```

An optional `priorities.yaml` biases curation toward domain-specific value signals.

## Consolidation Pipeline

Runs automatically every 30 minutes (or on demand):

```
 1. Decay         Apply activation decay (kind-specific rates)
 2. Retry         Re-evaluate previously rejected observations
 3. Promote       Buffer → Working (importance + quality gate)
 4. Promote       Working → Core (access count + age)
 5. Dedup         Merge near-duplicates (skip if different temporal windows)
 6. Contradict    Find conflicts → temporal sequence? soft stale : hard supersede
 7. Reflect       Synthesize insights from Working-layer clusters
 8. Evict         Remove lowest-importance Buffer overflow
 9. GC            Hard-delete expired observations
```

Every stage is deterministic and auditable. Consolidation runs are logged with timestamps. Nothing happens in the background that you can't inspect.

---

## MCP Tools

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

### Tool Inputs

| Tool | Key inputs |
|---|---|
| `save` | `title`, `content`, `observation_type`, `kind`, `confidence`, `topic_key`, `tags`, `files`, `namespace`, `retention` |
| `recall` | `query`, `observation_type`, `kind`, `namespace`, `files`, `include_stale`, `limit`, `debug` |
| `context` | `namespace`, `files`, `limit` |
| `update` | `id`, `title`, `content`, `observation_type`, `kind`, `confidence`, `tags`, `files`, `retention` |
| `forget` | `id` |
| `invalidate` | `observation_id`, `reason`, `replacement_title`, `replacement_content` |
| `session_start` | `title`, `directory`, `branch`, `namespace` |
| `session_end` | `session_id`, `summary` |
| `git_hook` | `changed_files`, `commit_sha`, `branch` |
| `health_check` | `days` |
| `curate` | `namespace`, `dry_run` |

---

## CLI Reference

| Command | What it does | Useful flags |
|---|---|---|
| `neurox mcp` | Start MCP server (stdio) | — |
| `neurox serve` | Start HTTP server + web dashboard on port 7438 | `--host` |
| `neurox save "title"` | Save observation | `--content`, `--type`, `--kind`, `--confidence`, `--topic-key`, `--tags`, `--files`, `--namespace` |
| `neurox recall "query"` | Search with temporal-aware ranking | `--type`, `--kind`, `--namespace`, `--files`, `--include-stale`, `--limit`, `--debug` |
| `neurox context` | Proactive context for namespace/files | `--namespace`, `--files`, `--limit` |
| `neurox invalidate <id>` | Mark observation incorrect + replace | `--reason`, `--replacement-title`, `--replacement-content` |
| `neurox status` | Brain, provider, and database stats | — |
| `neurox audit <id>` | Full lifecycle of an observation | — |
| `neurox consolidate` | Force full consolidation | — |
| `neurox graph` | Interactive HTML graph view | `--namespace`, `--type`, `--tags`, `--min-importance`, `--limit`, `--linked-only`, `--output`, `--no-browser` |
| `neurox setup <agent>` | Configure AI agent integration | — |
| `neurox config` | Print resolved runtime config | — |
| `neurox install-hook` | Install git post-commit hook | — |
| `neurox curate` | Deep memory curation | `--namespace`, `--dry-run` |
| `neurox reembed` | Re-embed all observations | — |
| `neurox export` | Export as Markdown files | `--format`, `--output`, `--namespace` |
| `neurox import` | Import .md observation files | `--source` |
| `neurox benchmark` | Run brain benchmark suite | `--scale`, `--category`, `--dimensions`, `--output`, `--output-html`, `--verbose` |
| `neurox update` | Update to latest version | `--yes` |

All data commands (`save`, `recall`, `context`, `invalidate`, `status`, `audit`, `config`) return JSON.

---

## REST API

```
GET    /health                               Health check
GET    /api/v1/status                        Brain statistics
GET    /api/v1/health-check                  Brain power score
GET    /api/v1/decay-timeline                Importance by layer per day
GET    /api/v1/stats/activity                Tool call activity per day
GET    /api/v1/stats/breakdown               Breakdown by type/layer/namespace/kind
GET    /api/v1/observations/browse           Browse recent observations
POST   /api/v1/observations                  Save observation
GET    /api/v1/observations/search?q=...     Search memories
GET    /api/v1/observations/context          Proactive context
GET    /api/v1/observations/{id}             Get observation
PUT    /api/v1/observations/{id}             Update
DELETE /api/v1/observations/{id}             Soft-delete
POST   /api/v1/observations/{id}/invalidate  Invalidate + replace
POST   /api/v1/sessions                      Start session
PUT    /api/v1/sessions/{id}/end             End session
POST   /api/v1/hooks/git                     Git hook
GET    /api/v1/graph                         Graph view (HTML or ?format=json)
POST   /api/v1/reflect                       Trigger reflection
POST   /api/v1/consolidate                   Force consolidation
POST   /api/v1/curate                        Deep curation
```

### Query Parameters

| Route | Parameters |
|---|---|
| `GET /observations/search` | `q`, `type`, `kind`, `namespace`, `files`, `staleness`, `include_stale`, `limit`, `debug` |
| `GET /observations/context` | `namespace`, `files`, `limit` |
| `GET /observations/browse` | `limit`, `offset`, `type`, `layer`, `namespace`, `kind`, `staleness` |
| `GET /graph` | `namespace`, `type`, `tags`, `min_importance`, `limit`, `linked_only`, `format` |
| `GET /stats/activity` | `days` |
| `GET /health-check` | `days` |
| `GET /decay-timeline` | `days`, `layers` |

### Payload Examples

```json
POST /api/v1/observations
{
  "title": "JWT auth middleware",
  "content": "What: Added RS256 middleware\nWhy: Standardize API auth\nWhere: internal/auth/middleware.go",
  "observation_type": "decision",
  "kind": "semantic",
  "confidence": 0.9,
  "tags": ["auth", "jwt"],
  "files": ["internal/auth/middleware.go"],
  "namespace": "myproject"
}
```

```json
POST /api/v1/hooks/git
{
  "changed_files": ["README.md", "main.go"],
  "commit_sha": "b04b533",
  "branch": "main"
}
```

---

## Agent Setup

Per-client setup guides:

| AI Client | Guide |
|---|---|
| Claude Code | [docs/claude-code.md](docs/claude-code.md) |
| Claude Desktop | [docs/claude-desktop.md](docs/claude-desktop.md) |
| Cursor | [docs/cursor.md](docs/cursor.md) |
| VS Code | [docs/vscode.md](docs/vscode.md) |
| OpenCode | [docs/opencode.md](docs/opencode.md) |

All clients use the same pattern: install the binary, add `neurox` to `mcpServers` with `command: "neurox"` and `args: ["mcp"]`, restart.

**Further reading:** [docs/concepts.md](docs/concepts.md) — key terms: decay curve, consolidation, memory layers, staleness, temporal intent, observations vs. facts, brain power score, provenance, debug mode.

---

## Observation Types

| Type | When to use | Example |
|---|---|---|
| `decision` | Architecture or design choices | "Chose SQLite for single-file deployment" |
| `bugfix` | What broke and why | "N+1 query in user list, fixed with preload" |
| `discovery` | Learned something about the codebase | "Auth middleware runs before CORS" |
| `pattern` | Recurring conventions | "All stores use constructor injection" |
| `gotcha` | Traps and pitfalls | "SQLite WAL requires single-writer" |
| `config` | Environment and tool setup | "CI uses Go 1.26" |
| `preference` | User corrections and preferences | "Prefer table-driven tests" |
| `question` | Open questions for review | "Should we split this package?" |

---

## Configuration

Config file: `~/.config/neurox/config.yaml`

```yaml
database:
  path: ~/.config/neurox/neurox.db

llm:
  provider: ""          # "ollama", "remote", "" (auto-detect)
  gate_mode: "auto"     # "auto", "full", "off"
  ollama_url: ""        # default: http://localhost:11434
  ollama_model: ""      # default: qwen2.5:3b
  remote_url: ""        # OpenAI-compatible endpoint
  remote_api_key: ""
  remote_model: ""

embeddings:
  provider: ""          # "ollama", "remote", "" (auto-detect)
  remote_url: ""        # OpenAI-compatible embeddings endpoint
  remote_api_key: ""
  remote_model: ""
  dimensions: 0         # auto-detect from provider

curator:
  provider: ""          # "remote" or "" (disabled)
  remote_url: ""        # OpenAI-compatible endpoint
  remote_api_key: ""
  remote_model: ""      # e.g. "gemini-2.5-flash"
  priorities_file: ""   # path to priorities.yaml

consolidation:
  dedup_threshold: 0.85
  contradiction_min: 0.65
  contradiction_max: 0.85
  related_min: 0.65
  related_max: 0.85
```

### Environment Variables

All settings can be overridden with `NEUROX_` prefix:

| Variable | Purpose |
|---|---|
| `NEUROX_DATABASE_PATH` | Custom SQLite database path |
| `NEUROX_HTTP_HOST` | HTTP bind address (default: `127.0.0.1`) |
| `NEUROX_LLM_PROVIDER` | `ollama`, `remote`, or empty |
| `NEUROX_LLM_GATE_MODE` | `auto`, `full`, or `off` |
| `NEUROX_EMBED_PROVIDER` | Embeddings provider |
| `NEUROX_CURATOR_PROVIDER` | Curator provider (`remote` or empty) |

Full list of environment overrides in [docs/concepts.md](docs/concepts.md).

---

## Performance

| Operation | Latency | Notes |
|---|---|---|
| `save` | <1ms | SQLite insert + FTS5 index + temporal extraction |
| `recall` (FTS) | <5ms | BM25 ranking with temporal scoring |
| `recall` (hybrid) | <50ms | FTS + semantic + cross-signal boost |
| `context` | <10ms | Proactive multi-signal retrieval |
| `consolidate` | <1s | Full cycle for 1,000 observations |
| Binary size | ~15MB | Single statically-linked executable |
| Memory | <150MB | With 10k observations + embeddings |

---

## Architecture

```
neurox/
├── main.go                    CLI entry point
├── internal/
│   ├── api/                   HTTP REST server + web dashboard (Brain, Explorer, Graph, Health tabs)
│   ├── benchmark/             Brain benchmark suite
│   ├── classify/              Auto-classification of type and kind
│   ├── config/                YAML + env config loading
│   ├── consolidate/           Background pipeline
│   ├── contradiction/         Conflict detection + temporal supersession
│   ├── curate/                Deep curation with external LLM
│   ├── db/                    SQLite schema, migrations, WAL mode
│   ├── decay/                 Activation decay, garbage collection
│   ├── embed/                 Ollama + OpenAI-compatible embeddings
│   ├── export/                Markdown export and import
│   ├── facts/                 Knowledge triples, LLM extraction
│   ├── filelink/              File-observation linking
│   ├── graph/                 Interactive HTML graph + queries
│   ├── health/                Brain power scoring with recommendations
│   ├── installer/             Bubble Tea TUI installer
│   ├── links/                 Observation relationships
│   ├── llm/                   LLM providers, quality gate
│   ├── mcp/                   MCP protocol server
│   ├── observation/           Core types, CRUD, temporal extraction
│   ├── proactive/             Context retrieval without queries
│   ├── recall/                FTS5 + semantic + temporal search
│   ├── reflect/               Insight synthesis
│   ├── session/               Session lifecycle
│   ├── telemetry/             Tool call tracking
│   └── temporal/              Date parser, mention storage
├── benchmarks/longmemeval/    LongMemEval harness
├── tests/integration/         E2E tests + performance benchmarks
└── scripts/post-commit        Git hook for staleness tracking
```

## Technology

- **Go 1.26+** — single binary, goroutines for background consolidation
- **SQLite 3** — WAL mode, FTS5 full-text search, via ncruces/go-sqlite3 (pure Go, no CGO required)
- **Embeddings** — Ollama (nomic-embed-text) or any OpenAI-compatible API (optional)
- **LLM** — Ollama or OpenAI-compatible (optional — for quality gate, reflection, facts)
- **MCP** — Model Context Protocol via mark3labs/mcp-go
- **IDs** — ULID (monotonic, sortable) via oklog/ulid

## License

[BSL 1.1](LICENSE) (Business Source License 1.1)

You can use, modify, and distribute Neurox for any purpose **except** offering it as a commercial hosted service that competes with the Licensor's paid offerings. On **2030-03-28**, the license automatically converts to **Apache 2.0**.

See the [LICENSE](LICENSE) file for the full text.

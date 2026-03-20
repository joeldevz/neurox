<p align="center">
  <!-- <img src="assets/neurox-banner.png" alt="Neurox" width="800"> -->
  <h1 align="center">Neurox</h1>
  <p align="center">
    <strong>A brain-inspired memory engine for AI coding agents</strong>
  </p>
  <p align="center">
    Three-layer memory &bull; Hybrid search &bull; Temporal reasoning &bull; Ebbinghaus decay &bull; Consolidation pipelines
  </p>
  <p align="center">
    <a href="#benchmark-results">98% Recall on LongMemEval</a> &bull;
    <a href="#quick-start">Quick Start</a> &bull;
    <a href="README.es.md">Leer en Espanol</a>
  </p>
</p>

---

Neurox gives AI coding agents persistent, structured memory that works like a brain. It stores observations across three memory layers, automatically promotes important memories, detects and resolves contradictions, and understands *when* things happened — not just *what*.

**98% retrieval accuracy** on LongMemEval benchmark (S setting, 48 distractor sessions per query). Pure FTS5, no LLM required.

## How it works

```
        You code with an AI agent
                  |
                  v
    +--------------------------+
    |     Agent saves memory   |   "We migrated to SQLite last week"
    +--------------------------+
                  |
                  v
    +--------------------------+
    |        Neurox            |
    |                          |
    |  1. Parse temporal info  |   -> "last week" = 2026-03-13
    |  2. Extract facts        |   -> migration | happened_on | 2026-03-13
    |  3. Store in Buffer      |   -> FTS5 indexed, embeddings queued
    |  4. Link to files        |   -> internal/db/schema.sql
    +--------------------------+
                  |
          (30 min cycle)
                  v
    +--------------------------+
    |    Consolidation         |
    |                          |
    |  Decay -> Promote ->     |
    |  Dedup -> Contradictions |
    |  -> Reflect -> Evict     |
    +--------------------------+
                  |
                  v
    +--------------------------+
    |     Agent asks memory    |   "What DB do we use currently?"
    +--------------------------+
                  |
                  v
    +--------------------------+
    |     Temporal-aware       |
    |       Recall             |
    |                          |
    |  1. Detect intent        |   -> current_state
    |  2. FTS5 + BM25          |   -> keyword matching
    |  3. Semantic search      |   -> cosine similarity
    |  4. Temporal scoring     |   -> boost fresh, penalize stale
    |  5. Return ranked        |   -> "SQLite" ranks first
    +--------------------------+
```

## The Three-Layer Memory Model

Inspired by human memory systems, Neurox organizes knowledge into three layers with automatic promotion based on importance and access patterns.

```
 Layer 0: Buffer                Layer 1: Working              Layer 2: Core
 ┌─────────────────┐           ┌─────────────────┐           ┌─────────────────┐
 │                  │           │                  │           │                  │
 │  Short-term      │   ───>   │  Medium-term     │   ───>   │  Long-term       │
 │  New observations│  promote │  Validated info   │  promote │  Proven knowledge│
 │  Unfiltered      │          │  Accessed often   │          │  High confidence │
 │                  │           │                  │           │                  │
 │  Capacity: 200   │           │  Dedup + Reflect │           │  Permanent       │
 │  Decay: fast     │           │  Decay: moderate │           │  Decay: slow     │
 └─────────────────┘           └─────────────────┘           └─────────────────┘
         │                              │                              │
         └──────────────────────────────┴──────────────────────────────┘
                                        │
                              Ebbinghaus Decay
                         (episodic: fast, semantic: medium, procedural: slow)
```

**Promotion rules:**
- Buffer → Working: importance threshold (0.3) or procedural type, with optional LLM quality gate
- Working → Core: accessed 5+ times AND older than 7 days
- Each layer has its own decay rate based on memory kind

## Temporal Reasoning

Neurox understands *time*. When you save "We migrated to SQLite last week" or ask "What database did we use before?", it knows what you mean.

### How it works

**On save** — temporal expressions are extracted and normalized:
```
"We migrated to SQLite last week"
  └─> kind: relative, normalized: 2026-03-13, confidence: 0.85

"Currently using PostgreSQL 16"
  └─> kind: current_state, confidence: 0.95

"Deployed on March 5, 2026"
  └─> kind: absolute, normalized: 2026-03-05, confidence: 0.95
```

Supports English and Spanish. Handles absolute dates, relative expressions (yesterday, 3 weeks ago, hace 2 meses), current-state markers, durations, and date ranges.

**On recall** — temporal intent is detected in the query and scoring is adjusted:

| Query pattern | Detected intent | Effect |
|---|---|---|
| "currently", "latest", "now" | `current_state` | Boosts fresh, penalizes stale |
| "before", "previously", "used to" | `history` | Includes expired, boosts old |
| "when did", "what date" | `when` | Boosts observations with dates |
| "how long", "since when" | `duration` | Boosts duration mentions |
| "March 2026", "last week" | `point_in_time` | Boosts temporal proximity |
| No temporal words | none | Standard tri-factor scoring |

**On contradiction** — temporal sequences are preserved, not destroyed:
```
Old: "We use PostgreSQL"     →  staleness: stale (still queryable as history)
New: "We migrated to SQLite" →  staleness: fresh (ranks first for current queries)
Link: new supersedes old
```

The old observation becomes *stale* (not *expired*), so "What did we use before?" still finds it.

## Hybrid Search

Recall combines multiple signals into a single score:

```
Score = (Recency × 0.3) + (Importance × 0.3) + (Relevance × 0.4)
        × Cross-signal boost (1.2x if FTS ∩ semantic)
        × Temporal multiplier (0.7x – 1.5x based on intent match)
```

| Signal | Source | What it captures |
|---|---|---|
| **Relevance** | FTS5 BM25 + semantic cosine | How well content matches the query |
| **Recency** | Ebbinghaus decay curve (30-day half-life) | How recently created or accessed |
| **Importance** | Initial weight + access boosts | How valuable the observation is |
| **Temporal** | Intent detection + mention matching | Whether this memory fits the time context |
| **Cross-signal** | FTS ∩ Semantic overlap | Confidence boost when both methods agree |

## Consolidation Pipeline

Runs automatically every 30 minutes (or on demand via `consolidate` tool):

```
 1. Decay         Apply Ebbinghaus curves to all observations
       ↓
 2. Retry         Re-evaluate previously rejected observations (3-strike system)
       ↓
 3. Promote       Buffer → Working (importance + quality gate)
       ↓
 4. Promote       Working → Core (access count + age)
       ↓
 5. Dedup         Merge near-duplicates (cosine ≥ 0.85)
       ↓           └─ Skip if different temporal windows (preserves timelines)
 6. Contradict    Find conflicting observations
       ↓           ├─ Temporal sequence? → soft supersession (stale)
       ↓           ├─ LLM confirms? → supersede (with temporal context: stale; without: expired)
       ↓           └─ No LLM? → create question for human review
 7. Reflect       Synthesize insights from Working-layer clusters
       ↓
 8. Evict         Remove lowest-importance Buffer overflow
       ↓
 9. GC            Hard-delete expired observations
```

## Knowledge Graph

Observations are enriched into structured facts (subject-predicate-object triples):

```
migration  | happened_on | 2026-03-06
database   | current     | sqlite
auth       | changed_to  | jwt
project    | uses        | go
```

Facts have temporal validity — when a fact is superseded, the old one keeps its history (`valid_until` set, `superseded_by` linked). You can query both current state and historical changes.

## Benchmark Results

Evaluated on [LongMemEval](https://github.com/xiaowu0162/LongMemEval) (ICLR 2025) — a benchmark for long-term conversational memory with 500 questions across 6 categories.

### LongMemEval-S (48 distractor sessions per query)

| Category | N | Recall@10 | NDCG@10 |
|---|---|---|---|
| knowledge-update | 72 | **100.0%** | 96.9% |
| single-session-user | 64 | 98.4% | 97.0% |
| single-session-assistant | 56 | 98.2% | 95.1% |
| temporal-reasoning | 127 | 97.6% | 87.2% |
| multi-session | 121 | 98.4% | 87.0% |
| single-session-preference | 30 | 93.3% | 73.8% |
| **Overall** | **470** | **98.1%** | **90.0%** |

> FTS5 + BM25 + temporal scoring, no LLM required. 500 questions in ~2 minutes.

## Quick Start

### Build

```bash
# Requires CGO for SQLite
CGO_ENABLED=1 go build -tags fts5 -o neurox .
```

Note: the resulting executable is portable, but not fully static; with CGO-enabled SQLite it links against system `libc`/`libm`.

### Use with AI agents (MCP)

```bash
./neurox mcp
```

### Use as HTTP API

```bash
./neurox serve  # localhost:7438
```

The git post-commit hook sends events to the HTTP server at `POST /api/v1/hooks/git`. The default hook port is `7438`; if your server listens elsewhere, set `NEUROX_PORT` before installing or running the hook.

### CLI

```bash
# Save a memory
neurox save "JWT auth pattern" \
  --content "Using JWT with RS256 for API auth" \
  --type decision \
  --tags "auth,jwt" \
  --files "internal/auth/middleware.go"

# Search memories
neurox recall "authentication" --namespace myproject --limit 5

# Get context for current work
neurox context --namespace myproject --files "src/auth.go"

# Check brain health
neurox status

# Force consolidation
neurox consolidate

# Open the interactive graph view
neurox graph --output neurox-graph.html

# Install git hook (auto-marks memories stale when files change)
neurox install-hook
```

## Agent Setup

### Claude Code

Add to `~/.claude/settings.json` or project `.mcp.json`:

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

Settings > MCP Servers > Add:
- Name: `neurox`
- Command: `/path/to/neurox mcp`
- Transport: `stdio`

### OpenCode

Add to `opencode.json`:

```json
{
  "mcp": {
    "neurox": {
      "type": "local",
      "command": ["/path/to/neurox", "mcp"],
      "enabled": true
    }
  }
}
```

### Windsurf / Copilot / HTTP clients

```bash
neurox serve  # REST API on port 7438
```

## MCP Tools

| Tool | Description |
|---|---|
| **`save`** | Save an observation to Buffer with FTS5 indexing and temporal extraction |
| **`recall`** | Temporal-aware search with hybrid scoring (FTS5 + semantic + temporal) |
| **`context`** | Proactive context: recent + important + file-linked observations |
| **`update`** | Update an existing observation by ID |
| **`forget`** | Soft-delete an observation |
| **`invalidate`** | Mark as incorrect, optionally create replacement with supersedes link |
| **`status`** | Brain stats: layers, staleness, facts, temporal mentions, providers |
| **`session_start`** | Start work session, auto-close previous, return relevant context |
| **`session_end`** | End session with summary, LLM extracts atomic observations |
| **`git_hook`** | Report changed files from commit, mark linked observations stale |
| **`reflect`** | Synthesize Working-layer observations into high-level insights |
| **`consolidate`** | Force immediate full consolidation cycle |

## REST API

```
GET    /health                              Health check
GET    /api/v1/status                       Brain statistics
GET    /api/v1/observations/browse          Browse recent observations
POST   /api/v1/observations                 Save observation
GET    /api/v1/observations/search?q=...    Search memories
GET    /api/v1/observations/context         Get proactive context
GET    /api/v1/observations/{id}            Get observation
PUT    /api/v1/observations/{id}            Update observation
DELETE /api/v1/observations/{id}            Soft-delete
POST   /api/v1/observations/{id}/invalidate Invalidate + replace
GET    /api/v1/stats/breakdown              Breakdown by type/layer/namespace/kind
POST   /api/v1/sessions                     Start session
PUT    /api/v1/sessions/{id}/end            End session
POST   /api/v1/hooks/git                    Git hook
GET    /api/v1/graph                        Interactive graph view (or JSON with ?format=json)
POST   /api/v1/reflect                      Trigger reflection
```

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
```

Agent setup examples match `install.sh`: Claude and Cursor use `command` + `args`, while OpenCode uses a local MCP entry with an array command.

Environment overrides use `NEUROX_` prefix:

```bash
NEUROX_DATABASE_PATH=/custom/path.db
NEUROX_LLM_PROVIDER=ollama
NEUROX_LLM_GATE_MODE=auto
NEUROX_EMBED_PROVIDER=ollama
```

### Graceful Degradation

Neurox works without any external services. Features activate based on what's available:

| Available | Features enabled |
|---|---|
| Nothing | FTS5 search, temporal parsing, heuristic promotion, decay |
| + Ollama embeddings | Hybrid search, semantic dedup, contradiction detection |
| + Ollama LLM | Quality gate, fact extraction, reflection, session extraction |
| + Remote API | Same as above with cloud provider |

## Observation Types

| Type | When to use | Example |
|---|---|---|
| `decision` | Architectural or design choices | "Chose SQLite for single-file deployment" |
| `bugfix` | What broke and why | "N+1 query in user list, fixed with preload" |
| `discovery` | Learned something about the codebase | "Auth middleware runs before CORS" |
| `pattern` | Recurring conventions | "All stores use constructor injection" |
| `gotcha` | Traps and pitfalls | "Must use -tags fts5 for build" |
| `config` | Environment and tool setup | "CI uses Go 1.23 with CGO" |
| `preference` | User corrections and preferences | "Prefer table-driven tests" |
| `question` | Open questions for review | "Should we split this package?" |

## Architecture

```
neurox/
├── main.go                    CLI entry point
├── internal/
│   ├── observation/           Core types, CRUD, temporal extraction
│   ├── recall/                FTS5 + semantic + temporal-aware search
│   ├── temporal/              Date parser, mention storage, helpers
│   ├── facts/                 Knowledge triples, LLM extraction
│   ├── consolidate/           Background pipeline (promote, dedup, evict)
│   ├── contradiction/         Conflict detection + temporal supersession
│   ├── decay/                 Ebbinghaus curves, garbage collection
│   ├── reflect/               Insight synthesis (Generative Agents pattern)
│   ├── session/               Session lifecycle, LLM observation extraction
│   ├── proactive/             Context retrieval without explicit queries
│   ├── embed/                 Ollama + OpenAI-compatible embeddings
│   ├── llm/                   LLM providers, quality gate, 3-strike system
│   ├── links/                 Observation relationships (supersedes, contradicts)
│   ├── db/                    SQLite schema, migrations, WAL mode
│   ├── mcp/                   MCP protocol server
│   ├── api/                   HTTP REST server + dashboard
│   ├── graph/                 Interactive HTML graph rendering + graph queries
│   ├── config/                YAML + env config loading
│   └── filelink/              File-observation linking
├── benchmarks/
│   └── longmemeval/           LongMemEval benchmark harness
├── tests/
│   └── integration/           E2E tests + performance benchmarks
└── scripts/
    └── post-commit            Git hook for staleness tracking
```

`internal/graph/` powers the public graph feature used by `neurox graph` and `GET /api/v1/graph`, rendering an interactive HTML visualization of observations and links.

## Troubleshooting

- Git hook events are sent to the HTTP server, so `neurox serve` must be running when commits happen.
- The hook uses port `7438` by default. If your server runs on another port, export `NEUROX_PORT=<port>` before installing or invoking the hook.

## Performance

| Operation | Latency | Notes |
|---|---|---|
| `save` | <1ms | SQLite insert + FTS5 index + temporal extraction |
| `recall` (FTS) | <5ms | BM25 ranking with temporal scoring |
| `recall` (hybrid) | <50ms | FTS + semantic + cross-signal boost |
| `context` | <10ms | Proactive multi-signal retrieval |
| `consolidate` | <1s | Full cycle for 1000 observations |
| Binary size | ~15MB | Single executable, but dynamically links libc/libm for SQLite/CGO |
| Memory | <150MB | With 10k observations + embeddings |

## Technology

- **Go 1.23** — single binary, goroutines for background consolidation
- **SQLite 3** — WAL mode, FTS5 full-text search, via mattn/go-sqlite3 (CGO)
- **Embeddings** — Ollama (nomic-embed-text, 768d) or any OpenAI-compatible API
- **LLM** — Ollama or OpenAI-compatible (optional, for quality gate + reflection + facts)
- **MCP** — Model Context Protocol via mark3labs/mcp-go
- **IDs** — ULID (monotonic, sortable) via oklog/ulid

## License

MIT

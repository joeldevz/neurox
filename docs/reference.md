# Neurox Reference

Complete reference for CLI commands, MCP tool inputs, REST API, configuration, and architecture.

---

## MCP Tool Inputs

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

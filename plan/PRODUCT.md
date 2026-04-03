# Neurox Core — Product Vision

## Vision

Neurox is the open-source memory engine for AI agents. Built with a **local-first philosophy**, it requires zero external dependencies and ships as a single Go binary. Whether you're a solo developer working on personal projects or part of a team building enterprise AI systems, Neurox provides the foundational memory layer that makes AI agents contextually aware, personally adapted, and continuously learning.

## What Neurox Core Is

Neurox Core is the foundational memory engine that powers both local (free) and enterprise (cloud) deployments. It implements a brain-inspired memory architecture with three distinct layers, decay-based forgetting, and intelligent consolidation—wrapped in clean interfaces that hide implementation complexity.

The Core repository defines **interfaces and contracts** that both local and cloud implementations can satisfy. The Core knows nothing about enterprise-specific features like multi-tenancy, billing, or hierarchical team structures. It simply provides the memory primitives: save, recall, consolidate, reflect.

## Core Capabilities

### 3-Layer Memory Model

| Layer | Name | Purpose | Promotion |
|-------|------|---------|-----------|
| 0 | **Buffer** | Fast capture of all observations | Automatic entry point |
| 1 | **Working** | Recently accessed, moderately important | Promoted via importance + access frequency |
| 2 | **Core** | Long-term stable knowledge | Promoted via consolidation strength |

### Ebbinghaus Decay Curves

Observations decay over time based on:
- **Access count**: More access = slower decay
- **Repetition**: Revisited memories consolidate
- **Importance**: Higher importance resists decay
- **Retention policy**: `operational` (never promotes) vs `durable` (eligible for Core)

### FTS5 + Semantic Hybrid Search

- **FTS5**: Full-text search with `observations_fts` virtual table
- **Semantic**: Vector similarity search using cosine distance
- **Hybrid**: Combines FTS relevance with semantic scores for best results

### Temporal Intent Detection

Automatically detects time-related queries and adjusts search:
- `current`, `now`, `latest` → Boost recent observations
- `history`, `when`, `duration` → Include stale/expired results
- Temporal keywords stripped from FTS to improve recall

### Consolidation Pipeline

Runs automatically every 30 minutes (or on-demand):
1. **Decay**: Apply time-based decay to all observations
2. **Promote**: Move high-value observations up layers
3. **Deduplicate**: Detect and merge similar observations
4. **Contradiction Detection**: Find conflicting statements
5. **Reflection**: Synthesize insights with LLM (when configured)
6. **Eviction**: Soft-delete expired observations

### Git Hooks Integration

Post-commit hooks automatically:
- Report changed files to Neurox
- Mark linked observations as potentially stale
- Track code evolution alongside knowledge evolution

### MCP Protocol (stdio Transport)

Native Model Context Protocol support via stdio:
- `save`, `recall`, `context`, `update`, `forget`, `invalidate`
- `session_start`, `session_end`, `git_hook`
- `reflect`, `consolidate`, `health_check`, `curate`, `backup`

### HTTP REST API

Full REST API on port 7438:
- `/api/v1/observations/*` — CRUD operations
- `/api/v1/observations/search` — Search with filters
- `/api/v1/sessions/*` — Session lifecycle
- `/api/v1/consolidate`, `/api/v1/reflect` — Background operations
- `/api/v1/health-check`, `/api/v1/status` — Observability
- `GET /` — Built-in dashboard

### Benchmark Suite

Multi-dimensional benchmark framework:
- **Performance**: Recall speed, write throughput, concurrent load
- **Cognitive**: Signal-to-noise ratio, knowledge lifecycle, temporal accuracy
- **Agent**: Workflow effectiveness, cross-session memory, lazy vs perfect recall

### Health Check / Brain Power Score

Computes 0-100% brain utilization across dimensions:
- Memory quality (Core ratio, staleness)
- Search effectiveness (FTS coverage, semantic coverage)
- System health (consolidation recency, error rates)
- Usage patterns (observations/day, active sessions)

## What Core Is NOT

The Core repository explicitly does **not** include:

| Feature | Belongs In | Reason |
|---------|------------|--------|
| Authentication / AuthZ | neurox-enterprise | Multi-tenant security |
| Billing / Plans / Stripe | neurox-platform | Business logic |
| Team management | neurox-enterprise | Org hierarchy |
| Hierarchical namespaces | neurox-enterprise | Complex ACLs |
| Escalation workflows | neurox-enterprise | Approval chains |
| Dashboard analytics | neurox-enterprise | Business metrics |

**Principle**: Core defines interfaces. Enterprise and Platform provide implementations.

## Target Users

### Individual Developers
- Solo developers building AI-powered tools
- Open source contributors
- Researchers experimenting with agent memory
- Hobbyists running local LLMs (Ollama, llama.cpp)

### Open Source Community
- Projects needing a pluggable memory layer
- Frameworks (LangChain, AutoGPT) wanting native integration
- Academic research on agent cognition

## Current Stack

| Component | Technology |
|-----------|------------|
| Language | Go 1.23+ |
| Database | SQLite 3 (WAL mode) |
| Full-Text Search | FTS5 (native) |
| Embeddings | Local: Ollama / Cloud: OpenAI-compatible APIs |
| LLM | Local: Ollama / Cloud: Claude, GPT, Gemini |
| IDs | ULID (oklog/ulid) |
| Config | YAML |
| MCP Protocol | mark3labs/mcp-go |

### Build Requirements

```bash
CGO_ENABLED=1 go build -tags fts5 ./...
```

- **CGO_ENABLED=1**: Required for mattn/go-sqlite3
- **-tags fts5**: Enables native FTS5 support

## Licensing

**MIT License** — free forever for everyone.

- Commercial use: ✅
- Modification: ✅
- Distribution: ✅
- Private use: ✅
- Sublicensing: ✅

No enterprise license required. The core memory engine is and will remain open source.

## Relationship to Enterprise

```
┌─────────────────────────────────────────────────────────┐
│              neurox-platform (private)                  │
│    Billing · Plans · Stripe · Usage Analytics           │
└─────────────────────────────────────────────────────────┘
                            ▲
                            │ imports
┌─────────────────────────────────────────────────────────┐
│             neurox-enterprise (private)                 │
│  Auth · Hierarchy · Escalation · Approval · Dashboard   │
│  PostgreSQL Store · OpenAI Embeddings · Claude LLM      │
└─────────────────────────────────────────────────────────┘
                            ▲
                            │ imports interfaces
┌─────────────────────────────────────────────────────────┐
│              neurox (this repo, public, MIT)            │
│   Store · Embedder · LLMProvider · RecallEngine         │
│   (interfaces defined here, implementations swappable)  │
└─────────────────────────────────────────────────────────┘
```

### Interface Principle

Core defines interfaces:

```go
type Store interface { ... }
type Embedder interface { ... }
type LLMProvider interface { ... }
type RecallEngine interface { ... }
```

**Local Implementation** (Core repo):
- `SQLiteStore` → SQLite + FTS5
- `OllamaEmbedder` → Local embeddings
- `OllamaProvider` → Local LLM

**Enterprise Implementation** (Private repo):
- `PostgresStore` → PostgreSQL + pgvector
- `OpenAIEmbedder` → OpenAI API
- `ClaudeProvider` → Anthropic API

Enterprise imports Core interfaces and provides its own implementations. Core never imports Enterprise.

## Roadmap

### Current (v0.5.x)
- ✅ Three-layer memory model
- ✅ FTS5 + semantic hybrid search
- ✅ Consolidation pipeline
- ✅ MCP protocol over stdio
- ✅ HTTP REST API
- ✅ Git hooks
- ✅ Benchmark suite
- ✅ Health scoring

### Near-term (v0.6.x)
- 🔲 Stable interface contracts (Store, Embedder, LLMProvider)
- 🔲 Plugin architecture for custom embedders
- 🔲 Distributed consolidation (leader election)

### Long-term (v1.0)
- 🔲 Full enterprise interface compatibility
- 🔲 Cloud-native deployments (Kubernetes operator)
- 🔲 Multi-region replication

---

*Neurox Core: Memory for AI that remembers what matters.*

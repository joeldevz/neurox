# Neurox Core — Technical Architecture

## Architecture Overview

Neurox Core is a modular, single-binary application organized into 28 internal packages. The architecture follows clean separation of concerns with clear interface boundaries that allow enterprise implementations to swap components without modifying core code.

```
neurox/
├── main.go              # CLI entry point, subcommand routing
├── internal/
│   ├── api/             # HTTP REST API server
│   ├── benchmark/       # Multi-dimensional benchmark suite
│   ├── classify/        # Content classification
│   ├── config/          # YAML configuration loading
│   ├── consolidate/     # Memory consolidation pipeline
│   ├── contradiction/   # Contradiction detection
│   ├── curate/          # Deep memory curation with LLM
│   ├── db/              # Database connection, schema, migrations
│   ├── decay/           # Ebbinghaus decay curves
│   ├── embed/           # Embedding providers (Ollama, remote)
│   ├── export/          # Import/export (JSON, Markdown)
│   ├── facts/           # Knowledge graph facts
│   ├── filelink/        # File-observation associations
│   ├── graph/           # Graph visualization
│   ├── health/          # Health scoring engine
│   ├── installer/       # Agent setup (Claude, Cursor, etc.)
│   ├── links/           # Observation relationships
│   ├── llm/             # LLM providers (Ollama, remote)
│   ├── mcp/             # MCP protocol server
│   ├── observation/     # Core observation types and storage
│   ├── proactive/       # Context retrieval engine
│   ├── recall/          # Search engine (FTS5 + semantic)
│   ├── reflect/         # Reflection/synthesis engine
│   ├── savepipeline/    # Unified save orchestration
│   ├── session/         # Session lifecycle management
│   ├── telemetry/       # Usage tracking
│   ├── temporal/        # Temporal mention extraction
│   └── updatecheck/     # Version checking
```

## Interface Contracts

The Core **MUST** define clean interfaces so Enterprise can swap implementations. These interfaces are the primary contract between Core and Enterprise layers.

### 1. Store Interface

```go
// Store defines the contract for observation persistence.
// SQLite implementation for local; PostgreSQL implementation for enterprise.
type Store interface {
    Save(ctx context.Context, obs Observation) (Observation, error)
    Update(ctx context.Context, obs Observation) (Observation, error)
    Get(ctx context.Context, id string) (Observation, error)
    SoftDelete(ctx context.Context, id string) error
    Search(ctx context.Context, query SearchQuery) ([]Observation, error)
    
    // Layer management
    Promote(ctx context.Context, id string, fromLayer, toLayer int) error
    GetByLayer(ctx context.Context, layer int, limit int) ([]Observation, error)
    
    // Decay operations
    ApplyDecay(ctx context.Context, epoch int) error
    GetPendingConsolidation(ctx context.Context, limit int) ([]Observation, error)
    
    // Namespace operations
    ListNamespaces(ctx context.Context) ([]string, error)
    GetNamespaceStats(ctx context.Context, namespace string) (NamespaceStats, error)
}
```

**Current Implementation**: `internal/observation/store.go` — SQLite-backed

**Enterprise Implementation**: PostgreSQL + pgvector, with row-level security for multi-tenancy

### 2. Embedder Interface

```go
// Provider generates embeddings for text.
// Ollama implementation for local; OpenAI/Cohere for enterprise.
type Provider interface {
    Embed(ctx context.Context, text string) ([]float32, error)
    EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
    Dimensions() int
    Name() string
}
```

**Current Implementation**: `internal/embed/ollama.go`

**Enterprise Implementation**: OpenAI, Cohere, or custom embedding services

### 3. LLMProvider Interface

```go
// Provider generates text completions from an LLM.
// Ollama implementation for local; Claude/GPT for enterprise.
type Provider interface {
    Complete(ctx context.Context, prompt string) (string, error)
    Name() string
}
```

**Current Implementation**: `internal/llm/ollama.go`

**Enterprise Implementation**: Claude (Anthropic), GPT (OpenAI), Gemini (Google)

### 4. RecallEngine Interface

```go
// Engine defines the contract for memory retrieval.
// FTS5+sqlite-vec locally; pgvector+reranker in cloud.
type RecallEngine interface {
    Search(ctx context.Context, options SearchOptions) ([]Result, error)
    
    // Semantic search only
    SemanticSearch(ctx context.Context, query string, limit int) ([]Result, error)
    
    // FTS search only
    FTSSearch(ctx context.Context, query string, limit int) ([]Result, error)
}
```

**Current Implementation**: `internal/recall/engine.go` — FTS5 primary, semantic boost

### 5. Database Interface (Internal)

```go
// rowQueryer abstracts SQL operations for testing
type rowQueryer interface {
    QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
    QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}
```

Used throughout stores for testability with `*sql.DB`, `*sql.Tx`, or mocks.

## Current Internal Packages

| Package | Purpose | Key Types |
|---------|---------|-----------|
| `observation` | Core memory primitive | `Observation`, `Store`, `Kind`, `ObservationType` |
| `recall` | Search engine | `Engine`, `SearchOptions`, `Result` |
| `consolidate` | Background pipeline | `Pipeline`, `Config`, batch processors |
| `decay` | Ebbinghaus curves | `Engine`, decay calculations |
| `embed` | Vector embeddings | `Provider`, `Queue`, serialization |
| `llm` | Text generation | `Provider`, `Gate`, sanitization |
| `reflect` | Insight synthesis | `Engine`, reflection prompts |
| `mcp` | MCP protocol | `Server`, handlers, tools |
| `api` | HTTP server | `Server`, `Deps`, route handlers |
| `config` | Configuration | `Config`, YAML loading |
| `db` | Database | `Open()`, `BackupWithResult()`, schema |
| `session` | Work sessions | `Manager`, `Session`, lifecycle |
| `facts` | Knowledge graph | `Fact`, `Store`, `Extractor` |
| `temporal` | Time extraction | `Extractor`, `Parser`, `Store` |
| `links` | Relationships | `Link`, `Store`, relationship types |
| `filelink` | File associations | `Store`, git-aware invalidation |
| `contradiction` | Conflict detection | Detection algorithms |
| `proactive` | Context retrieval | `Engine`, `ContextResult` |
| `health` | Health scoring | `Compute()`, dimension scoring |
| `classify` | Content classification | Auto-classification of observations |
| `curate` | Deep curation | `Engine`, LLM-based cleanup |

## MCP Tools

All MCP tools defined in `internal/mcp/tools.go`:

| Tool | Purpose | Handler |
|------|---------|---------|
| `save` | Save observation | `handleSave` |
| `recall` | Search memories | `handleRecall` |
| `context` | Get relevant context | `handleContext` |
| `update` | Update by ID | `handleUpdate` |
| `forget` | Soft-delete | `handleForget` |
| `invalidate` | Mark incorrect | `handleInvalidate` |
| `status` | Brain statistics | `handleStatus` |
| `session_start` | Begin session | `handleSessionStart` |
| `session_end` | End session | `handleSessionEnd` |
| `git_hook` | Report git changes | `handleGitHook` |
| `reflect` | Trigger reflection | `handleReflect` |
| `consolidate` | Force consolidation | `handleConsolidate` |
| `health_check` | Compute health score | `handleHealthCheck` |
| `backup` | Create DB backup | `handleBackup` |
| `curate` | Deep curation | `handleCurate` |

## HTTP API Endpoints

All routes defined in `internal/api/server.go`:

```
GET    /                          # Dashboard
GET    /health                    # Health check
GET    /api/v1/status             # Brain statistics
GET    /api/v1/observations/browse
GET    /api/v1/stats/breakdown

POST   /api/v1/observations       # Create
GET    /api/v1/observations/search
GET    /api/v1/observations/context
GET    /api/v1/observations/{id}  # Get
PUT    /api/v1/observations/{id}  # Update
DELETE /api/v1/observations/{id}  # Forget
POST   /api/v1/observations/{id}/invalidate

POST   /api/v1/sessions           # Start
PUT    /api/v1/sessions/{id}/end  # End

POST   /api/v1/hooks/git          # Git hook
POST   /api/v1/reflect            # Trigger reflection
POST   /api/v1/consolidate        # Force consolidation
POST   /api/v1/curate             # Deep curation
GET    /api/v1/graph              # Graph data
GET    /api/v1/health-check       # Health scoring
POST   /api/v1/backup             # Backup
GET    /api/v1/decay-timeline     # Decay visualization
GET    /api/v1/stats/activity     # Activity metrics

GET    /.well-known/mcp/server-card.json  # MCP discovery
```

## Database Schema

### Core Tables

**observations** — The central memory store:
```sql
id TEXT PRIMARY KEY,
title TEXT NOT NULL,
content TEXT NOT NULL,
observation_type TEXT CHECK (...),
layer INTEGER CHECK (0, 1, 2),
confidence REAL CHECK (0.0-1.0),
importance REAL CHECK (0.0-1.0),
access_count INTEGER DEFAULT 0,
last_accessed TEXT,
repetition_count INTEGER DEFAULT 0,
decay_rate REAL DEFAULT 1.0,
kind TEXT CHECK ('episodic', 'semantic', 'procedural'),
tags TEXT,
namespace TEXT DEFAULT 'default',
staleness TEXT CHECK ('fresh', 'stale', 'revalidated', 'expired'),
consolidation_status TEXT,
retention TEXT CHECK ('operational', 'durable'),
embedding BLOB,
source_surface, source_session_id, source_tool,
created_at, updated_at, deleted_at,
activation_level, consolidation_strength
```

**observation_links** — Relationships between observations:
```sql
id TEXT PRIMARY KEY,
source_id REFERENCES observations(id) ON DELETE CASCADE,
target_id REFERENCES observations(id) ON DELETE CASCADE,
relation_type CHECK ('supersedes', 'contradicts', 'relates_to', 
                     'derived_from', 'validates', 'refines'),
confidence REAL,
created_by CHECK ('agent', 'consolidator', 'user'),
created_at
```

**sessions** — Work session tracking:
```sql
id TEXT PRIMARY KEY,
title, directory, branch, namespace,
status CHECK ('active', 'completed', 'abandoned'),
summary, started_at, ended_at
```

**facts** — Knowledge graph:
```sql
id TEXT PRIMARY KEY,
subject, predicate, object,
observation_id REFERENCES observations(id) ON DELETE SET NULL,
namespace, valid_from, valid_until, superseded_by
```

**reflections** — Synthesized insights:
```sql
id TEXT PRIMARY KEY,
content TEXT NOT NULL,
source_observation_ids TEXT NOT NULL,
namespace, layer, created_at
```

**temporal_mentions** — Extracted time references:
```sql
id TEXT PRIMARY KEY,
observation_id REFERENCES observations(id) ON DELETE CASCADE,
raw_text, mention_kind,
normalized_start, normalized_end, anchor_time,
confidence, created_at
```

### FTS5 Virtual Table

```sql
CREATE VIRTUAL TABLE observations_fts USING fts5(
    id UNINDEXED,
    title, content, tags,
    content=observations,
    content_rowid=rowid
);
```

Triggers maintain FTS index on INSERT/UPDATE/DELETE.

## Build Requirements

### Prerequisites

- Go 1.23+
- CGO_ENABLED=1 (for SQLite)
- SQLite 3 with FTS5 support

### Build Commands

```bash
# Development build
CGO_ENABLED=1 go build -tags fts5 -o neurox .

# Production build
CGO_ENABLED=1 go build -tags fts5 -ldflags="-s -w" -o neurox .

# Run tests
CGO_ENABLED=1 go test -tags fts5 ./...

# Vendor dependencies
go mod vendor
```

### Platform Notes

| Platform | Requirements |
|----------|-------------|
| macOS | `brew install sqlite3` (with FTS5) |
| Linux | `libsqlite3-dev` or build from source |
| Windows | Use WSL2 or pre-built binary |

## Key Patterns

### 1. Store Pattern

Each domain has a `Store` struct:
```go
type Store struct {
    db *sql.DB
    idGenerator IDGenerator
}

func NewStore(db *sql.DB, idGen IDGenerator) *Store
func (s *Store) Save(...) error
```

### 2. ULID IDs

All entities use ULID (Universally Unique Lexicographically Sortable Identifier):
```go
type ulidGenerator struct{}
func (g *ulidGenerator) New() string { return ulid.Make().String() }
```

Benefits:
- Sortable by time (replaces created_at sorting)
- No central coordination needed
- URL-safe encoding

### 3. sql.Tx for ACID

All multi-step operations use transactions:
```go
tx, err := db.BeginTx(ctx, nil)
if err != nil { return err }
defer tx.Rollback() // Safe cleanup

// ... operations ...

return tx.Commit()
```

### 4. Table-Driven Tests

```go
func TestFeature(t *testing.T) {
    tests := []struct {
        name     string
        input    string
        want     string
        wantErr  bool
    }{
        {"valid", "input", "output", false},
        {"invalid", "", "", true},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := Feature(tt.input)
            if (err != nil) != tt.wantErr {
                t.Errorf("error = %v, wantErr %v", err, tt.wantErr)
            }
            if got != tt.want {
                t.Errorf("got %v, want %v", got, tt.want)
            }
        })
    }
}
```

### 5. Embedded SQL Migrations

```go
//go:embed schema.sql
var schemaSQL string

func initSchema(db *sql.DB) error {
    _, err := db.Exec(schemaSQL)
    return err
}
```

## Enterprise Compatibility Requirements

To support enterprise implementations, Core must ensure:

### 1. Interface-Driven Storage

✅ **Current**: Store operations abstracted through `observation.Store`

⚠️ **Needed**: Extract interface definition to `types/` or root package for clean imports

```go
// types/store.go — for enterprise import
type ObservationStore interface {
    Save(ctx context.Context, obs observation.Observation) (observation.Observation, error)
    // ...
}
```

### 2. Injectable Embedder/LLM

✅ **Current**: Auto-detection with fallback to Disabled

✅ **Ready**: Enterprise can inject custom implementations

### 3. Namespace Abstraction

✅ **Current**: Simple string namespaces

⚠️ **Needed**: Document namespace validation rules for hierarchical support

Enterprise may need:
```
namespace: "acme-corp/engineering/backend/api"
```

### 4. Packages Enterprise Will Import

Enterprise implementations should only import:

```go
import (
    "github.com/joeldevz/neurox/internal/observation"
    "github.com/joeldevz/neurox/internal/embed"
    "github.com/joeldevz/neurox/internal/llm"
    "github.com/joeldevz/neurox/internal/recall"
    // Plus any new interface packages we extract
)
```

**Never import from**:
- `internal/db` (enterprise uses PostgreSQL)
- `internal/consolidate` (enterprise may customize pipeline)
- `internal/mcp`, `internal/api` (enterprise has own transports)

### 5. Configuration Extensibility

Current config structure should remain compatible:

```yaml
# config.yaml — works for local and cloud
database:
  path: ./neurox.db  # Local only, ignored by enterprise
  
embeddings:
  provider: ollama   # or "remote"
  ollama_url: ...
  remote_url: ...   # Enterprise uses this
  
llm:
  provider: ollama   # or "remote"
  remote_url: ...   # Enterprise uses this
```

## Deployment Architecture

### Local (Current)

```
┌─────────────────────────────────────┐
│  neurox binary                      │
│  ┌─────────┐  ┌─────────┐          │
│  │ MCP srv │  │ HTTP srv│          │
│  └────┬────┘  └────┬────┘          │
│       └─────────────┘               │
│              │                      │
│       ┌──────┴──────┐              │
│       │  SQLite     │              │
│       │  + FTS5     │              │
│       └─────────────┘              │
└─────────────────────────────────────┘
```

### Enterprise (Target)

```
┌─────────────────────────────────────┐
│  neurox-enterprise binary           │
│  ┌─────────┐  ┌─────────┐          │
│  │ Auth    │  │ Billing │          │
│  │ Gateway │  │ Webhook │          │
│  └────┬────┘  └────┬────┘          │
│       └─────────────┘               │
│              │                      │
│  ┌───────────┴───────────┐          │
│  │   neurox Core (lib)   │          │
│  │  (interfaces only)    │          │
│  └───────────┬───────────┘          │
│              │                      │
│       ┌──────┴──────┐              │
│       │ PostgreSQL  │              │
│       │  + pgvector │              │
│       └─────────────┘              │
└─────────────────────────────────────┘
```

---

*Architecture designed for extensibility. Interfaces over implementations. Local-first, cloud-ready.*

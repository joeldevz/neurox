# Neurox Core — Interface Contracts

## Purpose

This document defines the **contract between neurox core and neurox-enterprise**. These interfaces are the architectural boundary that allows:

- **Core** to remain unaware of enterprise concerns (auth, billing, multi-tenancy)
- **Enterprise** to inject its own implementations (PostgreSQL, OpenAI, Claude)
- **Both** to evolve independently as long as interfaces are honored

## Principle

> Core defines interfaces, Enterprise provides implementations.

```
┌──────────────────────────────────────────────────────────────┐
│                    neurox-enterprise                          │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────┐  │
│  │ PostgresStore│  │OpenAIEmbedder│  │   ClaudeProvider   │  │
│  │  (implements)│  │ (implements) │  │   (implements)     │  │
│  └──────┬──────┘  └──────┬──────┘  └──────────┬──────────┘  │
│         │                │                    │             │
│         └────────────────┼────────────────────┘             │
│                          │                                  │
│              ┌───────────┴───────────┐                      │
│              │   imports interfaces  │                      │
│              └───────────┬───────────┘                      │
└──────────────────────────┼──────────────────────────────────┘
                           │
┌──────────────────────────┼──────────────────────────────────┐
│              neurox core │ (this repo)                       │
│              ┌───────────┴───────────┐                      │
│              │  defines interfaces   │                      │
│              │  ┌─────┐ ┌─────┐     │                      │
│              │  │Store│ │Embedder│   │                      │
│              │  │     │ │        │   │                      │
│              │  │LLM  │ │Recall  │   │                      │
│              │  └─────┘ └─────┘     │                      │
│              └───────────────────────┘                      │
└──────────────────────────────────────────────────────────────┘
```

## Required Interfaces

### 1. ObservationStore

```go
package types

import (
    "context"
    "time"
    
    "github.com/joeldevz/neurox/internal/observation"
)

// ObservationStore defines the contract for observation persistence.
// This is the primary interface that enterprise implementations must satisfy.
type ObservationStore interface {
    // Save persists a new observation to the Buffer layer.
    // If the observation has a TopicKey and an observation with that key
    // already exists in the same namespace, it performs an update instead.
    Save(ctx context.Context, obs observation.Observation) (observation.Observation, error)
    
    // Update modifies an existing observation by ID.
    // Returns sql.ErrNoRows if the observation doesn't exist or is deleted.
    Update(ctx context.Context, obs observation.Observation) (observation.Observation, error)
    
    // Get retrieves a single observation by ID.
    // Returns sql.ErrNoRows if not found.
    Get(ctx context.Context, id string) (observation.Observation, error)
    
    // SoftDelete marks an observation as deleted without removing data.
    // Deleted observations don't appear in search but remain for audit.
    SoftDelete(ctx context.Context, id string) error
    
    // Search performs a filtered search without ranking.
    // Used by the RecallEngine for initial candidate selection.
    Search(ctx context.Context, query SearchQuery) ([]observation.Observation, error)
    
    // SearchOptions provides filtering parameters for Search.
    type SearchQuery struct {
        Namespace       string
        ObservationType observation.ObservationType
        Kind            observation.Kind
        Staleness       string
        Retention       observation.Retention
        IncludeStale    bool
        Files           []string
        Limit           int
    }
    
    // Layer Management
    
    // Promote moves an observation from one layer to another (0→1→2).
    // Also updates consolidation_status and increments modified_epoch.
    Promote(ctx context.Context, id string, fromLayer, toLayer int) error
    
    // GetByLayer returns all observations in a specific layer.
    GetByLayer(ctx context.Context, layer int, limit int) ([]observation.Observation, error)
    
    // Decay & Consolidation
    
    // ApplyDecay applies time-based decay to all non-deleted observations.
    // Uses Ebbinghaus curve: decay_rate * importance * access_count modifier.
    ApplyDecay(ctx context.Context, epoch int) (DecayResult, error)
    
    type DecayResult struct {
        Processed int
        Expired   int
    }
    
    // GetPendingConsolidation returns observations eligible for consolidation.
    // Filters: layer < 2, durable retention, pending status, not stale.
    GetPendingConsolidation(ctx context.Context, limit int) ([]observation.Observation, error)
    
    // Namespace Operations
    
    // ListNamespaces returns all unique namespaces.
    // For enterprise: may filter by tenant/organization.
    ListNamespaces(ctx context.Context) ([]string, error)
    
    // GetNamespaceStats returns statistics for a namespace.
    GetNamespaceStats(ctx context.Context, namespace string) (NamespaceStats, error)
    
    type NamespaceStats struct {
        Total      int
        Buffer     int
        Working    int
        Core       int
        Stale      int
        Expired    int
        Links      int
        Facts      int
        ActiveSessions int
        Embeddings int
    }
    
    // Invalidation
    
    // Invalidate marks an observation as incorrect/stale.
    // Halves confidence, sets staleness='stale'.
    // Optionally creates a replacement observation with 'supersedes' link.
    Invalidate(ctx context.Context, input InvalidationInput) (InvalidationResult, error)
    
    type InvalidationInput struct {
        ObservationID      string
        Reason             string
        ReplacementTitle   string  // optional
        ReplacementContent string  // optional
    }
    
    type InvalidationResult struct {
        InvalidatedID string
        ReplacementID string  // empty if no replacement
        LinkID        string  // empty if no replacement
    }
    
    // Access Tracking
    
    // BumpAccess increments access_count, updates last_accessed,
    // and increases activation_level and consolidation_strength.
    BumpAccess(ctx context.Context, ids []string) error
}
```

**Enterprise Implementation Notes**:
- PostgreSQL table structure mirrors SQLite schema
- Add `tenant_id` column for multi-tenancy (filter all queries)
- Use pgvector for `embedding` column
- Row-level security policies enforce tenant isolation

### 2. Embedder

```go
package embed

import "context"

// Provider generates vector embeddings for text.
// Core provides Ollama implementation; Enterprise uses OpenAI/Cohere.
type Provider interface {
    // Embed returns a float32 vector for the given text.
    // Vector dimension must match Dimensions().
    Embed(ctx context.Context, text string) ([]float32, error)
    
    // EmbedBatch returns vectors for multiple texts.
    // More efficient than multiple Embed() calls for cloud APIs.
    EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
    
    // Dimensions returns the embedding vector size.
    // Must be consistent for all vectors in a database.
    // Common values: 384 (all-MiniLM), 768 (bge-base), 1536 (OpenAI), 3072 (OpenAI large)
    Dimensions() int
    
    // Name returns the provider name for logging and metrics.
    // Examples: "ollama:nomic-embed-text", "openai:text-embedding-3-small"
    Name() string
}

// Disabled is a no-op provider when embeddings are not available.
// Used when no embedding service is configured.
type Disabled struct{}

func (Disabled) Embed(ctx context.Context, text string) ([]float32, error) {
    return nil, fmt.Errorf("embedding provider is disabled")
}

func (Disabled) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
    return nil, fmt.Errorf("embedding provider is disabled")
}

func (Disabled) Dimensions() int { return 0 }
func (Disabled) Name() string    { return "disabled" }

// IsAvailable returns true if the provider is not nil and not Disabled.
func IsAvailable(p Provider) bool {
    if p == nil {
        return false
    }
    _, disabled := p.(Disabled)
    return !disabled
}
```

**Enterprise Implementation Notes**:
- Cache embeddings to reduce API costs
- Implement retry logic with exponential backoff
- Support for dimensionality reduction (OpenAI's `dimensions` param)
- Batch size limits (OpenAI: 2048 items/batch)

### 3. LLMProvider

```go
package llm

import (
    "context"
    "errors"
)

var ErrLLMDisabled = errors.New("LLM provider is disabled")

// Provider generates text completions from an LLM.
// Core provides Ollama implementation; Enterprise uses Claude/GPT.
type Provider interface {
    // Complete sends a prompt and returns the completion text.
    // The prompt is already formatted; implementor just sends to API.
    Complete(ctx context.Context, prompt string) (string, error)
    
    // Name returns the provider name for logging and metrics.
    // Examples: "ollama:llama3.2", "anthropic:claude-3-sonnet", "openai:gpt-4"
    Name() string
}

// Disabled is a no-op provider when no LLM is available.
type Disabled struct{}

func (Disabled) Complete(ctx context.Context, prompt string) (string, error) {
    return "", ErrLLMDisabled
}

func (Disabled) Name() string { return "disabled" }

// IsAvailable returns true if the provider is not nil and not Disabled.
func IsAvailable(p Provider) bool {
    if p == nil {
        return false
    }
    _, disabled := p.(Disabled)
    return !disabled
}
```

**Enterprise Implementation Notes**:
- Support for system prompts and message formatting
- Token counting and rate limiting
- Retry logic with jitter
- Support for streaming responses (future)

### 4. RecallEngine

```go
package recall

import (
    "context"
    "time"
    
    "github.com/joeldevz/neurox/internal/observation"
)

// Engine defines the contract for memory retrieval.
// Core implements FTS5+semantic hybrid; Enterprise may use pgvector+reranker.
type Engine interface {
    // Search performs hybrid search (FTS + semantic) with tri-factor scoring.
    // Scoring: recency × importance × relevance, with semantic boost and
    // temporal intent detection.
    Search(ctx context.Context, options SearchOptions) ([]Result, error)
    
    SearchOptions struct {
        Query           string
        ObservationType observation.ObservationType
        Kind            observation.Kind
        Namespace       string
        Staleness       string
        Retention       string
        IncludeStale    bool
        Debug           bool   // include score breakdown
        Files           []string
        Limit           int
        Weights         ScoreWeights
        Now             time.Time
    }
    
    ScoreWeights struct {
        Recency    float64
        Importance float64
        Relevance  float64
        Semantic   float64
    }
    
    Result struct {
        ID              string
        Title           string
        Content         string
        Score           float64
        Layer           int
        ObservationType observation.ObservationType
        Kind            observation.Kind
        Confidence      float64
        Tags            []string
        Staleness       string
        Retention       string
        LinkedFiles     []string
        SourceSurface   string
        SourceSessionID string
        SourceTool      string
        Breakdown       *ScoreBreakdown // only if Debug=true
    }
    
    ScoreBreakdown struct {
        Recency            float64
        Importance         float64
        Relevance          float64
        SemanticScore      float64
        TemporalMultiplier float64
        CrossSignalBoost   float64
        TypeIntentBoost    float64
        FinalScore         float64
    }
    
    // SemanticSearch performs vector similarity search only.
    // Used for semantic backfill when FTS returns few results.
    SemanticSearch(ctx context.Context, query string, opts SemanticOptions) ([]Result, error)
    
    SemanticOptions struct {
        Namespace       string
        IncludeStale    bool
        Limit           int
    }
    
    // FTSSearch performs full-text search only.
    // Used as primary search path.
    FTSSearch(ctx context.Context, query string, opts FTSOptions) ([]Result, error)
    
    FTSOptions struct {
        Namespace       string
        ObservationType observation.ObservationType
        Kind            observation.Kind
        IncludeStale    bool
        Limit           int
    }
}

// TemporalIntent represents detected time-related intent in queries.
type TemporalIntent struct {
    Kind IntentKind
    Keywords []string
}

type IntentKind string

const (
    IntentNone    IntentKind = "none"
    IntentCurrent IntentKind = "current"  // "current", "now", "latest"
    IntentHistory IntentKind = "history"  // "history", "when", "duration"
)

// DetectTemporalIntent analyzes a query for temporal keywords.
// Exported for use by enterprise search implementations.
func DetectTemporalIntent(query string, now time.Time) TemporalIntent
```

**Enterprise Implementation Notes**:
- pgvector for vector similarity (`<=>` operator for cosine distance)
- Reranker API (Cohere, Voyage) for final result ordering
- Caching layer for frequent queries
- Support for hybrid search weights tuning per-tenant

### 5. SessionManager

```go
package session

import "context"

// Manager defines the contract for session lifecycle.
// Enterprise may add audit logging and tenant validation.
type Manager interface {
    // Start creates a new session, auto-closing any active session
    // in the same namespace.
    Start(ctx context.Context, title, directory, branch, namespace string) (StartResult, error)
    
    StartResult struct {
        SessionID string
        Namespace string
        Abandoned int  // count of abandoned sessions
    }
    
    // End completes a session, extracting observations from the summary.
    End(ctx context.Context, sessionID, summary, surface string) (EndResult, error)
    
    EndResult struct {
        SessionID            string
        ObservationsExtracted int
        Warning              string
    }
    
    // GetActive returns the active session for a namespace, if any.
    GetActive(ctx context.Context, namespace string) (*Session, error)
    
    // List returns sessions for a namespace with optional status filter.
    List(ctx context.Context, namespace string, status *Status, limit int) ([]Session, error)
}

type Session struct {
    ID        string
    Title     string
    Directory string
    Branch    string
    Namespace string
    Status    Status
    Summary   string
    StartedAt time.Time
    EndedAt   *time.Time
}

type Status string

const (
    StatusActive    Status = "active"
    StatusCompleted Status = "completed"
    StatusAbandoned Status = "abandoned"
)
```

### 6. LinkStore

```go
package links

import "context"

// Store defines the contract for observation relationships.
type Store interface {
    // Create adds a link between two observations.
    Create(ctx context.Context, sourceID, targetID, relationType string, confidence float64) (Link, error)
    
    // GetBySource returns all links where the observation is the source.
    GetBySource(ctx context.Context, sourceID string) ([]Link, error)
    
    // GetByTarget returns all links where the observation is the target.
    GetByTarget(ctx context.Context, targetID string) ([]Link, error)
    
    // FindRelation returns a specific link if it exists.
    FindRelation(ctx context.Context, sourceID, targetID, relationType string) (*Link, error)
    
    // Delete removes a link by ID.
    Delete(ctx context.Context, linkID string) error
}

type Link struct {
    ID           string
    SourceID     string
    TargetID     string
    RelationType RelationType
    Confidence   float64
    CreatedBy    CreatedBy
    CreatedAt    time.Time
}

type RelationType string

const (
    RelationSupersedes  RelationType = "supersedes"
    RelationContradicts RelationType = "contradicts"
    RelationRelatesTo   RelationType = "relates_to"
    RelationDerivedFrom RelationType = "derived_from"
    RelationValidates   RelationType = "validates"
    RelationRefines     RelationType = "refines"
)

type CreatedBy string

const (
    CreatedByAgent        CreatedBy = "agent"
    CreatedByConsolidator CreatedBy = "consolidator"
    CreatedByUser         CreatedBy = "user"
)
```

## Implementation Matrix

| Interface | Local (Core) | Enterprise (Injected) |
|-----------|--------------|----------------------|
| `ObservationStore` | `internal/observation.Store` (SQLite) | `PostgresStore` (PostgreSQL + pgvector) |
| `Embedder` | `internal/embed.OllamaProvider` | `OpenAIEmbedder`, `CohereEmbedder` |
| `LLMProvider` | `internal/llm.OllamaProvider` | `ClaudeProvider`, `GPTProvider` |
| `RecallEngine` | `internal/recall.Engine` (FTS5+semantic) | `PostgresRecallEngine` (pgvector+reranker) |
| `SessionManager` | `internal/session.Manager` | `AuditedSessionManager` (with audit logs) |
| `LinkStore` | `internal/links.Store` | `PostgresLinkStore` |

## Dependency Rules

```
┌─────────────────────────────────────────────────────────┐
│  neurox-enterprise                                      │
│  ┌─────────────────────────────────────────────────┐   │
│  │ imports:                                        │   │
│  │   github.com/joeldevz/neurox/internal/observation│   │
│  │   github.com/joeldevz/neurox/internal/embed      │   │
│  │   github.com/joeldevz/neurox/internal/llm        │   │
│  │   github.com/joeldevz/neurox/internal/recall     │   │
│  │   github.com/joeldevz/neurox/internal/session    │   │
│  │   github.com/joeldevz/neurox/internal/links      │   │
│  └─────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────┘
                            ▲
                            │ imports interfaces
┌───────────────────────────┼─────────────────────────────┐
│  neurox core              │                             │
│  ┌────────────────────────┼───────────────────────────┐ │
│  │ defines interfaces     │                           │ │
│  │ provides local impl    │                           │ │
│  │                        ▼                           │ │
│  │ NEVER imports enterprise                           │ │
│  └────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────┘
```

## Configuration Interface

Enterprise implementations may need additional config. Core config should be extensible:

```go
package config

type Config struct {
    Database    DatabaseConfig
    Embeddings  EmbeddingsConfig
    LLM         LLMConfig
    Consolidation ConsolidationConfig
    
    // Enterprise extensions (ignored by core, used by enterprise)
    Enterprise EnterpriseConfig `yaml:"enterprise,omitempty"`
}

type EnterpriseConfig struct {
    TenantID      string `yaml:"tenant_id"`
    AuthProvider  string `yaml:"auth_provider"`
    AuditEndpoint string `yaml:"audit_endpoint"`
}
```

## Version Compatibility

| Core Version | Enterprise Compatibility |
|--------------|-------------------------|
| 0.5.x | Experimental interfaces |
| 0.6.x | Stable v1 interfaces |
| 1.0.x | Long-term support (LTS) |

**Interface Change Policy**:
- Minor versions: Additive changes only (new methods)
- Major versions: May remove or modify interfaces

Enterprise implementations should pin to major versions:
```go
// go.mod
github.com/joeldevz/neurox v0.6.x
```

---

*Interfaces are contracts. Honor them, and Core and Enterprise evolve in harmony.*

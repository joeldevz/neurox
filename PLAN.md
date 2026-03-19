# Plan: Neurox — Brain-Inspired Memory Engine for AI Agents

## Goal
Construir **neurox**, un motor de memoria para AI coding agents inspirado en el cerebro humano. Modelo de 3 capas (Buffer → Working → Core), hybrid search (FTS5 + embeddings + relational links), decay Ebbinghaus, consolidación async en background, reflection, proactive recall, contradiction detection, git-linked staleness, write gate, tri-factor scoring, y observation links. Un solo binario Go, MCP server + HTTP API, zero dependencies externas obligatorias.

## Business Context
- **Usuarios**: Desarrolladores que usan AI coding agents (Claude Code, Cursor, Copilot, Windsurf, OpenCode, etc.)
- **Problema**: Los agentes no tienen memoria persistente real. Engram (que usamos hoy) es flat — sin capas, sin decay, sin embeddings, sin consolidación automática. Los agentes repiten errores y olvidan decisiones.
- **Outcome esperado**: El agente "recuerda" como un humano — lo importante se queda, lo trivial se olvida, las contradicciones se resuelven, y el sistema sugiere memorias relevantes proactivamente.
- **Diferenciadores vs competencia**:
  - vs Engram: 3 capas + embeddings + decay + consolidación auto + observation links + git staleness
  - vs Mem0: local-first, zero API keys obligatorias, binario único, code-aware (file links)
  - vs engram-rs: Go (más accesible), MCP tools más ricos, reflection + proactive recall + write gate
  - vs Cortexia: OSS, sin pricing tiers, sin SNN overhead
- **Licencia**: MIT
- **Non-functional**: búsqueda <5ms, writes <1ms, binario <20MB, RAM <150MB
- **Future**: v2 añadirá multi-user/team (PostgreSQL, shared memory, RBAC). El diseño de v1 no debe cerrar esa puerta — namespace isolation lo prepara.

## Technical Context

### Stack
- **Lenguaje**: Go (binario único, goroutines para background work)
- **Storage**: SQLite WAL + FTS5 + HNSW in-memory vector index
- **Embeddings**: Pluggable providers — Ollama (default) > ONNX Runtime > Remote API > disabled
- **LLM gate**: Pluggable — Ollama > Remote > disabled (mode: auto/full/off)
- **Interfaces**: MCP server (stdio) + HTTP API
- **Config**: `~/.config/neurox/config.yaml` + `~/.config/neurox/neurox.db`

### Referencia de implementación
- **engram-rs** (kael-bit/engram-rs): 634 commits, Rust, modelo Atkinson-Shiffrin completo
- **Engram** (Gentleman-Programming): Go, MCP tools, FTS5 — referencia de API design
- **Mem0**: write gate pipeline (embed → top-10 similares → LLM decide ADD|UPDATE|DELETE|NOOP)
- **Generative Agents (Stanford)**: reflection mechanism, tri-factor retrieval (recency × importance × relevance)
- **Graphiti/Zep**: temporal knowledge graph con valid_from/valid_until/superseded_by
- **CrewAI**: semantic dedup (cosine ≥ 0.85), importance scoring composite, extract_memories() pattern

### Research incorporado (de la fase enterprise, adaptado a single-user)
- **Observation types**: decision, bugfix, discovery, pattern, gotcha, config, preference, question
- **Structured content**: What/Why/Where/Learned format (mejora sobre plain text)
- **Topic key upserts**: same topic_key = update in place, no duplicates
- **File-linked staleness**: observations vinculadas a file paths, git hooks marcan stale cuando cambian
- **Observation links**: supersedes, contradicts, relates_to, derived_from, validates, refines
- **Write gate**: dedup check al escribir (cosine > 0.92 → warn, 0.75-0.92 → LLM decide)
- **Tri-factor scoring**: recency × importance × relevance (no solo BM25)
- **Invalidation tool**: agentes pueden reportar memorias incorrectas con replacement
- **Git hook integration**: post-commit hook reporta archivos cambiados → marca memorias stale
- **Activation score**: Ebbinghaus decay + access boost (para GC y retrieval ranking)
- **Grafos**: NO ahora. Tablas de relaciones (observation_links, file_observations) + queries recursivas. Migrar a graph DB cuando haya >50k nodos y >30% queries necesiten multi-hop.

### Modelo de datos (schema SQLite)

```sql
-- =============================================================================
-- CORE: Observations (the memory unit)
-- =============================================================================
CREATE TABLE observations (
    id              TEXT PRIMARY KEY,        -- ULID for temporal ordering
    
    -- Content (structured)
    title           TEXT NOT NULL,            -- short, searchable ("Fixed N+1 in UserList")
    content         TEXT NOT NULL,            -- structured: What/Why/Where/Learned
    observation_type TEXT NOT NULL DEFAULT 'discovery'
                    CHECK (observation_type IN (
                        'decision', 'bugfix', 'discovery', 'pattern',
                        'gotcha', 'config', 'preference', 'question'
                    )),
    
    -- Brain layers
    layer           INTEGER NOT NULL DEFAULT 0 CHECK (layer IN (0, 1, 2)),
                    -- 0=Buffer, 1=Working, 2=Core
    
    -- Quality signals
    confidence      REAL NOT NULL DEFAULT 0.7 CHECK (confidence BETWEEN 0.0 AND 1.0),
    importance      REAL NOT NULL DEFAULT 0.5 CHECK (importance BETWEEN 0.0 AND 1.0),
    access_count    INTEGER NOT NULL DEFAULT 0,
    last_accessed   TEXT,                    -- ISO8601
    repetition_count INTEGER NOT NULL DEFAULT 0,
    decay_rate      REAL NOT NULL DEFAULT 1.0,
    
    -- Classification
    kind            TEXT NOT NULL DEFAULT 'semantic'
                    CHECK (kind IN ('episodic', 'semantic', 'procedural')),
    tags            TEXT,                    -- comma-separated, also indexed in FTS5
    namespace       TEXT NOT NULL DEFAULT 'default',
    source          TEXT,                    -- 'agent', 'user', 'consolidator', 'reflection'
    
    -- Topic key for upserts (same topic_key = update, not duplicate)
    topic_key       TEXT,
    
    -- Temporal validity
    valid_from      TEXT NOT NULL DEFAULT (datetime('now')),
    valid_until     TEXT,                    -- NULL = still valid
    invalidated_by  TEXT REFERENCES observations(id),
    
    -- Staleness tracking
    staleness       TEXT NOT NULL DEFAULT 'fresh'
                    CHECK (staleness IN ('fresh', 'stale', 'revalidated', 'expired')),
    
    -- Consolidation tracking
    consolidation_status TEXT NOT NULL DEFAULT 'pending'
                    CHECK (consolidation_status IN ('pending', 'promoted', 'rejected', 'rejected-2', 'rejected-final')),
    rejection_epoch INTEGER,                 -- epoch when last rejected (for 3-strike retry)
    
    -- Embedding (filled async by background worker)
    embedding       BLOB,                    -- f32 vector serialized
    
    -- Timestamps
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now')),
    deleted_at      TEXT,                    -- soft delete only
    modified_epoch  INTEGER NOT NULL DEFAULT 0
);

-- Partial unique index: one active observation per topic_key per namespace
CREATE UNIQUE INDEX uq_active_topic_key 
    ON observations(namespace, topic_key)
    WHERE topic_key IS NOT NULL AND deleted_at IS NULL;

-- Query indexes
CREATE INDEX idx_obs_layer ON observations(layer) WHERE deleted_at IS NULL;
CREATE INDEX idx_obs_namespace ON observations(namespace) WHERE deleted_at IS NULL;
CREATE INDEX idx_obs_type ON observations(observation_type) WHERE deleted_at IS NULL;
CREATE INDEX idx_obs_kind ON observations(kind) WHERE deleted_at IS NULL;
CREATE INDEX idx_obs_staleness ON observations(staleness) WHERE staleness = 'stale' AND deleted_at IS NULL;
CREATE INDEX idx_obs_created ON observations(created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX idx_obs_importance ON observations(importance DESC) WHERE deleted_at IS NULL;
CREATE INDEX idx_obs_consolidation ON observations(consolidation_status) WHERE consolidation_status = 'pending' AND deleted_at IS NULL;

-- FTS5 for keyword search
CREATE VIRTUAL TABLE observations_fts USING fts5(
    id UNINDEXED,
    title,
    content,
    tags,
    content=observations,
    content_rowid=rowid
);

-- Triggers to keep FTS5 in sync
CREATE TRIGGER trg_obs_ai AFTER INSERT ON observations BEGIN
    INSERT INTO observations_fts(rowid, id, title, content, tags) 
    VALUES (new.rowid, new.id, new.title, new.content, new.tags);
END;
CREATE TRIGGER trg_obs_ad AFTER DELETE ON observations BEGIN
    INSERT INTO observations_fts(observations_fts, rowid, id, title, content, tags) 
    VALUES ('delete', old.rowid, old.id, old.title, old.content, old.tags);
END;
CREATE TRIGGER trg_obs_au AFTER UPDATE ON observations BEGIN
    INSERT INTO observations_fts(observations_fts, rowid, id, title, content, tags) 
    VALUES ('delete', old.rowid, old.id, old.title, old.content, old.tags);
    INSERT INTO observations_fts(rowid, id, title, content, tags) 
    VALUES (new.rowid, new.id, new.title, new.content, new.tags);
END;

-- =============================================================================
-- RELATIONS (graph-like, relational — migratable to property graph later)
-- =============================================================================

-- Links between observations
CREATE TABLE observation_links (
    id              TEXT PRIMARY KEY,        -- ULID
    source_id       TEXT NOT NULL REFERENCES observations(id) ON DELETE CASCADE,
    target_id       TEXT NOT NULL REFERENCES observations(id) ON DELETE CASCADE,
    relation_type   TEXT NOT NULL CHECK (relation_type IN (
        'supersedes',       -- source replaces target (newer decision)
        'contradicts',      -- source conflicts with target (needs resolution)
        'relates_to',       -- general semantic relationship
        'derived_from',     -- source was synthesized from target (reflection)
        'validates',        -- source confirms target is still true
        'refines'           -- source adds detail to target
    )),
    confidence      REAL DEFAULT 1.0 CHECK (confidence BETWEEN 0.0 AND 1.0),
    created_by      TEXT NOT NULL DEFAULT 'consolidator'
                    CHECK (created_by IN ('agent', 'consolidator', 'user')),
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    
    CHECK (source_id != target_id),
    UNIQUE (source_id, target_id, relation_type)
);

CREATE INDEX idx_links_source ON observation_links(source_id);
CREATE INDEX idx_links_target ON observation_links(target_id);
CREATE INDEX idx_links_type ON observation_links(relation_type);

-- Observations linked to files in the codebase (for git-linked staleness)
CREATE TABLE file_observations (
    id              TEXT PRIMARY KEY,        -- ULID
    observation_id  TEXT NOT NULL REFERENCES observations(id) ON DELETE CASCADE,
    file_path       TEXT NOT NULL,            -- relative to repo root
    
    -- Git context
    commit_sha_from TEXT,                    -- commit when this link was created
    commit_sha_until TEXT,                   -- commit when this was invalidated (NULL = current)
    
    -- Temporal validity
    valid_from      TEXT NOT NULL DEFAULT (datetime('now')),
    valid_until     TEXT,                    -- NULL = still valid
    
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_file_obs_file ON file_observations(file_path);
CREATE INDEX idx_file_obs_observation ON file_observations(observation_id);
CREATE INDEX idx_file_obs_valid ON file_observations(file_path) WHERE valid_until IS NULL;

-- =============================================================================
-- SESSIONS
-- =============================================================================
CREATE TABLE sessions (
    id          TEXT PRIMARY KEY,            -- ULID
    title       TEXT,
    directory   TEXT,
    branch      TEXT,
    namespace   TEXT NOT NULL DEFAULT 'default',
    status      TEXT NOT NULL DEFAULT 'active'
                CHECK (status IN ('active', 'completed', 'abandoned')),
    summary     TEXT,                        -- structured: Goal/Discoveries/Accomplished/Next
    started_at  TEXT NOT NULL DEFAULT (datetime('now')),
    ended_at    TEXT
);

CREATE INDEX idx_sessions_status ON sessions(status) WHERE status = 'active';
CREATE INDEX idx_sessions_namespace ON sessions(namespace);

-- =============================================================================
-- FACTS (knowledge triples with temporal validity)
-- =============================================================================
CREATE TABLE facts (
    id              TEXT PRIMARY KEY,        -- ULID
    subject         TEXT NOT NULL,
    predicate       TEXT NOT NULL,
    object          TEXT NOT NULL,
    observation_id  TEXT REFERENCES observations(id) ON DELETE SET NULL,
    namespace       TEXT NOT NULL DEFAULT 'default',
    valid_from      TEXT NOT NULL DEFAULT (datetime('now')),
    valid_until     TEXT,
    superseded_by   TEXT REFERENCES facts(id),
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_facts_subject ON facts(subject);
CREATE INDEX idx_facts_object ON facts(object);
CREATE INDEX idx_facts_observation ON facts(observation_id);
CREATE INDEX idx_facts_namespace ON facts(namespace) WHERE valid_until IS NULL;

-- =============================================================================
-- CONSOLIDATION TRACKING
-- =============================================================================
CREATE TABLE consolidation_runs (
    id                      TEXT PRIMARY KEY,
    status                  TEXT NOT NULL DEFAULT 'running'
                            CHECK (status IN ('running', 'completed', 'failed')),
    epoch                   INTEGER NOT NULL,
    observations_processed  INTEGER DEFAULT 0,
    observations_promoted   INTEGER DEFAULT 0,
    observations_deduped    INTEGER DEFAULT 0,
    contradictions_found    INTEGER DEFAULT 0,
    reflections_created     INTEGER DEFAULT 0,
    started_at              TEXT NOT NULL DEFAULT (datetime('now')),
    completed_at            TEXT,
    error_message           TEXT,
    llm_tokens_used         INTEGER DEFAULT 0
);

-- =============================================================================
-- REFLECTIONS (high-level syntheses)
-- =============================================================================
CREATE TABLE reflections (
    id                  TEXT PRIMARY KEY,        -- ULID
    content             TEXT NOT NULL,
    source_observation_ids TEXT NOT NULL,         -- JSON array of observation IDs
    namespace           TEXT NOT NULL DEFAULT 'default',
    layer               INTEGER NOT NULL DEFAULT 2, -- reflections are Core by default
    created_at          TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_reflections_namespace ON reflections(namespace);
```

### MCP Tools

```
save            — guardar observación (→ Buffer, async embed, write gate check)
recall          — buscar memorias (hybrid: FTS + semantic + tri-factor scoring)
context         — obtener contexto reciente (Core + Working + file-linked)
update          — actualizar observación existente
forget          — soft-delete observación
invalidate      — reportar observación incorrecta + optional replacement
reflect         — sintetizar memorias en insights de alto nivel
session_start   — iniciar sesión de trabajo (auto-returns context)
session_end     — cerrar sesión con summary (auto-extracts observations)
git_hook        — reportar archivos cambiados (marca stale)
status          — estadísticas del cerebro (capas, counts, health)
```

---

## Implementation Steps

### Step 1: Project scaffold + SQLite + config
- **What**: Inicializar el proyecto Go con módulos, estructura de directorios, sistema de configuración (YAML), y schema SQLite con migrations.
- **Why**: Fundación sobre la que se construye todo. Sin DB y config, nada funciona.
- **Where**: 
  - `go.mod`, `main.go`
  - `internal/config/` — carga YAML, defaults, env vars
  - `internal/db/` — SQLite connection pool (WAL mode), schema creation, migrations
  - `internal/db/schema.sql` — todas las tablas del schema arriba
- **Acceptance**: 
  - `go build` produce un binario que arranca sin errores
  - Crea `~/.config/neurox/neurox.db` con el schema completo (observations, observation_links, file_observations, sessions, facts, consolidation_runs, reflections, FTS5)
  - Lee config de `~/.config/neurox/config.yaml` o usa defaults
  - Tests unitarios de config loading y schema creation pasan
- **Notes**: Usar `mattn/go-sqlite3` con CGO para FTS5 support. Consultar Context7 para API de `go-sqlite3` si es necesario.
- **Status**: [x] done

### Step 2: Observation model + save with write gate
- **What**: Implementar el modelo de Observation con structured content (title + content + observation_type + kind + confidence + topic_key + tags + files) y el write path con write gate: save inserta en Buffer (layer=0), crea file_observations si files provided, y ejecuta topic_key upsert logic.
- **Why**: Es el hot path — todo empieza aquí. Debe ser sync y <1ms para el insert, con write gate check async si embeddings disponibles.
- **Where**:
  - `internal/observation/` — Observation struct, ObservationType, Kind enums
  - `internal/observation/store.go` — SQLite repository (Save, Update, Get, SoftDelete)
  - `internal/observation/writegate.go` — write gate: dedup check contra existentes
  - `internal/filelink/` — FileObservation store (link observations to files)
- **Acceptance**:
  - `save` inserta observación en Buffer con FTS5 sync en <1ms
  - Structured content: title (required), content (required), observation_type, kind, confidence, tags, files
  - Topic key upsert: si topic_key ya existe (activo, mismo namespace) → UPDATE en lugar de INSERT
  - Si `files` provided: crea file_observations vinculadas
  - Write gate (async, non-blocking): si embeddings disponibles, check cosine > 0.92 contra existentes → warn (no block)
  - Auto-genera ULID (ordenable temporalmente)
  - Namespace default si no se especifica
  - observation_type default = "discovery", kind default = "semantic", confidence default = 0.7
  - Tests: save básico, save con topic_key upsert, save con files, save con tags
- **Status**: [x] done

### Step 3: Recall — FTS5 keyword search + tri-factor scoring
- **What**: Implementar `recall` con FTS5 (BM25) como base, tri-factor scoring (recency × importance × relevance), activation boost on recall, y filtros por observation_type, kind, namespace, staleness, files.
- **Why**: Sin recall, la memoria es write-only. Es la feature más usada.
- **Where**:
  - `internal/recall/` — RecallEngine con Search()
  - `internal/recall/scoring.go` — tri-factor scoring: `score = α·recency + β·importance + γ·relevance`
  - `internal/recall/fts.go` — FTS5 query builder con BM25
  - `internal/recall/filters.go` — filtros por type, kind, namespace, staleness, files
- **Acceptance**:
  - `recall "auth"` devuelve observaciones rankeadas por tri-factor score
  - Tri-factor: recency (exponential decay, half-life 30 days) × importance × FTS relevance (BM25)
  - Pesos configurables: default α=0.3, β=0.3, γ=0.4
  - Cada recall incrementa access_count y actualiza last_accessed
  - Excluye deleted_at IS NOT NULL y staleness = 'expired' por defecto
  - Filtros opcionales: observation_type, kind, namespace, include_stale, files (via file_observations JOIN)
  - Devuelve: id, title, content, score, layer, observation_type, kind, confidence, tags, staleness, linked_files
  - Respeta límite de resultados (default 10, max 50)
  - Tests: búsqueda por keyword, ranking por importance, filtro por type, filtro por files, activation boost
- **Status**: [x] done

### Step 4: Observation links + invalidation
- **What**: Implementar observation_links (supersedes, contradicts, relates_to, derived_from, validates, refines) y el tool `invalidate` que marca observaciones como stale y opcionalmente crea replacement con link `supersedes`.
- **Why**: Los links son la base para contradiction detection, reflection provenance, y traversal de conocimiento relacionado. Invalidation permite que agentes reporten errores.
- **Where**:
  - `internal/links/` — ObservationLink store (Create, GetBySource, GetByTarget, Traverse)
  - `internal/links/traverse.go` — recursive traversal (max depth 5)
  - `internal/observation/invalidate.go` — invalidation logic
- **Acceptance**:
  - Crear links entre observaciones con relation_type validado
  - `invalidate(observation_id, reason)` → marca staleness='stale', confidence *= 0.5
  - `invalidate(observation_id, reason, replacement_title, replacement_content)` → crea nueva observación + link supersedes
  - Traverse: dado un observation_id, obtener related observations hasta depth N (recursive query)
  - Recall puede incluir related observations (boost score si linked)
  - Tests: crear links, traverse 1-hop, traverse multi-hop, invalidate sin replacement, invalidate con replacement
- **Status**: [x] done

### Step 5: MCP Server (stdio)
- **What**: Implementar MCP server sobre stdio con todos los tools: `save`, `recall`, `context`, `update`, `forget`, `invalidate`, `reflect`, `session_start`, `session_end`, `git_hook`, `status`.
- **Why**: Es la interfaz principal — así se conectan Claude Code, Cursor, OpenCode, etc.
- **Where**:
  - `internal/mcp/` — MCP server, tool handlers, JSON-RPC
  - `internal/mcp/server.go` — stdio transport
  - `internal/mcp/tools.go` — tool definitions con inputSchema completos
  - `internal/mcp/handlers.go` — handler per tool
- **Acceptance**:
  - Arranca con `neurox mcp` como MCP server sobre stdio
  - Soporta protocol MCP (JSON-RPC 2.0, initialize, tools/list, tools/call)
  - Tool schemas incluyen todos los parámetros documentados con descriptions
  - `save`: title (required), content (required), observation_type, kind, confidence, topic_key, tags, files, session_id
  - `recall`: query (required), observation_type, kind, namespace, files, tags, include_stale, limit
  - `context`: namespace, files, limit — returns recent + high-importance + file-linked
  - `invalidate`: observation_id (required), reason (required), replacement_title, replacement_content
  - `git_hook`: changed_files (required), commit_sha (required), branch
  - `session_start`: title, directory, branch, namespace — returns session_id + auto-context
  - `session_end`: session_id, summary (required) — marks completed
  - `status`: returns layer counts, embedding health, staleness stats, consolidation last run
  - Se puede configurar en Claude Code: `"command": "neurox", "args": ["mcp"]`
  - Test de integración: send JSON-RPC → validate response per tool
- **Notes**: Consultar Context7 para `github.com/mark3labs/mcp-go` o implementar el protocolo minimal directamente.
- **Status**: [x] done

### Step 6: HTTP API
- **What**: Exponer los mismos endpoints como HTTP REST API para integración con herramientas que no soportan MCP (dashboards, scripts, git hooks).
- **Why**: Flexibilidad — web dashboards, scripts, integraciones custom, git post-commit hooks.
- **Where**:
  - `internal/api/` — HTTP router, handlers, middleware
  - `internal/api/server.go` — HTTP server con graceful shutdown
  - `internal/api/handlers.go` — endpoints REST
- **Acceptance**:
  - `neurox serve` arranca HTTP server en puerto configurable (default 7437)
  - Endpoints:
    - `POST /api/v1/observations` → save
    - `GET  /api/v1/observations/search?q=...&type=...&kind=...&files=...` → recall
    - `GET  /api/v1/observations/context?namespace=...&files=...` → context
    - `GET  /api/v1/observations/:id` → get
    - `PUT  /api/v1/observations/:id` → update
    - `DELETE /api/v1/observations/:id` → forget (soft-delete)
    - `POST /api/v1/observations/:id/invalidate` → invalidate
    - `POST /api/v1/sessions` → session_start
    - `PUT  /api/v1/sessions/:id/end` → session_end
    - `POST /api/v1/hooks/git` → git_hook
    - `POST /api/v1/reflect` → reflect
    - `GET  /api/v1/status` → status
    - `GET  /health` → health check
  - JSON request/response
  - CORS headers para uso desde browser
  - Tests de integración HTTP
- **Status**: [x] done

### Step 7: Git hook integration + staleness engine
- **What**: Implementar el `git_hook` tool/endpoint que recibe archivos cambiados en un commit y marca observaciones vinculadas como stale. Incluir script instalable de post-commit hook.
- **Why**: Sin esto, las memorias sobre código que cambió siguen apareciendo como válidas. Git-linked staleness es un diferenciador clave.
- **Where**:
  - `internal/staleness/` — StalenessEngine
  - `internal/staleness/gitlinked.go` — mark stale by changed files
  - `internal/staleness/decay.go` — Ebbinghaus activation score
  - `scripts/post-commit` — git hook script que llama al HTTP endpoint
- **Acceptance**:
  - `git_hook(changed_files, commit_sha)` → marca stale todas las observaciones vinculadas a esos files
  - Staleness: confidence *= 0.5, staleness = 'stale'
  - Actualiza file_observations.commit_sha_until para las file links afectadas
  - `recall` y `context` excluyen stale por defecto (opt-in con include_stale)
  - Script post-commit: `git diff --name-only HEAD~1` → `curl POST /api/v1/hooks/git`
  - `neurox install-hook` command que copia el script al repo actual
  - Activation score function: `base * e^(-0.1 * days_since_access) + 0.3 * ln(access_count + 1)`
  - Tests: mark stale, verify exclusion from recall, verify activation score calculation
- **Status**: [x] done

### Step 8: Embedding provider system + async embed queue
- **What**: Implementar la interfaz de embedding providers (Ollama, ONNX, Remote, Disabled) y el embed queue async que procesa batches cada 500ms.
- **Why**: Sin embeddings, solo hay keyword search. Con embeddings, hay búsqueda semántica y write gate real.
- **Where**:
  - `internal/embed/` — EmbedProvider interface, queue
  - `internal/embed/ollama.go` — Ollama provider (POST /api/embed)
  - `internal/embed/onnx.go` — ONNX Runtime provider (stub, futuro)
  - `internal/embed/remote.go` — OpenAI-compatible API provider
  - `internal/embed/queue.go` — async channel, batch every 500ms, retry with backoff
- **Acceptance**:
  - Auto-detect: si Ollama corre en localhost:11434 → usarlo; si no → disabled
  - Queue acepta observation IDs, procesa en batches de hasta 50
  - Embeddings guardados como BLOB en SQLite (f32 vector serializado)
  - Retry con exponential backoff (max 3 retries)
  - Modo disabled: todo funciona, solo sin búsqueda semántica ni write gate dedup
  - Config permite forzar provider: `embedding.provider: ollama|onnx|openai|disabled`
  - Tests unitarios con mock provider
- **Status**: [x] done

### Step 9: Semantic search + HNSW index + hybrid recall
- **What**: Agregar búsqueda semántica al RecallEngine: HNSW in-memory index, combinar con FTS5 para hybrid recall con cross-signal boosting. Actualizar tri-factor scoring para usar cosine similarity como componente de relevance.
- **Why**: FTS solo no captura sinónimos ni paráfrasis. Hybrid es el estado del arte (+26% accuracy según Mem0).
- **Where**:
  - `internal/recall/semantic.go` — HNSW index, vector search
  - `internal/recall/hybrid.go` — combinar FTS + semantic + cross-signal boosting
  - `internal/recall/engine.go` — actualizar RecallEngine, tri-factor con semantic relevance
- **Acceptance**:
  - Si embeddings disponibles: hybrid search (FTS + semantic)
  - Si no: degradación graceful a FTS-only (ya funciona desde Step 3)
  - Tri-factor actualizado: relevance component = max(FTS BM25 normalized, cosine similarity)
  - Cross-signal boosting: memorias que aparecen en FTS Y semantic → score boost
  - HNSW index se reconstruye al iniciar desde embeddings en SQLite
  - Write gate ahora funcional: cosine > 0.92 → warn, 0.75-0.92 → LLM decide (si available)
  - Tests: hybrid search vs FTS-only, cross-signal boost, write gate dedup detection
- **Status**: [x] done

### Step 10: Decay engine + Ebbinghaus curves
- **What**: Implementar el motor de decay con curva de Ebbinghaus diferenciada por kind (episodic/semantic/procedural), decay floor, activation boost, y GC de observaciones muertas.
- **Why**: Sin decay, la memoria crece infinitamente y el recall se degrada. El cerebro olvida lo que no usa.
- **Where**:
  - `internal/decay/` — DecayEngine
  - `internal/decay/ebbinghaus.go` — curva de decay por kind
  - `internal/decay/gc.go` — garbage collection de observaciones bajo threshold
- **Acceptance**:
  - `importance *= (1 - 0.02 × kind_ratio)` cada epoch
  - kind_ratio: episodic=1.0, semantic=0.6, procedural=0.2
  - Decay floor = 0.01 (nunca llega a 0)
  - Activation boost on recall = +0.03 (cap 1.0)
  - Memorias en Core (layer=2) no reciben decay (permanentes)
  - GC: soft-delete observaciones con activation_score < 0.1 y age > 30 days (configurable)
  - Tests: verificar decay rates por kind, floor, activation boost, GC threshold
- **Status**: [x] done

### Step 11: Background consolidation pipeline
- **What**: Implementar el background worker que ejecuta el pipeline de consolidación periódicamente: epoch++, promote Buffer→Working, promote Working→Core, eviction, dedup, decay.
- **Why**: Es el corazón del sistema — transforma el Buffer caótico en Working organizado y Core permanente.
- **Where**:
  - `internal/consolidate/` — Consolidator, pipeline steps
  - `internal/consolidate/pipeline.go` — orchestrator del pipeline
  - `internal/consolidate/promote.go` — Buffer→Working (score threshold + kind rules), Working→Core (access_count + age)
  - `internal/consolidate/dedup.go` — merge near-duplicates (cosine > 0.85 si embeddings available)
  - `internal/consolidate/eviction.go` — buffer cap (200), drop decayed (importance < 0.01)
- **Acceptance**:
  - Goroutine que corre cada 30 min (configurable)
  - Pipeline steps en orden: epoch++ → decay → promote → dedup → evict → GC
  - Buffer→Working: score threshold, o kind = procedural (auto-promote)
  - Working→Core: acceso sostenido (access_count > threshold) + edad > N epochs
  - Buffer cap = 200 (configurable), evict lowest activation score
  - Dedup: merge observaciones con cosine similarity > 0.85 → keep highest importance, link `supersedes`
  - Logs each run in consolidation_runs table
  - Graceful shutdown: pipeline completa paso actual antes de parar
  - Tests: promotion logic, eviction, dedup, run tracking
- **Status**: [x] done

### Step 12: LLM provider system + quality gate
- **What**: Implementar la interfaz de LLM providers (Ollama, Remote, Disabled) y el quality gate que enriquece la consolidación: decide si una memoria merece promotion, ayuda en write gate dedup decisions, y extrae facts.
- **Why**: El quality gate es lo que hace la consolidación inteligente vs solo heurísticas.
- **Where**:
  - `internal/llm/` — LLMProvider interface
  - `internal/llm/ollama.go` — Ollama provider (POST /api/generate)
  - `internal/llm/remote.go` — OpenAI-compatible API
  - `internal/consolidate/gate.go` — quality gate con 3-strike system
- **Acceptance**:
  - 3 modos: auto (heuristic pre-filter + LLM para inciertos), full (todo por LLM), off (solo heurísticas)
  - 3-strike system: rejected → retry 48 epochs → rejected-2 → retry 144 epochs → rejected-final (never retry)
  - Write gate enhanced: cuando dedup score en zona gris (0.75-0.92), LLM decide ADD|UPDATE|SKIP
  - Batch LLM calls para eficiencia (max 10 por batch)
  - Si LLM no available → degradar a mode "off" (solo heurísticas)
  - Config: `llm.provider`, `llm.gate_mode`, `llm.ollama_model`
  - Tests con mock LLM: verify gate decisions, 3-strike behavior
- **Status**: [x] done

### Step 13: Contradiction detection + resolution
- **What**: Detectar observaciones que se contradicen y resolverlas usando temporal validity (valid_until, invalidated_by) y observation_links de tipo `supersedes`/`contradicts`.
- **Why**: Sin esto, el sistema devuelve info desactualizada. "DB es Postgres 14" y "migramos a Postgres 16" deben resolverse.
- **Where**:
  - `internal/consolidate/contradiction.go` — detection + resolution
  - Integrar en pipeline de consolidación (nuevo paso: detect_contradictions)
- **Acceptance**:
  - Durante consolidación: buscar observaciones similares (cosine 0.5-0.85) que podrían contradecirse
  - LLM evalúa: "¿se contradicen?" → sí: marcar vieja con valid_until=now, invalidated_by=new_id, link `supersedes`
  - Si no hay LLM: crear observation_type='question' para revisión (flag para el usuario)
  - Recall nunca devuelve observaciones con valid_until < now (superseded)
  - Historial visible via observation_links traversal
  - Tests: detect contradicción, mark superseded, verify recall excluye, verify question creation sin LLM
- **Status**: [x] done

### Step 14: Fact graph — knowledge triples con temporal validity
- **What**: Implementar extracción de facts (subject-predicate-object) de observaciones y búsqueda multi-hop en el fact graph.
- **Why**: Los facts permiten razonamiento relacional — "¿qué framework usa este proyecto?" se resuelve con un triple.
- **Where**:
  - `internal/facts/` — FactStore, extraction, graph traversal
  - `internal/facts/extract.go` — LLM-based fact extraction
  - `internal/facts/graph.go` — multi-hop traversal (depth 2)
  - `internal/facts/temporal.go` — valid_from/valid_until management
- **Acceptance**:
  - Al guardar observación, extraer facts en background (si LLM available)
  - Facts: subject, predicate, object, valid_from, valid_until, superseded_by
  - Recall incluye fact graph results (multi-hop depth 2)
  - Superseded facts no aparecen (solo el más reciente)
  - Sin LLM: no se extraen facts, graph vacío, recall degrada a FTS+semantic
  - Tests: fact extraction, graph traversal, temporal superseding
- **Status**: [x] done

### Step 15: Reflection — sintetizar memorias en insights de alto nivel
- **What**: Implementar `reflect` que periódicamente sintetiza N observaciones relacionadas en una reflection de alto nivel. Las reflections se guardan como observaciones Core con links `derived_from`.
- **Why**: De 50 observaciones individuales extrae "reglas generales". Multiplicador de valor (Stanford Generative Agents).
- **Where**:
  - `internal/reflect/` — ReflectionEngine
  - `internal/reflect/synthesize.go` — LLM-based reflection generation
  - Integrar en pipeline de consolidación (cada N observaciones nuevas en Working)
  - MCP tool `reflect` para trigger manual
- **Acceptance**:
  - Trigger automático: cada N observaciones nuevas en Working (default 20, configurable)
  - Trigger manual: MCP tool `reflect` o HTTP `POST /api/v1/reflect`
  - LLM sintetiza: "Dadas estas N observaciones, ¿cuáles son las 3 inferencias de alto nivel?"
  - Reflections se guardan en tabla reflections + como observaciones Core con source links (derived_from)
  - Reflections aparecen en recall con boost de score (layer=Core)
  - Sin LLM: reflection deshabilitada
  - Tests: generación, aparecen en recall, links a source observations
- **Status**: [x] done

### Step 16: Proactive recall + session lifecycle
- **What**: Implementar `context` y `session_start`/`session_end` con auto-extraction. `session_start` genera embedding del contexto actual y devuelve memorias relevantes proactivamente. `session_end` extrae observaciones atómicas del summary (CrewAI extract_memories pattern).
- **Why**: Diferenciador killer — el agente "recuerda" sin que le preguntes. Session end es el punto de extracción más rico.
- **Where**:
  - `internal/proactive/` — ProactiveEngine
  - `internal/proactive/context.go` — context fingerprinting + relevant memory retrieval
  - `internal/session/` — SessionManager con extraction pipeline
  - `internal/session/extract.go` — LLM extracts atomic observations from summary
- **Acceptance**:
  - `session_start(title, directory, branch, namespace)`:
    - Crea session, auto-closes previous active sessions (mark abandoned)
    - Genera embedding del contexto (directory + branch + title)
    - Returns session_id + relevant observations (Core del namespace + top-K por contexto + file-linked)
  - `session_end(summary)`:
    - LLM extrae observaciones atómicas del summary → save cada una como Buffer
    - Marks session completed
    - Returns observations_extracted count
  - `context(namespace, files, limit)`:
    - Si files: observaciones vinculadas a esos archivos (most relevant first)
    - Always: top-N por activation_score (recent + important + accessed)
    - Incluye reflections relevantes del namespace
  - Sin embeddings: context devuelve Core + Working recientes (degradación graceful)
  - Sin LLM: session_end solo guarda summary, no extrae observaciones
  - Tests: session lifecycle, auto-context, extraction from summary, file-based context
- **Status**: [x] done

### Step 17: CLI commands + packaging
- **What**: Implementar CLI con subcommands completos y packaging multi-platform.
- **Why**: Usabilidad — poder interactuar desde terminal, no solo via MCP/HTTP.
- **Where**:
  - `cmd/neurox/` — main entrypoint
  - `internal/cli/` — cobra commands
- **Acceptance**:
  - `neurox mcp` — arranca MCP server (stdio)
  - `neurox serve` — arranca HTTP server
  - `neurox save "title" --content "..." --type decision --kind semantic --tags "auth,jwt" --files "src/auth.go"`
  - `neurox recall "query" --limit 5 --type bugfix --include-stale`
  - `neurox context --namespace myproject --files "src/auth.go,src/middleware.go"`
  - `neurox invalidate <id> --reason "outdated" --replacement-title "..." --replacement-content "..."`
  - `neurox status` — layer counts, embedding health, staleness stats, consolidation last run, fact count
  - `neurox install-hook` — instala git post-commit hook en el repo actual
  - `neurox config` — muestra config actual
  - `go build` produce binario único <20MB
  - Goreleaser config para builds multi-platform (linux/mac/windows, amd64/arm64)
  - Tests: CLI commands devuelven resultados esperados
- **Notes**: Consultar Context7 para `github.com/spf13/cobra`.
- **Status**: [x] done

### Step 18: Integration tests + documentation
- **What**: Tests E2E, README, config reference, setup per agent.
- **Why**: Sin tests de integración, no sabemos si las piezas encajan. Sin docs, nadie lo usa.
- **Where**:
  - `tests/integration/` — E2E tests
  - `README.md`
  - `docs/config.md`
  - `docs/agents.md` — setup per agent
  - `docs/mcp-protocol.md` — full MCP protocol reference
- **Acceptance**:
  - Test E2E: save 100 observations → consolidation → verify promotions → recall → verify ranking
  - Test E2E: save contradicting observations → verify superseding + observation_links
  - Test E2E: save observations with files → git_hook changed_files → verify staleness
  - Test E2E: save 20+ observations → trigger reflection → verify reflection quality
  - Test E2E: session_start → save during session → session_end with summary → verify extraction
  - Test E2E: topic_key upsert → verify single observation updated (not duplicated)
  - Test E2E: invalidate with replacement → verify supersedes link + recall exclusion
  - Test degradation: todo funciona sin Ollama (FTS-only, heuristic-only)
  - README: quick start, architecture diagram, MCP setup, all tools documented
  - Setup instructions: Claude Code, Cursor, OpenCode, Windsurf, Copilot
  - Benchmarks: save latency, recall latency, memory usage
- **Status**: [x] done

---

## Verification

### Automated
```bash
# Unit tests
go test ./...

# Integration tests  
go test ./tests/integration/ -v -timeout 5m

# Build
go build -o neurox ./cmd/neurox/

# Benchmark
go test -bench=. ./internal/recall/ ./internal/observation/
```

### Manual
1. Configurar neurox como MCP server en Claude Code
2. Sesión de trabajo real: save 20+ observaciones durante coding
3. Verificar que consolidation promueve observaciones correctas (Buffer→Working→Core)
4. Verificar recall con queries variados (keyword + semántico + filtros)
5. Verificar proactive recall al iniciar nueva sesión (context returns relevant memories)
6. Verificar que contradiction detection marca observaciones viejas + crea links supersedes
7. Verificar git hook: cambiar archivos → commit → observaciones vinculadas marcadas stale
8. Verificar topic_key upsert: save con mismo topic_key actualiza en lugar de duplicar
9. Verificar invalidate: marca stale + crea replacement con link
10. Forzar reflection y verificar que genera insights útiles
11. Verificar modo degradado: apagar Ollama, todo sigue funcionando (FTS-only)

### Performance targets
- `save`: <1ms (p99) — sync insert + FTS5
- `recall` FTS: <5ms (p99)
- `recall` hybrid: <50ms (p99)
- `context`: <10ms (p99)
- `git_hook`: <5ms per file (p99)
- Binary size: <20MB
- Memory RSS: <150MB con 10k observaciones

---

## Risks / Notes

1. **CGO dependency**: `mattn/go-sqlite3` requiere CGO para FTS5. Considerar `modernc.org/sqlite` (pure Go) pero verificar que soporte FTS5 primero.
2. **HNSW library**: No hay librería HNSW Go madura como `hnswlib` en C++. Opciones: brute force (OK para <50k vectors), o CGO wrapper de hnswlib.
3. **Ollama availability**: Muchos devs no tienen Ollama. Modo degradado (FTS-only, heuristic-only) debe ser first-class citizen.
4. **LLM quality gate cost**: Si el usuario usa API remota, consolidation genera LLM calls. Documentar costo y ofrecer mode "off".
5. **Schema migrations**: Desde v0.0.1, toda migration forward-only y no destructiva.
6. **Namespace = future team scope**: El namespace prepara para v2 multi-user. En v1 es solo organización local — un namespace per project.
7. **Embedding dimension**: Estandarizar en 768 dims (nomic-embed-text) o 384 dims (all-MiniLM-L6-v2). Decisión en Step 8.
8. **Graph decision**: NO graph DB en v1. observation_links + file_observations + recursive queries cubren el caso. Migrar a graph cuando haya >50k nodos y >30% queries multi-hop (Step 14 facts es el más cercano a graph, pero usa tablas relacionales).
9. **Write gate false positives**: Cosine similarity puede marcar como duplicadas observaciones que son similares pero distintas. Los thresholds (0.92 auto-skip, 0.75 zona gris) son de CrewAI — ajustar con datos reales.
10. **Session extraction quality**: La calidad de las observaciones extraídas de session summaries depende del LLM. Modo sin LLM: solo guarda el summary completo sin extraer.

---

## Future (v2 — Team/Enterprise)

Diseño documentado en:
- `~/my-mind/Research/Enterprise Code Memory - Engram para Empresas.md`
- `~/my-mind/Research/schema.sql` (PostgreSQL)
- `~/my-mind/Research/mcp-protocol.md` (MCP protocol con team scoping)

Key additions for v2:
- PostgreSQL (pgvector + tsvector) reemplaza SQLite
- Multi-tenant: teams → users → agents
- RBAC: admin, member, viewer
- Scopes: personal → team → org (promotion workflow via consolidation agent)
- Sleeptime consolidation agent: dedup between agents, promote to shared memory
- Auth: JWT + API keys per agent
- Audit log for compliance

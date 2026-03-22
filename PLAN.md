# Plan: Fix Embedding Pipeline, Tool Telemetry & Health Check

## Goal
Wire the embedding queue into save/update flows so all observations get vector embeddings, add a backfill step in consolidation for historical data, add tool call telemetry to track real MCP usage patterns, and add a `health_check` MCP tool that computes a "brain power" score (0-100%) combining static infrastructure quality and dynamic usage analysis. This unlocks hybrid FTS5+semantic search, cosine-similarity dedup, and contradiction detection — features already implemented but inert due to missing embeddings.

## Business Context
- **Problem**: 0 of 258 observations have embeddings despite Ollama running with `nomic-embed-text`. The `embed.Queue` is created and started but `Enqueue()` is never called from production code paths.
- **Impact**: Recall is FTS5-only (keyword matching). Semantic search, dedup (cosine ≥ 0.85), contradiction detection, and the 1.2x cross-signal boost are all silently disabled.
- **Users**: Every AI agent using Neurox MCP (Claude Code, OpenCode, etc.) gets degraded search quality.
- **Expected outcome**: After this fix, all new observations are embedded asynchronously via the queue, existing observations are backfilled during consolidation, every tool call is tracked with parameter usage, and a new `health_check` tool gives an instant "brain power" score (0-100%) with per-dimension breakdowns and actionable recommendations.

## Technical Context

### Root Cause
- `embed.Queue` is created in `initDeps()` (main.go:551-555) and started correctly
- `Queue.Enqueue(id)` is only called in test code (`queue_test.go`)
- `mcp.Deps` struct does not include the embed queue field
- `api.Deps` struct does not include the embed queue field
- The consolidation pipeline checks `embed.IsAvailable()` before dedup/contradiction but never generates missing embeddings
- No telemetry exists — zero tracking of tool calls, parameter usage, or feature adoption

### Affected Modules
- `main.go` — wiring `embedQueue`, provider metadata, and `Tracker` into MCP and API deps
- `internal/mcp/handlers.go` — `Deps` struct, `handleSave`, `handleUpdate`, new `handleHealthCheck`, telemetry instrumentation
- `internal/mcp/tools.go` — new `healthCheckTool()` definition
- `internal/mcp/server.go` — register health_check tool
- `internal/api/server.go` + `handlers.go` — `Deps` struct, `handleSave`, `handleUpdate`, new `/api/v1/health-check` endpoint
- `internal/consolidate/pipeline.go` — new backfill step, accept embedQueue
- `internal/embed/queue.go` — add `BackfillPending()` method
- `internal/db/003_tool_calls.sql` — **new migration** for tool_calls table
- `internal/db/db.go` — register migration 003
- `internal/telemetry/tracker.go` — **new package** for tool call tracking + usage reports
- `internal/health/health.go` — **new package** for brain power scoring (static + dynamic)

### Existing Patterns
- Queue uses channel-based batching (50 items, 500ms flush, 3 retries)
- Queue worker loads `title + content` from DB, calls `provider.EmbedBatch`, stores as BLOB
- `embed.IsAvailable()` checks provider is not `Disabled{}`
- `embed.PendingCount()` already exists (counts NULL embeddings)
- Migrations use `//go:embed` + sequential versioned SQL files in `internal/db/`
- Tests use `db.Open()` with temp dir for in-memory SQLite + `MockProvider` in `embed` package
- MCP tests use `newTestDeps(t)` helper + `mcpTestHelper.callTool()`

## Implementation Steps

### Step 1: Add EmbedQueue to MCP Deps and enqueue on save/update
- **What**: Add `EmbedQueue *embed.Queue` field to `mcp.Deps`. In `handleSave`, after successful save, call `d.EmbedQueue.Enqueue(saved.ID)`. In `handleUpdate`, after successful update, call `d.EmbedQueue.Enqueue(updated.ID)`. Guard both with nil check.
- **Why**: This is the primary fix — every observation saved or updated via MCP will get queued for embedding.
- **Where**: `internal/mcp/handlers.go`
- **How**:
  1. In `internal/mcp/handlers.go`, add import `"neurox/internal/embed"` at top
  2. Add field to `Deps` struct (after `LLMGate`):
     ```go
     EmbedQueue *embed.Queue
     ```
  3. In `handleSave`, after the `return toolResultJSON(saveResponse{...})` block (line ~93), insert before the return:
     ```go
     // Enqueue for embedding
     if d.EmbedQueue != nil {
         d.EmbedQueue.Enqueue(saved.ID)
     }
     ```
  4. In `handleUpdate`, after `d.ObservationStore.Update(ctx, obs)` succeeds (line ~251) and before the return, insert:
     ```go
     // Re-embed on content change
     if d.EmbedQueue != nil {
         d.EmbedQueue.Enqueue(updated.ID)
     }
     ```
- **Acceptance**:
  - `mcp.Deps` has `EmbedQueue *embed.Queue` field
  - `handleSave` enqueues after save succeeds
  - `handleUpdate` enqueues after update succeeds
  - Both are nil-guarded
  - `go build -tags fts5 ./...` passes (existing tests still pass because `EmbedQueue` is nil in test deps)
- **Status**: [x] done

### Step 2: Wire EmbedQueue and provider metadata in main.go for MCP and HTTP servers
- **What**: Pass `d.embedQueue` to both `neuroxmcp.Deps` and `api.Deps` when constructing them. Add `EmbedQueue *embed.Queue` to `api.Deps`. Also pass the real embedding provider into `mcp.Deps` so MCP status/health responses can report the same provider identity used by HTTP.
- **Why**: The queue exists in `deps` but is never passed downstream. Both server modes need it, and MCP/HTTP should expose consistent embedding-provider metadata.
- **Where**: `main.go`, `internal/api/server.go`, `internal/mcp/handlers.go`
- **How**:
  1. In `main.go` `runMCP()`, add to the `neuroxmcp.Deps` literal (line ~467):
     ```go
     EmbedQueue: d.embedQueue,
     ```
  2. In `internal/api/server.go`, add import `"neurox/internal/embed"` and add field to `api.Deps` struct (after `GateMode`):
      ```go
      EmbedQueue *embed.Queue
      ```
  3. In `internal/mcp/handlers.go`, add import `"neurox/internal/embed"` and add to `mcp.Deps`:
     ```go
     Embedder embed.Provider
     ```
  4. In `main.go` `runMCP()`, add to the `neuroxmcp.Deps` literal:
     ```go
     Embedder: d.embedder,
     ```
  5. In `main.go` `runHTTP()`, add to the `api.Deps` literal (line ~493):
      ```go
      EmbedQueue: d.embedQueue,
      ```
- **Acceptance**:
  - `runMCP` passes `EmbedQueue: d.embedQueue`
  - `runMCP` passes `Embedder: d.embedder`
  - `api.Deps` has `EmbedQueue *embed.Queue` field
  - `runHTTP` passes `EmbedQueue: d.embedQueue`
  - `go build -tags fts5 ./...` passes
- **Status**: [x] done

### Step 3: Enqueue on save/update in HTTP API handlers
- **What**: In `api/handlers.go`, after `handleSave` and `handleUpdate` succeed, enqueue for embedding.
- **Why**: The HTTP API (`neurox serve`) is another write path that must trigger embedding.
- **Where**: `internal/api/handlers.go`
- **How**:
  1. In `handleSave` (line ~81), before `writeJSON(w, http.StatusCreated, ...)`, insert:
     ```go
     if s.deps.EmbedQueue != nil {
         s.deps.EmbedQueue.Enqueue(saved.ID)
     }
     ```
  2. In `handleUpdate` (line ~247), before `writeJSON(w, http.StatusOK, ...)`, insert:
     ```go
     if s.deps.EmbedQueue != nil {
         s.deps.EmbedQueue.Enqueue(updated.ID)
     }
     ```
- **Acceptance**:
  - Both handlers enqueue after success, nil-guarded
  - `go build -tags fts5 ./...` passes
- **Status**: [x] done

### Step 4: Add backfill step to consolidation pipeline
- **What**: Add `BackfillPending` method to `embed.Queue` and a new step in the consolidation pipeline that embeds all observations missing embeddings.
- **Why**: Backfills existing 258 observations and acts as permanent safety net. Runs every 30 minutes.
- **Where**: `internal/embed/queue.go`, `internal/consolidate/pipeline.go`, `main.go`
- **How**:
  1. In `internal/embed/queue.go`, add method after `PendingCount`:
     ```go
     // BackfillPending queries all observations without embeddings and enqueues them.
     // Best-effort: logs errors but never returns them.
     func (q *Queue) BackfillPending(ctx context.Context) {
         rows, err := q.db.QueryContext(ctx,
             "SELECT id FROM observations WHERE embedding IS NULL AND deleted_at IS NULL ORDER BY importance DESC LIMIT 500")
         if err != nil {
             log.Printf("backfill query: %v", err)
             return
         }
         defer rows.Close()
     
         var count int
         for rows.Next() {
             var id string
             if err := rows.Scan(&id); err != nil {
                 continue
             }
             q.Enqueue(id)
             count++
         }
         if count > 0 {
             log.Printf("backfill: enqueued %d observations for embedding", count)
         }
     }
     ```
  2. In `internal/consolidate/pipeline.go`, add `embedQueue` field to `Pipeline` struct:
     ```go
     type Pipeline struct {
         db                    *sql.DB
         decay                 *decay.Engine
         embedder              embed.Provider
         embedQueue            *embed.Queue  // add this field
         gate                  *llm.Gate
         contradictionDetector *contradiction.Detector
         reflectEngine         *reflectpkg.Engine
         cfg                   Config
         stop                  chan struct{}
         wg                    sync.WaitGroup
     }
     ```
  3. Update `NewPipeline` signature to accept `embedQueue *embed.Queue` (add after `embedder`):
     ```go
     func NewPipeline(db *sql.DB, decayEngine *decay.Engine, embedder embed.Provider, embedQueue *embed.Queue, gate *llm.Gate, linkStore *links.Store, llmProvider llm.Provider, idGen filelink.IDGenerator, cfg Config) *Pipeline {
     ```
     And set `embedQueue: embedQueue,` in the struct literal.
  4. In `Pipeline.Run()`, add backfill step between step 1 (decay) and step 2 (retry rejected) — around line 238:
     ```go
     // 1.5 Backfill missing embeddings
     if p.embedQueue != nil {
         p.embedQueue.BackfillPending(ctx)
     }
     ```
  5. In `Pipeline.ForceRun()`, add same backfill step between decay (line ~131) and force promote (line ~138):
     ```go
     // 1.5 Backfill missing embeddings
     if p.embedQueue != nil {
         p.embedQueue.BackfillPending(ctx)
     }
     ```
  6. In `main.go` `initDeps()` (line ~558), update `NewPipeline` call to pass `embedQueue`:
     ```go
     pipeline := consolidate.NewPipeline(database, decayEngine, embedder, embedQueue, gate, linkStore, llmProvider, idGen, consolidate.Config{})
     ```
- **Acceptance**:
  - `BackfillPending` exists on `*embed.Queue`, queries `WHERE embedding IS NULL`, enqueues up to 500
  - `Pipeline` has `embedQueue` field, `NewPipeline` accepts it (nil-safe)
  - Both `Run()` and `ForceRun()` call backfill
  - `main.go` passes `embedQueue` to `NewPipeline`
  - Existing consolidation tests still pass (they pass `nil` for embedQueue)
  - `go build -tags fts5 ./...` passes
- **Status**: [x] done

### Step 5: Expose embedding stats in status responses
- **What**: Add embedding coverage fields to both MCP and HTTP status endpoints.
- **Why**: Without visibility, embedding failures go unnoticed.
- **Where**: `internal/mcp/handlers.go`, `internal/api/handlers.go`
- **How**:
  1. In `internal/mcp/handlers.go` `handleStatus`, add after `temporalMentionCount` query (line ~332):
     ```go
     var embeddingsTotal, embeddingsPending int
     d.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL AND embedding IS NOT NULL").Scan(&embeddingsTotal)
     d.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL AND embedding IS NULL").Scan(&embeddingsPending)
     
      embedProvider := "disabled"
      if d.Embedder != nil {
          embedProvider = d.Embedder.Name()
      }
     ```
  2. Add fields to `statusResponse` struct:
     ```go
     EmbeddingsTotal   int    `json:"embeddings_total"`
     EmbeddingsPending int    `json:"embeddings_pending"`
     EmbedProvider     string `json:"embedding_provider"`
     ```
  3. Populate them in the `return toolResultJSON(statusResponse{...})` call.
  4. In `internal/api/handlers.go` `handleStatus`, add same three queries and include in the response map:
     ```go
     var embeddingsTotal, embeddingsPending int
     db.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL AND embedding IS NOT NULL").Scan(&embeddingsTotal)
     db.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL AND embedding IS NULL").Scan(&embeddingsPending)
     ```
     Add `"embeddings_total": embeddingsTotal, "embeddings_pending": embeddingsPending` to the response map.
- **Acceptance**:
  - MCP `status` returns `embeddings_total`, `embeddings_pending`, `embedding_provider`
  - HTTP `status` returns `embeddings_total`, `embeddings_pending`
  - `embedding_provider` uses the real provider name consistently with HTTP (`ollama/...`, `remote/...`, or `disabled`)
  - Values are accurate
  - `go build -tags fts5 ./...` passes
- **Status**: [x] done

### Step 6: Tests for embedding pipeline
- **What**: Add tests to verify the embedding pipeline works end-to-end.
- **Why**: Without tests, the fix could regress silently — exactly what happened before.
- **Where**: `internal/mcp/server_test.go`, `internal/embed/queue_test.go`
- **How**:
  1. In `internal/embed/queue_test.go`, add test for `BackfillPending`:
     ```go
     func TestBackfillPendingEnqueuesObservationsWithoutEmbedding(t *testing.T) {
         dbPath := filepath.Join(t.TempDir(), "neurox.db")
         database, err := db.Open(context.Background(), dbPath)
         if err != nil {
             t.Fatalf("open db: %v", err)
         }
         defer database.Close()
     
         // Insert 3 observations: 1 with embedding, 2 without
         database.ExecContext(context.Background(), `
             INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
             VALUES('OBS_WITH', 'has embed', 'content', 'discovery', 0, 0.7, 0.5, 'semantic', 'default')
         `)
         database.ExecContext(context.Background(), `
             UPDATE observations SET embedding = X'00000000' WHERE id = 'OBS_WITH'
         `)
         database.ExecContext(context.Background(), `
             INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
             VALUES('OBS_NO1', 'no embed 1', 'content', 'discovery', 0, 0.7, 0.5, 'semantic', 'default')
         `)
         database.ExecContext(context.Background(), `
             INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
             VALUES('OBS_NO2', 'no embed 2', 'content', 'discovery', 0, 0.7, 0.5, 'semantic', 'default')
         `)
     
         provider := &MockProvider{dims: 384}
         q := NewQueue(provider, database)
         ctx := context.Background()
         q.Start(ctx)
     
         q.BackfillPending(ctx)
     
         time.Sleep(800 * time.Millisecond)
         q.Stop()
     
         // Verify: OBS_NO1 and OBS_NO2 should now have embeddings
         for _, id := range []string{"OBS_NO1", "OBS_NO2"} {
             var embedding []byte
             database.QueryRowContext(ctx, "SELECT embedding FROM observations WHERE id = ?", id).Scan(&embedding)
             if embedding == nil {
                 t.Errorf("%s should have embedding after backfill", id)
             }
         }
     }
     ```
  2. In `internal/mcp/server_test.go`, add test that verifies save triggers embedding:
     ```go
     func TestSaveEnqueuesEmbedding(t *testing.T) {
         deps := newTestDeps(t)
         // Create a mock embed queue
          provider := &mockEmbedProvider{dims: 384}
          queue := embed.NewQueue(provider, deps.DB)
         ctx, cancel := context.WithCancel(context.Background())
         queue.Start(ctx)
         deps.EmbedQueue = queue
     
         h := initServer(t, deps)
         result := h.callTool("save", map[string]any{
             "title": "test embedding", "content": "should get embedded",
         })
     
         var resp map[string]any
         json.Unmarshal([]byte(result), &resp)
         id := resp["id"].(string)
     
         // Wait for queue to flush
         time.Sleep(800 * time.Millisecond)
         cancel()
         queue.Stop()
     
         // Verify embedding exists
         var embeddingBlob []byte
         deps.DB.QueryRowContext(context.Background(),
             "SELECT embedding FROM observations WHERE id = ?", id).Scan(&embeddingBlob)
         if embeddingBlob == nil {
             t.Error("observation should have embedding after save+queue flush")
         }
     }
     ```
      **Note**: `MockProvider` currently exists only in `internal/embed/provider_test.go`, so it is not importable from `internal/mcp`. The safest plan is to define a tiny local `mockEmbedProvider` inside `internal/mcp/server_test.go` (or move a reusable test helper into a non-`_test.go` file if desired).
  3. Run full test suite: `go test -tags fts5 ./...`
- **Acceptance**:
  - `TestBackfillPendingEnqueuesObservationsWithoutEmbedding` passes
  - MCP save-embed test passes (if MockProvider is accessible) or equivalent embed-package test
  - `go test -tags fts5 ./...` all green
  - `go vet ./...` clean
- **Status**: [x] done

### Step 7: Tool call telemetry — tracking real usage
- **What**: Add a `tool_calls` table (migration 003), a new `internal/telemetry/` package with a `Tracker`, and instrument every MCP tool handler to record calls with metadata about which parameters were used.
- **Why**: Knowing that tools exist isn't enough. We need to know: are agents calling `recall` with `kind` filters? Are they using `topic_key` on saves? Without this data, `health_check` can only assess static DB quality, not dynamic usage patterns.
- **Where**: `internal/db/003_tool_calls.sql`, `internal/db/db.go`, `internal/telemetry/tracker.go`, `internal/mcp/handlers.go`
- **How**:
  1. Create `internal/db/003_tool_calls.sql`:
     ```sql
     CREATE TABLE IF NOT EXISTS tool_calls (
         id INTEGER PRIMARY KEY AUTOINCREMENT,
         tool_name TEXT NOT NULL,
         namespace TEXT NOT NULL DEFAULT '',
         params_used TEXT NOT NULL DEFAULT '[]',
         success INTEGER NOT NULL DEFAULT 1,
         duration_ms INTEGER NOT NULL DEFAULT 0,
         called_at TEXT NOT NULL DEFAULT (datetime('now'))
     );
     
     CREATE INDEX IF NOT EXISTS idx_tc_tool ON tool_calls(tool_name);
     CREATE INDEX IF NOT EXISTS idx_tc_called ON tool_calls(called_at);
     ```
  2. In `internal/db/db.go`, add the embed directive and migration entry:
     ```go
     //go:embed 003_tool_calls.sql
     ```
     Add to `var migrations`:
     ```go
     {version: 3, name: "tool_calls", path: "003_tool_calls.sql"},
     ```
  3. Create `internal/telemetry/tracker.go`:
     ```go
     package telemetry
     
     import (
         "context"
         "database/sql"
         "encoding/json"
         "log"
         "time"
     )
     
     type Tracker struct {
         db *sql.DB
     }
     
     func NewTracker(db *sql.DB) *Tracker {
         return &Tracker{db: db}
     }
     
     type CallRecord struct {
         ToolName   string
         Namespace  string
         ParamsUsed []string  // names of non-empty params
         Success    bool
         DurationMs int64
     }
     
     // Record logs a tool call asynchronously. Never blocks the caller.
     func (t *Tracker) Record(record CallRecord) {
         go func() {
             paramsJSON, _ := json.Marshal(record.ParamsUsed)
             success := 0
             if record.Success {
                 success = 1
             }
             ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
             defer cancel()
             if _, err := t.db.ExecContext(ctx,
                 `INSERT INTO tool_calls(tool_name, namespace, params_used, success, duration_ms)
                  VALUES(?, ?, ?, ?, ?)`,
                 record.ToolName, record.Namespace, string(paramsJSON), success, record.DurationMs,
             ); err != nil {
                 log.Printf("telemetry record: %v", err)
             }
         }()
     }
     
     // ParamStats holds per-param usage counts for a tool.
     type ParamStats struct {
         Total   int            `json:"total"`
         ByParam map[string]int `json:"by_param"`
     }
     
     // UsageReport contains aggregated usage data for health_check.
     type UsageReport struct {
         TotalCalls       int                   `json:"total_calls"`
         CallsByTool      map[string]int         `json:"calls_by_tool"`
         ParamUsageByTool map[string]ParamStats  `json:"param_usage_by_tool"`
         NeverUsed        []string               `json:"never_used"`
         Period           string                 `json:"period"`
     }
     
     // AllTools is the complete list of MCP tools for tracking coverage.
     var AllTools = []string{
         "save", "recall", "context", "update", "forget", "invalidate",
         "status", "session_start", "session_end", "git_hook", "reflect",
         "consolidate", "health_check",
     }
     
     // GetUsageReport returns aggregated usage stats for the last N days.
     func (t *Tracker) GetUsageReport(ctx context.Context, days int) (UsageReport, error) {
         report := UsageReport{
             CallsByTool:      make(map[string]int),
             ParamUsageByTool: make(map[string]ParamStats),
             Period:           fmt.Sprintf("last %d days", days),
         }
     
         cutoff := time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02 15:04:05")
     
         // Total calls by tool
         rows, err := t.db.QueryContext(ctx,
             `SELECT tool_name, COUNT(*) FROM tool_calls
              WHERE called_at >= ? GROUP BY tool_name ORDER BY COUNT(*) DESC`, cutoff)
         if err != nil {
             return report, fmt.Errorf("query tool calls: %w", err)
         }
         defer rows.Close()
     
         for rows.Next() {
             var name string
             var count int
             if err := rows.Scan(&name, &count); err != nil {
                 continue
             }
             report.CallsByTool[name] = count
             report.TotalCalls += count
         }
     
         // Param usage per tool
         paramRows, err := t.db.QueryContext(ctx,
             `SELECT tool_name, params_used FROM tool_calls WHERE called_at >= ?`, cutoff)
         if err != nil {
             return report, fmt.Errorf("query param usage: %w", err)
         }
         defer paramRows.Close()
     
         for paramRows.Next() {
             var toolName, paramsJSON string
             if err := paramRows.Scan(&toolName, &paramsJSON); err != nil {
                 continue
             }
             stats, ok := report.ParamUsageByTool[toolName]
             if !ok {
                 stats = ParamStats{ByParam: make(map[string]int)}
             }
             stats.Total++
             var params []string
             if json.Unmarshal([]byte(paramsJSON), &params) == nil {
                 for _, p := range params {
                     stats.ByParam[p]++
                 }
             }
             report.ParamUsageByTool[toolName] = stats
         }
     
         // Never used tools
         for _, tool := range AllTools {
             if _, ok := report.CallsByTool[tool]; !ok {
                 report.NeverUsed = append(report.NeverUsed, tool)
             }
         }
     
         return report, nil
     }
     ```
     **Note**: Add `"fmt"` to imports.
  4. Add `Tracker` to `mcp.Deps`:
     ```go
     Tracker *telemetry.Tracker
     ```
     Add import `"neurox/internal/telemetry"`.
  5. Instrument the **12 existing MCP handlers** in this step. Use a helper pattern at the top of each handler:
     ```go
     func (d *Deps) handleSave(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
         start := time.Now()
         // ... existing code ...
     
         // At the end, before return (both success and error paths), record:
         if d.Tracker != nil {
             d.Tracker.Record(telemetry.CallRecord{
                 ToolName:   "save",
                 Namespace:  obs.Namespace,
                 ParamsUsed: nonEmptyParams(req, "title", "content", "observation_type", "kind", "confidence", "topic_key", "tags", "files", "namespace"),
                 Success:    true,  // or false on error path
                 DurationMs: time.Since(start).Milliseconds(),
             })
         }
     ```
     Create helper in `handlers.go`:
     ```go
     // nonEmptyParams returns the names of parameters that have non-empty values.
     func nonEmptyParams(req mcp.CallToolRequest, names ...string) []string {
         var used []string
         for _, name := range names {
             if v := req.GetString(name, ""); v != "" {
                 used = append(used, name)
             }
         }
         // Also check numeric/boolean params
         if args := req.GetArguments(); args != nil {
             for _, name := range names {
                 if _, ok := args[name]; ok {
                     // Already counted by GetString above for strings;
                     // for numbers/booleans, check if present
                     if v := req.GetString(name, ""); v == "" {
                         used = append(used, name)
                     }
                 }
             }
         }
         return used
     }
     ```
      Apply the same pattern to all 12 handlers: `handleSave`, `handleRecall`, `handleContext`, `handleUpdate`, `handleForget`, `handleInvalidate`, `handleStatus`, `handleSessionStart`, `handleSessionEnd`, `handleGitHook`, `handleReflect`, `handleConsolidate`. Use `defer` + closure for cleaner error/success tracking:
     ```go
     func (d *Deps) handleRecall(ctx context.Context, req mcp.CallToolRequest) (result *mcp.CallToolResult, err error) {
         start := time.Now()
         defer func() {
             if d.Tracker != nil {
                 d.Tracker.Record(telemetry.CallRecord{
                     ToolName:   "recall",
                     Namespace:  req.GetString("namespace", ""),
                     ParamsUsed: nonEmptyParams(req, "query", "observation_type", "kind", "namespace", "files", "include_stale", "limit"),
                     Success:    err == nil && (result == nil || !result.IsError),
                     DurationMs: time.Since(start).Milliseconds(),
                 })
             }
         }()
         // ... existing code unchanged ...
     ```
  6. Wire tracker in `main.go`:
     - In `initDeps()`, create tracker:
       ```go
       tracker := telemetry.NewTracker(database)
       ```
     - Add `tracker` field to `deps` struct and return it
     - Pass to `neuroxmcp.Deps`:
       ```go
       Tracker: d.tracker,
       ```
- **Acceptance**:
  - Migration 003 creates `tool_calls` table
  - `Tracker.Record()` is async (goroutine), never blocks handler
  - Every MCP handler records: tool_name, namespace, params_used, success, duration_ms
  - `Tracker.GetUsageReport(ctx, 7)` returns correct aggregated stats
  - `go build -tags fts5 ./...` passes
  - `go test -tags fts5 ./...` passes
- **Status**: [x] done

### Step 8: Add `health_check` MCP tool and HTTP endpoint — brain power tracker (static + dynamic)
- **What**: Create `internal/health/` package plus a new MCP tool and HTTP endpoint that compute a brain power score combining static DB quality and dynamic tool usage analysis.
- **Why**: Makes the "¿se está usando el máximo potencial?" audit instant and repeatable.
- **Where**: `internal/health/health.go`, `internal/health/health_test.go`, `internal/mcp/handlers.go`, `internal/mcp/tools.go`, `internal/mcp/server.go`, `internal/api/handlers.go`, `internal/api/server.go`
- **How**:
  1. Create `internal/health/health.go`:
     ```go
     package health
     
     import (
         "context"
         "database/sql"
         "fmt"
         "sort"
     
         "neurox/internal/embed"
         "neurox/internal/llm"
         "neurox/internal/telemetry"
     )
     
     type Dimension struct {
         Name           string  `json:"name"`
         Category       string  `json:"category"`       // "static" or "dynamic"
         Score          float64 `json:"score"`
         Max            float64 `json:"max"`
         Status         string  `json:"status"`          // "healthy", "degraded", "disabled", "no_data"
         Detail         string  `json:"detail"`
         Recommendation string  `json:"recommendation"`
     }
     
     type Report struct {
         Score        int                   `json:"score"`         // 0-100
         Grade        string                `json:"grade"`         // A/B/C/D/F
         StaticScore  int                   `json:"static_score"`  // 0-60
         DynamicScore int                   `json:"dynamic_score"` // 0-40
         Dimensions   []Dimension           `json:"dimensions"`
         ToolUsage    *telemetry.UsageReport `json:"tool_usage,omitempty"`
         Summary      string                `json:"summary"`
         TopActions   []string              `json:"top_actions"`
     }
     
      type Deps struct {
          DB               *sql.DB
          Embedder         embed.Provider
          LLMProvider      llm.Provider
          EmbedderName     string
          LLMProviderName  string
          Tracker          *telemetry.Tracker
          UsageDays        int
      }
     
     func Check(ctx context.Context, deps Deps) Report {
         var dims []Dimension
         
         // --- STATIC DIMENSIONS (60 pts) ---
         dims = append(dims, checkEmbeddingsCoverage(ctx, deps.DB))       // 15
         dims = append(dims, checkLLMProvider(deps.LLMProvider))           // 10
         dims = append(dims, checkEmbedProvider(deps.Embedder))            // 10
         dims = append(dims, checkTagsCoverage(ctx, deps.DB))              // 7
         dims = append(dims, checkFileLinkCoverage(ctx, deps.DB))          // 7
         dims = append(dims, checkKindDiversity(ctx, deps.DB))             // 3
         dims = append(dims, checkTypeDiversity(ctx, deps.DB))             // 3
         dims = append(dims, checkLinkRichness(ctx, deps.DB))              // 2
         dims = append(dims, checkConsolidationHealth(ctx, deps.DB))       // 3
         
         // --- DYNAMIC DIMENSIONS (40 pts) ---
         var usageReport *telemetry.UsageReport
          usageDays := deps.UsageDays
          if usageDays <= 0 {
              usageDays = 7
          }
          if deps.Tracker != nil {
              ur, err := deps.Tracker.GetUsageReport(ctx, usageDays)
             if err == nil && ur.TotalCalls > 0 {
                 usageReport = &ur
                 dims = append(dims, checkSaveQuality(ur))          // 10
                 dims = append(dims, checkRecallDepth(ur))           // 10
                 dims = append(dims, checkSessionDiscipline(ctx, deps.DB)) // 5
                 dims = append(dims, checkToolBreadth(ur))           // 5
                 dims = append(dims, checkReflectUsage(ur))          // 5
                 dims = append(dims, checkGitHookActivity(ur))       // 5
             } else {
                 // No telemetry data yet
                 dims = append(dims, Dimension{
                     Name: "Usage tracking", Category: "dynamic",
                     Score: 0, Max: 40, Status: "no_data",
                     Detail: "No tool call data yet. Telemetry starts collecting after this update.",
                     Recommendation: "Use Neurox normally — data will accumulate automatically.",
                 })
             }
         }
         
         // Compute totals
         var staticTotal, dynamicTotal float64
         for _, d := range dims {
             if d.Category == "static" {
                 staticTotal += d.Score
             } else {
                 dynamicTotal += d.Score
             }
         }
         total := int(staticTotal + dynamicTotal)
         
         // Top actions: dimensions sorted by (max - score) descending
         type gap struct {
             dim Dimension
             gap float64
         }
         var gaps []gap
         for _, d := range dims {
             if d.Score < d.Max && d.Status != "no_data" {
                 gaps = append(gaps, gap{dim: d, gap: d.Max - d.Score})
             }
         }
         sort.Slice(gaps, func(i, j int) bool { return gaps[i].gap > gaps[j].gap })
         
         var topActions []string
         for i, g := range gaps {
             if i >= 5 { break }
             topActions = append(topActions, fmt.Sprintf("%s (+%.0f pts)", g.dim.Recommendation, g.gap))
         }
         
         return Report{
             Score:        total,
             Grade:        gradeFromScore(total),
             StaticScore:  int(staticTotal),
             DynamicScore: int(dynamicTotal),
             Dimensions:   dims,
             ToolUsage:    usageReport,
             Summary:      buildSummary(total, dims),
             TopActions:   topActions,
         }
     }
     
     func gradeFromScore(score int) string {
         switch {
         case score >= 90: return "A"
         case score >= 75: return "B"
         case score >= 60: return "C"
         case score >= 40: return "D"
         default:          return "F"
         }
     }
     ```
     Then implement each `check*` function. Examples for the key ones:
     ```go
     func checkEmbeddingsCoverage(ctx context.Context, db *sql.DB) Dimension {
         var total, withEmbed int
         db.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL").Scan(&total)
         db.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL AND embedding IS NOT NULL").Scan(&withEmbed)
         
         dim := Dimension{Name: "Embeddings coverage", Category: "static", Max: 15}
         if total == 0 {
             dim.Score = 15; dim.Status = "healthy"; dim.Detail = "No observations yet"
             return dim
         }
         ratio := float64(withEmbed) / float64(total)
         dim.Score = ratio * 15
         dim.Detail = fmt.Sprintf("%d/%d observations have embeddings (%.0f%%)", withEmbed, total, ratio*100)
         switch {
         case ratio >= 0.9:  dim.Status = "healthy"
         case ratio >= 0.5:  dim.Status = "degraded"
         default:            dim.Status = "disabled"
         }
         dim.Recommendation = "Ensure Ollama is running with nomic-embed-text. Embeddings enable semantic search and dedup."
         return dim
     }
     
     func checkSaveQuality(ur telemetry.UsageReport) Dimension {
         dim := Dimension{Name: "Save quality", Category: "dynamic", Max: 10}
         stats, ok := ur.ParamUsageByTool["save"]
         if !ok || stats.Total == 0 {
             dim.Status = "no_data"; dim.Detail = "No save calls recorded yet"
             return dim
         }
         // Score based on % of saves that include tags, files, kind, topic_key
         keyParams := []string{"tags", "files", "kind", "topic_key"}
         var avgUsage float64
         for _, p := range keyParams {
             avgUsage += float64(stats.ByParam[p]) / float64(stats.Total)
         }
         avgUsage /= float64(len(keyParams))  // 0.0 to 1.0
         dim.Score = avgUsage * 10
         dim.Detail = fmt.Sprintf("tags: %.0f%%, files: %.0f%%, kind: %.0f%%, topic_key: %.0f%%",
             pct(stats.ByParam["tags"], stats.Total),
             pct(stats.ByParam["files"], stats.Total),
             pct(stats.ByParam["kind"], stats.Total),
             pct(stats.ByParam["topic_key"], stats.Total))
         switch {
         case avgUsage >= 0.6: dim.Status = "healthy"
         case avgUsage >= 0.3: dim.Status = "degraded"
         default:              dim.Status = "disabled"
         }
         dim.Recommendation = "Always include tags, files, and kind when saving observations."
         return dim
     }
     
     func pct(n, total int) float64 {
         if total == 0 { return 0 }
         return float64(n) / float64(total) * 100
     }
     ```
      Implement all remaining `check*` functions following the same pattern:
      - `checkLLMProvider`: binary — use `llm.IsAvailable(p)` when `LLMProvider` is present, otherwise fall back to `LLMProviderName != "" && LLMProviderName != "disabled"`
      - `checkEmbedProvider`: binary — use `embed.IsAvailable(p)` when `Embedder` is present, otherwise fall back to `EmbedderName != "" && EmbedderName != "disabled"`
     - `checkTagsCoverage`: query `SELECT COUNT(*) WHERE tags IS NOT NULL AND tags != ''` / total, max 7
     - `checkFileLinkCoverage`: query `SELECT COUNT(DISTINCT observation_id) FROM file_observations` / total, max 7
     - `checkKindDiversity`: query `SELECT kind, COUNT(*) GROUP BY kind`, score based on all 3 present + none < 5%, max 3
     - `checkTypeDiversity`: query `SELECT COUNT(DISTINCT observation_type)` / 8, max 3
     - `checkLinkRichness`: query `SELECT COUNT(DISTINCT relation_type) FROM observation_links` / 6, max 2
     - `checkConsolidationHealth`: query `SELECT status, ran_at FROM consolidation_runs ORDER BY ran_at DESC LIMIT 3`, check last < 1h + no failures, max 3
     - `checkRecallDepth`: from `ur.ParamUsageByTool["recall"]`, % of calls with ≥ 1 filter param (kind, observation_type, namespace, files), max 10
     - `checkSessionDiscipline`: query `SELECT status, COUNT(*) FROM sessions GROUP BY status`, % completed / (completed + abandoned), max 5
     - `checkToolBreadth`: `len(ur.CallsByTool)` / `len(telemetry.AllTools)`, max 5
     - `checkReflectUsage`: `ur.CallsByTool["reflect"] + ur.CallsByTool["consolidate"]` > 0 ? 5 : 0, max 5
     - `checkGitHookActivity`: `ur.CallsByTool["git_hook"]` > 0 ? 5 : 0, max 5
  2. Create `internal/health/health_test.go` with at least 4 tests:
     ```go
     func TestCheckAllHealthy(t *testing.T)     // pre-populate DB with ideal data, verify score >= 90
     func TestCheckAllDegraded(t *testing.T)    // empty tags, no files, no embeddings, verify score < 50
     func TestCheckMixed(t *testing.T)          // some good, some bad, verify score is proportional
      func TestCheckNoTelemetryData(t *testing.T) // no tool_calls rows, verify dynamic shows "no_data"
      ```
  3. Add MCP tool definition in `internal/mcp/tools.go`:
     ```go
     func healthCheckTool() mcp.Tool {
         return mcp.NewTool("health_check",
             mcp.WithDescription("Compute brain power score (0-100%) showing how much of Neurox's potential is being used. Returns per-dimension breakdown with status and actionable recommendations."),
             mcp.WithNumber("days",
                 mcp.Description("Number of days to analyze for usage stats (default: 7)"),
             ),
         )
     }
     ```
  4. Add handler in `internal/mcp/handlers.go`:
     ```go
      func (d *Deps) handleHealthCheck(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
          start := time.Now()
          days := 7
          if args := req.GetArguments(); args != nil {
              if v, ok := args["days"]; ok {
                  if f, ok := v.(float64); ok && f > 0 {
                      days = int(f)
                  }
              }
          }
          defer func() {
              if d.Tracker != nil {
                  d.Tracker.Record(telemetry.CallRecord{
                     ToolName: "health_check", ParamsUsed: nonEmptyParams(req, "days"),
                     Success: true, DurationMs: time.Since(start).Milliseconds(),
                 })
             }
         }()

          report := health.Check(ctx, health.Deps{
              DB: d.DB, Embedder: d.Embedder, LLMProvider: d.LLMProvider,
              EmbedderName: d.Embedder.Name(), LLMProviderName: d.LLMProvider.Name(),
              Tracker: d.Tracker, UsageDays: days,
          })
          return toolResultJSON(report)
      }
      ```
      Guard the provider-name calls with nil checks in the real implementation.
   5. Register in `internal/mcp/server.go`:
      ```go
      add(healthCheckTool(), deps.handleHealthCheck)
      ```
   6. Add HTTP endpoint in `internal/api/server.go`:
      ```go
      mux.HandleFunc("GET /api/v1/health-check", s.handleHealthCheck)
      ```
      And handler in `internal/api/handlers.go` that reads `days` from query params and calls `health.Check()` with `DB`, provider name strings already stored in `api.Deps`, `Tracker`, and `UsageDays`.
   7. Extend `api.Deps` with:
      ```go
      Tracker *telemetry.Tracker
      ```
      Keep `LLMProvider` and `EmbedProvider` as strings for HTTP; the health package will use the string fallbacks when concrete providers are not passed.
   8. Wire `Embedder` and `Tracker` in `main.go` to `neuroxmcp.Deps`, and `Tracker` to `api.Deps`:
      ```go
      Embedder: d.embedder,
      Tracker:  d.tracker,
      ```
- **Acceptance**:
  - `internal/health/health.go` exists with `Check()` function returning `Report`
  - 15 evaluators total: 9 static + 6 dynamic, plus a no-data fallback path for the dynamic block
  - `health_check` MCP tool registered, callable with optional `days` param, and `days` actually affects the telemetry window
  - `GET /api/v1/health-check` HTTP endpoint works
  - Score correctly reflects DB state and telemetry
  - `top_actions` sorted by potential point gain
  - 4+ tests in `health_test.go`
  - `go test -tags fts5 ./...` passes
- **Status**: [x] done

### Step 9: Full verification and build
- **What**: Run the complete test suite, verify the binary builds, and do a manual smoke test.
- **Why**: Final quality gate before declaring the plan complete.
- **Where**: All modified files
- **How**:
  1. `CGO_ENABLED=1 go build -tags fts5 ./...`
  2. `go vet ./...`
  3. `go test -tags fts5 ./...`
  4. `go test -tags fts5 -race ./internal/embed/... ./internal/mcp/... ./internal/consolidate/... ./internal/health/... ./internal/telemetry/...`
  5. Rebuild binary: `CGO_ENABLED=1 go build -tags fts5 -o neurox .`
  6. Manual smoke test: restart MCP, call `health_check`, verify response has both static + dynamic dimensions
- **Acceptance**:
  - All commands above pass with zero errors
  - `health_check` returns valid JSON with `score`, `grade`, `dimensions`, `top_actions`
  - Binary size is reasonable (no massive bloat from new packages)
- **Status**: [x] done

## Verification
```bash
# Build
CGO_ENABLED=1 go build -tags fts5 ./...

# Lint
go vet ./...

# All tests
go test -tags fts5 ./...

# Race detector on critical packages
go test -tags fts5 -race ./internal/embed/... ./internal/mcp/... ./internal/consolidate/... ./internal/health/... ./internal/telemetry/...

# Manual smoke test: rebuild binary, restart MCP, save an observation, check embedding
CGO_ENABLED=1 go build -tags fts5 -o neurox .
sqlite3 ~/.config/neurox/neurox.db "SELECT COUNT(*) FROM observations WHERE embedding IS NOT NULL;"

# Manual smoke test: verify telemetry is recording
sqlite3 ~/.config/neurox/neurox.db "SELECT tool_name, COUNT(*) FROM tool_calls GROUP BY tool_name;"

# Manual smoke test: health_check
curl http://localhost:7438/api/v1/health-check | python3 -m json.tool
```

## Risks / Notes
- **Ollama availability**: If Ollama is not running when MCP starts, `embed.AutoDetect` returns `Disabled{}`, queue is nil, and enqueues are no-ops. Safe but means no embeddings until restart.
- **Backfill volume**: 258 existing observations × `nomic-embed-text` ≈ 10-30 seconds. Batches through the queue.
- **Binary rebuild required**: Must `go build -tags fts5` and restart MCP after implementing.
- **Queue capacity**: Channel buffer is 1000. 258 backfill + normal saves is well within capacity.
- **NewPipeline signature change**: Adding `embedQueue` param is a breaking change to `NewPipeline`. All callers (main.go + tests) must be updated.
- **Telemetry overhead**: One async INSERT per tool call. SQLite WAL handles this well. ~1000 rows/day at heavy usage. Consider TTL cleanup (keep 90 days) in future.
- **Health check is read-only**: Only reads DB stats, provider status, and telemetry aggregates. No side effects.
- **Cold start**: When telemetry is first deployed, dynamic dimensions default to "no_data" status, not "failing".
- **MockProvider accessibility**: `MockProvider` is in `embed` package as an exported type but only in `_test.go` files — not accessible from other packages' tests. For MCP tests, prefer a local mock in `internal/mcp/server_test.go` unless there is a clear reason to introduce shared test helpers.

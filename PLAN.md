# Plan: Separate Fast Memory from Core Knowledge

## Goal
Implement a clear memory lifecycle so Neurox keeps operational tracking and step-by-step execution context in fast memory (Buffer/Working) without automatically promoting it into Core, while Core only retains stable, generalized, reusable knowledge for the project namespace.

## Business Context
- **Problem**: The current memory store mixes durable project knowledge with task logs like `Step 4`, `Plan completed`, build-success notes, and overly literal consolidator outputs. This pollutes long-term recall and makes Core less trustworthy.
- **Desired behavior**: Execution traces, plans, and short-term progress notes should remain available for recent context and traceability, but they should not become top long-term memories unless they are generalized into a durable lesson.
- **Preservation rule**: Consolidating Buffer should not delete useful short-term memories by default. Consolidation should reorganize, summarize, downgrade visibility, or promote selectively.
- **Core rule**: Core should only contain stable project knowledge such as business decisions, generalized architecture, durable gotchas, conventions, and reusable patterns.
- **Edge cases**:
  - Reflections should not be stored if they are empty or low-signal.
  - Personal/user metadata should not be stored in the project namespace unless it is truly project-scoped.
  - Existing step/plan memories already in Core need a migration path so the new policy improves current data, not only future saves.

## Technical Context
- **Schema** (`internal/db/schema.sql`): Models `layer` (0/1/2), `staleness`, `kind`, `source`, `consolidation_status` (`pending/promoted/rejected/rejected-2/rejected-final`), but has no explicit retention or promotion policy for operational vs durable memory. FTS5 virtual table `observations_fts` syncs via triggers on `id`, `title`, `content`, `tags` — no retention field needed in FTS since it's used for filtering, not full-text search.
- **Migration system** (`internal/db/db.go`): Numbered `.sql` files embedded via `//go:embed`, registered in `var migrations` slice. Current: `schema.sql` (v1), `002_temporal_mentions.sql` (v2), `003_tool_calls.sql` (v3). Next migration is `004_retention_policy.sql` (v4).
- **Promotion pipeline** (`internal/consolidate/pipeline.go`):
  - `promoteBufferToWorking()`: importance >= 0.3 or procedural; with LLM gate for gray zone.
  - `promoteWorkingToCore()`: `access_count >= 5` AND `age >= 7 days` — no content-quality or retention check.
  - `ForceRun()`: Promotes **everything** from Buffer→Working→Core unconditionally (lines 144-166) — worst offender for pollution.
  - `evictBuffer()`: Soft-deletes lowest-importance Buffer observations when count exceeds cap (200).
- **Reflection engine** (`internal/reflect/engine.go`): `saveReflection()` does raw `INSERT INTO observations` with hardcoded `layer=2, confidence=0.9, importance=0.9` (line 237-239), **bypassing the observation Store entirely** — no validation, no retention classification, no write gate.
- **Proactive context** (`internal/proactive/context.go`): `getTopActivation()` sorts by `layer DESC, importance DESC`, so any high-layer junk dominates startup context.
- **Observation model** (`internal/observation/observation.go`): No first-class retention concept. `ApplyDefaults()` forces `Layer = 0` on all new saves.
- **Write paths that create observations** (all must be aware of retention):
  1. MCP `handleSave` → `observation.Store.Save()` (has LLM SaveGate)
  2. HTTP `handleSave` → `observation.Store.Save()` (no SaveGate)
  3. `reflect/engine.go` `saveReflection()` → raw SQL INSERT (bypasses Store)
  4. `consolidate/pipeline.go` dedup/eviction → only deletes, no creation issue
- **Health check** (`internal/health/health.go`): `checkConsolidationHealth()` scores based on layer distribution and consolidation runs. Changes to promotion behavior may shift the expected ratios.
- **Existing `consolidation_status`**: Tracks the lifecycle of _whether_ an observation was promoted, not _whether it should be_. The new `retention` field is orthogonal — an operational observation can have `consolidation_status = 'promoted'` (promoted to Working for short-term use) while `retention = 'operational'` (never eligible for Core).

## Implementation Steps

### Step 1: Add retention field to schema and observation model
- **What**:
  1. Create `internal/db/004_retention_policy.sql` with `ALTER TABLE observations ADD COLUMN retention TEXT NOT NULL DEFAULT 'durable' CHECK (retention IN ('operational', 'durable'))`.
  2. Register migration v4 in `db.go`: add `//go:embed 004_retention_policy.sql` directive and entry in `var migrations` slice.
  3. Add `Retention` field to `observation.Observation` struct with type `Retention string` and constants `RetentionOperational = "operational"`, `RetentionDurable = "durable"`.
  4. Update `ApplyDefaults()` to default `Retention` to `RetentionDurable` if empty.
  5. Update `Validate()` to check `Retention` is valid.
  6. Update `Store.saveTx()` and `Store.Update()` SQL to include the `retention` column.
  7. Update `Store.getTx()` scan to read `retention`.
  8. FTS5 triggers do NOT need changes — `retention` is used for filtering, not text search.
- **Why**: The schema cannot currently distinguish a step log from a stable architectural decision, so promotion rules are forced to guess.
- **Where**: `internal/db/004_retention_policy.sql` (new), `internal/db/db.go`, `internal/observation/observation.go`, `internal/observation/store.go`.
- **Acceptance**:
  - Migration runs successfully on existing databases; `retention` column exists with default `'durable'`.
  - `Observation` struct has `Retention` field; `ApplyDefaults` and `Validate` handle it.
  - `Save`, `Update`, and `Get` round-trip the `retention` field correctly.
  - Existing saves continue to work with `'durable'` as safe default.
  - `go test -tags fts5 ./internal/observation/... ./internal/db/...` passes.
- **Tests** (in this step): Add table-driven test in `observation/store_test.go` verifying retention round-trip and default behavior.
- **Status**: [x] done

### Step 2: Wire retention into all write paths and add classification logic
- **What**:
  1. Create `internal/classify/classify.go` with a centralized `InferRetention(title, content, observationType, source string) Retention` function that applies heuristic rules:
     - `source == "consolidator"` → `operational`
     - `source == "reflection"` → `durable` (but subject to quality check in Step 3)
     - Title matches step/plan patterns (e.g., `"Implement Step"`, `"Step N:"`, `"Plan completed"`, `"Build flags"`) → `operational`
     - `observation_type` in `{decision, gotcha, pattern, preference}` → `durable`
     - `observation_type` in `{bugfix, discovery, config, question}` → `durable` (default)
     - Fallback → `durable`
  2. MCP `handleSave`: if caller does not explicitly provide `retention`, call `InferRetention()` to auto-classify.
  3. HTTP `handleSave`: same — call `InferRetention()` when retention is not provided.
  4. Expose `retention` as an optional MCP tool parameter so agents can explicitly mark operational saves (e.g., session summaries, plan tracking).
  5. **Fix `reflect/engine.go` `saveReflection()`**: Refactor to either use `observation.Store.Save()` or at minimum include the `retention` column in the raw INSERT. Add a quality guard: reject and skip persistence if `strings.TrimSpace(content) == ""` or content length < 50 chars.
  6. Add tests for `classify.InferRetention()` with table-driven cases.
- **Why**: If classification only happens at consolidation time, polluted data keeps entering Core. All write paths must be aware of retention at creation time.
- **Where**: `internal/classify/classify.go` (new), `internal/classify/classify_test.go` (new), `internal/mcp/handlers.go`, `internal/api/handlers.go`, `internal/reflect/engine.go`.
- **Acceptance**:
  - `InferRetention` is tested and reusable from any write path.
  - MCP save accepts optional `retention` param; auto-classifies when absent.
  - HTTP save auto-classifies when `retention` not provided in body.
  - `saveReflection()` includes `retention = 'durable'` in its INSERT and rejects empty/short reflections.
  - Empty reflections are not persisted (unit test confirms).
  - `go test -tags fts5 ./internal/classify/... ./internal/mcp/... ./internal/reflect/...` passes.
- **Status**: [x] done

### Step 3: Change promotion rules to respect retention policy
- **What**:
  1. **`promoteWorkingToCore()`**: Add `AND retention = 'durable'` to the promotion SQL. Operational observations in Working stay in Working regardless of age/access.
  2. **`promoteBufferToWorking()`**: Operational observations can still be promoted to Working (they need to be queryable), but the gate-assisted path should log retention awareness. No blocking change here — the key gate is Working→Core.
  3. **`ForceRun()`**: Fix the unconditional promotion. Buffer→Working can remain unconditional (fast memory should be visible), but Working→Core MUST add `AND retention = 'durable'`. This is the biggest behavioral fix.
  4. **`evictBuffer()`**: When evicting, prefer evicting `retention = 'operational'` observations first (lower preservation priority). Modify the eviction ORDER BY to sort operational before durable at same importance level.
  5. Define what happens to long-lived operational observations in Working: they stay in Working indefinitely but get decayed normally. If they become stale/expired through Ebbinghaus decay, GC can collect them. No special expiry beyond normal decay.
  6. Add table-driven tests for each modified function covering both retention classes.
- **Why**: This is the core behavioral change — consolidation must stop promoting operational tracking into Core memory.
- **Where**: `internal/consolidate/pipeline.go`, `internal/consolidate/pipeline_test.go`.
- **Acceptance**:
  - `promoteWorkingToCore()` only promotes `retention = 'durable'` observations.
  - `ForceRun()` only promotes `retention = 'durable'` from Working→Core.
  - Operational observations survive in Working without being deleted or promoted.
  - `evictBuffer()` prefers evicting operational observations first.
  - Tests: operational step memory stays in Working after consolidation; durable gotcha reaches Core; ForceRun respects retention.
  - `go test -tags fts5 ./internal/consolidate/...` passes.
- **Status**: [x] done

### Step 4: Adjust retrieval to prefer durable knowledge
- **What**:
  1. **`proactive/context.go` `getTopActivation()`**: Add secondary sort preference for `retention = 'durable'` so durable observations rank above operational at the same layer/importance. Add `AND (retention = 'durable' OR created_at > datetime('now', '-7 days'))` to exclude old operational noise while keeping recent operational items visible.
  2. **`proactive/context.go` `getReflections()`**: Add `AND content != '' AND LENGTH(content) > 50` to filter out empty/trivial reflections from context.
  3. **`recall/engine.go`**: No hard filter — recall should find everything when queried. But add `retention` to the response payload so callers can see and filter by it. Include retention as an optional filter parameter in `SearchOptions`.
  4. **`health/health.go`**: Review `checkConsolidationHealth()` — if it scores based on Core observation counts, adjust expectations since fewer observations will now reach Core. Add a note/recommendation if operational ratio in Core is high.
  5. Update `ContextItem` struct in proactive to include `Retention` field.
- **Why**: Even with better promotion, retrieval should clearly separate "what the project knows" from "what happened during the current workstream."
- **Where**: `internal/proactive/context.go`, `internal/proactive/context_test.go`, `internal/recall/engine.go`, `internal/health/health.go`.
- **Acceptance**:
  - Proactive context ranks durable knowledge above operational traces.
  - Empty/trivial reflections are excluded from context results.
  - Recall search results include `retention` field.
  - Recall supports optional `retention` filter in `SearchOptions`.
  - Health check doesn't penalize a healthy brain that has fewer Core items post-policy.
  - Tests demonstrate ranking difference between durable and operational observations.
  - `go test -tags fts5 ./internal/proactive/... ./internal/recall/... ./internal/health/...` passes.
- **Status**: [x] done

### Step 5: Migrate existing polluted memories
- **What**:
  1. Create `internal/db/005_backfill_retention.sql` (migration v5) with UPDATE statements that reclassify existing data:
     - `UPDATE observations SET retention = 'operational' WHERE source = 'consolidator' AND deleted_at IS NULL` — consolidator-generated implementation notes.
     - `UPDATE observations SET retention = 'operational' WHERE title LIKE 'Implement Step%' OR title LIKE 'Step %' OR title LIKE 'Plan completed%' OR title LIKE 'Build flags%' OR title LIKE 'File Observations%' OR title LIKE 'Embeddings%' OR title LIKE 'TestToolsList%' OR title LIKE 'Tracker Wiring%' OR title LIKE 'Fixing schema%' OR title LIKE 'Renamed Local%' OR title LIKE 'Named Return%'` — known operational titles from current data.
     - `UPDATE observations SET deleted_at = datetime('now') WHERE source = 'reflection' AND (content = '' OR LENGTH(TRIM(content)) < 50) AND deleted_at IS NULL` — soft-delete empty/trivial reflections.
     - Do NOT touch observations with `observation_type IN ('decision', 'gotcha', 'preference')` and `source IS NULL OR source = ''` — these are likely genuine user saves.
  2. Register migration v5 in `db.go`.
  3. The migration is idempotent (WHERE clauses are safe to re-run; soft-delete checks `deleted_at IS NULL`).
- **Why**: Without backfill, the new policy only helps future saves while the current Core remains noisy with step logs and empty reflections.
- **Where**: `internal/db/005_backfill_retention.sql` (new), `internal/db/db.go`.
- **Acceptance**:
  - Existing empty reflections are soft-deleted.
  - Existing consolidator-generated step/plan memories are marked `retention = 'operational'`.
  - Genuine user decisions, gotchas, and preferences remain `retention = 'durable'` and untouched.
  - Migration is idempotent — running it twice produces no change.
  - `go test -tags fts5 ./internal/db/...` passes.
- **Status**: [x] done

### Step 6: End-to-end verification and integration tests
- **What**:
  1. Add an integration-style test in `internal/consolidate/pipeline_test.go` that exercises the full lifecycle:
     - Save one operational observation and one durable observation.
     - Run consolidation (both `Run` and `ForceRun`).
     - Assert: operational stays in Working, durable reaches Core.
     - Assert: proactive context returns durable items first.
  2. Add a test in `internal/reflect/engine_test.go` verifying empty reflection rejection.
  3. Run full project verification.
- **Why**: This change affects the meaning of memory across multiple packages. Integration tests catch cross-package regressions that unit tests miss.
- **Where**: `internal/consolidate/pipeline_test.go`, `internal/reflect/engine_test.go`, `internal/proactive/context_test.go`.
- **Acceptance**:
  - Full lifecycle test passes for both operational and durable observations.
  - Empty reflection rejection test passes.
  - `CGO_ENABLED=1 go build -tags fts5 ./...` passes.
  - `go vet ./...` passes.
  - `go test -tags fts5 ./...` passes (all packages).
- **Status**: [x] done

## Verification
```bash
CGO_ENABLED=1 go build -tags fts5 ./...
go vet ./...
go test -tags fts5 ./...
go test -tags fts5 ./internal/consolidate/... ./internal/reflect/... ./internal/proactive/... ./internal/observation/... ./internal/db/... ./internal/classify/... ./internal/recall/... ./internal/health/...
```

Manual checks:
- Save one operational/step-style observation and verify it stays in fast memory after consolidation.
- Save one durable project decision/gotcha and verify it remains core-eligible and reaches Core.
- Run `ForceRun` consolidation and verify operational observations do NOT reach Core.
- Run proactive context for the namespace and verify durable knowledge ranks ahead of operational logs.
- Verify empty or weak reflections are not returned in default context.
- Check health score is reasonable after migration (no false penalties for fewer Core items).

## Risks / Notes
- **Default `'durable'`**: Chosen as the safe default because most agent saves are intended as lasting knowledge. Operational saves are the exception (consolidator output, step tracking) and are auto-classified. If an agent explicitly saves step tracking without marking it, the classifier catches common patterns.
- **`ForceRun` is the worst offender**: It unconditionally promotes everything. Step 3 fixes this, but be careful — if someone relies on ForceRun to "make everything permanent," the behavior change will be visible.
- **`saveReflection()` bypasses the Store**: This is a pre-existing code smell. Step 2 adds retention to the raw INSERT and a quality guard, but a future refactor should route reflections through the Store for full validation coverage.
- **Existing data migration must be conservative**: The backfill in Step 5 uses explicit pattern matching, not broad heuristics. It's better to miss a few operational memories than to accidentally demote real knowledge.
- **Health check scoring**: Step 4 adjusts the health check, but monitor whether the score changes significantly post-migration. If Core observation counts drop meaningfully, the scoring thresholds may need tuning.
- **Interaction with `consolidation_status`**: The new `retention` field is orthogonal to `consolidation_status`. An operational observation can be `promoted` (to Working for short-term use) while staying `operational` (never eligible for Core). These are independent dimensions.
- **FTS5 is unaffected**: The `retention` column is not indexed in FTS5. It's used for SQL WHERE filtering only. No trigger changes needed.
- **Reflection quality gating**: The 50-char minimum is a conservative floor. If legitimate insights are shorter, this threshold can be lowered, but current empty reflections (content = "") are clearly bugs.

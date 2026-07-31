# Plan: Recall engine — validated search fixes (Tier 0+1)

## Goal

Fix 9 verified bugs in the recall engine (`internal/recall/`, `internal/facts/`) that silently degrade search quality — 4 correctness bugs (BM25 weight misalignment, cross-namespace/expired leakage, write-blocking access bumps, a recency feedback loop) and 5 industry-validated ranking improvements (facts as a real RRF channel instead of unranked noise, wider fusion pools, configurable semantic threshold, membership-based cross-signal boost, and build-tag consistency). All 9 are scoped, code-verified against HEAD `96039f3`, and validated against competitor post-mortems (Letta/Mem0/Zep/AriGraph avoid read-feedback in ranking; Graphiti's fact-as-FTS-channel pattern).

## Business Context

Recall quality is Neurox's core product surface — it is a memory engine for AI coding agents, and every one of these bugs directly degrades what an agent sees when it queries its own memory. None of the 9 fixes are speculative: each has a cited file:line showing the exact defect, and 8 of 9 are compile-time-safe, mechanical fixes with clear test coverage. This work builds on top of the already-shipped RRF hybrid-search refactor (`plan/recall-merge-fix/`, done) and does not touch the graph-traversal/reranking work in flight (`plan/PLAN-benchmark-portfolio.md` Steps 7-8, still pending — untouched by this plan).

**Note on repo state**: a pre-existing `PLAN.md` (benchmark portfolio + graph traversal/reranking initiative, steps 1-6 done, 7-8 pending) was preserved at `plan/PLAN-benchmark-portfolio.md` before this plan was written to the same path. See "Risks" for why.

## Technical Context

- **Stack**: Go 1.26, SQLite via `mattn/go-sqlite3 v1.14.24` (CGO required), FTS5 gated behind a build tag on the vendored driver (`//go:build sqlite_fts5 || fts5` in `vendor/github.com/mattn/go-sqlite3/sqlite3_opt_fts5.go`). ULID IDs (`oklog/ulid/v2`), YAML config (`internal/config/config.go`).
- **Verification commands**: `go build -tags fts5 ./...`, `go vet -tags fts5 ./...`, `go test -tags fts5 ./...` (matches `.skynex/project-config.yaml`; CI currently uses `-tags sqlite_fts5`, see Step 9).
- **Key constraint — single-writer SQLite WAL**: `internal/db/db.go` opens the DB with `max 1 connection`. Any synchronous write on the read (`Search`) path serializes against every other writer in the process (saves, consolidation, embedding backfill). Fix 3 exists specifically to remove `Search`'s write dependency.
- **Existing async-write pattern to imitate for Fix 3**: `internal/observation/savequeue.go` — `SaveQueue` with a buffered channel (`defaultQueueSize = 500`), single background worker goroutine started via `Start(ctx)`, graceful `Stop()` that drains, and a synchronous fallback when the channel is full. Reuse this shape, not a new pattern.
- **RRF hybrid search already in place** (verified in code, not something this plan introduces): `candidate.FTSRank`/`candidate.SemRank` (`internal/recall/engine.go:90-91`), `rrfScore(ftsRank, semRank, k)` (`internal/recall/scoring.go:210-219`), `RecallConfig{RRF RRFConfig; DisableBackfill bool}` in `internal/config/config.go:83-86` with env overrides `NEUROX_RECALL_RRF_K` / `NEUROX_RECALL_DISABLE_BACKFILL` (`config.go:260-272`). Fix 5 and Fix 7 extend this existing infrastructure, they do not create it.
- **Migrations**: numbered SQL files in `internal/db/`, embedded via `//go:embed` directives and registered in the `migrations []migration` slice in `internal/db/db.go:15-96`. Latest on disk is `012_provenance.sql` (version 12) — the next migration is **013**.
- **Benchmark harnesses that exist today** (verified — the brief's reference to `benchmarks/longmemeval/` FTS-only mode is directionally correct but the default data file is missing, see Risks):
  - `go run . benchmark --category cognitive` (`benchmarks/README.md`) — fully self-contained, `FakeEmbedder`, no Ollama/network, ~30s at `--scale small`. **Use this as the primary regression gate** (Verification section).
  - `benchmarks/longmemeval/main.go` — real LongMemEval Go runner (`-data`, `-output`, `-limit`, `-k`, `-embed` flags), but `benchmarks/longmemeval/data/` only contains `locomo10.json`/`locomo10_converted.json` on disk today, not the default-flag `longmemeval_oracle.json` or the `longmemeval_s.json` referenced in `benchmarks/BASELINE_2026.md`. Run it opportunistically if a data file is obtained; do not block the plan on it.
  - `go test -tags fts5 ./internal/recall/...` — the primary unit/integration suite (`internal/recall/engine_test.go`, 3663 lines, is the file every fix step below adds tests to).

## Implementation Steps

### Step 1: Fix BM25 column weight misalignment (FTS ranking bug)
- **What**: Add an explicit weight for the `UNINDEXED id` column so the 3 intended weights (title/content/tags) land on the correct FTS5 columns instead of being shifted by one position.
- **Why**: `bm25()` assigns weights positionally across **all** columns in the virtual table, including `UNINDEXED` ones. The FTS5 table `observations_fts` (`internal/db/schema.sql:59-66`) has columns `id UNINDEXED, title, content, tags` — 4 columns — but the current call only supplies 3 weights, so SQLite applies them as `id=2.0, title=1.0, content=0.5, tags=1.0(default)`. The intended weighting (title matches matter most) is silently broken; tags currently outweigh content.
- **Where**: `internal/recall/filters.go:75` (only call site of `bm25(observations_fts, ...)` in the codebase — confirmed no other callers via grep).
- **How**:
  1. In `buildSearchQuery` (`internal/recall/filters.go`), change line 75 from:
     ```go
     bm25(observations_fts, 2.0, 1.0, 0.5) AS relevance,
     ```
     to:
     ```go
     // bm25() weights are positional across ALL fts5 columns, including
     // UNINDEXED ones: id, title, content, tags. 0.0 disables id (it never
     // matches text), then title > content > tags by design.
     bm25(observations_fts, 0.0, 2.0, 1.0, 0.5) AS relevance,
     ```
  2. Add a table-driven test in `internal/recall/engine_test.go` (near `TestSearchKeywordRanksByTriFactorScore`, line 19) named `TestSearchBM25WeightsFavorTitleOverTags`: save two observations, one with the query term only in `Title`, one with the query term only in `Tags` (e.g. `Tags: "widget"` vs `Title: "widget refactor"`), equal `importance`/`created_at` (use `setObservationFields` helper at `engine_test.go:616` to pin both), then assert the title-match observation ranks first (lower `RawRelevance` = better, since BM25 is negative and results order `relevance ASC`).
- **Acceptance**: **Given** two observations with equal importance/recency, one matching the query only in `title` and one matching only in `tags`, **When** `engine.Search` runs, **Then** the title-match observation is ranked above the tags-match observation. `go test -tags fts5 -run TestSearchBM25WeightsFavorTitleOverTags ./internal/recall/...` passes.
- **Status**: [x] done

### Step 2: Fix `loadObservationsByIDs` — missing valid_until + namespace filters (leak bug)
- **What**: Apply the same `valid_until` (temporal-intent-aware) and `namespace` filters to `loadObservationsByIDs` that `buildSearchQuery` already applies on the FTS path, so semantic-only and fact-sourced candidates cannot leak expired or cross-namespace observations into results.
- **Why**: `loadObservationsByIDs` (`internal/recall/semantic.go:117-217`) hydrates full observation rows for semantic-only and fact-sourced candidate IDs. Its `clauses` (built at lines 129-166) apply `observation_type`, `kind`, `staleness`, `retention`, and `files` filters — mirroring `buildSearchQuery` — but **omits** both: (a) the `valid_until` clause that `buildSearchQuery` applies at `filters.go:19-21` (`(o.valid_until IS NULL OR o.valid_until > datetime('now'))`, skipped only for history intent), and (b) any `namespace` clause at all (verified: no `options.Namespace` check anywhere in the function, unlike `loadNamespaceBackfill` which has one at `semantic.go:227-229`). A semantic match or fact match on an expired observation, or one from another namespace, currently reaches the user.
- **Where**: `internal/recall/semantic.go` (`loadObservationsByIDs`, lines 117-217), `internal/recall/engine.go` (both call sites: line 255 inside `Search`, and line 507 inside `searchFacts`; `searchFacts` itself at lines 475-520 also needs the intent threaded through since it's called from `Search` at line 274).
- **How**:
  1. Change `loadObservationsByIDs` signature to accept the temporal intent:
     ```go
     func loadObservationsByIDs(ctx context.Context, db *sql.DB, ids []string, options SearchOptions, intent TemporalIntent) ([]candidate, error) {
     ```
  2. Inside the `clauses` build (after the `id IN (...)` and `deleted_at IS NULL` clauses, mirroring `filters.go:17-21`), add:
     ```go
     if intent.Kind != IntentHistory {
         clauses = append(clauses, "o.valid_until IS NULL OR o.valid_until > datetime('now')")
     }
     if options.Namespace != "" {
         clauses = append(clauses, "o.namespace = ?")
         args = append(args, options.Namespace)
     }
     ```
     (Note: wrap the valid_until clause in parens exactly as `filters.go:20` does: `"(o.valid_until IS NULL OR o.valid_until > datetime('now'))"`.)
  3. Update `Engine.Search` (`internal/recall/engine.go:255`) call site:
     ```go
     semCandidates, loadErr := loadObservationsByIDs(ctx, e.db, semanticOnlyIDs, normalized, intent)
     ```
  4. Update `Engine.searchFacts` signature (`internal/recall/engine.go:475`) to accept and forward `intent`:
     ```go
     func (e *Engine) searchFacts(ctx context.Context, options SearchOptions, intent TemporalIntent) ([]candidate, error) {
         ...
         loaded, err := loadObservationsByIDs(ctx, e.db, obsIDs, options, intent)
     ```
     and update its call site at `engine.go:274`: `factCandidates, factErr := e.searchFacts(ctx, normalized, intent)`.
  5. Add two tests in `internal/recall/engine_test.go`:
     - `TestLoadObservationsByIDsExcludesExpiredForNormalQuery`: seed an observation with `valid_until` in the past, call through semantic-only hydration path (or unit-test `loadObservationsByIDs` directly with `IntentKind` default), assert it's excluded.
     - `TestLoadObservationsByIDsIncludesExpiredForHistoryIntent`: same seed, call with a history-intent query (e.g. `"what did we decide before"`, matching existing history-intent detection used in `TestSearchHistoryIncludesExpiredObservations` at line 287), assert it's included.
     - `TestLoadObservationsByIDsExcludesCrossNamespace`: seed an observation in `namespace="other"`, run `Search` with `Namespace: "default"` and a query that would semantically or fact-match it, assert it's excluded from results.
- **Acceptance**: **Given** an expired observation (`valid_until` in the past) and a normal (non-history-intent) query, **When** it would otherwise be a semantic-only or fact-sourced match, **Then** it is excluded from results — but included when the query has history intent. **Given** an observation in namespace `other` and a search scoped to `Namespace: "default"`, **When** that observation is a semantic-only or fact-sourced match, **Then** it is excluded. `go test -tags fts5 -run TestLoadObservationsByIDs ./internal/recall/...` passes.
- **Status**: [x] done

### Step 3: Decouple `bumpAccess` from the `Search` read path (write-on-read + fail-the-read bug)
- **What**: Replace the synchronous `bumpAccess` UPDATE at the end of `Search` with an async, coalesced, buffered-channel writer (same shape as `observation.SaveQueue`), so `Search` never fails or blocks because of a write, and repeated recalls of the same IDs in a short window collapse into one UPDATE.
- **Why**: `Engine.Search` (`internal/recall/engine.go:339-343`) currently does:
  ```go
  if len(ids) > 0 {
      if err := e.bumpAccess(ctx, ids); err != nil {
          return nil, err
      }
  }
  ```
  `bumpAccess` (lines 451-469) runs a synchronous `UPDATE observations SET access_count = access_count + 1, last_accessed = datetime('now'), activation_level = ..., consolidation_strength = ... WHERE id IN (...)`. Two problems: (1) if this UPDATE fails for any reason (e.g. DB momentarily busy, closed, or contended by the single-writer WAL — `internal/db/db.go` caps the pool at 1 connection), the entire `Search` call fails even though the read already succeeded; (2) every `Search` call synchronously contends for the single writer connection, adding write latency to every read.
- **Where**: `internal/recall/engine.go` (`Engine` struct lines 22-28, `bumpAccess` lines 451-469, call site lines 339-343), new file `internal/recall/accessqueue.go`. Reference pattern: `internal/observation/savequeue.go` (buffered channel + single worker goroutine + graceful `Stop()` + synchronous-fallback-on-full-channel shape — do not reinvent this).
- **How**:
  1. Create `internal/recall/accessqueue.go` with an `accessQueue` type:
     ```go
     package recall

     import (
         "context"
         "database/sql"
         "fmt"
         "log"
         "strings"
         "sync"
         "time"
     )

     const (
         accessQueueSize   = 1024
         accessFlushEvery  = 2 * time.Second
         accessFlushBatch  = 100
     )

     // accessQueue coalesces recall access-bump events (access_count++,
     // last_accessed=now) into periodic batch UPDATEs so Search never blocks
     // on or fails because of a write. Events are counted per ID so repeated
     // recalls in the same flush window increment access_count correctly.
     type accessQueue struct {
         db   *sql.DB
         ch   chan string // observation ID
         stop chan struct{}
         wg   sync.WaitGroup
     }

     func newAccessQueue(db *sql.DB) *accessQueue {
         return &accessQueue{db: db, ch: make(chan string, accessQueueSize), stop: make(chan struct{})}
     }

     func (q *accessQueue) start(ctx context.Context) {
         q.wg.Add(1)
         go q.worker(ctx)
     }

     func (q *accessQueue) stopAndWait() {
         close(q.stop)
         q.wg.Wait()
     }

     // enqueue submits IDs for a coalesced access bump. Never blocks: if the
     // channel is full, the event is dropped (telemetry, not correctness —
     // access_count/last_accessed are best-effort signals, not required for
     // read correctness) and logged at debug level.
     func (q *accessQueue) enqueue(ids []string) {
         for _, id := range ids {
             select {
             case q.ch <- id:
             default:
                 log.Printf("DEBUG: access queue full (%d), dropping bump for %s", accessQueueSize, id)
             }
         }
     }

     func (q *accessQueue) worker(ctx context.Context) {
         defer q.wg.Done()
         counts := make(map[string]int)
         ticker := time.NewTicker(accessFlushEvery)
         defer ticker.Stop()

         flush := func() {
             if len(counts) == 0 {
                 return
             }
             if err := q.flush(counts); err != nil {
                 log.Printf("DEBUG: access queue flush failed: %v", err)
             }
             counts = make(map[string]int)
         }

         for {
             select {
             case <-ctx.Done():
                 flush()
                 return
             case <-q.stop:
                 flush()
                 return
             case id := <-q.ch:
                 counts[id]++
                 if len(counts) >= accessFlushBatch {
                     flush()
                 }
             case <-ticker.C:
                 flush()
             }
         }
     }

     func (q *accessQueue) flush(counts map[string]int) error {
         fctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
         defer cancel()
         for id, n := range counts {
             if _, err := q.db.ExecContext(fctx, `
                 UPDATE observations
                 SET access_count = access_count + ?,
                     last_accessed = datetime('now'),
                     activation_level = MIN(1.0, activation_level + 0.08),
                     consolidation_strength = MIN(1.0, consolidation_strength + 0.02)
                 WHERE deleted_at IS NULL AND id = ?
             `, n, id); err != nil {
                 return fmt.Errorf("bump access for %s: %w", id, err)
             }
         }
         return nil
     }
     ```
     (Batched per-ID is simplest and matches the existing single-writer WAL; a multi-ID `IN (...)` batch with `access_count + 1` uniformly is an acceptable alternative if the coder prefers — either satisfies the acceptance criteria. `strings` import above is unused if the per-ID loop is kept; drop it or use it if switching to an `IN (...)` batch form.)
  2. Add `accessQ *accessQueue` to the `Engine` struct (`engine.go:22-28`), initialize it in `NewEngine` (line 94-100) and start it there: `e.accessQ = newAccessQueue(database); e.accessQ.start(context.Background())`.
  3. Add `func (e *Engine) Close()` that calls `e.accessQ.stopAndWait()` — wire this into whatever already closes the DB/engine at shutdown (check `main.go` for existing `Close()`/shutdown handling on other queues, e.g. `SaveQueue.Stop()` usage, and add the mirrored call).
  4. Replace the `Search` tail (`engine.go:339-343`):
     ```go
     if len(ids) > 0 {
         e.accessQ.enqueue(ids)
     }
     ```
     Remove the old synchronous `bumpAccess` method (lines 451-469) entirely (superseded by `accessQueue.flush`).
  5. Tests in `internal/recall/engine_test.go` (or new `internal/recall/accessqueue_test.go`):
     - `TestSearchSucceedsWhenAccessBumpFails`: close the DB (or use a DB wrapper that fails UPDATEs) after the read but reachable by the queue's flush — verify `Search` still returns results without error. (Simplest approach: call `engine.Search` normally, assert no error, then separately unit-test `accessQueue.flush` against a closed `*sql.DB` and assert it returns an error that is only logged, never propagated.)
     - `TestAccessQueueCoalescesRepeatedBumps`: enqueue the same ID 5 times, force a flush (call `worker`'s `flush` path directly or wait past `accessFlushEvery` in a shortened test window), assert `access_count` increased by exactly 5 in one UPDATE, not 5 separate UPDATEs (this can be asserted via the resulting `access_count` value; verifying single-UPDATE aspect only needs to prove correctness, not the exact combination).
     - `TestAccessQueueGracefulShutdownFlushesPending`: enqueue several IDs, call `stopAndWait()` without waiting for the ticker, assert the pending count was flushed (not dropped).
- **Acceptance**: **Given** `Search` returns candidates and the subsequent access-bump write fails (DB closed/contended), **When** `Search` completes, **Then** it still returns the results with no error — the failure is only logged. **Given** the same observation ID is returned by 5 consecutive `Search` calls within one flush window, **When** the queue flushes, **Then** `access_count` increases by 5 via one coalesced UPDATE. **Given** `Engine.Close()` is called with pending queued bumps, **When** it returns, **Then** all pending bumps have been flushed (no silent loss on shutdown). `go test -tags fts5 -run TestSearchSucceedsWhenAccessBumpFails|TestAccessQueue ./internal/recall/...` passes.
- **Status**: [x] done

### Step 4: Remove `last_accessed` from recency scoring (rich-get-richer feedback loop)
- **What**: Make `recencyScore` derive purely from `CreatedAt` (knowledge age), not from `LastAccessed`. Keep `last_accessed`/`access_count`/`activation_level` as telemetry columns (untouched by this step — Step 3's queue still writes them) but stop feeding `LastAccessed` into the ranking formula.
- **Why**: `recencyScore` (`internal/recall/scoring.go:114-124`) currently does:
  ```go
  reference := item.CreatedAt
  if item.LastAccessed != nil && item.LastAccessed.After(reference) {
      reference = *item.LastAccessed
  }
  ```
  Combined with Step 3's access bumps (or even today's synchronous ones), every time an old observation is returned, its `last_accessed` becomes "now", which makes its recency score jump back to ~1.0 on the **next** search — even for unrelated queries. This creates a self-reinforcing loop where already-surfaced results become permanently more likely to be surfaced again, drowning out genuinely new/relevant content. No competitor examined (Letta, Mem0, Zep, AriGraph) feeds read/access recency into ranking — only write recency (creation/update time).
  **This bug is currently asserted as intentional behavior by an existing test** — `TestSearchActivationBoostUsesLastAccessed` (`internal/recall/engine_test.go:143-200`) explicitly runs a `Search` to bump `boosted`'s `last_accessed`, then asserts a **different, unrelated query**'s score for `boosted` **increases** as a result. This test encodes the bug and must be rewritten, not just left passing.
- **Where**: `internal/recall/scoring.go` (`recencyScore`, lines 114-124), `internal/recall/engine_test.go` (`TestSearchActivationBoostUsesLastAccessed`, lines 143-200).
- **How**:
  1. Change `recencyScore` to:
     ```go
     func recencyScore(item candidate, now time.Time) float64 {
         reference := item.CreatedAt
         days := now.Sub(reference).Hours() / 24
         if days <= 0 {
             return 1
         }
         return math.Exp(-math.Ln2 * (days / defaultHalfLifeDays))
     }
     ```
     (Drop the `item.LastAccessed` branch entirely. Half-life stays `defaultHalfLifeDays = 30.0` — unchanged per scope. Do **not** remove `LastAccessed` from the `candidate` struct or `scanCandidate` — it is still scanned/available for other consumers, e.g. `ORDER BY ... o.last_accessed DESC` in `loadNamespaceBackfill`, `semantic.go:291`, which is out of scope for this fix and must keep working.)
  2. Rewrite `TestSearchActivationBoostUsesLastAccessed` (rename to `TestSearchAccessDoesNotBoostRecencyForUnrelatedQuery`) to assert the **corrected** behavior: after `engine.Search(ctx, SearchOptions{Query: "fallback"})` bumps `boosted`'s `last_accessed`, a subsequent unrelated `Search(ctx, SearchOptions{Query: "auth"})` for `boosted` returns a score that is **unchanged** (or at least does not increase) relative to `initialBoostedScore`. Keep the rest of the test setup (two observations, `importance`/`created_at` seeded via `setObservationFields`) — only the final assertion direction changes, from `boostedScore <= initialBoostedScore → Fatalf` to something like:
     ```go
     if boostedScore > initialBoostedScore {
         t.Fatalf("boosted score = %f increased after unrelated access, want <= initial score %f (recency must not respond to reads)", boostedScore, initialBoostedScore)
     }
     ```
  3. Add a new test `TestSearchFreshlyAccessedOldObservationDoesNotOutrankNewer`: seed an old observation (`created_at` 180 days ago) and a newer one (`created_at` 7 days ago) with equal importance and matching content for a shared query; access the old one via a `Search` call for a different query to bump its `last_accessed` to "now"; assert the newer observation still ranks above the old one on the shared query.
  4. Grep the full test file for any other test relying on `LastAccessed` affecting ranking order (`grep -n "LastAccessed\|last_accessed" internal/recall/engine_test.go`) and confirm none besides the one renamed in step 2 assert a ranking-boost-from-access behavior; fix any that do.
- **Acceptance**: **Given** an old, low-recency observation that is freshly accessed via one `Search` call, **When** a subsequent unrelated `Search` call scores it, **Then** its recency contribution does not increase relative to before the access. **Given** an old observation (accessed recently) and a newer, unaccessed observation with equal importance/relevance, **When** both match a query, **Then** the newer observation ranks at or above the older one. `go test -tags fts5 -run TestSearchAccessDoesNotBoostRecencyForUnrelatedQuery|TestSearchFreshlyAccessedOldObservationDoesNotOutrankNewer ./internal/recall/...` passes, and the full `internal/recall` suite still passes (no other test depends on the old behavior).
- **Status**: [x] done

### Step 5: Facts as a real RRF channel (replace `LIKE` substring search with FTS5 + rank)
- **What**: Add an FTS5 index over facts (`subject || ' ' || predicate || ' ' || object`), replace the `LIKE '%q%'` fact search with a BM25-ranked FTS query, and integrate fact hits into RRF as a third weighted channel (weight `0.5`) instead of the current `RawRelevance=0`/no-rank noise.
- **Why**: `facts.Store.Search` (`internal/facts/store.go:137-158`) does:
  ```go
  q := "%" + query + "%"
  ...WHERE ... (subject LIKE ? OR object LIKE ? OR predicate LIKE ?)
  ```
  — the **whole multi-word query** is used as one contiguous substring, so a query like `"auth token refresh"` will never match a fact `subject="token"` unless the exact 3-word phrase appears verbatim. Worse, `Engine.searchFacts` (`engine.go:475-520`) sets `loaded[i].RawRelevance = 0` (line 516) for every fact-sourced candidate, and today's `rrfScore` gives them no `FTSRank`/`SemRank`, so they enter the final ranking with `rrf=0` — contributing zero relevance signal and ranking purely on recency+importance, i.e. noise in the fusion. This mirrors Graphiti's validated pattern: facts should be a proper ranked channel, not an unranked side-load.
- **Where**: new `internal/db/013_facts_fts.sql`, `internal/db/db.go` (embed + migrations slice), `internal/facts/store.go` (new ranked search method), `internal/recall/engine.go` (`candidate` struct, `searchFacts`, merge logic), `internal/recall/scoring.go` (`rrfScore`, `applyScores`).
- **How**:
  1. **Migration `internal/db/013_facts_fts.sql`** (mirror the `observations_fts` pattern at `schema.sql:59-83` exactly — external-content FTS5 table + sync triggers):
     ```sql
     -- Migration 013: FTS5 index for facts (subject/predicate/object triples)
     -- Replaces LIKE '%query%' substring search with tokenized BM25 search.

     CREATE VIRTUAL TABLE facts_fts USING fts5(
         id UNINDEXED,
         fact_text,
         content=facts,
         content_rowid=rowid
     );

     INSERT INTO facts_fts(rowid, id, fact_text)
     SELECT rowid, id, subject || ' ' || predicate || ' ' || object
     FROM facts;

     CREATE TRIGGER trg_facts_ai AFTER INSERT ON facts BEGIN
         INSERT INTO facts_fts(rowid, id, fact_text)
         VALUES (new.rowid, new.id, new.subject || ' ' || new.predicate || ' ' || new.object);
     END;

     CREATE TRIGGER trg_facts_ad AFTER DELETE ON facts BEGIN
         INSERT INTO facts_fts(facts_fts, rowid, id, fact_text)
         VALUES ('delete', old.rowid, old.id, old.subject || ' ' || old.predicate || ' ' || old.object);
     END;

     CREATE TRIGGER trg_facts_au AFTER UPDATE ON facts BEGIN
         INSERT INTO facts_fts(facts_fts, rowid, id, fact_text)
         VALUES ('delete', old.rowid, old.id, old.subject || ' ' || old.predicate || ' ' || old.object);
         INSERT INTO facts_fts(rowid, id, fact_text)
         VALUES (new.rowid, new.id, new.subject || ' ' || new.predicate || ' ' || new.object);
     END;
     ```
     Note: `facts.Store.Save`/`SaveWithValidFrom` issue an `UPDATE facts SET valid_until=..., superseded_by=... WHERE id=?` on supersession (`store.go:55-58`) — the `trg_facts_au` trigger will re-sync harmlessly since subject/predicate/object don't change on that UPDATE.
  2. **Register the migration** in `internal/db/db.go`: add `//go:embed 013_facts_fts.sql` to the embed block (after line 26) and append to the `migrations` slice (after the `provenance` entry, line 91-95):
     ```go
     {
         version: 13,
         name:    "facts_fts",
         path:    "013_facts_fts.sql",
     },
     ```
  3. **New ranked search method** in `internal/facts/store.go` (add alongside `Search`, do not remove `Search` yet if other callers exist — check via grep first; if `Search` is only called from `engine.go:476`, replace that call site and leave `Search`/the `LIKE` path only if still used elsewhere, e.g. tests):
     ```go
     // RankedFact pairs a Fact with its 1-based FTS rank (best match = 1).
     type RankedFact struct {
         Fact
         Rank int
     }

     // SearchRanked finds active facts matching query via FTS5 (tokenized,
     // BM25-ranked), scoped to namespace, returning results best-first with
     // their scan-order rank.
     func (s *Store) SearchRanked(ctx context.Context, query string, namespace string, limit int) ([]RankedFact, error) {
         if limit <= 0 {
             limit = 20
         }
         matchQuery := buildFactsMatchQuery(query)
         if matchQuery == "" {
             return nil, nil
         }

         rows, err := s.db.QueryContext(ctx, `
             SELECT f.id, f.subject, f.predicate, f.object, f.observation_id,
                    f.namespace, f.valid_from, f.valid_until, f.superseded_by, f.created_at
             FROM facts_fts
             JOIN facts f ON f.rowid = facts_fts.rowid
             WHERE facts_fts MATCH ?
               AND f.valid_until IS NULL
               AND f.namespace = ?
             ORDER BY bm25(facts_fts) ASC
             LIMIT ?
         `, matchQuery, namespace, limit)
         if err != nil {
             return nil, fmt.Errorf("search facts fts: %w", err)
         }
         defer rows.Close()

         facts, err := scanFacts(rows)
         if err != nil {
             return nil, err
         }
         ranked := make([]RankedFact, len(facts))
         for i, f := range facts {
             ranked[i] = RankedFact{Fact: f, Rank: i + 1}
         }
         return ranked, nil
     }

     // buildFactsMatchQuery tokenizes a query into a quoted OR-joined FTS5
     // MATCH expression. Simpler than recall's buildFTSMatchQuery (no
     // stopwords/prefix expansion needed for short subject/predicate/object
     // triples) — kept local to avoid an internal/recall <-> internal/facts
     // import cycle (recall already imports facts).
     func buildFactsMatchQuery(query string) string {
         tokens := strings.Fields(query)
         parts := make([]string, 0, len(tokens))
         for _, token := range tokens {
             trimmed := strings.TrimSpace(token)
             if trimmed == "" {
                 continue
             }
             escaped := strings.ReplaceAll(trimmed, `"`, `""`)
             parts = append(parts, `"`+escaped+`"`)
         }
         return strings.Join(parts, " OR ")
     }
     ```
  4. **`candidate` struct** (`internal/recall/engine.go:79-92`): add a third rank field next to `FTSRank`/`SemRank`:
     ```go
     FactRank int // 1-based rank in fact-FTS results; 0 if not fact-sourced
     ```
  5. **`Engine.searchFacts`** (`engine.go:475-520`, already modified in Step 2 to accept `intent`): replace the `e.factStore.Search(...)` call with `e.factStore.SearchRanked(ctx, options.Query, options.Namespace, factsPoolSize(options.Limit))` (pool sizing `factsPoolSize` defined in Step 6), and propagate `Rank` onto the loaded candidate instead of `RawRelevance = 0`:
     ```go
     matchedFacts, err := e.factStore.SearchRanked(ctx, options.Query, options.Namespace, factsPoolSize(options.Limit))
     ...
     rankByObs := make(map[string]int, len(matchedFacts))
     for _, rf := range matchedFacts {
         if rf.ObservationID == "" { continue }
         if _, dup := rankByObs[rf.ObservationID]; dup { continue } // keep best (first/lowest) rank
         rankByObs[rf.ObservationID] = rf.Rank
         factByObs[rf.ObservationID] = rf.Fact
     }
     ...
     for i := range loaded {
         f := factByObs[loaded[i].ID]
         loaded[i].Title = fmt.Sprintf("[fact] %s | %s | %s", f.Subject, f.Predicate, f.Object)
         loaded[i].RawRelevance = 0
         loaded[i].FactRank = rankByObs[loaded[i].ID]
     }
     ```
  6. **Merge logic in `Engine.Search`** (`engine.go:269-289`): currently fact candidates whose ID already exists in `candidates` are dropped entirely. Change to: if the ID already exists, set `FactRank` on the **existing** candidate (don't add a duplicate); if new, append as before:
     ```go
     if e.factStore != nil {
         factCandidates, factErr := e.searchFacts(ctx, normalized, intent)
         if factErr != nil {
             log.Printf("fact search: %v", factErr)
         } else if len(factCandidates) > 0 {
             indexByID := make(map[string]int, len(candidates))
             for i, c := range candidates {
                 indexByID[c.ID] = i
             }
             for _, fc := range factCandidates {
                 if idx, exists := indexByID[fc.ID]; exists {
                     candidates[idx].FactRank = fc.FactRank
                 } else {
                     indexByID[fc.ID] = len(candidates)
                     candidates = append(candidates, fc)
                 }
             }
         }
     }
     ```
  7. **`rrfScore`** (`internal/recall/scoring.go:210-219`): add the third channel at weight `0.5`:
     ```go
     func rrfScore(ftsRank, semRank, factRank, k int) float64 {
         score := 0.0
         if ftsRank > 0 {
             score += 1.0 / float64(k+ftsRank)
         }
         if semRank > 0 {
             score += 1.0 / float64(k+semRank)
         }
         if factRank > 0 {
             score += 0.5 / float64(k+factRank)
         }
         return score
     }
     ```
     Update the call site in `applyScores` (`scoring.go:66`): `rrf := rrfScore(items[index].FTSRank, items[index].SemRank, items[index].FactRank, rrfK)`. Leave the `relevance := rrf * float64(rrfK+1) / 2.0` normalization constant as-is — it is calibrated to the FTS+Semantic 2-channel max; the fact channel is an additive 0.5-weight bonus signal on top, not a rebalancing of the base scale. Add a one-line comment noting this above the normalization.
  8. Tests:
     - `internal/facts/store_test.go`: `TestSearchRankedMatchesMultiWordTokensNotContiguousSubstring` — save a fact `subject="auth"`, `predicate="uses"`, `object="jwt token"`, search with query `"token auth"` (reordered, not a contiguous substring of any field), assert it's found with `Rank >= 1`.
     - `internal/recall/engine_test.go`: extend or add near `TestFactsIntegratedInRecall` (line 1927) — `TestFactRankedCandidateCarriesRealRankAndWeight`: seed a fact-only candidate (no FTS/semantic match), assert `Breakdown.RRFScore > 0` (requires `Debug: true` in `SearchOptions`) and that removing the fact does not leave a `0`-relevance no-signal candidate ranking above genuinely irrelevant namespace-backfill entries.
     - Remove/replace `TestFactsIntegratedInRecall` assertions if they assert `RawRelevance == 0` as the terminal signal (grep for `RawRelevance` in that test first).
- **Acceptance**: **Given** a fact whose subject/predicate/object tokens match a multi-word query in a different order (not a contiguous substring), **When** `facts.Store.SearchRanked` runs, **Then** the fact is found and ranked. **Given** a fact-sourced candidate with no FTS/semantic match, **When** `applyScores` runs, **Then** its `RRFScore` is `> 0` (driven by `FactRank` at weight 0.5), not `0`. `go test -tags fts5 -run TestSearchRanked|TestFactRanked ./internal/facts/... ./internal/recall/...` passes.
- **Status**: [x] done

### Step 6: Widen fusion pools — truncate to `limit` only after RRF+scoring
- **What**: Raise the per-channel candidate pool sizes before fusion: FTS `max(50, 5*limit)`, semantic `max(50, 5*limit)` (was `limit*2`), facts `max(25, 3*limit)` (new, from Step 5). Final truncation to the user-facing `limit` (`maxLimit` stays `50`) continues to happen only after `applyScores` + sort, which is already the case today.
- **Why**: `buildSearchQuery` currently binds the FTS `LIMIT` to the raw `options.Limit` (`filters.go:86` LIMIT clause, `filters.go:116` `args = append(args, options.Limit)`), and semantic search is capped at `normalized.Limit*2` (`engine.go:195`). With the default `limit=10`, only the top 10 FTS matches and top 20 semantic matches ever enter fusion — a document ranked, say, 15th in FTS but very strong semantically can never win RRF because it's excluded from the FTS pool entirely before scoring even runs. Industry practice (and this codebase's own final-truncation-after-sort design, already correct at `engine.go:326-328`) is to retrieve a wide pool per channel and let fused scoring pick the true top-N.
- **Where**: `internal/recall/filters.go` (`buildSearchQuery`), `internal/recall/engine.go` (semantic search call, `searchFacts`/pool sizing helper), `internal/recall/scoring.go` or a small new helper for the pool-size formulas (keep it near `defaultLimit`/`maxLimit` constants, `engine.go:17-20`).
- **How**:
  1. Add pool-size helpers in `internal/recall/engine.go` near the `defaultLimit`/`maxLimit` constants (lines 17-20):
     ```go
     const (
         defaultLimit = 10
         maxLimit     = 50
     )

     // ftsPoolSize/semanticPoolSize/factsPoolSize compute per-channel retrieval
     // pool sizes. Retrieval is intentionally wider than the user-facing limit
     // so RRF fusion can surface strong candidates that rank outside the naive
     // top-N of any single channel. Final truncation to `limit` happens only
     // after applyScores sorts by fused score (engine.go, end of Search).
     func ftsPoolSize(limit int) int {
         if limit*5 > 50 {
             return limit * 5
         }
         return 50
     }

     func semanticPoolSize(limit int) int {
         return ftsPoolSize(limit) // same formula: max(50, 5*limit)
     }

     func factsPoolSize(limit int) int {
         if limit*3 > 25 {
             return limit * 3
         }
         return 25
     }
     ```
  2. `internal/recall/filters.go` — `buildSearchQuery` currently ends with `args = append(args, options.Limit)` (line 116). Change to bind the wider pool instead of the user limit:
     ```go
     args = append(args, ftsPoolSize(options.Limit))
     ```
     (`ftsPoolSize` lives in `engine.go` but is the same package, `recall` — no import needed.)
  3. `internal/recall/engine.go:195` — change the semantic search call:
     ```go
     semScores, semErr := semanticSearch(ctx, e.db, e.embedder, normalized.Query, semanticPoolSize(normalized.Limit), semFilter)
     ```
  4. `Engine.searchFacts` (already touched in Step 5) — use `factsPoolSize(options.Limit)` for the `SearchRanked` call limit (already specified in Step 5's How, item 5 — just confirm it references this helper, not a literal).
  5. **Verify backfill trigger semantics are unaffected** (do not skip this check): `shouldNamespaceBackfill` (`engine.go:384-395`) triggers on `currentCount < options.Limit`, where `currentCount = len(candidates)` at `engine.go:291`. Because FTS `LIMIT` is applied *after* the `MATCH` filter (SQLite doesn't invent rows), a query with genuinely few real matches still yields few candidates even with a wider pool cap — `len(candidates)` reflects true match count, not the pool ceiling, so backfill's trigger condition is unaffected by this change. Confirm this holds by running the existing backfill tests (`TestNamespaceBackfillFillsBroadNamespaceQueries`, `TestNamespaceBackfillKeepsDirectMatchesAhead`, `TestNamespaceBackfillIsConservative`, lines 1527-1637+) unmodified after the pool-size change — they must still pass without edits.
  6. New test `TestSearchWideFTSPoolLetsRank15WinFusion` in `engine_test.go`: seed ~20 observations that all match an FTS query (via shared keyword) with deliberately varied `importance`/`created_at` so one lands around FTS rank 15 by BM25 but has a strong semantic match (seed its embedding to be near-identical to the query embedding, following the pattern used in existing semantic tests, e.g. `TestIntegrationFTSSemanticFallbackAndFacts` at line 2577 for embedding-seeding technique) — assert it appears in the final top-`limit` results (would have been excluded entirely under the old `LIMIT options.Limit` FTS cap).
- **Acceptance**: **Given** an FTS query with 20+ genuine matches, **When** `Search` runs with `Limit: 10`, **Then** the FTS pool retrieved for fusion is `max(50, 5*10)=50`, not `10` — verified by a document ranked ~15th in raw BM25 but strong semantically appearing in the final top-10. **Given** the existing namespace-backfill tests, **When** run unmodified after this change, **Then** they still pass (backfill trigger semantics unchanged). `go test -tags fts5 -run TestSearchWideFTSPoolLetsRank15WinFusion|TestNamespaceBackfill ./internal/recall/...` passes.
- **Status**: [x] done

### Step 7: Configurable semantic similarity threshold
- **What**: Move the hardcoded `minSemanticSimilarity = 0.4` constant into `RecallConfig` as `recall.semantic_min_score` (YAML) / `NEUROX_RECALL_SEMANTIC_MIN_SCORE` (env), default `0.2`, and thread it through `semanticSearch`.
- **Why**: `internal/recall/semantic.go:16` defines `minSemanticSimilarity = 0.4` and applies it at line 90 (`if sim > minSemanticSimilarity`). This is uncalibrated for local embedding models (the comment itself admits "typically 0.4-0.7 for related content" — a wide, unverified range) and cannot be tuned per-deployment without a code change. The existing `RecallConfig` (`internal/config/config.go:83-86`) already holds `RRF.K` and `DisableBackfill` with the exact env-override pattern to follow (`config.go:260-272`).
- **Where**: `internal/config/config.go` (`RecallConfig` struct, `defaultConfig`, `applyEnvOverrides`), `internal/recall/semantic.go` (`semanticSearch` signature + threshold use), `internal/recall/engine.go` (`Engine` struct + `NewEngine`/`EngineOption`, mirroring `WithRRFK`), call sites that construct `Engine`.
- **How**:
  1. `internal/config/config.go` — extend `RecallConfig` (lines 83-86):
     ```go
     type RecallConfig struct {
         RRF             RRFConfig `yaml:"rrf"`
         DisableBackfill bool      `yaml:"disable_backfill"`
         SemanticMinScore float64  `yaml:"semantic_min_score"`
     }
     ```
  2. `defaultConfig` (lines 148-163) — set the default:
     ```go
     Recall: RecallConfig{
         RRF:              RRFConfig{K: 60},
         DisableBackfill:  false,
         SemanticMinScore: 0.2,
     },
     ```
  3. `applyEnvOverrides` (after the `RECALL_DISABLE_BACKFILL` block, lines 267-272) — add:
     ```go
     if value := strings.TrimSpace(os.Getenv(envPrefix + "RECALL_SEMANTIC_MIN_SCORE")); value != "" {
         if v, err := strconv.ParseFloat(value, 64); err == nil {
             cfg.Recall.SemanticMinScore = v
             cfg.Meta.Source = "env"
         }
     }
     ```
     (`strconv` is already imported in `config.go`.)
  4. `internal/recall/semantic.go` — remove the package-level `minSemanticSimilarity` constant (line 16) and add a parameter to `semanticSearch`:
     ```go
     func semanticSearch(ctx context.Context, db *sql.DB, provider embed.Provider, query string, limit int, filter semanticFilter, minScore float64) (map[string]float64, error) {
         ...
         if sim > minScore {
             candidates = append(candidates, scored{id: id, score: sim})
         }
     ```
  5. `internal/recall/engine.go` — add a `semanticMinScore float64` field to `Engine` (near `rrfK`, lines 22-28), default it in `NewEngine` (`e := &Engine{db: database, embedder: embed.Disabled{}, rrfK: 60, semanticMinScore: 0.2}`), add a `WithSemanticMinScore(v float64) EngineOption` mirroring `WithRRFK` (lines 130-139) — only override when `v > 0`. Update the `semanticSearch` call (line 195) to pass `e.semanticMinScore`.
  6. Wire the config value into engine construction wherever `NewEngine` is called with `WithRRFK(cfg.Recall.RRF.K)` today (grep `WithRRFK(` across `main.go`/`internal/mcp/`/`internal/api/` for the exact call sites) — add `recall.WithSemanticMinScore(cfg.Recall.SemanticMinScore)` alongside each.
  7. Tests:
     - `internal/config/config_test.go` (or wherever existing `RECALL_RRF_K` env test lives — grep for `TestApplyEnvOverrides` or similar): add a case asserting `SemanticMinScore` defaults to `0.2` and that `NEUROX_RECALL_SEMANTIC_MIN_SCORE=0.5` overrides it.
     - `internal/recall/engine_test.go`: `TestSemanticMinScoreThresholdApplied` — construct an engine with `WithSemanticMinScore(0.9)`, seed a candidate with cosine similarity ~0.5 (below threshold), assert it's excluded from semantic results; construct with `WithSemanticMinScore(0.1)`, assert the same candidate is now included.
- **Acceptance**: **Given** no config/env override, **When** `config.Load` runs, **Then** `cfg.Recall.SemanticMinScore == 0.2`. **Given** `NEUROX_RECALL_SEMANTIC_MIN_SCORE=0.5`, **When** `config.Load` runs, **Then** `cfg.Recall.SemanticMinScore == 0.5`. **Given** an engine constructed with a specific `WithSemanticMinScore`, **When** semantic search runs, **Then** only candidates above that threshold are included. `go test -tags fts5 -run TestApplyEnvOverrides|TestSemanticMinScoreThresholdApplied ./internal/config/... ./internal/recall/...` passes.
- **Status**: [x] done

### Step 8: Cross-signal boost — membership, not normalized-score-positive
- **What**: Gate the `crossSignalBoost` (1.2x) on channel membership (`FTSRank > 0 && SemRank > 0`) instead of `SemanticScore > 0 && ftsRelevance > 0`, and remove the now-dead `ftsRelevance`/`normalizeRelevance` min-max normalization if no other consumer needs it.
- **Why**: `applyScores` (`internal/recall/scoring.go:73`) gates the boost with:
  ```go
  if items[index].SemanticScore > 0 && ftsRelevance > 0 {
  ```
  where `ftsRelevance := normalizeRelevance(items[index].RawRelevance, minRelevance, maxRelevance)` (line 60, using `normalizeRelevance` at lines 126-134 — classic min-max normalization). Min-max maps the **worst** BM25 candidate in the pool to exactly `0` by construction (`normalizeRelevance` returns `1 - (raw-min)/(max-min)`, so `raw==max` (worst since BM25 is negative-is-better here — `matched` sorted `relevance ASC`, so `max` = numerically largest = worst) always yields `0`). That candidate loses the cross-signal boost purely because of where it landed in the normalization range — even though it genuinely appears in both the FTS and semantic channels (i.e. `FTSRank > 0 && SemRank > 0`). Membership in `FTSRank`/`SemRank` (populated at `engine.go:90-91`, already the source of truth for RRF since the earlier RRF refactor) is the correct signal, not a post-hoc normalized score. Additionally, since Step 5 introduces `RawRelevance = 0` sentinels from fact-sourced candidates into the same pool `normalizeRelevance` min-maxes over, the normalization is now mixing negative BM25 values with `0.0` sentinels from facts/backfill — another reason it's unreliable for this purpose.
- **Where**: `internal/recall/scoring.go` (`applyScores` lines 38-112, specifically the boost block at lines 71-76; `normalizeRelevance` lines 126-134; the `ftsRelevance` computation at line 60).
- **How**:
  1. Change the boost gate (`scoring.go:71-76`):
     ```go
     // Cross-signal boost: if appears in both FTS and semantic channels
     // (by rank membership, not normalized score), boost score.
     csBoost := 1.0
     if items[index].FTSRank > 0 && items[index].SemRank > 0 {
         csBoost = crossSignalBoost
         items[index].Score *= csBoost
     }
     ```
  2. Check remaining consumers of `ftsRelevance`/`normalizeRelevance` before deleting: `ftsRelevance` (line 60) is also stored nowhere else visible in the read code — grep `ftsRelevance\b` and `normalizeRelevance\(` across `internal/recall/` to confirm. If `ftsRelevance` has no other use after removing the boost gate, delete the local variable at line 60 and the `normalizeRelevance` function (lines 126-134) and the `minRelevance`/`maxRelevance` tracking loop (lines 46-56) **only if nothing else in `applyScores` reads them** — re-verify by re-reading the full function after Step 5's changes land, since Step 5 adds `FactRank` handling to the same function. If `Breakdown.Relevance` (the `ScoreBreakdown` field, `engine.go:50`) is meant to expose a human-readable FTS relevance number, keep a simplified version rather than deleting outright — check `ScoreBreakdown` usage/consumers (e.g. debug API endpoints) before removing anything user-facing.
  3. Tests:
     - `internal/recall/engine_test.go`: `TestCrossSignalBoostAppliesToWorstBM25CandidateInBothChannels` — seed a pool where the weakest BM25 match (highest raw relevance in the `matched` CTE ordering) also has a genuine semantic match; assert it receives `csBoost == crossSignalBoost` (`Breakdown.CrossSignalBoost == 1.2`, requires `Debug: true`) even though it would have normalized to `ftsRelevance == 0` under the old logic.
     - Confirm existing cross-signal tests (grep `crossSignalBoost\|CrossSignalBoost` in `engine_test.go`) still pass with the membership-based gate — they should, since membership is a strict superset check equivalent to the intended (not buggy) behavior for non-edge-case pools.
- **Acceptance**: **Given** a candidate that is the numerically-worst BM25 match in its pool (min-max normalizes to `ftsRelevance == 0`) but genuinely appears in both FTS and semantic result sets (`FTSRank > 0 && SemRank > 0`), **When** `applyScores` runs, **Then** it still receives the `1.2x` cross-signal boost. `go test -tags fts5 -run TestCrossSignalBoost ./internal/recall/...` passes, and the full `internal/recall` suite passes.
- **Status**: [x] done

### Step 9: Standardize FTS5 build tag across CI, release, and docs (chore)
- **What**: Pick one build tag (`fts5`, per the existing `.skynex/project-config.yaml`) and apply it consistently across CI, release automation, and all docs that currently use the longer `sqlite_fts5` form.
- **Why**: The vendored driver constraint (`vendor/github.com/mattn/go-sqlite3/sqlite3_opt_fts5.go:6`: `//go:build sqlite_fts5 || fts5`) accepts either tag, so nothing is currently broken — but the repo is inconsistent in practice, and worse than the brief's original estimate: **`sqlite_fts5` is actually the majority form** (`.github/workflows/ci.yml`, `.github/workflows/release.yml`, `README.md`, `README.es.md`, `benchmarks/README.md`, and 6 files under `docs/` all use `sqlite_fts5`; only `.skynex/project-config.yaml`, `benchmarks/BASELINE_2026.md`, and `plan/TECHNICAL.md`/`plan/PRODUCT.md` use `fts5`). A plain `go build` (no tag) compiles successfully but silently produces a binary where `observations_fts`/`facts_fts` (Step 5) queries fail at runtime with `no such module: fts5` — a footgun for anyone following inconsistent copy-pasted instructions.
- **Where**: `.github/workflows/ci.yml` (lines 24, 27, 30, 33), `.github/workflows/release.yml` (lines 41, 76, 108), `README.md` (lines 75, 263), `README.es.md` (lines 75, 263), `benchmarks/README.md` (lines 251, 255-256), `docs/vscode.md`, `docs/quickstart.md`, `docs/opencode.md`, `docs/cursor.md`, `docs/claude-desktop.md`, `docs/claude-code.md` (each has 1-2 occurrences of `-tags sqlite_fts5`). `CLAUDE.md` does not mention the tag (verified — no change needed there). Historical/completed planning docs under `plan/recall-merge-fix/*.md` reference `sqlite_fts5` extensively but describe already-shipped work — leave them untouched (they're a historical record, not active instructions).
- **How**:
  1. Replace every `-tags sqlite_fts5` → `-tags fts5` (and `-tags "sqlite_fts5"` variants) in the files listed under **Where** above (excluding `plan/recall-merge-fix/*`, `vendor/`, and this plan). A simple project-wide search/replace scoped to those exact paths is sufficient — do not touch `vendor/`.
  2. Add a runtime startup check: in `main.go` (or wherever the DB is opened at process start, near `db.Open` call), after opening the DB, run a cheap FTS5 availability probe and fail fast with a clear error if unavailable:
     ```go
     var fts5Check int
     if err := database.QueryRowContext(ctx, "SELECT count(*) FROM pragma_compile_options WHERE compile_options LIKE 'ENABLE_FTS5%'").Scan(&fts5Check); err != nil || fts5Check == 0 {
         return fmt.Errorf("FTS5 support is not compiled in — rebuild with `CGO_ENABLED=1 go build -tags fts5 ./...`")
     }
     ```
     Place this in `internal/db/db.go`'s `Open` function (after migrations run, before returning) so both `neurox serve`/`neurox mcp` and any `go run . benchmark` invocation catch a missing tag immediately instead of failing confusingly on the first `observations_fts` query.
  3. Verify no other tag variant (`sqlite_fts5`) remains active-instruction-reachable via `grep -rn "sqlite_fts5" --include="*.md" --include="*.yml" .` excluding `vendor/` and `plan/recall-merge-fix/`.
- **Acceptance**: **Given** `.github/workflows/ci.yml`, `.github/workflows/release.yml`, `README.md`, `README.es.md`, `benchmarks/README.md`, and all `docs/*.md` build instructions, **When** grepped for `-tags`, **Then** all active instructions use `fts5` (not `sqlite_fts5`). **Given** a binary built without any FTS5 tag (`CGO_ENABLED=1 go build ./...`, no `-tags`), **When** it starts and opens the DB, **Then** it fails fast with a clear "rebuild with -tags fts5" error instead of failing obscurely on the first search query. `go build -tags fts5 ./...` and `CGO_ENABLED=1 go build ./...` (no tag, expect the new startup check to trigger — verify via a manual run, not a `go test`, since this is a runtime-only guard) both behave as specified.
- **Status**: [x] done

### Step 10: Verification sweep — full regression + benchmark comparison
- **What**: Run the full build/vet/test suite plus the self-contained cognitive benchmark before/after this plan's changes, and record the comparison.
- **Why**: 9 changes across the hybrid search core need a single point of confidence that nothing regressed and that the intended improvements (Fix 1, 5, 6, 8 specifically target ranking quality) show up in an aggregate score, not just unit tests in isolation.
- **Where**: repo root; `benchmarks/README.md` documents the exact commands.
- **How**:
  1. Full verification:
     ```bash
     CGO_ENABLED=1 go build -tags fts5 ./...
     CGO_ENABLED=1 go vet -tags fts5 ./...
     CGO_ENABLED=1 go test -tags fts5 ./...
     CGO_ENABLED=1 go test -tags fts5 -race ./internal/recall/... ./internal/facts/... ./internal/db/...
     ```
  2. Baseline capture **before** merging Steps 1-9 (run once on a clean HEAD checkout, or use the numbers already in `benchmarks/BASELINE_2026.md` / `benchmarks/results_2026-03-22_large.json` if recent enough — check the file dates first since data may already be stale relative to current HEAD):
     ```bash
     CGO_ENABLED=1 go run -tags fts5 . benchmark --category cognitive --scale small --output /tmp/neurox_baseline.json
     ```
  3. Post-change run (same command) → `/tmp/neurox_postfix.json`, then diff:
     ```bash
     jq '.OverallScore, .Dimensions[] | {DimensionName, Score, Grade}' /tmp/neurox_baseline.json
     jq '.OverallScore, .Dimensions[] | {DimensionName, Score, Grade}' /tmp/neurox_postfix.json
     ```
     Pay particular attention to the **Knowledge Evolution** and **Cross-Session Memory** cognitive dimensions (`benchmarks/README.md` dimensions table) since they're the ones most sensitive to recency/ranking/staleness fixes (Steps 1, 4, 6, 8).
  4. **Opportunistic** LongMemEval run — only if a data file is available (none is present in the working copy today besides `locomo10.json`/`locomo10_converted.json`; do not block this step on obtaining `longmemeval_oracle.json`/`longmemeval_s.json`):
     ```bash
     CGO_ENABLED=1 go run -tags fts5 ./benchmarks/longmemeval/ -data <path-to-available-data>.json -output /tmp/lme_postfix.jsonl -limit 50
     ```
  5. Document results directly in this PLAN.md under this step (append a small before/after table once run) and/or in a new dated section of `benchmarks/BASELINE_2026.md` if the numbers are meant to become the new baseline of record.
- **Acceptance**: **Given** all 9 fix steps merged, **When** the full verification suite runs, **Then** `go build`/`go vet`/`go test` (including `-race` on the touched packages) all pass with zero failures. **Given** the `go run . benchmark --category cognitive` before/after comparison, **When** scores are diffed, **Then** no cognitive dimension regresses versus the pre-change baseline, and at least one of Knowledge Evolution / Cross-Session Memory shows measurable improvement (documented with actual numbers, not asserted).
- **Status**: [x] done

### Step 10 Results (2026-07-02)

Working tree = commit `96039f3` + Steps 1-9. Baseline `/tmp/neurox_baseline.json` captured from a clean HEAD worktree at `96039f3`; post-change run `/tmp/neurox_postfix.json`.

**Verification suite — all PASS:**

| Command | Result |
|---------|--------|
| `CGO_ENABLED=1 go build -tags fts5 ./...` | PASS |
| `CGO_ENABLED=1 go vet -tags fts5 ./...` | PASS |
| `CGO_ENABLED=1 go test -tags fts5 ./...` (forced `-count=1`) | PASS — 741 tests, 0 failed, 0 skipped |
| `CGO_ENABLED=1 go test -tags fts5 -race ./internal/recall/... ./internal/facts/... ./internal/db/... ./internal/config/...` (forced `-count=1`) | PASS — 229 tests, 0 failed, 0 data races |

**Cognitive benchmark before/after** (`benchmark --category cognitive --scale small`):

| Dimension | Baseline | Post | Delta |
|-----------|----------|------|-------|
| **Overall** | 88.00 (A) | 90.75 (A) | **+2.75** |
| Knowledge Evolution | 90.83 (A) | 90.83 (A) | 0.00 |
| Signal vs Noise | 95.91 (S) | 95.91 (S) | 0.00 |
| Cross-Session Memory | 97.63 (S) | 97.63 (S) | 0.00 (at ceiling: 24/24, recall 100%) |
| Temporal Cognition | 93.15 (A) | 93.15 (A) | 0.00 |
| 30-Day Brain Simulation | 62.50 (C) | 76.25 (B) | **+13.75** |

Notes:
- No dimension regressed. The post-change benchmark was run twice; both runs produced byte-identical scores (deterministic with `FakeEmbedder`), so the deltas are real, not run-to-run variance.
- Acceptance nuance: Knowledge Evolution and Cross-Session Memory did not move — Cross-Session was already at ceiling in the baseline (24/24, recall 100%), leaving no headroom. The measurable improvement landed in 30-Day Brain Simulation (+13.75, C→B), lifting Overall +2.75.
- LongMemEval: skipped — `longmemeval_oracle.json`/`longmemeval_s.json` absent from the working copy (opportunistic per Risks table; not blocking).

## Verification

```bash
CGO_ENABLED=1 go build -tags fts5 ./...
CGO_ENABLED=1 go vet -tags fts5 ./...
CGO_ENABLED=1 go test -tags fts5 ./...
CGO_ENABLED=1 go test -tags fts5 -race ./internal/recall/... ./internal/facts/... ./internal/db/... ./internal/config/...
CGO_ENABLED=1 go run -tags fts5 . benchmark --category cognitive --scale small
```

## Risks

| Risk | Severity | Mitigation |
|------|----------|------------|
| A pre-existing `PLAN.md` at repo root (benchmark portfolio + graph traversal/reranking, steps 7-8 pending) was overwritten by this plan | medium | Preserved verbatim at `plan/PLAN-benchmark-portfolio.md` before writing this file. Steps 7-8 of that plan (graph traversal, reranking) are untouched by this work and should be resumed from that file, not lost. |
| `TestSearchActivationBoostUsesLastAccessed` (Step 4) currently encodes the recency-feedback bug as expected behavior | high if missed | Step 4's How explicitly rewrites this test; flagged so the coder doesn't treat a red test as a regression to revert. |
| Step 5's `RawRelevance = 0` sentinels from fact/backfill candidates already complicate `normalizeRelevance` before Step 8 removes it | medium | Sequenced 5 → 6 → 8 in this plan (facts channel before pool sizing before cross-signal fix) so Step 8's How explicitly accounts for Step 5's sentinel values when deciding what's safe to delete. |
| `benchmarks/longmemeval/` data files (`longmemeval_oracle.json`/`longmemeval_s.json`) referenced by the original brief and by `benchmarks/BASELINE_2026.md` are **not present** in the working copy (only `locomo10.json`/`locomo10_converted.json` exist) | low | Step 10 treats LongMemEval as opportunistic, not blocking; `go run . benchmark --category cognitive` (self-contained, `FakeEmbedder`, no external data) is the primary regression gate instead. |
| Build tag drift (Step 9) is wider than originally scoped — `sqlite_fts5` is the majority form across CI/release/docs, not a minor outlier | low | Step 9's Where/How lists every file found via repo-wide grep, not just the 2 files the brief named. |
| Step 3's async access queue changes `Engine` lifecycle (needs `Close()`/shutdown wiring) | medium | Step 3 explicitly requires locating and mirroring existing shutdown wiring for `SaveQueue.Stop()` before considering the step done — do not skip this or queued bumps leak on process exit. |
| Single-writer WAL (`internal/db/db.go`, max 1 connection) means Step 3's periodic flush and Step 5's fact-write triggers both compete with save/consolidation writes | low | Step 3's flush is batched (every 2s or 100 events) specifically to minimize write frequency; Step 5's triggers only fire on existing fact writes (no new write volume, just index maintenance on writes that already happen). |

## Open Decisions
- Step 3: per-ID loop UPDATE vs single batched `IN (...)` UPDATE for the access-queue flush — either satisfies acceptance criteria; left to the coder's judgment, noted explicitly in the How section.
- Step 9: whether to also update `plan/recall-merge-fix/*.md` for tag consistency — recommended **no** (historical record of already-shipped work), but flagged for a human call if strict repo-wide consistency is preferred over preserving historical docs as-written.
- Step 10: whether benchmark results get appended to `benchmarks/BASELINE_2026.md` as the new baseline of record, or kept as an ad-hoc before/after note in this PLAN.md — left as a judgment call at execution time depending on how stale the existing baseline is.

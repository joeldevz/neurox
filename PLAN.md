# Plan: Fix Search Recall Quality — Semantic Fallback, Stemming & Type Boost

## Goal

Fix the core recall weakness that causes Neurox to return 0 results when stored observations use different words than the query. This is the #1 product-quality issue — Cross-Session Memory benchmark scored 33.3% (Grade F) because of it.

## Business Context

- **Problem**: A user saves "We prefer PostgreSQL over MySQL" and later asks "what database do we use" — Neurox returns nothing. The semantic search *finds* the match but discards it because FTS5 didn't find it first. This makes the memory system feel broken.
- **Impact**: Grade F on Cross-Session Memory, Grade C on 30-Day Brain Simulation. Users lose trust when their memory system "forgets" things it actually has stored.
- **Success criteria**: Cross-Session Memory benchmark improves to B+ or better. Zero-result queries with semantic matches drop to near zero.

## Technical Context

### Root cause analysis

The recall pipeline in `internal/recall/engine.go` (line 101-189) works like this:

```
1. FTS5 keyword search → candidates (matched observations)
2. Semantic search → scores (cosine similarity for ALL embeddings)
3. MERGE: only boost FTS candidates that also appear in semantic results
4. Score & rank
```

**The bug is in step 3**: semantic search results that DON'T overlap with FTS candidates are silently discarded. The merge is an intersection-boost, not a union. When FTS returns 0 results, the entire pipeline returns empty even if semantic search found perfect matches.

### Current code (engine.go lines 138-155)

```go
// Hybrid: if embeddings available, boost candidates that also appear in semantic search
if embed.IsAvailable(e.embedder) {
    semScores, semErr := semanticSearch(ctx, e.db, e.embedder, normalized.Query, normalized.Limit*2, semFilter)
    if semErr == nil && len(semScores) > 0 {
        for i := range candidates {
            if semScore, ok := semScores[candidates[i].ID]; ok {
                if semScore > 0 {
                    candidates[i].SemanticScore = semScore
                }
            }
        }
    }
}
```

This only sets `SemanticScore` on existing FTS candidates. Semantic-only results are thrown away.

### Other issues

- **FTS5 uses default tokenizer**: No stemming. "authenticate" doesn't match "authentication".
- **No observation_type boost**: Searching "what gotchas" doesn't prioritize type=gotcha observations.
- **No zero-result fallback**: When FTS returns empty, search returns empty — no semantic fallback.
- **Facts not in recall**: Knowledge graph facts are completely disconnected from search.

### Key files

- `internal/recall/engine.go` — Search() main pipeline (335 lines)
- `internal/recall/semantic.go` — semanticSearch() brute-force cosine (109 lines)
- `internal/recall/scoring.go` — tri-factor scoring (108 lines)
- `internal/recall/fts.go` — FTS5 query builder (19 lines)
- `internal/recall/filters.go` — SQL query builder with filters (118 lines)
- `internal/recall/engine_test.go` — existing tests (1314 lines)

## Implementation Steps

### Step 1: Add semantic-only results when FTS returns few results
- **What**: Change `Search()` in `engine.go` to merge semantic-only results into the candidate list when FTS returns fewer than `limit` results. When FTS returns 0 results, use semantic search as the sole source. The merge logic should:
  1. Run FTS search as before → `ftsCandidates`
  2. Run semantic search → `semScores` (map[id]score)
  3. For FTS candidates: boost with semantic scores (existing behavior)
  4. **NEW**: If `len(ftsCandidates) < limit`, load the top semantic-only results (those NOT in ftsCandidates) directly from the DB and add them as candidates with `RawRelevance = 0` and `SemanticScore = cosine_score`
  5. The scoring formula already handles this: `relevance = max(ftsRelevance, semanticScore)` — so semantic-only results will use their cosine similarity as the relevance component
- **Why**: This is the P0 fix. Without it, conceptual/synonym queries return 0 results even when perfect semantic matches exist.
- **Where**: `internal/recall/engine.go` (Search function), `internal/recall/semantic.go` (may need a helper to load observation data by IDs)
- **Acceptance**:
  - Save observation "We prefer PostgreSQL over MySQL" → search "what database do we use" → finds it via semantic match
  - Save observation "Authentication uses JWT tokens" → search "login mechanism" → finds it via semantic match  
  - FTS results are still preferred and ranked higher when they exist (cross-signal boost applies)
  - When embeddings are disabled, behavior is unchanged (FTS-only)
  - Existing recall tests pass
  - New test: `TestSemanticFallbackWhenFTSReturnsEmpty`
  - New test: `TestSemanticFillsRemainingSlots`
  - `go build -tags fts5 ./...` passes
  - `go test -tags fts5 ./internal/recall/... -v` passes
- **Status**: [x] done

### Step 2: Add FTS5 prefix matching for better recall
- **What**: Modify `buildFTSMatchQuery()` in `fts.go` to append a wildcard `*` to query tokens, enabling prefix matching. `"auth"` becomes `"auth" OR "auth"*` which matches `authentication`, `authorize`, `auth-token`, etc. Only append `*` to tokens longer than 3 characters to avoid noisy short-token matches.
- **Why**: Without prefix matching, "auth" doesn't find "authentication". This is the most common recall failure after the semantic gap.
- **Where**: `internal/recall/fts.go` (buildFTSMatchQuery)
- **Acceptance**:
  - Search "auth" finds observations containing "authentication", "authorize", "auth-token"
  - Search "db" does NOT use prefix match (too short, ≤3 chars)
  - Search "config" finds "configuration", "configuring"
  - Existing FTS tests still pass
  - New test: `TestFTSPrefixMatching`
  - `go test -tags fts5 ./internal/recall/... -v` passes
- **Status**: [x] done

### Step 3: Add observation_type boost in scoring
- **What**: In `scoring.go`, detect when the query mentions an observation type keyword (e.g., "gotcha", "decision", "bug", "pattern", "preference") and apply a 1.3x multiplier to candidates whose `ObservationType` matches the detected type. Add a `detectTypeIntent()` function that maps query keywords to observation types.
- **Why**: Searching "what gotchas should I know" should prioritize type=gotcha observations. This directly fixes multiple failures in the 30-Day Brain Simulation benchmark.
- **Where**: `internal/recall/scoring.go` (new detectTypeIntent + multiplier in applyScores)
- **Acceptance**:
  - Search "what gotchas" boosts observations with type=gotcha
  - Search "architecture decisions" boosts type=decision
  - Search "any bugs" boosts type=bugfix
  - Search "my preferences" boosts type=preference
  - Boost is multiplicative (1.3x), not absolute — relevance still matters
  - New test: `TestTypeIntentBoost`
  - `go test -tags fts5 ./internal/recall/... -v` passes
- **Status**: [x] done

### Step 4: Integrate facts into recall results
- **What**: After the main search pipeline, also query `facts.Store.Search()` for matching facts. Convert matching facts into lightweight result entries (using the observation they came from) and merge them into the result set if they're not already present. Facts should appear with a `[fact]` prefix in their title for clarity.
- **Why**: The knowledge graph has structured facts (subject-predicate-object) that are highly relevant to direct questions like "what database do we use" but are never consulted during recall.
- **Where**: 
  - `internal/recall/engine.go` — add fact search after main pipeline
  - Engine needs access to `*facts.Store` (add to Engine struct or pass as option)
- **Acceptance**:
  - If a fact exists for "database | current | PostgreSQL", searching "what database" returns the source observation
  - Facts don't duplicate observations already in results
  - When no fact store is configured, behavior is unchanged
  - New test: `TestFactsIntegratedInRecall`
  - `go build -tags fts5 ./...` passes
  - `go test -tags fts5 ./internal/recall/... -v` passes
- **Status**: [x] done

### Step 5: Add tests and run benchmark
- **What**: 
  1. Write integration tests that cover the full recall pipeline with semantic fallback, prefix matching, type boost, and fact integration working together
  2. Run the Cross-Session Memory benchmark dimension to measure improvement
  3. Run `go test -race` to verify no race conditions in the modified code
- **Why**: Verify the combined changes actually fix the Grade F problem and don't introduce regressions.
- **Where**: `internal/recall/engine_test.go`, benchmark CLI
- **Acceptance**:
  - All existing tests pass
  - New integration tests pass
  - No race conditions
  - `go test -tags fts5 -race ./internal/recall/...` passes
  - `go build -tags fts5 ./...` passes
- **Status**: [x] done

## Verification

```bash
# Full build and vet
CGO_ENABLED=1 go build -tags fts5 ./...
CGO_ENABLED=1 go vet -tags fts5 ./...

# Recall tests (verbose)
CGO_ENABLED=1 go test -tags fts5 -v ./internal/recall/...

# Race detection
CGO_ENABLED=1 go test -tags fts5 -race ./internal/recall/...

# Full test suite
CGO_ENABLED=1 go test -tags fts5 ./...

# Benchmark (optional, requires full setup)
./neurox benchmark --scale small --dimensions cross-session
```

## Sequencing & Dependencies

```
Step 1 (semantic fallback) ──> independent (P0 critical fix)
Step 2 (prefix matching)   ──> independent
Step 3 (type boost)        ──> independent
Step 4 (facts integration) ──> independent
Step 5 (tests + benchmark) ──> depends on Steps 1-4
```

Steps 1, 2, 3, 4 can run in parallel. Step 5 runs last.

## Risks / Notes

- **Semantic fallback requires embeddings**: If no embedding provider is configured, the fallback path is a no-op. Users without Ollama or a remote embedding service won't benefit from Step 1. This is acceptable — the system degrades gracefully.
- **Prefix matching could increase noise**: `"auth"*` might match "author", "authority" which are unrelated. The BM25 scoring should handle this (lower relevance for partial matches), and the 3-character minimum prevents worst-case noise from 1-2 character tokens.
- **Type boost is heuristic**: The keyword-to-type mapping in Step 3 is a simple dictionary. It won't catch every possible way a user might ask for gotchas or decisions. But it handles the most common patterns and directly addresses benchmark failures.
- **Facts integration increases Engine dependencies**: Adding `*facts.Store` to the recall engine couples two packages. Use an interface or optional dependency pattern to keep it clean.
- **Performance**: Semantic fallback adds a second DB query to load observation data for semantic-only results. This is bounded by `limit` (max 50) and uses primary key lookups, so impact is negligible.

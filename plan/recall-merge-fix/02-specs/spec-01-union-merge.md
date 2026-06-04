# Spec-01 — Union Merge

**Component:** `internal/recall/engine.go`  
**Lines affected (current):** 152-210  
**Status:** Pending implementation

---

## Problem: Limit-Saturation

The current merge at `engine.go:152-210` is **FTS-anchored with conditional semantic-only fill**:

- **Phase 1 (162-171):** boosts FTS candidates that also appear in semantic results (correct — no change needed)
- **Phase 2 (173-208):** adds semantic-only candidates **only if** `len(candidates) < normalized.Limit`

**The bug:** when FTS returns ≥ `limit` candidates, `len(candidates) < normalized.Limit` is **false** → Phase 2 does not run → high-scoring semantic-only matches are silently dropped.

This is NOT "FTS returns empty" (already handled by Phase 2 when FTS is sparse). This is **limit-saturation**: FTS fills the quota before semantic-only candidates get a chance. The current behavior can be described as: *effectively an intersection when FTS saturates the limit*.

---

## Target Behavior

Compute the **union** of FTS results and semantic results **before** applying the `limit` truncation. Every document that appears in either channel enters the candidate pool. The pool is scored by RRF (spec-02) and the tri-factor pipeline, and **then** truncated to `limit` after the final sort.

- For semantic-only candidates (not in FTS): loaded from DB via `loadObservationsByIDs`, `SemanticScore` set to cosine value, `RawRelevance = 0`.
- For FTS-only candidates: `SemanticScore` remains 0 (as today).
- For candidates in both channels: existing Phase 1 boosting logic is preserved.

---

## Rank Derivation (net-new work)

`semanticSearch` (semantic.go:28) returns `map[string]float64` (ID → cosine similarity score) — it does **not** return ranks. FTS returns ordered rows from the sqlite-fts5 query, not numbered ranks. Both rank sequences must be **derived**:

- **Semantic rank:** sort the cosine map descending by score; assign 1-based rank (highest score = rank 1). Tie-break: sort by ID ascending for determinism.
- **FTS rank:** use the scan order of FTS rows (1st scanned row = rank 1). The FTS query already returns rows ordered by BM25 relevance desc.

These derived ranks are stored as fields on the `candidate` struct and passed to `applyScores` for use by the RRF formula (see spec-02).

---

## Behavioral Specification

### Scenario A — FTS saturates the limit, strong semantic-only match exists

```gherkin
Given  a corpus of 20 observations
And    FTS returns exactly limit=10 candidates for query Q
And    observation X is NOT in the FTS results
And    observation X has a semantic cosine similarity higher than the lowest-scoring FTS candidate
When   Search() is called with query Q and limit=10
Then   the result set MUST contain observation X
And    the result set size MUST NOT exceed limit
And    observation X's SemanticScore MUST equal its cosine similarity
And    observation X's RawRelevance MUST be 0
```

This is the canonical limit-saturation bug scenario. The union must include X even though FTS is full.

### Scenario B — FTS returns fewer than limit (normal case, must not regress)

```gherkin
Given  FTS returns 5 candidates for query Q with limit=10
And    semantic search returns 8 candidates total (3 semantic-only)
When   Search() is called
Then   all 5 FTS candidates appear in results
And    up to 3 semantic-only candidates appear in results (subject to scoring)
And    total results ≤ 10
```

### Scenario C — No embeddings available (pure FTS mode)

```gherkin
Given  the engine has no embedder configured (embed.IsAvailable = false)
When   Search() is called
Then   behavior is identical to current FTS-only path
And    no semantic calls are made
And    no rank derivation occurs
```

### Scenario D — DisableBackfill=true (diagnostic mode)

```gherkin
Given  RecallConfig.DisableBackfill = true
And    shouldNamespaceBackfill gate would otherwise return true
When   Search() is called
Then   loadNamespaceBackfill is NOT called
And    results are derived purely from union merge + RRF + remaining multipliers
```

---

## Scope boundary — Facts are a third source, NOT part of the union merge

Facts (`searchFacts`, engine.go:212-232) are a **third candidate source**, distinct from FTS observations and semantic observations. They are appended **after** the FTS∪semantic union block (152-210), deduped by observation ID, with `RawRelevance=0`, and ranked by the existing tri-factor (importance/recency) in `applyScores`. Facts are **NOT** part of the union merge and are **NOT** subject to RRF in this task — their existing logic is unchanged.

**The union merge change (this spec) must preserve the fact-integration block (engine.go:212-232) untouched and in its current position.** Do not relocate, reorder, or modify the `searchFacts` call or the fact candidate append logic.

---

## Implementation Notes

The merge block replacement at `engine.go:152-210` must:

1. Call `semanticSearch(ctx, e.db, e.embedder, normalized.Query, normalized.Limit*2, semFilter)` — **unchanged** (the `limit*2` cap is a DB performance bound, not a merge bound)
2. Build `ftsIDs` map from FTS scan results — **unchanged**
3. Derive **FTS ranks**: immediately after scanning FTS rows, record scan-order index as rank for each candidate
4. Derive **semantic ranks**: sort `semScores` map desc by value, assign 1-based rank (tie-break by ID asc)
5. Boost FTS candidates with semantic scores (Phase 1 logic) — **unchanged**
6. **New — union:** collect ALL semantic-only IDs (those in `semScores` but NOT in `ftsIDs`) **regardless** of `len(candidates)` vs `limit`
7. Load semantic-only candidates via `loadObservationsByIDs(ctx, e.db, semanticOnlyIDs, normalized)` (existing function)
8. Set `SemanticScore` and `RawRelevance = 0` on semantic-only candidates — same as current Phase 2 logic
9. Append all semantic-only candidates to `candidates` (union complete before truncation)
10. Store FTS rank and semantic rank on each `candidate` (add `FTSRank int`, `SemRank int` fields to candidate struct)
11. `shouldNamespaceBackfill` guard: if `RecallConfig.DisableBackfill` is true, return false from `shouldNamespaceBackfill` regardless of other conditions
12. Truncation happens **after** `applyScores` sorts by final score — no early truncation in the merge block

The `normalized.Limit*2` cap on `semanticSearch` is unchanged — it bounds DB load, not union membership.

---

## Files

- `internal/recall/engine.go` — lines 152-210 (merge block rewrite)
- `internal/recall/engine.go` — candidate struct (add `FTSRank int`, `SemRank int` fields)
- `internal/recall/engine.go` — `shouldNamespaceBackfill` (318-326) — add `DisableBackfill` guard

---

## Acceptance

- Unit test `TestUnionMerge_LimitSaturation`: FTS returns exactly `limit` candidates; semantic-only doc with highest cosine → must appear in output, total results ≤ limit
- Unit test `TestUnionMerge_NoRegression`: FTS < limit → existing behavior preserved
- Build: `CGO_ENABLED=1 go build -tags sqlite_fts5 ./...` succeeds
- Tests: `CGO_ENABLED=1 go test -tags sqlite_fts5 -run TestUnionMerge ./internal/recall/...` pass

# Spec-02 — RRF Scoring

**Component:** `internal/recall/scoring.go`  
**Lines affected (current):** 61-65 (relevance term only)  
**Status:** Pending implementation

---

## Current Behavior

`scoring.go:61-65`:

```go
// Hybrid: use max(FTS relevance, semantic cosine similarity) as relevance
relevance := ftsRelevance
if items[index].SemanticScore > relevance {
    relevance = items[index].SemanticScore
}
```

This is the `max()` fusion function. It produces the `Relevance` component fed into the tri-factor score at line 67.

---

## Target Behavior

Replace the `max()` relevance term with **Reciprocal Rank Fusion (RRF)**:

```
rrf_score(doc) = 1/(k + rank_fts(doc)) + 1/(k + rank_sem(doc))
```

Where:
- `k` = `RecallConfig.RRF.K` (default 60, configurable via YAML and env — see spec-03)
- `rank_fts(doc)` = FTS rank if doc appears in FTS results, else **0 (absent, contributes nothing)**
- `rank_sem(doc)` = semantic rank if doc appears in semantic results, else **0 (absent, contributes nothing)**
- For FTS-only docs: `rrf_score = 1/(k + rank_fts)` (one-sided)
- For semantic-only docs: `rrf_score = 1/(k + rank_sem)` (one-sided)
- For docs in both channels: `rrf_score = 1/(k + rank_fts) + 1/(k + rank_sem)` (two-sided)

**One-sided contribution is intentional** — union merge means every doc gets scored based on whichever channels it appears in.

---

## What RRF Replaces vs What Stays

| Component | Current | After fix |
|---|---|---|
| `relevance` term (scoring.go:61-65) | `max(ftsRelevance, SemanticScore)` | `rrfScore(FTSRank, SemRank, k)` |
| tri-factor (scoring.go:67) | `Recency×0.3 + Importance×0.3 + Relevance×0.4` | **unchanged** — RRF value feeds into the Relevance component |
| crossSignalBoost ×1.2 (69-74) | conditional on SemanticScore>0 && ftsRelevance>0 | **unchanged — stays** |
| temporalMultiplier (81-82) | unchanged | **unchanged** |
| typeIntentBoost ×1.3 (84-89) | unchanged | **unchanged** |
| namespaceBackfillBoost ×0.35 (91-93) | unchanged | **unchanged** |

> `crossSignalBoost` is NOT removed. It is outside the frozen scope of this task.

---

## Rank Derivation (required, net-new work)

RRF requires integer ranks. Neither FTS nor semantic results currently expose ranks as integers — this is **net-new work** implemented in the merge block (`engine.go:152-210`) and stored on the `candidate` struct as `FTSRank int` and `SemRank int`.

### Semantic rank derivation (in engine.go merge block)

```go
// Sort cosine map descending; assign 1-based ranks. Tie-break: sort by ID ascending.
type idScore struct{ id string; score float64 }
sorted := make([]idScore, 0, len(semScores))
for id, s := range semScores {
    sorted = append(sorted, idScore{id, s})
}
sort.Slice(sorted, func(i, j int) bool {
    if sorted[i].score != sorted[j].score {
        return sorted[i].score > sorted[j].score
    }
    return sorted[i].id < sorted[j].id
})
semRanks := make(map[string]int, len(sorted))
for i, item := range sorted {
    semRanks[item.id] = i + 1  // 1-based
}
```

Note: `idScore` struct already exists locally in engine.go (lines 180-183) as `struct{ id string; score float64 }` — reuse the pattern.

### FTS rank derivation (in engine.go merge block)

FTS rows are returned in BM25 relevance order by the sqlite-fts5 query. Scan order directly gives rank:

```go
ftsRanks := make(map[string]int, len(candidates))
for i, c := range candidates {  // candidates as scanned from FTS
    ftsRanks[c.ID] = i + 1  // 1-based, 1st row = rank 1
}
```

### RRF score function (in scoring.go or a shared helper)

```go
func rrfScore(ftsRank, semRank, k int) float64 {
    score := 0.0
    if ftsRank > 0 {
        score += 1.0 / float64(k+ftsRank)
    }
    if semRank > 0 {
        score += 1.0 / float64(k+semRank)
    }
    return score
}
```

---

## Data flow: ranks from engine.go to scoring.go

Ranks computed in the merge block (engine.go) must be carried through `applyScores`. The approach:

1. Add `FTSRank int` and `SemRank int` fields to the `candidate` struct
2. Populate during merge block: set `c.FTSRank = ftsRanks[c.ID]` and `c.SemRank = semRanks[c.ID]` on each candidate (0 if absent in that channel)
3. Pass `rrfK int` to `applyScores` — add as a parameter:

```go
func applyScores(items []candidate, weights ScoreWeights, now time.Time, intent TemporalIntent, mentionMap map[string][]mentionInfo, debug bool, query string, rrfK int)
```

4. In `applyScores`, replace the `max()` block at lines 61-65 with:

```go
relevance := rrfScore(items[index].FTSRank, items[index].SemRank, rrfK)
```

5. Update `ScoreBreakdown` struct to add `RRFScore float64` for debug mode.

---

## Academic framing (honest)

RRF is the correct choice here because Neurox's problem is **recall**, not ranking precision. RRF as a set-union operation ensures no candidate is dropped due to channel-specific quota. Bruch et al. 2022 (arXiv:2210.11934) shows convex combination outperforms RRF when labeled tuning data exists — Neurox does not have that data. Without a tuned alpha, CC is fragile (sensitive to normalization, poor out-of-domain transfer). k=60 is the zero-shot production default.

Known limitation: RRF discards score magnitude (rank #1 at cosine 0.99 = rank #1 at cosine 0.51). Accepted — the goal is recall improvement, not precision tuning.

---

## Behavioral Specification

### Scenario A — Document in both channels ranked differently

```gherkin
Given  doc D has FTS rank 3 and semantic rank 1 with k=60
When   RRF score is computed
Then   rrf(D) = 1/(60+3) + 1/(60+1) = 1/63 + 1/61 ≈ 0.0322
And    this value is used as the Relevance input to the tri-factor score at scoring.go:67
```

### Scenario B — Semantic-only document (FTS rank absent, FTSRank=0)

```gherkin
Given  doc D appears only in semantic results with SemRank=2 and k=60
And    doc D has FTSRank=0
When   RRF score is computed
Then   rrf(D) = 0 + 1/(60+2) = 1/62 ≈ 0.0161
And    crossSignalBoost does NOT apply (SemanticScore > 0 but ftsRelevance == 0)
```

### Scenario C — FTS-only document

```gherkin
Given  doc D appears only in FTS results with FTSRank=1 and k=60
And    doc D has SemRank=0
When   RRF score is computed
Then   rrf(D) = 1/(60+1) + 0 = 1/61 ≈ 0.0164
```

### Scenario D — k override via config

```gherkin
Given  NEUROX_RECALL_RRF_K=30 is set in the environment
When   Search() is called
Then   k=30 is used in all RRF computations (passed through RecallConfig.RRF.K to applyScores)
```

---

## Files

- `internal/recall/scoring.go` — lines 61-65: replace `max()` with `rrfScore(...)` call
- `internal/recall/scoring.go` — `applyScores` signature: add `rrfK int` parameter
- `internal/recall/scoring.go` — `ScoreBreakdown` struct: add `RRFScore float64` field
- `internal/recall/engine.go` — `candidate` struct: add `FTSRank int`, `SemRank int` fields
- `internal/recall/engine.go` — merge block: derive and assign ranks, call `applyScores` with `rrfK`

---

## Acceptance

- Unit test `TestRRFScore`: verify formula for dual-channel, FTS-only, and semantic-only docs
- Unit test `TestRankDerivation`: verify semantic rank derivation produces stable sort with tie-break
- `CGO_ENABLED=1 go test -tags sqlite_fts5 -run TestRRF ./internal/recall/...` passes
- `CGO_ENABLED=1 go build -tags sqlite_fts5 ./...` succeeds

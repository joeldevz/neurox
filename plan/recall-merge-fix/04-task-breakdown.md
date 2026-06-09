# 04 — Task Breakdown

> 1 parent task + 4 subtasks. Orden de dependencia estricto. Estimación total ~1.5-2h.

**Last updated:** 2026-06-03

---

## Parent

| Field | Value |
|---|---|
| **Title** | Fix recall merge: union + RRF with k configurable + diagnostic backfill flag |
| **Type** | Task (engineering) |
| **Description** | Hybrid search en Neurox descarta silenciosamente matches semánticos cuando FTS satura el límite (limit-saturation bug en `engine.go:152-210`). El merge actual es FTS-anchored con conditional semantic-only fill — efectivamente una intersección cuando FTS llena el límite. El fix es: (1) crear `RecallConfig` struct con `RRF.K` y `DisableBackfill`, (2) cambiar merge a unión explícita cargando semantic-only desde DB + derivar ranks para ambos canales, (3) reemplazar `max(fts, semantic)` con Reciprocal Rank Fusion usando k configurable, (4) medir diagnósticamente con backfill desactivado para validar que el fix es estructural. |
| **Acceptance criteria** | Los 6 gates de 03-acceptance-gates.md |
| **Estimation** | ~1.5-2h total |
| **Out of scope** | Ver 05-out-of-scope.md |

---

## Subtasks

### Subtask 1 — Create `RecallConfig` struct + `DisableBackfill` + `RRF.K` in `internal/config/config.go`

| Field | Value |
|---|---|
| **Type** | Task |
| **Complexity** | **M** (~20-30 min) |
| **Depends on** | — |
| **Blocks** | 2, 3 |
| **Files** | `internal/config/config.go` |
| **Description** | `RecallConfig` does NOT exist in the codebase. Must be created. Steps: (a) define `RRFConfig{K int}` and `RecallConfig{RRF RRFConfig; DisableBackfill bool}` in `config.go`, (b) add `Recall RecallConfig` field to `Config` struct, (c) set defaults in `defaultConfig()` (`K:60`, `DisableBackfill:false`), (d) add manual `os.Getenv` parsing in `applyEnvOverrides` for `NEUROX_RECALL_RRF_K` (strconv.Atoi) and `NEUROX_RECALL_DISABLE_BACKFILL` (strconv.ParseBool), (e) validate `K > 0` and return error from `Load()` if invalid. NO struct tags `env:` or `default:` — they are not processed. Import `strconv` if not already present. |
| **Acceptance criteria** | • `RRFConfig{K int}` and `RecallConfig` compile with correct `yaml:` tags<br>• `Config.Recall.RRF.K` defaults to 60 when no YAML/env set<br>• `Config.Recall.DisableBackfill` defaults to false<br>• `NEUROX_RECALL_RRF_K=30` env var → `K=30`<br>• `NEUROX_RECALL_DISABLE_BACKFILL=true` env var → `DisableBackfill=true`<br>• `K ≤ 0` → `config.Load()` returns error<br>• `CGO_ENABLED=1 go build -tags sqlite_fts5 ./...` passes |
| **Spec** | [Spec-03 — Config Fields](./02-specs/spec-03-config-fields.md) |

---

### Subtask 2 — Replace intersection merge with union in `engine.go:152-210` + rank derivation

| Field | Value |
|---|---|
| **Type** | Task |
| **Complexity** | **M** (~40 min) |
| **Depends on** | 1 |
| **Blocks** | 3 |
| **Files** | `internal/recall/engine.go` |
| **Description** | (a) Add `FTSRank int` and `SemRank int` fields to the `candidate` struct. (b) After scanning FTS rows (engine.go:140-150), immediately record scan-order rank for each candidate: `ftsRanks[c.ID] = i+1`. (c) Derive semantic ranks from `semScores` map: sort by value desc (tie-break ID asc) → assign 1-based rank → store in `semRanks` map. (d) Boost Phase 1 (162-171) is unchanged. (e) REPLACE Phase 2 condition: instead of `if len(candidates) < normalized.Limit`, collect ALL semantic-only IDs (not in `ftsIDs`) regardless of how many FTS candidates exist. Load them via `loadObservationsByIDs`, set `SemanticScore` and `RawRelevance=0`. (f) Set `FTSRank` and `SemRank` on every candidate from the maps (0 if absent in that channel). (g) Append semantic-only candidates to `candidates` (union). (h) Add `DisableBackfill` guard in `shouldNamespaceBackfill` — pass engine's `RecallConfig.DisableBackfill` and return false when true. Truncation remains AFTER `applyScores` sorts — no early truncation in the merge block. |
| **Acceptance criteria** | • `candidate` struct has `FTSRank int`, `SemRank int` fields<br>• FTS-saturated scenario (Scenario A of spec-01): doc with high cosine not in FTS appears in results<br>• FTS-under-limit scenario (Scenario B): behavior preserved, no regression<br>• No-embeddings scenario (Scenario C): pure FTS path unchanged<br>• `shouldNamespaceBackfill` returns false when `DisableBackfill=true`<br>• `CGO_ENABLED=1 go test -tags sqlite_fts5 -run TestUnionMerge ./internal/recall/...` passes |
| **Spec** | [Spec-01 — Union Merge](./02-specs/spec-01-union-merge.md) |

---

### Subtask 3 — Replace `max(fts, semantic)` with RRF in `scoring.go:61-65`

| Field | Value |
|---|---|
| **Type** | Task |
| **Complexity** | **M** (~20-30 min) |
| **Depends on** | 1, 2 |
| **Blocks** | 4 |
| **Files** | `internal/recall/scoring.go` |
| **Description** | (a) Add `rrfScore(ftsRank, semRank, k int) float64` helper function: `score=0.0; if ftsRank>0 { score += 1.0/float64(k+ftsRank) }; if semRank>0 { score += 1.0/float64(k+semRank) }; return score`. (b) Update `applyScores` signature to accept `rrfK int` as parameter. (c) Replace the `max()` block at lines 61-65 with: `relevance := rrfScore(items[index].FTSRank, items[index].SemRank, rrfK)`. (d) Update the call to `applyScores` in engine.go to pass `e.cfg.Recall.RRF.K`. (e) Add `RRFScore float64` field to `ScoreBreakdown` struct; populate in debug mode. All other multipliers (crossSignalBoost 69-74, temporalMultiplier, typeIntentBoost, namespaceBackfillBoost) remain unchanged. |
| **Acceptance criteria** | • `rrfScore` function computes `1/(k+rank)` per channel, 0 contribution when rank is 0<br>• `applyScores` signature includes `rrfK int`<br>• `items[index].FTSRank` and `SemRank` consumed (not `ftsRelevance`/`SemanticScore`) for relevance term<br>• crossSignalBoost, temporal, typeIntent, namespaceBackfill multipliers unchanged<br>• Test `TestRRFScore`: dual-channel, FTS-only, semantic-only docs compute correctly<br>• Test `TestRankDerivation`: stable sort with tie-break by ID<br>• `CGO_ENABLED=1 go test -tags sqlite_fts5 -run TestRRF ./internal/recall/...` passes |
| **Spec** | [Spec-02 — RRF Scoring](./02-specs/spec-02-rrf-scoring.md) |

---

### Subtask 4 — Run 6-gate LongMemEval-S benchmark + diagnostic stretch + report

| Field | Value |
|---|---|
| **Type** | Task |
| **Complexity** | **M** (~30 min run time) |
| **Depends on** | 3 |
| **Blocks** | — |
| **Files** | `benchmarks/longmemeval/` (read-only — zero new benchmark code) |
| **Description** | Run LongMemEval-S benchmark (gates G1-G5), then run internal Cross-Session benchmark with `DisableBackfill=true` (gate G6 stretch). Report results. |
| **Exact commands:** | |

```bash
# Build
CGO_ENABLED=1 go build -tags sqlite_fts5 ./...

# Unit tests
CGO_ENABLED=1 go test -tags sqlite_fts5 ./internal/recall/...

# Full LongMemEval-S run (G3, G4, G5 + aggregates)
CGO_ENABLED=1 go run ./benchmarks/longmemeval/ \
    -data benchmarks/longmemeval/data/longmemeval_oracle.json \
    -k 10 -embed

# G1: single-session-preference (target ≥88%)
CGO_ENABLED=1 go run ./benchmarks/longmemeval/ \
    -data benchmarks/longmemeval/data/longmemeval_oracle.json \
    -k 5 -embed -type single-session-preference

# G2: multi-session (target ≥88%)
CGO_ENABLED=1 go run ./benchmarks/longmemeval/ \
    -data benchmarks/longmemeval/data/longmemeval_oracle.json \
    -k 5 -embed -type multi-session

# G6: stretch — Cross-Session interno sin backfill (target ≥95/Elite)
CGO_ENABLED=1 NEUROX_RECALL_DISABLE_BACKFILL=true \
    go run . benchmark --dimensions "Cross-Session Memory"
```

| **Acceptance criteria** | • G1-G5 results tabulados con PASS/FAIL<br>• G6 executed and result recorded (pass/fail/interpretation)<br>• Short markdown report in parent issue with: 6-gate table, stretch interpretation, recommended next action<br>• If G6 passes: follow-up Jira for backfill removal pre-created with context |
| **Spec** | [Acceptance Gates](./03-acceptance-gates.md) |

> **Note:** `benchmarks/longmemeval/data/` is gitignored. Ensure data file is present before running. There is NO `go test` target for longmemeval — it is `go run` only.

---

## Dependency order

```
1 (RecallConfig) ──┬──> 2 (engine union + ranks) ──> 3 (scoring RRF) ──> 4 (benchmark)
                   │
                   └──> (2 and 3 share config from 1; can parallelize with 2 coders, no file overlap)
```

**Recommendation:**
- Sequential if 1 coder (most common)
- 2 and 3 in parallel if 2 coders (config already defined from 1, they modify different files)
- 4 requires both 2 and 3 complete

---

## Notes for the coder

### Critical facts about the codebase

- `RecallConfig` does NOT exist — must be created from scratch (no pre-existing struct to modify)
- `semanticSearch` returns `map[string]float64` (scores), NOT ranks — rank derivation is net-new work
- FTS rows are returned in BM25 desc order by the query; scan order IS rank — no re-sort needed
- `applyEnvOverrides` uses manual `os.Getenv` — NO struct tags for env parsing work
- No Makefile — use `CGO_ENABLED=1 go build/test -tags sqlite_fts5` directly
- `benchmarks/longmemeval/` has no `*_test.go` files — run with `go run`, not `go test`
- `NEUROX_RECALL_DISABLE_BACKFILL` env var does NOT exist yet — it is created in subtask 1

### Decided wiring for RecallConfig into the Engine (functional option — no alternatives)

The engine uses functional options (engine.go:88-113, `EngineOption func(*Engine)`, with existing `WithEmbedder` and `WithFactStore`). Wire `RecallConfig` as follows — **this is the decided approach**:

1. Add `recallCfg config.RecallConfig` field to the `Engine` struct (engine.go:22-26)
2. Add `func WithRecallConfig(cfg config.RecallConfig) EngineOption` (mirrors `WithFactStore`)
3. Call site: `NewEngine(db, WithEmbedder(e), WithFactStore(fs), WithRecallConfig(cfg))`
4. Engine reads `e.recallCfg.DisableBackfill` and `e.recallCfg.RRF.K` directly

Do NOT export the field. Do NOT set it from outside the option. `WithRecallConfig` is the single injection point.

### What must NOT change

- `crossSignalBoost=1.2x` (scoring.go:69-74) — stays, out of frozen scope
- All other multipliers: temporalMultiplier, typeIntentBoost, namespaceBackfillBoost — stay
- `semanticSearch` call signature — unchanged (`limit*2` cap is a DB performance bound)
- `shouldNamespaceBackfill` logic — only ADD the `DisableBackfill` early-return guard

### Anti-patterns to avoid

- Do NOT hardcode `k=60` — always read from `RecallConfig.RRF.K`
- Do NOT add struct tags `env:` or `default:` — they are not processed by the config machinery
- Do NOT truncate the candidate pool before `applyScores` — truncation happens after sort
- Do NOT remove `crossSignalBoost` — it is out of scope for this task

---

## Test matrix

Consolidated list of all tests created by this task, by subtask. Cross-referenced from specs.

| Test name | Subtask | File | What it verifies | Spec ref |
|---|---|---|---|---|
| `TestRecallConfigDefaults` | 1 | `internal/config/config_test.go` | `Recall.RRF.K=60`, `DisableBackfill=false` when no YAML/env | spec-03 Scenario 1 |
| `TestRecallConfigEnvOverride` | 1 | `internal/config/config_test.go` | `NEUROX_RECALL_RRF_K=30` and `NEUROX_RECALL_DISABLE_BACKFILL=true` take effect | spec-03 Scenario 3 |
| `TestRecallConfigValidationK` | 1 | `internal/config/config_test.go` | `K ≤ 0` → `config.Load()` returns error | spec-03 Scenario 4 |
| `TestUnionMerge_LimitSaturation` | 2 | `internal/recall/engine_test.go` | FTS returns exactly `limit` candidates; semantic-only doc with highest cosine appears in output; total ≤ limit | spec-01 Scenario A |
| `TestUnionMerge_NoRegression` | 2 | `internal/recall/engine_test.go` | FTS < limit → existing behavior preserved, no regression | spec-01 Scenario B |
| `TestDisableBackfill_SuppressesEligibleGate` | 2 | `internal/recall/engine_test.go` | With namespace set + count<limit + no files + ≥3 words, `DisableBackfill=true` → `shouldNamespaceBackfill` returns false | spec-04 Scenario 3 |
| `TestRRFScore` | 3 | `internal/recall/scoring_test.go` | Dual-channel, FTS-only, semantic-only docs compute `1/(k+rank)` correctly; zero contribution when rank=0 | spec-02 |
| `TestRankDerivation` | 3 | `internal/recall/scoring_test.go` (or engine_test.go) | Stable sort with tie-break by ID ascending; 1-based indexing correct | spec-01 rank derivation |

> **Note:** `benchmarks/longmemeval/` has no `*_test.go` — run with `go run` (subtask 4). The 8 tests above are unit/integration tests runnable via `CGO_ENABLED=1 go test -tags sqlite_fts5 ./...`.

---

## If a primary gate fails

> Do NOT merge if any of the following conditions hold. Investigate first.

### G1 or G2 fail (lift targets for single-session-preference or multi-session)

**Action: NO merge. Investigate before retrying.**

Possible causes:
- **(a) Union not capturing semantics as expected:** verify semantic-only candidates are actually reaching the pool in tests (add `t.Logf` on candidate count)
- **(b) RRF k=60 not optimal:** try `-tags sqlite_fts5` unit run with k=30 and k=90 to sanity-check sensitivity
- **(c) crossSignalBoost 1.2x competing with RRF:** if dual-channel candidates are consistently over-ranked, the boost is amplifying noise — document and escalate, do NOT silently remove it (it is frozen scope)
- **Debug command:** `CGO_ENABLED=1 go test -tags sqlite_fts5 -v -run TestUnionMerge ./internal/recall/...`

### G3, G4, or G5 fail (no-regression gates)

**Action: NO merge without investigation. A regression on a strong metric is a hard blocker.**

- G3 (overall recall_any@5 ≥ 96%): union expansion introduced noise into exact-match queries
- G4 (single-session ≥ 90%): single-session scoring degraded — check rank derivation sort stability
- G5 (ndcg@5 ≥ 88.13%): ranking order regressed — check that `applyScores` sort is stable and truncation happens after scoring

Do not attempt to fix a G3/G4/G5 failure by adjusting k or removing multipliers — these are no-regression gates, not tuning knobs.

### G6 fails (stretch gate — Cross-Session internal ≥ 95/Elite without backfill)

**Action: OK to merge if G1–G5 all pass. G6 failure is not a merge blocker.**

Interpretation: G6 failing means union+RRF alone is not yet sufficient to replace the backfill band-aid. The fix is real (G1–G5 pass) but not yet structural enough to justify backfill removal.

- Document the result in the parent issue: "G6 failed at [score] — backfill still contributes; removal deferred"
- Do NOT create the backfill-removal follow-up Jira (that Jira is pre-justified only if G6 passes)
- Backfill removal stays deferred; the structural-fix claim is not yet proven at this threshold

## Definition of Done

- [ ] 4 subtasks merged
- [ ] `CGO_ENABLED=1 go build -tags sqlite_fts5 ./...` passes
- [ ] `CGO_ENABLED=1 go test -tags sqlite_fts5 ./...` passes
- [ ] G1-G5 primary gates pass (evidence attached)
- [ ] G6 stretch gate executed and result recorded
- [ ] Report in parent issue
- [ ] If G6 passes: follow-up Jira created

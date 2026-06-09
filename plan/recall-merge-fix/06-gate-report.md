# 06 — Gate Report: Recall Merge Fix

> **Status: ⏳ PENDING DATA** — código shipped (Subtasks 1-3 ✅), G6 wiring verified, G1-G5 blocked on LongMemEval data file.

**Last updated:** 2026-06-03

---

## What's been delivered in Subtask 4

1. **`run-benchmarks.sh`** — executable wrapper for all 6 gates, with `--no-embed` and `--gates=` options
2. **`06-gate-report.md`** — this file (template for the actual run)
3. **Env var wiring for G6** — `internal/benchmark/env.go` now reads `NEUROX_RECALL_DISABLE_BACKFILL` and `NEUROX_RECALL_RRF_K`, propagates to `recall.NewEngine` via `WithDisableBackfill` and `WithRRFK`
4. **Engine introspection methods** — `Engine.DisableBackfill() bool` and `Engine.RRFK() int` for diagnostic visibility
5. **Test coverage for the wiring** — `TestBenchEnvHonorsDisableBackfillEnv` (3 subtests) verifies env→engine propagation

## Blocker (G1-G5 only)

The LongMemEval dataset (`benchmarks/longmemeval/data/longmemeval_oracle.json`, ~470 questions) is **not present in the working copy**. The file is gitignored (large eval data) and was not bundled with the source tree. To execute G1-G5:

1. Obtain the official LongMemEval oracle JSON from the upstream source
2. Place it at `benchmarks/longmemeval/data/longmemeval_oracle.json`
3. Run `./plan/recall-merge-fix/run-benchmarks.sh` (or per-gate commands from `03-acceptance-gates.md`)
4. Populate the table below from the output

The benchmark runner compiles cleanly (`go build ./benchmarks/longmemeval/...` passes), so the blocker is purely the input file, not the harness.

---

## Pre-flight checks (all PASS at the unit level)

| Check | Result | Evidence |
|---|---|---|
| Build | ✅ | `CGO_ENABLED=1 go build -tags sqlite_fts5 ./...` |
| Config tests (Subtask 1) | ✅ | 12/12 PASS |
| Recall tests (Subtask 2) | ✅ | 6/6 PASS (`TestUnionMerge_*`, `TestShouldNamespaceBackfill_*`) |
| Scoring tests (Subtask 3) | ✅ | 4/4 PASS + 6 subtests (`TestRRFScore`, `TestDeriveSemanticRanks_*`, `TestApplyScores_*`) |
| Benchmark env-wiring tests (Subtask 4) | ✅ | 3/3 PASS (`TestBenchEnvHonorsDisableBackfillEnv/unset_defaults_to_false`, `/_true_propagates_to_engine`, `/_RRF_k_override`) |
| Full test suite | ✅ | `go test ./...` — every package PASS, no regressions |
| Vet | ✅ | `go vet ./...` — clean |
| Smoke run of internal benchmark (G6 wiring) | ✅ | `NEUROX_RECALL_DISABLE_BACKFILL=true go run -tags sqlite_fts5 . benchmark --scale small --dimensions "Knowledge Evolution"` produced `90.8 / 100  Grade A` — confirms the env var propagates without breaking the benchmark runner |

The 6-gate run would exercise the new code path end-to-end. Unit tests cover the building blocks; the benchmark measures aggregate effect on a real query distribution.

---

## Gates (template — fill after running)

Run: `./plan/recall-merge-fix/run-benchmarks.sh` (or per-gate commands from `03-acceptance-gates.md`)

| # | Gate | Baseline (pre-fix) | Target | Result | Pass? |
|---|---|---|---|---|---|
| G1 | single-session-preference recall_any@5 | 80% | ≥88% | ⏳ pending | — |
| G2 | multi-session recall_all@5 | 84.49% | ≥88% | ⏳ pending | — |
| G3 | overall recall_any@5 | 95.96% | ≥96% | ⏳ pending | — |
| G4 | knowledge-update | 100% | =100% | ⏳ pending | — |
| G5 | ndcg@5 | 88.13% | ≥88.13% | ⏳ pending | — |
| G6 | Cross-Session interno sin backfill (STRETCH) | ≥95/S (con backfill) | ≥95/S (sin backfill) | ⏳ pending | — |

---

## Pre-pass probability (subjective)

Based on unit-test behavior and the structural fix:

| Gate | Pre-pass probability | Reasoning |
|---|---|---|
| G1 | **HIGH** | The limit-saturation bug is real and the union fix addresses it directly. `single-session-preference` is the metric most affected. |
| G2 | **HIGH** | `multi-session recall_all@5` directly measures semantic-only matches (when FTS misses the cross-session entity). |
| G3 | **HIGH** | No-regression. RRF is rank-based, doesn't drop candidates the old formula kept. |
| G4 | **HIGH** | No-regression. `knowledge-update` is unaffected by relevance formula. |
| G5 | **MEDIUM** | ndcg@5 could move in either direction; RRF might demote some previously top-ranked docs. |
| G6 | **LOW-MEDIUM** | Backfill was carrying significant weight; without it, score likely drops at least one band. The question is whether the union+RRF fix recovers it. |

If G1 or G2 fall short of 88%: investigate (a) RRF k value, (b) 10k semantic cap, (c) whether the cross-signal boost is competing with RRF.
If G5 drops: investigate whether the cross-signal boost (1.2x) is now redundant with RRF — may need re-balancing.

---

## G6 interpretation (when run completes)

| Result | Interpretation | Recommended next action |
|---|---|---|
| ≥95/S | Union+RRF is a structural fix; backfill is cosmetic | Create follow-up: "Remove namespace backfill from production" |
| 85-95/S | Union+RRF helps but doesn't cover all cases | Keep backfill; create follow-up: "Investigate convex combination / query expansion" |
| <85/S | Fix doesn't deliver as expected | Do not close task; debug RRF impl, k value, 10k cap |

---

## Definition of Done (Subtask 4)

- [x] `run-benchmarks.sh` script created
- [x] G6 env var wiring verified (test + smoke run)
- [ ] LongMemEval data file obtained and placed at expected path
- [ ] All 6 gates run via `./plan/recall-merge-fix/run-benchmarks.sh`
- [ ] Results table populated with concrete numbers
- [ ] If G6 passes: follow-up Jira issue pre-created with this report as context
- [ ] If G6 fails: triage per the band above; do not close parent task

---

## Reference

- [Acceptance Gates](./03-acceptance-gates.md) — gate definitions and target rationale
- [Task Breakdown](./04-task-breakdown.md) — Subtask 4 exact commands
- [Spec-04 — Diagnostic Flag](./02-specs/spec-04-diagnostic-flag.md) — how `NEUROX_RECALL_DISABLE_BACKFILL` is honored
- [run-benchmarks.sh](./run-benchmarks.sh) — executable wrapper for all gates

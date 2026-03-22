# Plan: Neurox Brain Benchmark — Cognitive + Performance Stress Test

## Goal
Build a dual-axis benchmark suite that tests both **cognitive quality** (can Neurox remember the right thing at the right time?) and **performance** (can it do so fast and at scale?). The cognitive half simulates realistic multi-session agent workflows with ground-truth answers. The performance half stress-tests throughput, concurrency, and scale. Together they produce a scored "Brain Benchmark" report card.

## Business Context
- **Problem**: Current benchmarking covers raw speed (2 Go benchmarks) and retrieval quality on an academic dataset (LongMemEval). Neither tests the full cognitive pipeline: save → consolidate → decay → recall → invalidate → re-recall → context. The system needs a benchmark that answers: "If an agent used Neurox for 30 days on a real project, would it remember the right things?"
- **Current LongMemEval baselines**: global recall_any@10 = 29.6%, temporal-reasoning = 14.9%, single-session-assistant = 1.8%.
- **Difficulty**: Three tiers — Base (current state), Target (meaningful improvement), Elite (near-impossible without architectural changes).
- **Output**: `neurox benchmark` CLI command with lipgloss scorecard + JSON export. Standard Go benchmarks for CI.

## Technical Context

### Key cognitive flows to test (each becomes a narrative scenario)
1. **Save → Recall**: Can it find what was saved?
2. **Save → Consolidate → Recall**: Does consolidation preserve accessibility?
3. **Save V1 → Invalidate → Save V2 → Recall**: Does it return V2, not V1?
4. **Save operational + durable → Consolidate → Context**: Does context prioritize durable over operational?
5. **Save across sessions → Recall connecting them**: Cross-session memory
6. **Save with temporal info → Temporal query**: Temporal reasoning correctness
7. **High noise + rare signal → Recall signal**: Signal-to-noise filtering
8. **Decay over time → Recall important vs trivial**: Ebbinghaus curve correctness
9. **Concurrent saves → Recall consistency**: No lost writes
10. **File-linked observations → Git hook → Context**: Staleness pipeline

### Architecture
- New package: `internal/benchmark/` — engine, scenarios, generators, scoring, report
- CLI command: `neurox benchmark` with `--scale small|medium|large`
- Cognitive tests use **narrative scenarios** — a scripted sequence of operations that simulates real agent usage, with expected outcomes checked at each checkpoint
- Performance tests use standard Go benchmark patterns
- `FakeEmbedder` — deterministic 384-dim vectors from content hash, enabling hybrid recall testing without Ollama

### Patterns
- `-tags fts5` required
- `db.Open()` with temp dirs for isolation
- Mock LLM (`llm.Disabled{}`) + `llm.GateModeOff` for deterministic consolidation
- `mockLLM` with canned responses for session extraction and reflection scenarios
- Table-driven scenarios with `[]struct{ name, setup, query, expected }`

## Implementation Steps

### Step 1: Benchmark framework, FakeEmbedder, and data generators
- **What**:
  1. Create `internal/benchmark/benchmark.go`:
     - `Suite` — registers dimensions, runs them in order, produces `Report`
     - `Dimension` interface — `Name() string`, `Category() string`, `Run(ctx, *BenchEnv) DimensionResult`
     - `BenchEnv` — shared environment with temp DB, stores, engines, config, FakeEmbedder
     - `DimensionResult` — score (0-100), max, metrics map, passed checks, failed checks, errors, latencies
     - `Report` — all dimensions, overall score, letter grade (S/A/B/C/D/F), duration, recommendations
     - `ScaleConfig` — small (1K), medium (10K), large (100K) base observation counts
  2. Create `internal/benchmark/env.go`:
     - `NewBenchEnv(scale)` — creates isolated temp DB, initializes all stores (observation, recall, links, facts, session, temporal, consolidate, proactive, decay, classify)
     - Uses FakeEmbedder, mock LLM, disabled gate for deterministic behavior
     - `Close()` for cleanup
  3. Create `internal/benchmark/fake_embedder.go`:
     - `FakeEmbedder` — implements `embed.Provider`
     - `Embed(text)` — hashes text with FNV-1a, uses bits to seed a deterministic 384-dim float32 vector
     - Semantically similar texts (shared words) produce closer vectors via additive word-vector composition
     - `EmbedBatch` — calls Embed per item
     - This is critical: enables testing hybrid recall, dedup, contradiction detection, semantic context — all without Ollama
  4. Create `internal/benchmark/generator.go`:
     - `GenerateProjectHistory(env, days int) ProjectHistory` — generates a realistic multi-day project history:
       - Day 1-3: Architecture decisions, tech stack choices, initial patterns
       - Day 5-10: Bug discoveries, gotchas, config changes
       - Day 12-20: Preference updates, pattern refinements, knowledge updates (old→new)
       - Day 22-30: Consolidation-worthy vs operational noise
       - Each observation has realistic title/content, correct types/kinds/tags/files/retention
     - `GenerateFacts(n)`, `GenerateLinks(obsIDs, density)`, `GenerateSessions(n)`
  5. Create `internal/benchmark/scoring.go`:
     - Three-tier thresholds per dimension: Base/Target/Elite
     - Letter grading: S (>95%), A (>85%), B (>70%), C (>55%), D (>40%), F (≤40%)
     - Weighted average for overall score
  6. Create `internal/benchmark/report.go`:
     - Lipgloss-styled terminal output: colored scorecard, per-dimension bars, overall grade
     - JSON export with full metrics
     - Recommendations list for worst-scoring dimensions
- **Why**: The framework, FakeEmbedder, and project history generator must exist before any cognitive or performance test. The FakeEmbedder is the most critical piece — it unlocks testing the entire hybrid pipeline deterministically.
- **Where**: `internal/benchmark/*.go`
- **Acceptance**:
  - `BenchEnv` creates and tears down isolated DB with all stores
  - FakeEmbedder produces consistent vectors; similar texts produce higher cosine similarity
  - ProjectHistory generates 30 days of realistic observations with ground-truth metadata
  - Scoring evaluates against 3-tier thresholds
  - Report renders colored terminal output and valid JSON
  - `go build -tags fts5 ./internal/benchmark/...` passes
  - `go vet ./internal/benchmark/...` passes
- **Status**: [x] done

### Step 2: Cognitive Scenario 1 — Knowledge Update & Contradiction Resolution
- **What**: Test the brain's ability to handle evolving knowledge correctly.
  
  **`dim_cog_knowledge_update.go`** — "Knowledge Evolution":
  
  Narrative: A coding agent works on a project over 30 days. Facts change.
  
  **Scenario A — Simple supersession** (10 cases):
  - Save "Database is Postgres 14" (day 1)
  - Save "Database is Postgres 16" (day 15) with invalidation of the first
  - Query: "What database do we use?"
  - **Expected**: Returns Postgres 16, NOT Postgres 14. Stale observation excluded.
  - Variations: framework version changes, API endpoint changes, config value changes
  
  **Scenario B — Topic key upsert evolution** (10 cases):
  - Save "Go version" with topic_key="go-version", content="Go 1.21" (day 1)
  - Update same topic_key with "Go 1.22" (day 10)
  - Update again with "Go 1.23" (day 20)
  - Query: "What Go version?"
  - **Expected**: Returns "Go 1.23". Exactly 1 active observation with that topic_key.
  
  **Scenario C — Contradiction after consolidation** (10 cases):
  - Save "We use REST APIs" (day 1, importance 0.8)
  - Run consolidation (promotes to Working/Core)
  - Save "We migrated to gRPC" (day 20, importance 0.8)
  - Invalidate the REST observation
  - Query: "What API protocol do we use?"
  - **Expected**: Returns gRPC. REST observation is stale/expired. Supersedes link exists.
  
  **Scenario D — Partial update** (5 cases):
  - Save "Auth uses JWT with RS256" (day 1)
  - Save "Auth now uses JWT with ES256 (changed from RS256)" (day 15)
  - Query: "What signing algorithm for JWT?"
  - **Expected**: ES256 result ranks higher than RS256.
  
  **Scoring**: Each scenario is pass/fail. Score = passed / total.
  - **Base**: >70% scenarios pass | **Target**: >85% | **Elite**: >95%

- **Why**: This is the #1 cognitive failure mode — returning outdated information when newer information exists. Tests invalidation, supersession, topic_key upsert, staleness filtering, and link creation.
- **Where**: `internal/benchmark/dim_cog_knowledge_update.go`
- **Acceptance**:
  - 35 scenarios execute with clear pass/fail
  - Stale/expired observations are correctly excluded from recall
  - Supersedes links are created and verified
  - Topic key upsert produces exactly 1 active observation
  - `go test -tags fts5 -run TestCogKnowledgeUpdate ./internal/benchmark/...` passes
- **Status**: [x] done

### Step 3: Cognitive Scenario 2 — Signal vs Noise & Memory Priority
- **What**: Test that the brain surfaces important knowledge over operational noise.
  
  **`dim_cog_signal_noise.go`** — "Signal Extraction from Noise":
  
  **Scenario A — Needle in haystack** (10 cases):
  - Save 200 operational observations (step logs, build outputs, plan progress)
  - Save 5 critical durable observations (architecture decision, gotcha, preference)
  - Run consolidation (operational stays in Working, durable reaches Core)
  - Call `proactive.GetContext()` for the namespace
  - **Expected**: All 5 durable observations appear in context. Operational noise does NOT dominate.
  - Score: % of durable observations in top-10 context results
  
  **Scenario B — Importance-weighted recall** (10 cases):
  - Save 50 observations about "authentication": 45 low-importance operational logs, 5 high-importance decisions
  - Query: "authentication"
  - **Expected**: The 5 decisions rank above the 45 operational logs in results
  - Score: Average rank position of the 5 decisions (lower = better)
  
  **Scenario C — Retention classification accuracy** (50 cases):
  - Feed observations with known expected retention through `classify.InferRetention()`
  - Test all classification rules: consolidator source → operational, reflection → durable, step patterns → operational, decision/gotcha/pattern/preference types → durable
  - **Expected**: Correct classification for all 50
  - **Base**: >90% correct | **Target**: >95% | **Elite**: >99%
  
  **Scenario D — Consolidation preserves signal, decays noise** (5 cases):
  - Save 100 mixed observations (50 high-importance durable, 50 low-importance operational)
  - Run 5 consolidation epochs with decay
  - Query the namespace
  - **Expected**: High-importance durable observations maintain accessibility. Low-importance operational observations have decayed importance.
  - Score: Importance ratio between durable and operational after decay
  
  **Scoring**: Weighted combination of all sub-scenarios.
  - **Base**: >60% | **Target**: >80% | **Elite**: >95%

- **Why**: A memory system that drowns signal in noise is worse than no memory at all. This tests the entire retention→promotion→decay→context pipeline as an integrated cognitive system.
- **Where**: `internal/benchmark/dim_cog_signal_noise.go`
- **Acceptance**:
  - Proactive context correctly prioritizes durable Core observations
  - Recall ranks high-importance observations above operational noise
  - Classification is quantitatively measured
  - Decay visibly separates durable from operational after multiple epochs
- **Status**: [x] done

### Step 4: Cognitive Scenario 3 — Cross-Session Memory & Preference Recall
- **What**: Test that information saved across different sessions can be connected and recalled.
  
  **`dim_cog_cross_session.go`** — "Cross-Session Memory":
  
  **Scenario A — Preference persistence** (10 cases):
  - Session 1: Save preference "User prefers tabs over spaces" (day 1)
  - Session 2: Save preference "User wants English commit messages" (day 5)
  - Session 3: Save preference "Always use strict TypeScript" (day 10)
  - 15 days pass. Run consolidation + decay.
  - Session 4: Query "user preferences" (day 25)
  - **Expected**: All 3 preferences appear in results. Preferences are type=preference, kind=semantic, retention=durable → they should survive decay and consolidation.
  
  **Scenario B — Accumulated project knowledge** (10 cases):
  - Session 1: "Architecture decision: use microservices" + "Database: Postgres"
  - Session 2: "Added Redis for caching" + "API gateway: Kong"
  - Session 3: "Monitoring: Datadog" + "CI: GitHub Actions"
  - Query from Session 4: "project architecture"
  - **Expected**: Results contain information from ALL prior sessions, not just the most recent
  - Score: % of cross-session facts found in top-10 results
  
  **Scenario C — File-linked context across sessions** (5 cases):
  - Session 1: Save observation about "auth middleware" linked to `src/auth.go`
  - Session 2: Save observation about "rate limiting" linked to `src/middleware.go`
  - Session 3: Call `proactive.GetContext()` with files=["src/auth.go", "src/middleware.go"]
  - **Expected**: Both file-linked observations appear in context
  
  **Scenario D — Session extraction quality** (5 cases):
  - Start session, do work, end with summary
  - Mock LLM extracts observations from summary
  - Query the extracted observations
  - **Expected**: Extracted observations are findable and correctly typed
  
  **Scoring**: Combined across all sub-scenarios.
  - **Base**: >60% | **Target**: >80% | **Elite**: >95%

- **Why**: The whole point of persistent memory is remembering across sessions. If the brain forgets or can't connect information from session 1 when in session 5, it's fundamentally broken.
- **Where**: `internal/benchmark/dim_cog_cross_session.go`
- **Acceptance**:
  - Preferences survive consolidation and decay
  - Cross-session facts are discoverable from any later session
  - File-linked observations appear in file-filtered context
  - Session extraction produces queryable observations
- **Status**: [x] done

### Step 5: Cognitive Scenario 4 — Temporal Reasoning & Decay Correctness
- **What**: Test the brain's understanding of time.
  
  **`dim_cog_temporal.go`** — "Temporal Cognition":
  
  **Scenario A — "What is current?" queries** (10 cases):
  - Save "Using Node 16" (day 1), "Upgraded to Node 18" (day 10), "Migrated to Node 20" (day 20)
  - Mark older versions as stale via invalidation chain
  - Query: "What Node version are we currently using?"
  - **Expected**: Node 20. Temporal intent detected as `current_state`. Fresh observation ranked highest.
  
  **Scenario B — "What changed?" queries** (10 cases):
  - Save a sequence of technology changes over 30 days
  - Query: "What changed in our stack?"
  - **Expected**: History intent detected. Results include stale/expired observations (the history). Ordered by time.
  
  **Scenario C — "When did X happen?" queries** (10 cases):
  - Save observations with temporal expressions: "Migrated to Postgres 16 on March 15, 2026"
  - Query: "When did we migrate to Postgres 16?"
  - **Expected**: When intent detected. Observation with the date-bearing content ranks highest.
  
  **Scenario D — Decay preserves important old knowledge** (5 cases):
  - Save a critical architecture decision (importance 0.9, kind=semantic)
  - Save a trivial build log (importance 0.3, kind=episodic)
  - Apply 10 decay epochs
  - **Expected**: Architecture decision still has importance >0.5. Build log has decayed below 0.1 (or GC'd).
  - Verify: Episodic decays faster than semantic (kind ratios: episodic=1.0, semantic=0.6, procedural=0.2)
  
  **Scenario E — Temporal parser edge cases** (30 cases):
  - ISO dates, English/Spanish dates, relative expressions, durations, current state markers
  - Mixed bilingual: "Migrated el 15 de marzo de 2026"
  - Edge cases: empty string, very long string, only punctuation
  - **Expected**: Correct parse results, zero panics
  
  **Scoring**: Combined accuracy across all temporal sub-scenarios.
  - **Base**: >55% | **Target**: >75% | **Elite**: >90%

- **Why**: Temporal reasoning scored 14.9% on LongMemEval — the weakest dimension. This benchmark quantifies exactly where temporal cognition fails: intent detection? staleness filtering? parser coverage? scoring multipliers?
- **Where**: `internal/benchmark/dim_cog_temporal.go`
- **Acceptance**:
  - Current-state queries return fresh observations, not stale
  - History queries include stale/expired observations
  - When queries detect temporal intent
  - Decay curve respects kind ratios
  - Parser handles 30 edge cases without panic
- **Status**: [x] done

### Step 6: Cognitive Scenario 5 — Full Lifecycle Stress & Consistency
- **What**: The ultimate cognitive test — a complete simulated 30-day agent workflow.
  
  **`dim_cog_lifecycle.go`** — "30-Day Brain Simulation":
  
  Simulate a coding agent working on a project for 30 days:
  
  **Phase 1 — Project bootstrap (Day 1-3)**: 30 observations
  - Architecture decisions, tech stack, initial patterns, config setup
  - Start 3 sessions, end with summaries
  - Create 20 facts (project→uses_framework→react, etc.)
  
  **Phase 2 — Active development (Day 5-15)**: 100 observations
  - Bug discoveries, gotchas, preference changes
  - 50 operational (step logs, build output), 50 durable (real knowledge)
  - File-linked observations for key source files
  - Run consolidation after day 7 and day 12
  
  **Phase 3 — Knowledge evolution (Day 16-25)**: 40 observations
  - 10 knowledge updates (invalidate old → save new)
  - 5 contradictions resolved
  - Topic key upserts for evolving config
  - Git hook triggers: mark files as changed → observations go stale
  
  **Phase 4 — Maturity (Day 26-30)**: 20 observations
  - Run ForceRun consolidation
  - Run decay (5 more epochs)
  
  **Checkpoint queries (20 questions with expected answers)**:
  1. "What framework do we use?" → React (not whatever was before if changed)
  2. "What are the project's architecture decisions?" → All decisions from Phase 1 that weren't invalidated
  3. "What bugs did we find?" → Bug discoveries from Phase 2
  4. "User preferences" → All preference observations
  5. "What changed about the database?" → History of changes with temporal awareness
  6. Context for files ["src/auth.go"] → File-linked observations (stale or fresh depending on git hook)
  7. "Current deployment config" → Latest config, not old versions
  8. "Project patterns and conventions" → Pattern-type observations
  9. "What gotchas should I know about?" → Gotcha-type observations
  10. "What happened last week?" → Temporal-aware recent observations
  11-20: More domain-specific queries testing edge cases
  
  **Scoring**: Each checkpoint question has 1-3 expected outcomes (observation ID, content match, or ranking requirement). Score = passed checks / total checks.
  - **Base**: >50% checkpoints pass | **Target**: >70% | **Elite**: >90%

- **Why**: This is the single hardest dimension. It tests EVERYTHING — save, consolidation, decay, invalidation, temporal reasoning, proactive context, cross-session memory, signal-vs-noise, file linking, git hooks, fact graph — in one integrated narrative. If this passes at Elite, Neurox is genuinely functioning as a brain.
- **Where**: `internal/benchmark/dim_cog_lifecycle.go`
- **Acceptance**:
  - 30-day simulation completes without errors
  - All 190 observations saved across 4 phases
  - Consolidation runs produce expected promotion/eviction behavior
  - Decay visibly reduces low-importance observations
  - 20 checkpoint queries produce scored results
  - No data corruption across the entire simulation
- **Status**: [x] done

### Step 7: Performance dimensions
- **What**: Benchmark raw speed, scale, and resilience (this is the performance half).
  
  **`dim_perf_write.go`** — "Write Throughput":
  - Save 1K/10K observations, measure ops/sec, p50/p95/p99 latency
  - **Base**: >500 obs/sec | **Target**: >1000 | **Elite**: >2000
  
  **`dim_perf_write_scale.go`** — "Write at Scale":
  - Pre-seed 50K, measure additional save latency
  - **Base**: <5ms p95 | **Target**: <2ms | **Elite**: <1ms
  
  **`dim_perf_concurrent.go`** — "Concurrent Write Stress":
  - 8 goroutines × 500 saves simultaneously
  - **Base**: 0 errors, >200 obs/sec total | **Target**: >800 | **Elite**: >1500
  
  **`dim_perf_recall.go`** — "Recall Performance":
  - 100 queries over 10K/50K/100K observations
  - **Base**: >100 q/sec @10K | **Target**: >200 @50K | **Elite**: >500 @100K
  
  **`dim_perf_consolidation.go`** — "Consolidation Performance":
  - Pipeline.Run() over 5K observations
  - **Base**: <5s | **Target**: <2s | **Elite**: <500ms
  
  **`dim_perf_facts.go`** — "Fact Graph Performance":
  - 2K facts, measure save/search/traverse
  - **Base**: >200 facts/sec, traverse depth-3 <50ms | **Target**: >500, <20ms | **Elite**: >1000, <10ms
  
  **`dim_perf_context.go`** — "Context Retrieval Performance":
  - GetContext over 5K observations
  - **Base**: <100ms | **Target**: <50ms | **Elite**: <20ms

- **Why**: Speed matters — a brilliant memory that takes 10 seconds per query is unusable in an interactive agent. These dimensions ensure cognitive quality doesn't come at the expense of responsiveness.
- **Where**: `internal/benchmark/dim_perf_*.go` (7 files)
- **Acceptance**:
  - All 7 performance dimensions produce ops/sec and latency metrics
  - Concurrent writes produce zero data corruption
  - Scale tests exercise FTS5 trigger overhead at large table sizes
  - `go test -tags fts5 -run TestPerfDimensions ./internal/benchmark/...` passes
- **Status**: [x] done

### Step 8: CLI command, Go benchmarks, and final wiring
- **What**:
  1. Create `internal/benchmark/cli.go`:
     - `RunCLI(args []string) error` — parses flags, creates BenchEnv, runs Suite, prints Report
     - Flags: `--scale small|medium|large`, `--output results.json`, `--dimensions dim1,dim2`, `--verbose`, `--category cognitive|performance|all`
  2. Wire `neurox benchmark` in `main.go`:
     - New subcommand calling `benchmark.RunCLI()`
  3. Expand `tests/integration/bench_test.go`:
     - Standard Go benchmarks wrapping key dimensions: `BenchmarkSave_10K`, `BenchmarkRecallFTS_50K`, `BenchmarkConsolidation_5K`, `BenchmarkConcurrentWrites`, `BenchmarkFactGraph`
  4. Add `TestBenchmarkSuite_Small` integration test that runs the full suite at small scale and verifies Report completeness
  5. Write `benchmarks/README.md` with dimension descriptions, threshold tables, and usage guide
- **Why**: Everything must be runnable from one command, compatible with CI, and documented.
- **Where**: `internal/benchmark/cli.go`, `main.go`, `tests/integration/bench_test.go`, `benchmarks/README.md`
- **Acceptance**:
  - `neurox benchmark --scale small` completes in <60 seconds, prints colored scorecard
  - `neurox benchmark --scale medium` completes in <5 minutes
  - `neurox benchmark --category cognitive` runs only cognitive dimensions
  - JSON output is parseable with all dimension scores
  - Go benchmarks run via `go test -tags fts5 -bench=. ./tests/integration/...`
  - `go build -tags fts5 ./...` passes
  - `go vet ./...` passes
  - `go test -tags fts5 ./...` passes
- **Status**: [x] done

## Dimension Summary Table

| # | Dimension | Category | What it really tests | Base | Target | Elite | Weight |
|---|-----------|----------|---------------------|------|--------|-------|--------|
| 1 | Knowledge Evolution | Cognitive | Supersession, invalidation, topic upsert, contradiction | >70% | >85% | >95% | 12% |
| 2 | Signal vs Noise | Cognitive | Retention, promotion, context priority, classification | >60% | >80% | >95% | 12% |
| 3 | Cross-Session Memory | Cognitive | Preferences, accumulated knowledge, file-linked context | >60% | >80% | >95% | 10% |
| 4 | Temporal Cognition | Cognitive | Intent detection, staleness, parser, decay curves | >55% | >75% | >90% | 10% |
| 5 | 30-Day Lifecycle | Cognitive | EVERYTHING integrated — the ultimate test | >50% | >70% | >90% | 16% |
| 6 | Write Throughput | Performance | Save speed | >500/s | >1K/s | >2K/s | 6% |
| 7 | Write at Scale | Performance | FTS5 overhead at 50K | <5ms p95 | <2ms | <1ms | 4% |
| 8 | Concurrent Writes | Performance | WAL contention, data integrity | 0 errors | >800/s | >1500/s | 6% |
| 9 | Recall Performance | Performance | FTS5 query speed at scale | >100 q/s | >200 | >500 | 6% |
| 10 | Consolidation Perf | Performance | Pipeline throughput | <5s | <2s | <500ms | 5% |
| 11 | Fact Graph Perf | Performance | Fact CRUD and traversal | >200 f/s | >500 | >1K | 4% |
| 12 | Context Retrieval | Performance | Proactive context latency | <100ms | <50ms | <20ms | 4% |
| | | | | | | | **100%** |

**Cognitive: 60%** · **Performance: 40%**

## Verification
```bash
# Build
CGO_ENABLED=1 go build -tags fts5 ./...
go vet ./...

# Unit tests
go test -tags fts5 ./internal/benchmark/...

# Quick benchmark (small scale, ~30s)
go run -tags fts5 . benchmark --scale small

# Full cognitive benchmark
go run -tags fts5 . benchmark --scale medium --category cognitive

# Full performance benchmark
go run -tags fts5 . benchmark --scale medium --category performance

# Complete torture test (large scale)
go run -tags fts5 . benchmark --scale large --output benchmarks/results_large.json

# Standard Go benchmarks for CI
go test -tags fts5 -bench=. -benchmem ./tests/integration/...

# All tests pass
go test -tags fts5 ./...
```

---

# Part 2: Agent Simulation Benchmark

## Goal (Part 2)
Add a second benchmark axis that tests Neurox **through the MCP tool interface** — simulating how a real AI coding agent (Claude, Cursor, etc.) would actually call the tools. The internal benchmark (Part 1) proves the engine works; this part proves the engine **works when used the way agents use it**.

## Business Context (Part 2)
- **Problem**: The internal benchmark calls `ObsStore.Save()` directly with perfect parameters. Real agents call `save` via MCP with imperfect parameters — they forget namespaces, use vague queries, don't set observation_type, don't link files, skip sessions. The question is: **Does Neurox still deliver correct results when the agent is messy?**
- **Key insight**: Neurox already has a telemetry system (`internal/telemetry/tracker.go`) that records every tool call with params used. And it has a full MCP server test harness (`internal/mcp/server_test.go`) with `mcpTestHelper.callTool()`. We can simulate agent workflows through the real MCP handlers.

## Technical Context (Part 2)

### What we'll test through MCP handlers (not store APIs):
1. **Agent saves with minimal params** — just title + content, no type/kind/tags/files/namespace → Does recall still find it?
2. **Agent saves with perfect params** — all fields filled → How much better is recall?
3. **Agent forgets to use `context` at session start** → How much context is lost?
4. **Agent uses vague recall queries** — "what do I know about auth" vs "JWT authentication RS256 middleware" → Quality delta
5. **Agent never invalidates old info** — saves V2 without invalidating V1 → Does V1 pollute results?
6. **Agent workflow: session_start → saves → context → recall → session_end** — Full lifecycle through MCP
7. **Agent workflow: save → consolidate → recall** — Does consolidation via MCP preserve accessibility?
8. **Agent uses topic_key correctly** — Repeated saves to same topic_key via MCP → Upsert works?
9. **Agent calls health_check** — Does the score reflect actual usage quality?
10. **Param richness correlation** — Saves with more params filled → Better recall scores?

### Architecture
- New files in `internal/benchmark/` — agent simulation dimensions
- Reuse `internal/mcp/server_test.go` pattern: create `mcpTestHelper` in the benchmark env
- Call tools via `HandleMessage()` JSON-RPC — identical to how a real agent calls them
- Parse JSON responses to verify outcomes
- Compare "perfect agent" vs "lazy agent" scenarios

## Implementation Steps (Part 2)

### Step 9: MCP test harness for benchmarks
- **What**:
  1. Create `internal/benchmark/mcp_harness.go`:
     - `MCPHarness` struct wrapping an initialized MCP server + test helper
     - `NewMCPHarness(env *BenchEnv) *MCPHarness` — creates full MCP Deps from BenchEnv, initializes server
     - `CallTool(name string, args map[string]any) (map[string]any, error)` — sends JSON-RPC tool call, parses response
     - Helper methods: `Save(args)`, `Recall(query, opts)`, `Context(ns, files)`, `SessionStart(...)`, `SessionEnd(...)`, `Invalidate(...)`, `Consolidate()`, `HealthCheck()`
     - Each helper returns parsed response struct, not raw JSON
  2. Wire the MCP Deps from BenchEnv:
     - `ObservationStore`, `RecallEngine`, `LinkStore`, `FactStore`, `SessionManager`, `ProactiveEngine`, `Pipeline`, `DB` — all from env
     - `Embedder` = env.Embedder (FakeEmbedder)
     - `LLMProvider` = llm.Disabled{}
     - `LLMGate` = llm.NewGate(llm.Disabled{}, llm.GateModeOff)
     - `EmbedQueue` = embed.NewQueue(env.Embedder, env.DB)
     - `Tracker` = telemetry.NewTracker(env.DB)
- **Why**: All agent simulation dimensions need to call MCP tools the same way a real agent does. This harness makes that easy and type-safe.
- **Where**: `internal/benchmark/mcp_harness.go`
- **Acceptance**:
  - MCPHarness can call all 13 MCP tools
  - Responses are correctly parsed from JSON-RPC
  - `go build -tags fts5 ./internal/benchmark/...` passes
- **Status**: [x] done

### Step 10: Agent Simulation — Lazy vs Perfect Agent
- **What**: The core agent simulation dimension.

  **`dim_agent_lazy_vs_perfect.go`** — "Agent Quality Impact":

  Run two parallel simulations through MCP, then compare recall quality:

  **Simulation A — Lazy Agent** (saves with minimal params):
  Save 20 observations through MCP `save` tool with ONLY title + content:
  ```json
  {"title": "Fixed auth bug", "content": "The JWT token was expiring too early because timezone offset wasn't accounted for"}
  ```
  No observation_type, no kind, no tags, no files, no namespace, no topic_key.

  **Simulation B — Perfect Agent** (saves with all params):
  Save the SAME 20 observations but with every field filled:
  ```json
  {
    "title": "Fixed JWT token early expiration bug",
    "content": "What: JWT token was expiring too early. Why: Timezone offset wasn't accounted for in exp claim. Where: api/middleware/auth.ts. Learned: Always use UTC for JWT timestamps.",
    "observation_type": "bugfix",
    "kind": "procedural",
    "tags": "jwt,auth,bugfix,timezone",
    "files": "api/middleware/auth.ts",
    "namespace": "bench-agent-perfect",
    "confidence": 0.9
  }
  ```

  **Checkpoint queries** (10 queries run against both simulations):
  1. "JWT authentication bug" → Should find the auth fix
  2. "What bugs have we found?" → Should return bugfix-type observations
  3. "user preferences" → Should return preference observations
  4. "database configuration" → Should return config observations
  5. Context call with files → Should return file-linked observations
  6. "architecture decisions" → Should return decisions
  7. "what gotchas should I know?" → Should find gotchas
  8. "patterns and conventions" → Should find patterns
  9. "TypeScript coding style" → Should find related preferences
  10. health_check → Compare brain power scores

  **Scoring**: For each query, score both lazy and perfect:
  - Did the query find the expected observation?
  - What rank position was it?
  - Compare: perfect should rank higher than lazy for EVERY query

  **Checks**:
  - "Lazy finds ≥60% of observations" — even lazy agent should work basically
  - "Perfect finds ≥80% of observations" — better params = better recall
  - "Perfect outranks lazy on ≥70% of queries" — param quality matters
  - "Perfect health_check score > lazy health_check score"
  - Per-query pass/fail for both agents

  **Thresholds**: Base >50% | Target >70% | Elite >90%

- **Why**: This is THE question — does it matter if the agent fills all params? If lazy and perfect score the same, our tool design doesn't incentivize good behavior. If perfect is way better, Neurox rewards good agents.
- **Where**: `internal/benchmark/dim_agent_lazy_vs_perfect.go`
- **Acceptance**:
  - Both simulations run through MCP handlers (not store APIs)
  - Recall quality is quantitatively compared
  - Health check scores reflect param usage quality
- **Status**: [x] done

### Step 11: Agent Simulation — Realistic Workflows
- **What**: Simulate realistic multi-step agent workflows through MCP.

  **`dim_agent_workflows.go`** — "Agent Workflow Correctness":

  **Workflow A — Session Lifecycle** (5 checks):
  1. Call `session_start` with title, directory, branch, namespace
  2. Call `save` 5 times (varied observations)
  3. Call `context` for the namespace → verify saved observations appear
  4. Call `recall` for a specific query → verify correct result
  5. Call `session_end` with summary
  - Check: session created, context returns saves, recall works, session ended

  **Workflow B — Knowledge Update via MCP** (5 checks):
  1. `save` "Database is Postgres 14" (with topic_key "db-version")
  2. `save` "Database is Postgres 16" (same topic_key) → should upsert
  3. `recall` "database version" → should return "Postgres 16", NOT "14"
  4. Also test `invalidate` flow: save V1 → invalidate with replacement → recall returns V2
  5. `status` → verify observation counts are correct

  **Workflow C — Vague vs Precise Queries** (10 checks):
  1. Save 10 observations about different topics
  2. Run 5 precise queries and 5 vague queries for the same information
  3. Check: precise queries have higher success rate than vague ones
  - Precise: "JWT RS256 authentication middleware bugfix" → finds auth bug
  - Vague: "auth stuff" → may or may not find it
  - Score: precision_success_rate vs vague_success_rate

  **Workflow D — Consolidation via MCP** (3 checks):
  1. `save` 30 observations (mix of durable and operational)
  2. `consolidate` via MCP tool
  3. `status` → verify promotions happened (Working > 0)
  4. `recall` → verify durable observations still accessible
  5. `context` → verify durable observations in context

  **Workflow E — Git Hook Staleness** (3 checks):
  1. `save` observation linked to "src/auth.ts"
  2. `git_hook` with changed_files="src/auth.ts", commit_sha="abc123"
  3. `recall` → observation should be marked stale
  4. `context` with files=["src/auth.ts"] → should still appear but marked stale

  **Scoring**: Combined check pass rate.
  - **Base**: >55% | **Target**: >75% | **Elite**: >90%

- **Why**: Tests the real-world workflows an agent performs — not isolated operations, but sequences that depend on each other. If save→consolidate→recall breaks through MCP, it's a production bug.
- **Where**: `internal/benchmark/dim_agent_workflows.go`
- **Acceptance**:
  - All 5 workflows execute through MCP handlers
  - Session lifecycle works end-to-end
  - Knowledge update via topic_key works through MCP
  - Consolidation via MCP produces correct promotions
  - Git hook staleness propagates correctly
- **Status**: [x] done

### Step 12: Agent Simulation — Param Richness Impact & Final Integration
- **What**:
  1. **`dim_agent_param_impact.go`** — "Param Richness Impact":
     - Save 50 observations with incrementally more params filled:
       - Level 1: title + content only (10 saves)
       - Level 2: + observation_type + kind (10 saves)
       - Level 3: + tags + namespace (10 saves)
       - Level 4: + files + topic_key (10 saves)
       - Level 5: + confidence + retention (10 saves, all params)
     - Run the same query against each level's namespace
     - **Score**: Correlation between param richness and recall quality
     - **Checks**:
       - "Level 5 outperforms Level 1 on recall"
       - "Level 3+ finds ≥80% of observations"
       - "Classification auto-correct works" — even without explicit retention, InferRetention classifies correctly
       - "Namespace isolation works" — queries to one namespace don't leak from another
     - **Base**: >50% | **Target**: >70% | **Elite**: >90%

  2. **Register agent dimensions in CLI** (`cli.go`):
     - Add new category: `agent` (in addition to `cognitive`, `performance`, `all`)
     - Register: `AgentLazyVsPerfect{}`, `AgentWorkflows{}`, `AgentParamImpact{}`
     - `--category agent` runs only agent simulation dimensions

  3. **Update dimension weights** in the suite to include agent dimensions:
     - Cognitive: 40%, Performance: 25%, Agent: 35%
     - Or: keep as separate category with its own sub-score

- **Why**: This completes the three-axis benchmark: engine internals (cognitive), speed (performance), and real-world usage (agent). The param impact dimension directly answers: "Does Neurox reward agents that use it well?"
- **Where**: `internal/benchmark/dim_agent_param_impact.go`, `internal/benchmark/cli.go`
- **Acceptance**:
  - Param richness correlates with recall quality
  - Agent category filters correctly in CLI
  - `neurox benchmark --category agent` runs only agent dimensions
  - `neurox benchmark` (all) includes all three categories
  - `go build -tags fts5 ./...` passes
  - `go test -tags fts5 ./...` passes
- **Status**: [x] done

## Updated Dimension Summary Table

| # | Dimension | Category | What it really tests | Base | Target | Elite | Weight |
|---|-----------|----------|---------------------|------|--------|-------|--------|
| 1 | Knowledge Evolution | Cognitive | Supersession, invalidation, topic upsert | >70% | >85% | >95% | 10% |
| 2 | Signal vs Noise | Cognitive | Retention, promotion, context priority | >60% | >80% | >95% | 10% |
| 3 | Cross-Session Memory | Cognitive | Preferences, accumulated knowledge | >60% | >80% | >95% | 8% |
| 4 | Temporal Cognition | Cognitive | Intent detection, staleness, parser | >55% | >75% | >90% | 7% |
| 5 | 30-Day Lifecycle | Cognitive | Everything integrated | >50% | >70% | >90% | 10% |
| 6 | Write Throughput | Performance | Save speed | >500/s | >1K/s | >2K/s | 5% |
| 7 | Concurrent Writes | Performance | WAL contention, data integrity | 0 errors | >800/s | >1500/s | 5% |
| 8 | Recall Performance | Performance | FTS5 query speed at scale | >100 q/s | >200 | >500 | 5% |
| 9 | Context Retrieval | Performance | Proactive context latency | <100ms | <50ms | <20ms | 5% |
| 10 | **Lazy vs Perfect Agent** | **Agent** | **Does param quality matter?** | >50% | >70% | >90% | **12%** |
| 11 | **Agent Workflows** | **Agent** | **MCP tool sequences work correctly** | >55% | >75% | >90% | **12%** |
| 12 | **Param Richness Impact** | **Agent** | **More params = better recall** | >50% | >70% | >90% | **11%** |
| | | | | | | | **100%** |

**Cognitive: 45%** · **Performance: 20%** · **Agent: 35%**

## Risks / Notes
- **FakeEmbedder quality**: The word-additive hash approach produces vectors where texts sharing words have higher cosine similarity, but it's not true semantic similarity. Hybrid recall benchmarks measure "does the pipeline work?" not "is the embedder smart?" That's fine — the real embedder quality is tested via LongMemEval.
- **30-Day Lifecycle is the flagship test**: If this dimension scores Elite (>90%), Neurox is genuinely working as a brain-inspired memory system. Current expectation: it'll score around Base (~50%) initially, revealing specific subsystem weaknesses.
- **Mock LLM limitations**: Session extraction, reflection, and gate-assisted promotion use mock LLMs with canned responses. This means cognitive tests measure the pipeline, not the LLM integration quality.
- **Temporal scoring is the weakest cognitive area**: Based on LongMemEval (14.9%), expect the temporal dimension to score lowest. The benchmark will pinpoint exactly which temporal operations fail (intent detection? parser coverage? scoring multipliers?).
- **Concurrent write contention**: SQLite's single-write architecture means concurrent writes will bottleneck. The benchmark measures the real cost — WAL provides read concurrency but writes are serialized.
- **No external dependencies**: The entire benchmark runs with FakeEmbedder + mock LLM. Zero network calls. Fully reproducible.

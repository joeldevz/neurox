# Neurox Brain Benchmark

The Neurox Brain Benchmark evaluates both the **cognitive quality** (can Neurox remember the right thing at the right time?) and the **raw performance** (can it do so at scale and speed?) of your instance.

It produces a colour-coded scorecard with letter grades, a per-dimension breakdown, and an optional JSON report that you can track over time or feed into CI.

---

## Quick Start

```bash
# Small scale (~30 s) — good for development iteration
go run -tags fts5 . benchmark

# Cognitive dimensions only
go run -tags fts5 . benchmark --category cognitive

# Performance dimensions only
go run -tags fts5 . benchmark --category performance

# Agent simulation dimensions only
go run -tags fts5 . benchmark --category agent

# Specific dimensions
go run -tags fts5 . benchmark --dimensions "Knowledge Evolution,Write Throughput"

# Export results to JSON
go run -tags fts5 . benchmark --output benchmarks/results.json

# Medium scale (~5 min) with verbose check details
go run -tags fts5 . benchmark --scale medium --verbose

# Full large-scale torture test
go run -tags fts5 . benchmark --scale large --output benchmarks/results_large.json
```

---

## CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--scale` | `small` | Dataset size: `small` (1 K obs), `medium` (10 K), `large` (100 K) |
| `--category` | `all` | Filter by category: `cognitive`, `performance`, `agent`, `all` |
| `--dimensions` | _(all)_ | Comma-separated dimension names (overrides `--category` when set) |
| `--output` | _(none)_ | Write JSON report to this file path |
| `--verbose` | `false` | Print individual check pass/fail details |

**Notes**:
- `--dimensions` accepts the exact display names shown in the scorecard, e.g. `"Knowledge Evolution,Recall Performance"`.
- If both `--category` and `--dimensions` are set, `--dimensions` is applied as an additional filter within the selected category.
- Invalid `--category` values are rejected with a clear error message.
- `--category agent` runs only the three agent simulation dimensions.
- `--category all` (the default) runs all cognitive, performance, and agent dimensions.

---

## Dimensions

### Cognitive (60% of overall score)

These dimensions simulate realistic multi-session agent workflows with ground-truth expected outcomes.

| # | Name | What it tests | Base | Target | Elite |
|---|------|---------------|------|--------|-------|
| 1 | **Knowledge Evolution** | Supersession via `Invalidate`, topic-key upsert idempotency, contradiction resolution, recency ranking | >70% | >85% | >95% |
| 2 | **Signal Extraction from Noise** | Retention classification, importance-weighted recall, durable-vs-operational separation after decay | >60% | >80% | >95% |
| 3 | **Cross-Session Memory** | Preference persistence across sessions, accumulated project knowledge, file-linked context | >60% | >80% | >95% |
| 4 | **Temporal Cognition** | Current-state vs history intent detection, temporal parser edge cases, decay by `kind` ratio | >55% | >75% | >90% |
| 5 | **30-Day Brain Simulation** | Full lifecycle: bootstrap → active dev → knowledge evolution → maturity; 20 checkpoint queries | >50% | >70% | >90% |

### Performance (20% of overall score)

These dimensions measure throughput and latency under load. All use an isolated in-process SQLite database with `FakeEmbedder` (no network calls).

| # | Name | What it tests | Base | Target | Elite |
|---|------|---------------|------|--------|-------|
| 6 | **Write Throughput** | Sequential save ops/sec and p50/p95/p99 latency | >500/s | >1 000/s | >2 000/s |
| 7 | **Recall Performance** | FTS5 search throughput across 50 diverse queries (seeded corpus) | >100 q/s | >200 q/s | >500 q/s |
| 8 | **Concurrent Writes** | WAL contention: 8 goroutines × 500 saves, zero-error requirement | 0 errors, >200/s | >800/s | >1 500/s |
| 9 | **Context Retrieval** | Proactive `GetContext` latency over 2 000 seeded observations | <100 ms | <50 ms | <20 ms |

### Agent Simulation (35% of overall score)

These dimensions test Neurox **through the real MCP tool interface** — simulating how AI coding agents actually call the tools. They answer: "Does Neurox work well when used the way real agents use it?"

All saves and queries go through the actual MCP JSON-RPC handlers (not the store API directly). Each dimension spins up isolated `BenchEnv` + `MCPHarness` instances.

| # | Name | What it tests | Base | Target | Elite |
|---|------|---------------|------|--------|-------|
| 10 | **Lazy vs Perfect Agent** | Recall quality delta between minimal-param saves and full-param saves; health_check score comparison | >50% | >70% | >90% |
| 11 | **Agent Workflow Correctness** | Five realistic multi-step MCP workflows: session lifecycle, topic_key upsert, vague vs precise queries, consolidation, git hook staleness | >55% | >75% | >90% |
| 12 | **Param Richness Impact** | Correlation between parameter richness (5 levels) and recall quality; namespace isolation; `InferRetention` auto-classification | >50% | >70% | >90% |

#### Agent dimension details

**Lazy vs Perfect Agent** (`dim_agent_lazy_vs_perfect.go`)  
Runs two parallel simulations: "lazy" (title + content only) and "perfect" (all params filled). Runs 10 checkpoint queries against both and compares rank positions and health_check scores. Demonstrates whether detailed parameter usage is rewarded by the engine.

**Agent Workflow Correctness** (`dim_agent_workflows.go`)  
Five end-to-end MCP tool sequences:
- **Workflow A** — `session_start` → 5 saves → `context` → `recall` → `session_end`
- **Workflow B** — `topic_key` upsert + `invalidate` + `recall` verifies latest version wins
- **Workflow C** — 10 observations, precise vs vague query hit-rate comparison
- **Workflow D** — 30 mixed saves → `consolidate` → `status` → `recall` → `context`
- **Workflow E** — file-linked save → `git_hook` → staleness propagation verification

**Param Richness Impact** (`dim_agent_param_impact.go`)  
Saves 10 observations at five levels of parameter richness (each in an isolated namespace) and queries each level with the same queries:
- Level 1: title + content only
- Level 2: + `observation_type` + `kind`
- Level 3: + `tags` + `namespace`
- Level 4: + `files` + `topic_key`
- Level 5: + `confidence` + `retention` (all params)

Checks: Level 5 outperforms Level 1, Level 3+ achieves ≥80% recall, `InferRetention` correctly classifies all corpus observations as durable, and namespace isolation prevents cross-namespace leakage.

---

## Scoring

### Three-tier thresholds

Every dimension is normalised to a 0–100 score:

| Raw score range | Normalised score | Tier |
|-----------------|-----------------|------|
| `< Base` | 0–40 | Base |
| `Base ≤ x < Target` | 40–70 | Target |
| `Target ≤ x < Elite` | 70–95 | Elite |
| `x ≥ Elite` | 95–100 | Beyond |

### Letter grades

| Grade | Score |
|-------|-------|
| S | > 95 |
| A | > 85 |
| B | > 70 |
| C | > 55 |
| D | > 40 |
| F | ≤ 40 |

### Overall score

The overall score is an **equal-weighted average** of all selected dimension scores.  
A run with `--category cognitive` only averages the five cognitive dimensions; `--category agent` averages the three agent dimensions; a full `--category all` run averages all twelve dimensions.

Approximate category weights in a full run: **Cognitive 45%** · **Performance 20%** · **Agent 35%**.

---

## JSON Output

Pass `--output path/to/results.json` to export the full report. The file is valid JSON (indented) with the following structure:

```jsonc
{
  "Dimensions": [
    {
      "DimensionName": "Knowledge Evolution",
      "Category": "cognitive",
      "Score": 72.4,
      "Max": 100,
      "Grade": "B",
      "Checks": [
        { "Name": "A1: V2 in results", "Passed": true, "Detail": "v2ID=01K... found=true results=5" },
        { "Name": "A1: V1 excluded",  "Passed": true, "Detail": "v1ID=01K... in_results=false" }
      ],
      "Metrics": {
        "checks_passed": 30,
        "checks_total":  35,
        "recall_rate":   85.7
      },
      "Errors": [],
      "Duration": 1200000000
    }
    // ... more dimensions
  ],
  "OverallScore": 68.2,
  "Grade": "B",
  "Duration": 8300000000,
  "Scale": "small",
  "Recommendations": [
    "Improve Temporal Cognition (grade C, gap 24.3 pts)",
    "Improve 30-Day Brain Simulation (grade D, gap 32.1 pts)"
  ]
}
```

`Duration` values are in nanoseconds (standard `time.Duration` JSON serialisation).

### Tracking results over time

```bash
# Run at each release and archive results
go run -tags fts5 . benchmark --scale medium --output "benchmarks/$(date +%Y-%m-%d).json"

# Compare two runs
jq '.OverallScore' benchmarks/2026-01-01.json benchmarks/2026-03-22.json
```

---

## Standard Go Benchmarks (CI)

In addition to the `neurox benchmark` command, the repository includes standard Go benchmarks for CI integration:

```bash
# Run all integration benchmarks
go test -tags fts5 -bench=. -benchmem ./tests/integration/...

# Run specific benchmarks
go test -tags fts5 -bench=BenchmarkSave_10K ./tests/integration/...
go test -tags fts5 -bench=BenchmarkRecallFTS_50K ./tests/integration/...
go test -tags fts5 -bench=BenchmarkConsolidation_5K ./tests/integration/...
go test -tags fts5 -bench=BenchmarkConcurrentWrites ./tests/integration/...
go test -tags fts5 -bench=BenchmarkFactGraph ./tests/integration/...
```

Available Go benchmarks:

| Benchmark | What it measures |
|-----------|-----------------|
| `BenchmarkSave` | Per-op save throughput (N iterations) |
| `BenchmarkSave_10K` | Sustained write throughput (10 K obs / iteration) |
| `BenchmarkRecallFTS` | FTS5 recall over 1 K seeded observations |
| `BenchmarkRecallFTS_50K` | FTS5 recall over 50 K seeded observations |
| `BenchmarkConsolidation_5K` | Full consolidation pipeline over 5 K observations |
| `BenchmarkConcurrentWrites` | 8-goroutine concurrent write stress (100 writes/goroutine) |
| `BenchmarkFactGraph` | Fact save throughput (subject/predicate/object triples) |

### Integration test

```bash
# Full suite smoke test at small scale (runs all 12 dimensions, ~30 s)
go test -tags fts5 -run TestBenchmarkSuite_Small ./tests/integration/...

# Category and dimension filter unit tests
go test -tags fts5 -run TestBenchmarkSuite_CategoryFilter ./tests/integration/...
go test -tags fts5 -run TestBenchmarkSuite_DimensionFilter ./tests/integration/...
```

---

## Architecture Notes

- **FakeEmbedder**: All benchmark dimensions use a deterministic word-hash embedder (no Ollama/OpenAI required). This enables hybrid recall testing — FTS5 + cosine similarity — without any external service.
- **Isolated SQLite**: Each `BenchEnv` uses a temporary directory with a fresh WAL-mode SQLite database. Dimensions do not share state.
- **Mock LLM**: `llm.Disabled{}` + `llm.GateModeOff` ensures deterministic consolidation. LLM quality is tested separately via LongMemEval.
- **Build requirement**: `CGO_ENABLED=1` and `-tags fts5` are required. Without FTS5, all full-text searches will fail.

```bash
# Required build invocation
CGO_ENABLED=1 go build -tags fts5 ./...
CGO_ENABLED=1 go test -tags fts5 ./...
```

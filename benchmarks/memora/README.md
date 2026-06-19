# Memora FAMA Benchmark for Neurox

This benchmark evaluates Neurox's memory capabilities using the **FAMA metric** (Forgetting-Aware Memory Accuracy) from the Memora research paper: "From Recall to Forgetting: Benchmarking Long-Term Memory for Personalized Agents".

## Overview

The benchmark tests three memory skills:
- **Remembering**: Factual recall of user information
- **Reasoning**: Inferences based on user history
- **Recommending**: Personalized suggestions considering user preferences

## Key Innovation: FAMA Metric

Unlike standard accuracy, **FAMA** penalizes responses that rely on invalidated or superseded memory. This is critical for agent systems because:

- **Standard Accuracy** checks: "Is the answer correct?" (0 or 1)
- **FAMA Accuracy** checks: "Is the answer correct AND sourced from fresh memory?" (0 or 1)

### Example

```
Session 1: "My favorite drink is coffee"
Session 2: "I stopped drinking coffee, I prefer tea now"

Question: "What is the user's favorite drink?"

Response: "Coffee" (using old observation)
  Standard Accuracy: FAIL (wrong answer)
  FAMA Accuracy: FAIL (uses invalidated memory)

Response: "Tea" (using new observation)  
  Standard Accuracy: PASS (correct answer)
  FAMA Accuracy: PASS (uses fresh memory)
```

## Dataset

The benchmark includes a **synthetic dataset** with:
- **20 synthetic users** with realistic profiles
- **3 sessions per user** spanning 3 months, each evolving facts about the user
- **25 evaluation questions** covering all three skills
- Explicit `invalidated_answer` for each question marking what the stale answer would be

### Dataset Format

```json
{
  "users": [
    {
      "user_id": "user-001",
      "sessions": [
        {
          "session_id": "sess-001-a",
          "date": "2026-01-01T10:00:00Z",
          "turns": [
            {"role": "user", "content": "..."},
            {"role": "assistant", "content": "..."}
          ]
        }
      ]
    }
  ],
  "questions": [
    {
      "q_id": "q-001",
      "question_type": "remembering|reasoning|recommending",
      "question": "...",
      "answer": "ground truth",
      "invalidated_answer": "the stale/wrong answer",
      "user_id": "user-001"
    }
  ]
}
```

Located in: `testdata/memora_synthetic.json`

## Running the Benchmark

### Prerequisites
- Neurox server running on `http://localhost:7438`
- Go 1.26+

### Basic Run

```bash
cd benchmarks/memora
go run . -limit 25 -output report.json
```

### With Options

```bash
go run . \
  -limit 25 \
  -config synthetic-weekly \
  -namespace bench-memora-prod \
  -server http://localhost:7438 \
  -output results.json
```

### Flags

- `-limit N`: Evaluate top N questions (default: 100)
- `-config STR`: Configuration profile (default: synthetic-weekly)
- `-namespace STR`: Neurox namespace for observations (default: bench-memora)
- `-server URL`: Neurox API server URL (default: http://localhost:7438)
- `-output FILE`: Save JSON report to file (optional)
- `-dataset FILE`: Use custom dataset JSON file (optional)

## Benchmark Phases

### Phase 1: Ingest Sessions
Each session is saved as an `episodic` observation tagged with the user ID.

### Phase 2: Mark Superseded Observations
Earlier sessions from the same user are marked as `stale` via `POST /observations/{id}/invalidate`.

This simulates the natural lifecycle of memory where facts become outdated.

### Phase 3: Evaluate Questions
For each question:
1. Search for relevant observations using the question text + user ID
2. Check if the correct answer appears in retrieved content (standard accuracy)
3. Check if the answer uses only fresh observations (FAMA accuracy)
4. Calculate FAMA gap: percentage of answers that failed FAMA due to stale memory

## Output Format

### Console Output

```
NEUROX MEMORA/FAMA REPORT
=========================
Config: synthetic-weekly
Users: 20, Sessions per user: avg 3.2

STANDARD ACCURACY (by task):
  remembering:  12/16 (75.0%)
  reasoning:    0/5 (0.0%)
  recommending: 0/4 (0.0%)
  OVERALL:      12/25 (48.0%)

FAMA ACCURACY (penalizes stale memory use):
  remembering:  1/16 (6.2%)
  reasoning:    0/5 (0.0%)
  recommending: 0/4 (0.0%)
  OVERALL:      1/25 (4.0%)

FAMA gap: -91.7% (respuestas que usaron memoria invalidada)
Staleness distribution of retrieved observations:
  fresh:      34.4%
  stale:      65.6%
  expired:    0.0%
```

### JSON Report

Includes:
- `standard_accuracy`: Per-task and overall accuracy without FAMA penalty
- `fama_accuracy`: Per-task and overall accuracy with FAMA penalty
- `fama_gap_percent`: Percentage point difference
- `staleness_distribution`: Breakdown of fresh/stale/expired observations retrieved
- `evaluated_questions`: Array of per-question evaluation results with retrieved observations
- `notes`: Implementation notes and caveats

## Evaluation Method

This benchmark uses **exact-match normalized comparison** (case-insensitive, punctuation-stripped) rather than LLM-based evaluation. This is:

✅ **Deterministic** — same results every run  
✅ **Fast** — no LLM latency  
✅ **Transparent** — simple string matching is auditable  

❌ **Limited** — doesn't capture semantic variations or partial credit  

For production evaluation, integrate an LLM evaluator to assess semantic correctness.

## Architecture

```
main.go          — CLI entry point, flag parsing, report formatting
dataset.go       — Dataset types and loading
runner.go        — Orchestration: ingest → supersede → evaluate
fama.go          — FAMA logic and metrics definitions
fama_test.go     — Unit tests for FAMA scoring
testdata/
  memora_synthetic.json  — Embedded synthetic dataset
```

## Testing

```bash
go test ./benchmarks/memora/ -v
```

Tests cover:
- FAMA logic correctness (4 scenarios)
- Accuracy percentage calculation

## Integration with Neurox API

The runner communicates with Neurox via HTTP:

- `POST /api/v1/observations` — Save session as observation
- `GET /api/v1/observations/search?q=...&namespace=...` — Search for relevant observations
- `POST /api/v1/observations/{id}/invalidate` — Mark observation as stale
- `GET /api/v1/status` — Check server health

## Future Enhancements

- [ ] Support real Memora dataset from geniesinc/Memora repo
- [ ] Add weekly/monthly/quarterly temporal profiles (currently only "synthetic")
- [ ] LLM-based evaluation for semantic correctness
- [ ] Multi-tenant namespace isolation
- [ ] Bulk dataset loading optimization
- [ ] Performance metrics (latency per search, ingestion throughput)

## References

- **Paper**: "From Recall to Forgetting: Benchmarking Long-Term Memory for Personalized Agents"
- **Original Repo**: https://github.com/geniesinc/Memora (Apache-2.0)
- **License**: This benchmark is part of Neurox (Apache-2.0)

## Notes for Developers

- The benchmark creates observations in a dedicated namespace to avoid polluting production data
- Observations are never cleaned up automatically; use `neurox consolidate` or manually delete
- Search ranking may return irrelevant results for broad queries; consider user-specific tagging
- FAMA metric assumes binary staleness (fresh/stale); doesn't model gradual decay

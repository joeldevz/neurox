# Neurox Memorybench Harness

A Node.js harness for benchmarking Neurox against the LongMemEval-S dataset with **LLM-powered answer generation and evaluation**.

Results are **comparable with commercial systems** (Mem0, Zep, Supermemory) when using an LLM judge (Anthropic Claude, OpenAI GPT, or compatible gateway).

## Prerequisites

- Node.js >= 18
- Neurox running on `http://localhost:7438` (or set `NEUROX_BASE_URL`)
- LongMemEval-S dataset at `../longmemeval/data/longmemeval_s.json`
- **For comparable results**: API key for an LLM provider:
  - `ANTHROPIC_API_KEY` (Anthropic Claude) — **recommended**
  - `OPENAI_API_KEY` (OpenAI GPT)
  - `GATEWAY_API_KEY` + `GATEWAY_URL` (OpenAI-compatible gateway)

## Installation

```bash
npm install
```

## Quick Start

### Smoke test (3 questions, fallback to exact-match if no LLM key)
```bash
node src/index.js run -p neurox -b longmemeval -l 3 -r smoke-test
```

### With Claude (comparable results)
```bash
ANTHROPIC_API_KEY=sk-ant-... node src/index.js run -p neurox -b longmemeval -l 30 \
  --judge-provider anthropic --judge-model claude-sonnet-4-5 -r claude-judged
```

### With OpenAI GPT-4o
```bash
OPENAI_API_KEY=sk-... node src/index.js run -p neurox -b longmemeval -l 30 \
  --judge-provider openai --judge-model gpt-4o -r openai-judged
```

### With OpenAI-compatible gateway (e.g., opencode.ai/zen)
```bash
GATEWAY_API_KEY=... GATEWAY_URL=https://opencode.ai/zen/go/v1 \
node src/index.js run -p neurox -b longmemeval -l 30 \
  --judge-provider gateway --judge-model deepseek-v4-pro -r gateway-judged
```

### Full benchmark (all questions)
```bash
ANTHROPIC_API_KEY=sk-ant-... node src/index.js run -p neurox -b longmemeval \
  --judge-provider anthropic -r full-claude
```

### Check run status
```bash
node src/index.js status -r smoke-test
```

### Clear namespace
```bash
node src/index.js clear -n benchmark-smoke-test
```

## CLI Options

### run command
```
  -p, --provider <name>       Provider: neurox (required)
  -b, --benchmark <name>      Benchmark: longmemeval (required)
  -l, --limit <number>        Max questions (default: all ~500)
  -r, --run-id <id>           Run ID for checkpointing (default: auto-generated)
  --no-ingest                 Skip ingest phase (reuse existing sessions)
  --judge-provider <prov>     Judge: anthropic|openai|gateway|exact|auto (default: auto)
  --judge-model <model>       Judge model name (e.g., claude-sonnet-4-5, gpt-4o)
  --answer-model <model>      Answer generation model (same provider as judge)
```

### Judge Provider Auto-detection

When `--judge-provider auto` (default):
1. If `ANTHROPIC_API_KEY` is set → uses Anthropic Claude
2. Else if `OPENAI_API_KEY` is set → uses OpenAI GPT
3. Else if `GATEWAY_API_KEY` is set → uses configured gateway
4. Else → falls back to exact-match with **warning that results are NOT comparable**

## Output

Results are saved to `data/runs/{run-id}/`:
- `checkpoint.json` — phase progress and partial results
- `report.json` — full detailed report (includes judge_provider, judge_mode)
- `stdout` — human-readable summary

## Architecture

```
src/
  ├── index.js               — CLI entry point
  ├── judge.js              — Multi-provider LLM abstraction + exact-match fallback
  ├── answer.js             — LLM-based answer generation
  ├── runner.js             — 6-phase pipeline orchestrator
  ├── providers/
  │   └── neurox.js         — Neurox HTTP adapter
  ├── benchmarks/
  │   └── longmemeval.js    — Dataset loader
  └── utils.js              — Helpers
```

## Evaluation Pipeline

The benchmark follows a 6-phase pipeline to match commercial systems:

1. **ingest** — Parse dataset, create sessions as observations in Neurox
2. **wait** — Brief delay to allow consolidation and indexing
3. **search** — Query each question, retrieve top-K context
4. **answer** — **LLM generates answer from context** (new, critical)
5. **evaluate** — **LLM judge evaluates answer vs. ground truth** (new, critical)
6. **report** — Compute metrics and save report

### Why this pipeline is comparable

- **Mem0, Zep, Supermemory** follow the same pipeline:
  1. Ingest knowledge into vector DB
  2. Search for relevant context
  3. **Call LLM to generate answer from context** ← critical
  4. **Call LLM judge to evaluate answer** ← critical
  
- **Neurox** now does the same with multi-provider LLM support

### Without LLM keys (exact-match fallback)

If no API keys are set, the benchmark **still completes** but:
- Answer generation: extracts first non-empty line from context
- Judge: uses fuzzy string matching (70% word overlap)
- **Results are NOT comparable** with commercial systems (clearly warned)

## Judge Modes

### LLM Judge (Comparable)
- Uses Claude, GPT, or compatible API
- Strict evaluation: specific names, dates, numbers must match
- Tolerates phrasing differences
- JSON parsing + fallback text search for robustness

Example prompt:
```
You are a strict evaluator of question-answering memory systems.
Given a question, ground-truth answer, and predicted answer, decide if the prediction is CORRECT.

Rules:
- The prediction must contain the specific information in the ground truth.
- A vague answer that identifies the topic but misses the specific detail is INCORRECT.
- "I don't know" is INCORRECT unless the ground truth is an abstention.
- Ignore differences in phrasing, capitalization, or extra correct detail.

[Question / Ground Truth / Predicted]

Respond with ONLY a JSON object: {"correct": true|false, "reason": "<one sentence>"}
```

### Exact-Match Judge (Fallback, NOT Comparable)
- Fuzzy string matching with normalization
- Case-insensitive, punctuation-removed
- 70% word overlap threshold
- **Warned prominently in output**

## Output Format

```
============================================================
NEUROX MEMORYBENCH REPORT
============================================================
Run ID:    claude-judged
Benchmark: LongMemEval-S
Provider:  Neurox (http://localhost:7438)
Judge:     llm (anthropic)
Questions: 30

ACCURACY BY TYPE:
  single-session-user:     18/20  (90.0%)
  temporal-reasoning:      8/10   (80.0%)
  ─────────────────────────
  OVERALL                  26/30  (86.7%)

LATENCY:
  avg search: 24ms  p50: 22ms  p95: 31ms

MemScore: 86.7% / 24ms / 12000tok
============================================================
```

## Environment Variables

- `NEUROX_BASE_URL` — Neurox HTTP API (default: `http://localhost:7438`)
- `ANTHROPIC_API_KEY` — Anthropic Claude API key (for LLM judge/answer generation)
- `OPENAI_API_KEY` — OpenAI API key (alternative LLM provider)
- `GATEWAY_API_KEY` — OpenAI-compatible gateway API key
- `GATEWAY_URL` — Gateway endpoint (default: `https://opencode.ai/zen/go/v1`)
- `JUDGE_PROVIDER` — Override judge provider in auto-detect (anthropic|openai|gateway)
- `JUDGE_MODEL` — Override judge model name
- `DEBUG` — Enable stack traces on error

## Performance Characteristics

Typical performance with Claude Sonnet (LongMemEval-S, 30 questions):
- Ingest: ~2s (30 sessions)
- Search: ~0.7s (24ms avg per question)
- Answer generation: ~15s (500ms per question with Claude)
- Judge: ~30s (1s per question with Claude evaluation)
- Total: ~50s for 30 questions

## Troubleshooting

### "Neurox not available"
```bash
# Make sure Neurox is running
neurox serve --http 7438
```

### "LLM API error"
- Check API key is set: `echo $ANTHROPIC_API_KEY`
- Check API key is valid
- Check network connectivity to API endpoint

### "Answer generation failed"
- Falls back to context extraction (non-LLM)
- Check logs for API errors

### Results show "NOT COMPARABLE"
- Set `ANTHROPIC_API_KEY` or `OPENAI_API_KEY` before running
- Exact-match judge is for testing only

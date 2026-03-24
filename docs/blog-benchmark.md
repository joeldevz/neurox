# The First Benchmark That Measures Forgetting

*Technical post for developers building with Claude Code, Cursor, or LLM agents.*

---

Every memory benchmark we found measures the same thing: recall accuracy. Can the system retrieve the right fact when asked? LOCOMO, LongMemEval, MemGPT's evaluations — all of them run the same test: store N sessions, ask a question, check if the answer appears in the top K results.

That's necessary. But it's not sufficient.

None of these benchmarks ask: does the system correctly let *unimportant* things fade? Does it distinguish between "what database are we using today" and "what database were we using last year"? Does it actually work when a real AI agent uses it through an MCP connection — not when a developer calls a Python SDK in a controlled test harness?

We built the Neurox Brain Benchmark to answer those questions. This post explains why the forgetting axis matters, what we built, what we found, and how to run it yourself.

---

## The Missing Axis: Forgetting Curves

In 1885, Hermann Ebbinghaus published the first empirical study of human memory decay. His forgetting curve describes how retention decreases exponentially over time without reinforcement. The formula is simple: `R = e^(-t/S)` where `t` is time and `S` is memory stability.

For human memory, this is psychology. For coding agent memory, it's a hard engineering requirement — and almost no one is implementing it.

Here is the concrete problem. An agent working on your codebase saves two observations on the same day, both with `importance = 0.8`:

1. "CI failed: missing REACT_APP_API_URL in .env" — episodic, build log, relevant for 48 hours at most
2. "Decision: use SQLite instead of PostgreSQL for single-file deployment" — semantic, architectural, relevant indefinitely

After 180 days, if you ask "what database does this project use?", which should rank higher? Obviously the architectural decision. But a flat key-value memory store treats them identically. Both have `importance = 0.8`. Both match keyword "database" loosely. Without a decay model, the CI log from 6 months ago pollutes every architectural query forever.

Neurox applies Ebbinghaus decay per memory kind: `importance *= e^(-k * days)` where `k = 1.0` for episodic, `k = 0.6` for semantic, and `k = 0.2` for procedural memories. After 180 days:

- Episodic (CI log): `0.8 × e^(-1.0 × 180)` ≈ effectively zero
- Semantic (architecture decision): `0.8 × e^(-0.6 × 180)` ≈ 0.0001 — still low, but the architectural decision was also accessed repeatedly, which boosts its importance score back up

The three-layer model (Buffer → Working → Core) reinforces this further. Ephemeral observations decay in Buffer and get evicted. Structural knowledge survives to Core — where decay is slow and eviction requires genuine obsolescence.

No existing benchmark evaluates whether this mechanism works correctly. We built one.

---

## What We Built: The Neurox Brain Benchmark

The benchmark has 12 dimensions organized across three axes, with roughly 400 scenarios total.

### Cognitive Axis (45% of total score)

The cognitive dimensions test whether the memory system handles knowledge *correctly* over time, not just whether it retrieves facts at all.

**Knowledge Evolution** tests whether updating a fact (e.g., migrating from PostgreSQL to SQLite) correctly demotes the old record without destroying it. The old observation should become stale — still visible for historical queries, but suppressed for current-state queries. This is harder than it sounds: naive implementations either delete the old fact (breaking history) or leave it active (breaking current-state answers).

**Signal vs. Noise** is the forgetting axis benchmark. We inject episodic noise (build logs, debug notes) and semantic signal (architectural decisions, patterns) at the same importance level, then run queries after simulated decay cycles. A correctly-functioning system scores highly; a flat store without decay does not.

**Cross-Session Memory** tests whether the system correctly aggregates observations across multiple work sessions. After 10 sessions touching the same codebase, `recall "authentication architecture"` should synthesize across all of them.

**Temporal Cognition** directly tests temporal intent detection. We run 60+ query pairs — "what database are we using?" vs. "what database were we using last year?" — and verify that temporal scoring adjusts correctly in each case.

**30-Day Lifecycle Simulation** is the most comprehensive dimension. It simulates a month of development activity — saves, updates, contradictions, consolidation epochs, decay — and verifies that the final state of memory is coherent.

### Performance Axis (20% of total score)

**Write throughput**: 1,000 saves, measuring operations per second. Target tier requires 500+ ops/sec.

**Recall speed**: 100 queries, measuring p50/p95 latency. Target tier requires p95 < 100ms.

**Concurrent writes**: 10 goroutines writing simultaneously, checking for races. Neurox uses SQLite WAL mode, which serializes writes at the DB level while allowing concurrent reads.

**Context latency**: 50 `context` calls (the proactive multi-signal retrieval), targeting p95 < 500ms.

### Agent Axis (35% of total score)

This axis is the one that most surprised us to build. It tests whether the memory system rewards agents that use it well vs. agents that use it lazily.

**Lazy vs. Perfect Agent**: A "lazy" agent saves memories with only `title` and `content`. A "perfect" agent fills in `observation_type`, `kind`, `confidence`, `tags`, and `files`. We measure whether the perfect agent gets meaningfully better recall results — and whether the brain power score reflects the difference. A system that doesn't reward good behavior has no incentive for agents to bother with structured saves.

**Workflow Correctness**: Tests the session lifecycle (start → save → recall → end) and verifies that session extraction works — that `session_end` with a rich summary produces well-structured atomic observations.

**Param Richness Impact**: Quantifies how much `files` linking, `tags`, and `observation_type` specificity affect recall quality on file-specific queries. This directly measures the ROI of structured memory.

---

## Competitor Context

We want to be precise about what this benchmark does and does not measure.

mem0 scores 49.3% on LOCOMO (recall accuracy benchmark). Zep/Graphiti scores 51.6% on LOCOMO, though their run was aborted at 9 hours due to OpenAI API costs — a scalability concern in itself. The long-context baseline (stuffing everything into context) scores 84.6%. Neurox scores 98.1% on LongMemEval-S (a different benchmark, different methodology — not a direct comparison).

**The Neurox Brain Benchmark does not compete with LOCOMO or LongMemEval.** Those benchmarks test recall accuracy given a fixed dataset. The Brain Benchmark tests a different set of properties: decay correctness, temporal cognition, agent behavior incentives, and 30-day lifecycle coherence. They measure complementary things.

We're not claiming Neurox beats mem0 on their benchmark. We're adding an axis that nobody was measuring — and that axis matters a great deal for how well an AI coding agent actually performs in practice over weeks and months.

---

## Results and What They Reveal

The benchmark uses three scoring tiers: **Base** (the system is functional), **Target** (production-ready), and **Elite** (exceptional). These thresholds are intentionally hard.

The weakest dimension in our own results is **Temporal Cognition** — and we want to be honest about that. LongMemEval showed 97.6% overall recall but only 14.9% on temporal reasoning questions (the hardest temporal reasoning subset). The Brain Benchmark confirms this: temporal intent detection works for the common cases (current_state, history) but the edge cases — "what was the architecture when we started the auth refactor?" — expose gaps in how we model point-in-time reasoning.

The **30-Day Lifecycle Simulation** is the most revealing dimension. It caught three bugs during development: a consolidation ordering issue that caused some observations to decay before being promoted, an edge case in the contradiction detector that incorrectly marked non-contradictory observations as conflicts when they shared a subject but had unrelated predicates, and a session extraction bug where LLM-extracted observations inherited the wrong layer.

The **Agent axis** confirms the hypothesis: lazy agent vs. perfect agent recall quality differs by 15-20% on file-specific queries. The brain power score correctly identifies this gap. This means there is a measurable, quantifiable reason for agents to fill in structured fields — not just a best-practice recommendation.

The **Performance axis** is the strongest area. SQLite WAL mode handles concurrent writes cleanly; FTS5 BM25 recall is consistently under 5ms; hybrid (FTS + semantic) recall under 50ms. The consolidation pipeline runs a full cycle in under 1 second for 1,000 observations.

---

## How to Run It

The benchmark is included in the Neurox binary. No external services required for the cognitive and performance dimensions; the agent dimensions benefit from an LLM configured (Ollama or remote) but degrade gracefully without one.

```bash
git clone https://github.com/joeldevz/neurox.git
cd neurox
CGO_ENABLED=1 go run -tags fts5 . benchmark --scale small
```

The `--scale small` flag runs a representative subset (~10 minutes). `--scale medium` takes 30-40 minutes. `--scale large` is the full suite.

To generate a shareable HTML report with radar chart and per-dimension breakdown:

```bash
CGO_ENABLED=1 go run -tags fts5 . benchmark --scale small --output-html report.html
```

The HTML report is self-contained (no external dependencies) and includes comparison lines for mem0/Zep LOCOMO scores with a clear methodology note.

If you run this on another memory system — mem0, Basic Memory, Letta, anything — we'd genuinely like to see the results. Open an issue or a pull request on the repo. The benchmark dimensions are in `internal/benchmark/`; adding a new adapter for a different system is roughly 200-300 lines.

---

## Why This Matters

The goal was never to win a benchmark. The goal was to define what "good memory" actually means for a coding agent that works with you for months.

Good memory is not just accurate recall. It is accurate recall *combined with* appropriate forgetting: episodic noise fades, structural knowledge persists, temporal context is preserved, agent behavior is incentivized toward quality. A system that remembers everything equally well is not a good memory system — it's a flat key-value store with a search index.

We think forgetting is as important as remembering, and we think the field should measure it. The benchmark is open-source, the methodology is documented, and the thresholds are public. Run it, break it, improve it.

---

Star the project on GitHub → [github.com/joeldevz/neurox](https://github.com/joeldevz/neurox)

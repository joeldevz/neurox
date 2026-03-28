# Vocabulary for Agent Memory Systems

A reference guide to the core concepts behind persistent memory in AI coding agents. These definitions describe how Neurox models memory — but the terms apply broadly to any system that gives agents durable, structured recall.

---

## Decay Curve

**Definition:** The mathematical function that reduces a memory's relevance over time, ensuring that old, unused observations gradually lose priority to newer, more pertinent ones.

**In practice:** A build log from yesterday ("CI failed due to missing env var") should rank high when you ask about recent CI errors — but after 60 days of silence, it's noise. Meanwhile, the architectural decision "we chose SQLite for single-file deployment" was made six months ago and is still essential context. Both were saved with `importance = 0.8`; the difference is how fast their curves descend.

**In Neurox:** Neurox applies Ebbinghaus exponential decay — `importance *= e^(-k * days)` — where `k` varies by memory kind: `episodic` (k=1.0, fast decay), `semantic` (k=0.6, moderate), `procedural` (k=0.2, slow). A build log tagged `episodic` halves in importance roughly every day; a design pattern tagged `procedural` takes weeks to halve. Decay runs automatically on every consolidation epoch.

---

## Consolidation Epoch

**Definition:** A scheduled pipeline run that processes all stored observations — applying decay, promoting valuable memories, evicting noise, resolving contradictions, and synthesizing insights.

**In practice:** Think of it as a background process that runs while you sleep. After a day of coding, your agent has accumulated dozens of raw observations: stack traces, decisions, half-formed notes. The consolidation epoch sorts them: important observations move up to longer-term storage; duplicates are merged; contradictions are flagged or resolved; low-signal entries are quietly dropped.

**In Neurox:** Neurox runs a consolidation epoch automatically every 30 minutes (or on demand via `neurox consolidate`). The pipeline has nine stages: decay → retry → promote (Buffer→Working) → promote (Working→Core) → dedup → contradict → reflect → evict → GC. Each stage has its own logic, and the whole cycle typically completes in under one second for 1,000 observations.

---

## Memory Layer

**Definition:** A tier of storage that determines how aggressively an observation decays, how hard it is to evict, and how it was earned its place.

**In practice:** Not all memories deserve the same permanence. A "TODO: investigate this error" note from this morning belongs in fast-moving short-term storage. "We standardize on RS256 for JWT" belongs somewhere it won't be touched. Mixing them in one flat list means either both survive forever (noise accumulates) or both expire quickly (you lose the signal).

**In Neurox:** Three layers model this separation. **Buffer** (Layer 0) is RAM — new, unvalidated observations, capacity 200, fast decay. **Working** (Layer 1) is SSD — validated, frequently accessed observations that survived promotion from Buffer. **Core** (Layer 2) is archival — proven knowledge, accessed 5+ times, older than 7 days, flagged `retention: durable`. Each layer applies Ebbinghaus decay at its own rate. Only Core observations are truly long-lived; Buffer observations are the default landing zone for every `save` call.

---

## Staleness

**Definition:** A flag indicating that an observation's content may no longer reflect the current state of the codebase, making it unreliable for forward-looking queries but still valid for historical ones.

**In practice:** You saved "We use PostgreSQL" six months ago. Last week you migrated to SQLite. The old observation isn't wrong — it accurately describes the past — but it should not be the top result when your agent asks "what database are we using now?" Staleness is the mechanism that demotes it without destroying it.

**In Neurox:** Observations become stale through three triggers: a git commit changes a linked file (git hook fires automatically), an explicit `invalidate` call marks the observation incorrect, or its importance decays below a configurable threshold. Stale observations are excluded from default recall results but remain queryable with `include_stale: true`. Historical queries like "what database did we use before?" will surface them. This preserves timelines without polluting current-state answers.

---

## Temporal Intent

**Definition:** The semantic meaning of the time dimension embedded in a user's query — distinguishing between questions about current state, past history, a specific moment, a time range, or an elapsed duration.

**In practice:** "What database are we using?" and "What database were we using in January?" look syntactically similar but require opposite retrieval strategies. The first should suppress stale observations; the second should surface them and boost temporal proximity to January. Treating both queries identically produces wrong answers for one of them.

**In Neurox:** Neurox detects six temporal intents: `current_state` ("now", "currently", "latest"), `history` ("before", "previously", "used to"), `when` ("when did", "what date"), `point_in_time` ("March 2026", "last week"), `time_range`, and `duration` ("how long", "since when"). Detection runs automatically on every `recall` query. Once detected, it adjusts the tri-factor score — boosting or penalizing observations based on their temporal fit. The temporal keywords are also stripped from the FTS5 query to improve keyword matching coverage.

---

## Observation vs. Fact

**Definition:** An observation is a free-text memory unit with metadata; a fact is a structured subject-predicate-object triple with a validity window that enables graph traversal and temporal queries over structured knowledge.

**In practice:** "We switched from PostgreSQL to SQLite in March for single-file deployment" is an observation — richly descriptive, searchable by keyword, but not structured. `(database, current, sqlite, valid_from: 2026-03)` is a fact — machine-queryable, linkable to other facts, and automatically superseded when you write a new `(database, current, ...)` fact. Both serve different retrieval needs.

**In Neurox:** Observations are the primary unit — every `save` call creates one. Facts are extracted from observations by the LLM layer (when configured) and stored as subject-predicate-object triples in the `facts` table with `valid_from` and `valid_until` timestamps. When a fact is superseded, `valid_until` is set and a `superseded_by` link is written, so you can always query the full history. Observations support full-text and semantic search; facts enable structured graph queries and precise temporal lookups.

---

## Brain Power Score

**Definition:** A composite metric (0–100) that measures how effectively an agent is using its memory system — not just whether memories exist, but whether they are rich, well-structured, and actively maintained.

**In practice:** An agent that saves everything as bare titles with no tags, no linked files, and no consistent types will technically "have memory" but won't perform well. The brain power score makes this visible: an agent using Neurox poorly might score 38/100, while the same agent following best practices scores 87/100. The score creates a feedback loop — agents (and their developers) can see the impact of saving quality observations vs. lazy ones.

**In Neurox:** The score has two components. **Static (60 pts)** evaluates the stored memory graph: embedding coverage, tag richness, file linking ratio, link density between observations, and consolidation health. **Dynamic (40 pts)** evaluates behavioral quality: save param richness (did the agent fill in type, kind, tags, files?), recall depth (are searches using filters?), session discipline (start/end calls), and tool breadth (are multiple MCP tools being used?). The score and a per-dimension breakdown are available via `GET /api/v1/health-check` and visible in the dashboard's Health tab. The `health_check` MCP tool returns the same data with actionable recommendations.

---

## Deep Curation

**Definition:** A batch review process where a large language model examines an entire namespace of observations to identify noise, recalibrate importance weights, and delete low-value entries — essentially a "spring cleaning" for agent memory.

**In practice:** After weeks of active development, an agent's memory accumulates hundreds of observations. Many are redundant ("fixed the build" appears five times with slight variations), some have importance scores distorted by aggressive decay math (a critical decision shows 0.01 importance because it hasn't been accessed recently), and others are outright noise (temporary debugging notes that were never cleaned up). Manual review at this scale is impractical. Curation automates the judgment call — "is this observation still worth keeping, and if so, how important is it really?"

**In Neurox:** The `curate` command (CLI and MCP tool) sends all observations in a namespace to a configured curator provider — typically a large, capable model like Gemini Flash. The curator classifies each observation as KEEP (with recalibrated importance) or DELETE (soft-delete). An optional `priorities.yaml` file lets users bias the curation toward domain-specific value signals — for example, marking "database decisions" as high-priority and "temporary debugging notes" as low-priority. Dry-run mode previews all changes without applying them. Results are recorded in a `curation_runs` audit table. Configure the curator under the `curator:` section in `config.yaml` or via `NEUROX_CURATOR_*` environment variables.

---

## Brain Benchmark

**Definition:** A self-contained test suite that measures the quality and performance of the memory engine across multiple dimensions — cognitive accuracy, system performance, and agent simulation — producing a composite score and letter grade.

**In practice:** Knowing that "recall works" is insufficient. You need to know: Does knowledge update correctly when facts change? Can the system distinguish signal from noise at scale? Does temporal reasoning actually affect ranking? How fast are writes under concurrent load? Does a lazy agent (bare saves, no tags) perform meaningfully worse than a disciplined one? The brain benchmark answers all of these with reproducible, isolated tests that never touch production data.

**In Neurox:** `neurox benchmark` runs 12 dimensions across 3 weighted categories: Cognitive (45% — knowledge update, signal-noise separation, cross-session retrieval, temporal reasoning, memory lifecycle), Performance (20% — write throughput, recall latency, concurrent access, context retrieval), and Agent Simulation (35% — lazy vs. perfect agent comparison, realistic multi-session workflows, parameter impact analysis). Each dimension produces a raw score mapped to four tiers: Base (0-40), Target (40-70), Elite (70-95), and Beyond (95-100). Scores aggregate into a letter grade (S through F). Scale configs control synthetic dataset size: `small` (1,000 observations), `medium` (10,000), `large` (100,000). All tests run against a fresh in-memory SQLite database. Output is available as terminal table, JSON (`--output`), or HTML report (`--output-html`).

---

## Activation Signals

**Definition:** Two complementary metrics — `activation_level` and `consolidation_strength` — that separate an observation's transient accessibility from its durable semantic value, enabling more accurate memory-like retention behavior than a single `importance` score alone.

**In practice:** A critical architectural decision made six months ago ("we chose SQLite for single-file deployment") has high durable value but hasn't been accessed recently. A build log from yesterday ("CI failed due to missing env var") has high transient accessibility but low long-term value. If both are tracked by a single `importance` score, one of two things goes wrong: either the old decision decays below the new log (losing signal), or the new log inherits importance it doesn't deserve (adding noise). Separating these signals lets decay curves, promotion rules, and recall scoring each target the right dimension.

**In Neurox:** `importance` (0.0-1.0) represents enduring semantic value — it changes slowly through access patterns and curation, never through time-based decay alone. `activation_level` (0.0-1.0, default 0.5) tracks recent accessibility — it decays with time and bumps on recall access, making recently-used memories easier to find without inflating their long-term importance. `consolidation_strength` (0.0-1.0, default 0.0) tracks how well-established a memory is within the system — it grows through promotion, repeated access, and curation validation. Together, these three signals drive the promotion pipeline (Buffer→Working→Core), recall ranking, and decay behavior, producing a more faithful approximation of human memory dynamics where frequently-recalled knowledge stays accessible while rarely-accessed-but-important knowledge survives through consolidation.

---

## Provenance

**Definition:** Structured metadata attached to every observation that records its origin — which surface created it, during which session, and through which operation — enabling users to trace any memory back to its source and understand how it entered the system.

**In practice:** An observation saying "We use SQLite" might have been saved by a human via the CLI, extracted from a session summary by the LLM, or synthesized during a reflection cycle. Without provenance, all three look identical. With provenance, you can answer "where did this come from?" and "should I trust it?" — a session-extracted observation created by an LLM deserves different scrutiny than a direct human save.

**In Neurox:** Every observation carries three provenance fields: `source_surface` (which entry point: `mcp`, `http`, `cli`, or `consolidator`), `source_session_id` (the session active at creation, if any), and `source_tool` (which operation created it: `save`, `invalidate`, `session_end`, `reflect`, `curate`, or `consolidate`). These fields are set at creation time by the surface that creates the observation — they are never backfilled or inferred. Provenance appears in `recall` and `context` responses across all surfaces (MCP, HTTP, CLI), and the `audit` command shows the full provenance alongside the observation's lifecycle.

---

## Memory Debugging

**Definition:** The ability to inspect *why* a memory was returned in a particular position — breaking down the composite score into its component signals so users and developers can understand, verify, and tune recall behavior.

**In practice:** When your agent asks "what database are we using?" and gets an unexpected result, you need to know: was it ranked high because of keyword match? Semantic similarity? Recency? Temporal intent? Without score visibility, recall is a black box — you can see the output but not the reasoning. Memory debugging makes the scoring transparent, turning "that's wrong" into "the temporal multiplier penalized this correctly, but the relevance score is too high because of a keyword overlap."

**In Neurox:** Pass `debug: true` to the `recall` tool (MCP), `--debug` flag (CLI), or `?debug=true` query parameter (HTTP). When enabled, each result includes a `score_breakdown` object with seven component scores: `recency` (Ebbinghaus decay signal), `importance` (durable value weight), `relevance` (FTS5 BM25 match), `semantic_score` (cosine similarity from embeddings), `temporal_multiplier` (intent-based boost or penalty), `cross_signal_boost` (confidence boost when FTS and semantic agree), and `final_score` (the composite result). The `audit <id>` command complements this by showing an observation's full lifecycle: creation timestamp, provenance, layer promotions, links (supersedes, contradicts, relates_to), staleness transitions, file associations, and temporal mentions.

---

## Further Reading

- [docs/quickstart.md](quickstart.md) — Get Neurox running with your AI client in under 5 minutes
- [GitHub README](https://github.com/joeldevz/neurox) — Architecture, MCP tools reference, benchmark results, and configuration

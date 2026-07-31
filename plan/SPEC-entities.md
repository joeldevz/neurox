# SPEC: Entities — first-class people/projects linked to memories

> GitHub issue: https://github.com/joeldevz/neurox/issues/10
> Status: proposed (not started)

## Problem

Neurox cannot answer "what do we know about X?" (a person, a project, a tool).
Recall returns a top-10 ranked by relevance for a query — it cannot aggregate
everything about one subject.

Real data from the production DB (~9k observations):

| Signal | Value |
|---|---|
| Observations mentioning "clasing" | 949 |
| Observations mentioning "vicente" (a person) | 4 |
| Way to view them grouped today | None |
| Facts with subject "clasing" | 15 (fragmented variants) |
| Facts with subject "vicente" | 0 — people not captured as subjects |
| Fact subjects appearing exactly once | 11,905 / 17,196 (69%) |

Conclusions: (a) the aggregation use case exists massively; (b) fact subjects
alone cannot power it (5% mention coverage, fragmented identities, people
missing) — membership must be derived from content mentions, deterministic
and cheap.

## Solution overview

A first-class entities layer following the pattern the market converged on in
2026 (Mem0 v2 lightweight entities, Hindsight entity summaries, Basic Memory
file-per-entity), avoiding documented anti-patterns (Mem0's removed LLM
graph — 3.2x write latency; GraphRAG eager summaries — 1000x indexing cost;
exact-only dedup — LightRAG duplicates).

1. **Detection (deterministic, zero LLM)**: on save, scan observation text for
   known entity names/aliases; link via `entity_mentions`. No LLM on write path.
2. **Catalog**: `entities` table (canonical name, kind: person/project/concept,
   namespace, summary). New entities promoted during consolidation from repeated
   unmatched mentions (never eagerly).
3. **Aliases**: mechanical fuzzy resolution ("clasing-prod" → clasing;
   "Chris" → Christopher). Ambiguous merges queued for consolidation-time LLM
   review.
4. **Entity card**: MCP tool + HTTP `entity(name)` → summary + linked
   observations + related entities (co-occurrence gives nesting, e.g. vicente
   inside clasing).
5. **Living summary**: per-entity LLM summary regenerated lazily in
   consolidation (clone of `reflect` engine, entity-scoped source query), with
   source-observation provenance for staleness invalidation.

### Explicitly out of scope (anti-patterns)

- LLM extraction per write
- Full graph traversal at query time (entities are a boost/filter/card, not a
  navigable KG)
- Eager global summarization

## Phases

| Phase | What | Effort |
|---|---|---|
| 1 | Migration: `entities` + `entity_mentions`; deterministic mention detection on save; initial backfill | M |
| 2 | `entity(name)` MCP tool + HTTP route (card); alias resolution; merge queue | M |
| 3 | Lazy per-entity summaries in consolidation with provenance + staleness re-generation | M |
| 4 | Recall boost/filter by entity; entity nodes in `neurox graph` | S |

## Test plan

Unit tests (Go, table-driven, existing patterns):

- Save mentioning "clasing"+"vicente" → both linked
- "Chris"/"Christopher" → same entity (alias)
- Generic term ("apple", "build") → no entity created / filtered
- Invalidated observation → entity summary flagged for re-generation

Acceptance test on real data (copy of production DB to /tmp):

1. Run backfill on `~/.config/neurox/neurox.db` (copy)
2. Pass: `entity clasing` returns ~900+ of the 949 mentions linked;
   `entity vicente` returns its 4; generic terms ("tests", "build", "project")
   below threshold or absent from catalog
3. Fail: <70% mention coverage, or >30% of catalog is garbage entities

## References

- Competitor research (Jul 2026): Mem0 v2 entity linking, Graphiti 3-tier
  dedup, Hindsight 4-network design (83.6% LongMemEval, arXiv:2512.12818),
  Supermemory Profiles, Basic Memory wiki pattern
- Existing infra to reuse: `facts` subjects (structured detail), `reflect`
  engine (summary template), FTS5, consolidation pipeline
- Benchmark impact: NOT the goal — daily-use feature. Benchmark lever remains
  the multi-session diagnosis (see plan/PLAN-benchmark-portfolio.md steps 7-8).

# Neurox Strategic Roadmap — 2026-2028

## Executive Frame

Neurox's strategic position is clear: be the memory layer that makes AI coding agents measurably better over time. The 12-month priority is closing the gap between what Neurox claims and what it ships — unifying product surfaces, making provenance and temporal truth real in retrieval, and producing a public benchmark that proves coding-agent memory matters. The 12-24 month horizon extends into team memory, sync, and ecosystem breadth, but only after the single-agent story is airtight. Every quarter should be evaluated against one question: does a coding agent with Neurox produce better code than one without it, and can we prove it?

---

## 0–12 Months: Dominate Single-Agent Coding Memory

### Surface Parity & Claim Alignment (Q1-Q2)
- Audit every MCP tool, HTTP endpoint, and CLI command; produce a parity matrix and close gaps systematically.
- Remove or downgrade documentation claims that outrun shipped code (hybrid search candidate generation, facts in scoring, interference-aware decay).
- Expose `save` status for async consolidation flows across all three surfaces.
- Route session extraction, reflection, and curation through the same internal pipeline regardless of entry point.

### Provenance as a First-Class Retrieval Signal (Q2-Q3)
- Attach origin metadata (source agent, session, commit, file, command) to every observation at write time.
- Make provenance queryable: filter and boost recall results by source, confidence lineage, and supersession chain.
- Surface provenance in all retrieval responses (MCP, HTTP, CLI) so agents and users can trace why a memory was returned.

### Memory Debugging & Audit UX (Q2-Q3)
- Ship a `neurox debug <query>` CLI command that shows candidate generation, scoring breakdown, decay state, and final ranking for any recall.
- Expose the same breakdown via HTTP/MCP for agent-side introspection.
- Add a `neurox audit` command: timeline view of an observation's lifecycle (created -> promoted -> decayed -> invalidated -> superseded).
- Make contradiction detection and dedup results visible, not silent.

### Facts Subsystem Activation (Q3)
- Integrate facts into retrieval candidate generation — not just storage.
- Allow facts to boost or suppress recall results (e.g., "this file was renamed" should suppress stale file-linked memories).
- Expose fact CRUD in CLI and MCP, not just HTTP.

### Coding-Agent Benchmark (Q3-Q4)
- Define a reproducible benchmark: N coding tasks, with and without Neurox, measuring correctness, context efficiency, and error recurrence.
- Publish methodology, dataset, and results openly.
- Use the benchmark internally as a regression gate for every release.

### Interference-Aware Memory Management (Q4)
- Move beyond pure time-decay (Ebbinghaus) to interference-aware scoring: memories on the same topic/file compete, and retrieval strengthens winners.
- Implement retrieval practice as the primary strengthening signal — memories that get recalled in useful contexts gain durability.
- Deprecate pure age-based eviction in favor of interference + utility scoring.

---

## 12–24 Months: Team Memory & Ecosystem Breadth

### Scoped & Team Memory Controls (Q5-Q6)
- Introduce memory scopes: personal, project, team — with explicit read/write policies per scope.
- Ship a sync protocol (CRDTs or event-log merge) for local-first nodes to replicate project/team memories without a central server.
- Add access controls: who can write to team memory, who can invalidate, audit trail for governance.

### Retrieval Planner (Q6-Q7)
- Replace single-pass retrieval with a lightweight planner that selects retrieval strategy per query (FTS, semantic, graph/facts, or hybrid blend).
- Allow agents to express retrieval intent (e.g., "recent decisions about auth" vs. "all known bugs in payments").

### Ecosystem Adapters (Q6-Q8)
- Ship first-class adapters for top 3 coding-agent frameworks beyond Claude Code (Cursor, Cline, Aider, Continue — pick based on traction).
- Publish an integration spec so third-party agents can adopt Neurox without custom work.

### Health & Governance Dashboard (Q7-Q8)
- Web-based dashboard for memory health: staleness distribution, consolidation throughput, contradiction rate, retrieval hit rate.
- Team admin view: scope utilization, policy violations, memory growth trends.

---

## Top 5 Bets

| Bet | Rationale |
|---|---|
| **Provenance as retrieval signal** | No competitor treats origin metadata as a scoring factor. This makes Neurox memories trustworthy, not just available. |
| **Memory debugging / audit UX** | Agents and developers will not trust opaque memory. Explainability is a moat — once users rely on `debug` and `audit`, switching costs are real. |
| **CLI/MCP/HTTP parity** | A fragmented surface erodes trust and slows adoption. Parity is table stakes before any growth push. |
| **Coding-agent benchmark** | Without a public, reproducible benchmark, "memory helps" is a claim. With one, it becomes a fact competitors must respond to. |
| **Scoped/team memory controls** | Team memory is the unlock for enterprise adoption and the only path to revenue beyond individual developers. But only after single-agent is proven. |

## Top 5 No-Bets

| No-Bet | Rationale |
|---|---|
| **Generic chat memory** | Dilutes positioning, invites competition from Mem0, LangMem, and every RAG wrapper. Neurox wins by being specific to coding agents. |
| **Another vector DB** | Neurox is not a retrieval infrastructure product. Hybrid search is a means, not the value prop. Do not compete with Chroma, Qdrant, etc. |
| **Broad SDK-for-everything** | SDKs for Python, JS, Rust, etc. spread engineering thin. MCP + HTTP cover integration; invest in adapters for specific agents, not generic SDKs. |
| **"Human-like memory" as GTM** | Neuroscience metaphors are useful internally but confuse buyers. GTM should be about measurable outcomes: fewer repeated errors, faster context loading, better code. |
| **Horizontal expansion too early** | Customer support memory, personal knowledge management, general-purpose agents — all plausible, all distracting. Do not pursue until coding-agent NPS and retention are proven. |

---

## Dependencies & Sequencing

1. **Parity before growth.** Surface unification and claim alignment must complete before any public benchmark or ecosystem push. Credibility cannot be repaired after launch.
2. **Provenance before team memory.** Origin tracking is a prerequisite for team-grade access control and audit. Ship it for single-agent first.
3. **Facts before retrieval planner.** The planner needs facts as a retrieval channel; activating facts in scoring comes first.
4. **Benchmark before adapters.** Prove the value with one agent (Claude Code) before investing in multi-agent integration.
5. **Interference-aware scoring before team sync.** Memory competition and strengthening must work locally before distributed merge introduces new conflict patterns.

## Competitive Watchlist

- **Mem0 / LangMem**: Closest positioning. Watch for coding-agent-specific features or benchmark claims.
- **Cursor / Windsurf built-in memory**: Platform lock-in risk. If major editors ship "good enough" memory, Neurox must prove cross-agent portability.
- **Zep**: Team memory and enterprise features. Watch for local-first moves.
- **LLM context window growth**: Longer contexts reduce some memory urgency. Neurox's counter: temporal truth, provenance, and cross-session persistence are not solved by larger windows.

## Kill Criteria & Course-Correction Signals

- **If benchmark shows <10% improvement** on coding tasks with Neurox vs. without: re-examine core value proposition before further investment.
- **If 3+ coding-agent platforms ship built-in memory** with comparable provenance/temporal features within 12 months: accelerate open-source community and adapter strategy, consider protocol standardization play.
- **If team memory adoption is <20% of active users** after 6 months of availability: team memory may be premature — double down on single-agent depth.
- **If surface parity takes >6 months**: the architecture is too fragmented. Consider collapsing to a single canonical API with thin adapters.
- **If facts subsystem sees <5% query involvement** after activation: facts model may be wrong. Run user research before further investment.

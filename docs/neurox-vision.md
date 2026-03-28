# Neurox — Vision

## North Star

Neurox is the system of record for agent memory: portable, temporal, auditable, and built for agents doing real work.

## Vision at 3–5 Years

Today, agent memory is either absent, ephemeral, or locked inside the vendor that hosts the model. An agent using Claude forgets everything when it talks to a different project. An agent using GPT has no way to carry verified decisions to a different tool. Memory that does exist is flat — it stores what was said, not when it was true, what replaced it, or whether it was ever validated.

Neurox exists to close that gap. In 3–5 years, Neurox should be the neutral memory layer that any agent — coding, ops, internal enterprise — relies on for durable, inspectable, correct memory. Not memory as a convenience feature bolted onto a chat interface. Memory as infrastructure: queryable, portable across vendors and tools, temporally honest, and auditable by both humans and machines.

Concretely, that means:

- **Any coding agent** — Claude Code, Cursor, Copilot, OpenCode, custom agents — writes to and reads from the same memory substrate, regardless of which model powers it.
- **Memory travels with the codebase**, not with the vendor session. A decision recorded during a Claude session is available during a Cursor session, linked to the files it concerns, invalidated when git changes those files.
- **Teams share verified memory** across agents, repos, and environments — local dev, CI, staging — with scoped access and governance controls.
- **Temporal truth is native**. Every observation answers: when was this true? Is it still true? What replaced it? Agents stop confidently repeating stale decisions because the memory system itself tracks supersession.
- **Provenance is queryable**. An engineer or auditor can trace any memory back to its source, its derivation chain, and its verification state.

## The Category Neurox Creates

**Agent Memory Control Plane.**

Not a vector store. Not a chat memory feature. Not a retrieval library.

A control plane means Neurox owns the canonical schema for what an agent remembers, the temporal validity of that memory, the provenance of where it came from, the policy for who can access it, and the audit trail of how it changed. The LLM provider, the embedding model, the IDE, the agent framework — those are replaceable components. The memory record is not.

This is analogous to how Git became the system of record for source code, independent of which editor, CI system, or hosting platform you use. Neurox aims to do the same for agent memory: a portable, inspectable, vendor-neutral substrate.

## The Historical Problem

Agents today operate in a paradox: they are increasingly trusted with complex, multi-step work — refactoring codebases, resolving incidents, managing infrastructure — but they have no durable, trustworthy memory of prior work. Every session starts cold. Every decision is re-derived from context windows. When memory does exist, it is opaque, unversioned, and impossible to correct.

This is not a retrieval problem. It is a **correctness** problem. The failure mode is not "the agent forgot." The failure mode is "the agent remembered something stale, conflicting, or unverifiable, and acted on it with full confidence."

## Why Now

Three forces converge:

1. **Agents are doing real work.** Coding agents ship production code. Internal agents handle ops workflows. The stakes of bad memory are no longer hypothetical — they produce real bugs, real regressions, real incidents.

2. **Vendor memory will be commoditized.** OpenAI, Anthropic, Google, and Microsoft will all ship basic persistent memory. That memory will be convenient, locked to their ecosystem, and shallow on provenance. The gap between "memory exists" and "memory is trustworthy" will widen.

3. **Local-first matters more, not less.** As agents touch source code, credentials, internal documentation, and proprietary logic, organizations need memory that stays under their control. Cloud-only memory is a non-starter for the most sensitive and valuable agent work.

## What Neurox Is Not

- **Not another vector database.** Neurox uses embeddings as one retrieval signal among several (FTS5, temporal scoring, file context, graph links). Storage is not the product; correct retrieval with provenance is.
- **Not "human-like memory for AI."** Human memory is a design input, not a market identity. Neurox borrows principles — consolidation, interference management, contextual cues — but rejects the parts that make memory opaque and unreliable. Agents need dependable memory, not biological imitation.
- **Not a general-purpose memory SDK.** Neurox starts with coding agents because that is where the requirements for temporal truth, file-awareness, git integration, and session continuity are most concrete and most painful. Horizontal expansion follows from depth, not breadth.
- **Not vendor-locked infrastructure.** Neurox works with any LLM provider, any embedding model, any agent framework. The value is in the memory record and its properties — not in which model wrote it.

## Thesis

Basic agent memory will become table stakes. Every platform will offer it. The question that remains unanswered — the one that matters for agents doing real, consequential work — is not *whether* agents remember, but whether what they remember is **correct, current, traceable, and under your control**.

Neurox is the answer to that question.

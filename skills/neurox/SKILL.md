---
name: neurox
description: Persistent memory for AI agents — remember preferences, decisions, failures, and learnings across sessions.
---

# Neurox Memory Skill

Neurox is not a note-taking tool. It is the agent's persistent memory.

> Remember first. Investigate second. Act third. Learn before closing.

---

## 1. Session Protocol

Every work session follows this loop:

```
START ─→ RECALL ─→ INVESTIGATE ─→ WORK ─→ LEARN ─→ CLOSE
```

### Start

1. `session_start(title, directory, branch, namespace)`
2. `context(namespace)` — load project memory
3. `context(namespace, files=...)` — load file-linked memory if you know the target files
4. Read returned context before proceeding

### Recall

Before answering, proposing, or editing:

- `recall(query, namespace)` with short keyword queries
- use filters: `observation_type`, `kind`, `files`, `include_stale`
- prefer multiple targeted passes over one vague query

### Investigate

Memory gives priors. Files give truth.

Before making claims about code:
- open the target files
- read nearby tests
- check schema/config when relevant
- search for similar patterns in the codebase
- compare with prior decisions in Neurox

### Work

Act with the full context of memory + investigation.

### Learn

After meaningful work, save durable learnings:
- `save` new knowledge
- `update` refined knowledge
- `invalidate` wrong knowledge

### Close

`session_end` with structured summary:
- `Goal:` what you set out to do
- `Discoveries:` what you learned
- `Accomplished:` what got done
- `Next:` what remains

---

## 2. Three Reflexes

### Reflex A: Remember the user

Before coding, recover how the user works:
- coding style and conventions
- level of autonomy preferred
- testing expectations
- naming preferences
- prior corrections and rejected approaches

When the user states a preference, save it immediately as `observation_type: preference`.

### Reflex B: Remember the project

Before touching code, recover:
- architectural decisions
- conventions and patterns
- gotchas and build requirements
- config and environment details

### Reflex C: Remember past failures

Before implementing, recall:
- prior bugfixes in the same area
- build/deploy gotchas
- rejected solutions
- security-related fixes
- regressions

The goal is not to remember everything. The goal is to not repeat mistakes.

---

## 3. Deep Retrieval

When you need to find something and the first recall is weak:

1. Run 2-3 more passes with alternate keywords
2. Try `observation_type` filters: `preference`, `decision`, `bugfix`, `gotcha`
3. Try `include_stale: true` for older knowledge
4. Search without namespace for cross-project or user-level memory
5. Only say you don't know after all passes fail

```
recall(query="preferred name", observation_type="preference")
recall(query="auth bug duplicate", namespace="myproject", observation_type="bugfix", include_stale=true)
recall(query="fts5 build", observation_type="gotcha")
```

---

## 4. Memory Quality

Quality over volume. Every save should include:

| Field | When |
|-------|------|
| `title` | always |
| `content` | always — use `What/Why/Where/Learned` format |
| `observation_type` | always |
| `kind` | always |
| `tags` | always |
| `namespace` | always |
| `files` | when code-related |
| `confidence` | when you have a clear signal |
| `retention` | when it matters (`durable` vs `operational`) |
| `topic_key` | when the topic may evolve over time |

### Save vs Update vs Invalidate vs Forget

| Action | When |
|--------|------|
| `save` | new durable insight, no matching prior memory |
| `update` | same memory but richer or more precise |
| `invalidate` | existing memory is proven wrong — provide replacement |
| `forget` | memory is no longer useful but not factually wrong |

Rule: prefer `update` over creating duplicates.

---

## 5. Types and Kinds

### Observation types

| Type | Use for |
|------|---------|
| `decision` | architecture, design choices, direction taken |
| `bugfix` | what broke, why, how it was fixed |
| `discovery` | facts learned about the codebase |
| `pattern` | recurring conventions worth reusing |
| `gotcha` | traps, hidden requirements, brittle behavior |
| `config` | environment, setup, toolchain details |
| `preference` | user style, workflow, coding preferences |
| `question` | unresolved issues worth tracking |

### Memory kinds

| Kind | Use for |
|------|---------|
| `semantic` | durable project knowledge, architecture, preferences |
| `procedural` | reusable how-to, recurring bug patterns, processes |
| `episodic` | session-specific events and temporary context |

### Retention

| Retention | Use for |
|-----------|---------|
| `durable` | knowledge that should survive across sessions and may promote to Core |
| `operational` | temporary execution context that should not become permanent |

---

## 6. Tool Reference

| Tool | Purpose |
|------|---------|
| `session_start` | begin work session — always first |
| `context` | load relevant project and file memory |
| `recall` | search memory before acting |
| `save` | persist new durable knowledge |
| `update` | refine existing memory |
| `invalidate` | replace wrong memory with correction |
| `forget` | soft-delete obsolete memory |
| `session_end` | close session with summary — always last |
| `reflect` | synthesize observations into insights |
| `consolidate` | trigger memory consolidation cycle |
| `git_hook` | mark file-linked memories stale after commits |
| `status` | inspect brain layer counts and health |
| `health_check` | get brain power score with recommendations |

---

## Summary

```
┌─ Before working ─────────────────────────────────┐
│  session_start → context → recall                │
│  Remember: user preferences, project decisions,  │
│  past failures, file-linked knowledge            │
├─ While working ──────────────────────────────────┤
│  Investigate files before claiming anything       │
│  save decisions, bugfixes, patterns, preferences │
│  update or invalidate when knowledge evolves     │
├─ After working ──────────────────────────────────┤
│  Save durable learnings                          │
│  session_end with Goal/Discoveries/Done/Next     │
└──────────────────────────────────────────────────┘
```

Neurox works best when the agent thinks with memory, not about memory.

---
name: neurox
description: Gives AI agents persistent, brain-inspired memory across sessions — remembering decisions, patterns, preferences, and discoveries automatically.
---

# Neurox Memory Skill

## What is Neurox?

Neurox is a brain-inspired persistent memory engine for AI coding agents. It stores observations across three memory layers (Buffer → Working → Core), applies Ebbinghaus decay curves to model forgetting, and retrieves relevant context using hybrid FTS5 + semantic + temporal scoring.

**You have access to 15 Neurox MCP tools. Use them proactively — do not wait for the user to ask.**

Neurox remembers decisions, bug fixes, architecture patterns, user preferences, and project discoveries across sessions. Your job is to actively build and use this memory to give the user a better experience every time.

---

## Three Reflexes

These three saves fire automatically, without confirmation. They are the minimum expected behavior.

**Reflex A — User fact or correction**
The user corrects your approach, states a preference, or shares a personal fact.
→ `save(observation_type: "preference", kind: "procedural", retention: "durable")` — no namespace.

**Reflex B — Architectural decision**
Any design, architecture, or technology decision is made during the session.
→ `save(observation_type: "decision", kind: "semantic", namespace: "<project>", retention: "durable")`

**Reflex C — Bug fixed or pattern found**
A bug is resolved → `save(observation_type: "bugfix", kind: "procedural")`.
A codebase convention is confirmed → `save(observation_type: "pattern", kind: "semantic")`.

---

## When to Use Neurox (without being asked)

| Situation | Action |
|-----------|--------|
| Opening a project or starting work | Call `session_start` |
| Before answering technical questions | Call `recall` with keywords |
| At the start of every conversation | Call `context` with the project namespace |
| After making an architectural decision | Call `save` with type `decision` |
| After fixing a bug | Call `save` with type `bugfix` |
| After discovering a codebase pattern | Call `save` with type `pattern` or `discovery` |
| When the user corrects your approach | Call `save` with type `preference` |
| When the user provides a personal fact or preference | Call `save` immediately |
| After completing a work session | Call `session_end` with a summary |
| When you notice stale or wrong information | Call `invalidate` |
| Every 30 minutes automatically (or on demand) | Neurox calls `consolidate` automatically — you can trigger it manually too |

---

## The 15 MCP Tools — When and How

### `session_start`

Call this **at the beginning of every work session**, before doing anything else.

```
session_start(
  title: "Working on JWT auth refactor",
  directory: "/home/user/myproject",
  branch: "feat/jwt-v2",
  namespace: "myproject"
)
```

Returns: session ID + relevant context from previous sessions. Read the returned context before proceeding.

### `session_end`

Call this **when finishing a work session**. Use the Goal/Discoveries/Accomplished/Next format.

```
session_end(
  session_id: "01ABCDEF...",
  summary: "Goal: Refactor JWT auth. Discoveries: RS256 requires key pair generation step. Accomplished: Updated middleware and tests. Next: Update API docs."
)
```

### `save`

Call this **whenever you make a non-trivial decision, fix a bug, discover a pattern, or learn something important**. Use the What/Why/Where/Learned format for content.

```
save(
  title: "Fixed JWT expiration timezone bug",
  content: "What: JWT tokens expired too early. Why: Server clock in UTC but exp claim used local time. Where: api/middleware/auth.go. Learned: Always use time.UTC() for JWT timestamps.",
  observation_type: "bugfix",
  kind: "procedural",
  tags: "jwt,auth,bugfix,timezone",
  files: "api/middleware/auth.go",
  namespace: "myproject"
)
```

Fill **all fields** — incomplete saves are less retrievable. Choose `observation_type` and `kind` carefully (see reference below).

### `recall`

Call this **before answering technical questions, before making changes to familiar code, or when you need to check what was decided before**.

```
recall(
  query: "JWT authentication middleware",
  namespace: "myproject",
  limit: 5
)
```

Use short keyword-style queries, not long natural-language questions. FTS5 matches on actual words in the stored content.

For personal/user questions (name, preferences): search without namespace first, try multiple keyword variants, and use `observation_type: "preference"`.

### `context`

Call this **at the start of every conversation** to get proactive context about the current namespace and files.

```
context(
  namespace: "myproject",
  files: "src/auth.go,src/middleware.go",
  limit: 20
)
```

Use `files` when you know which files you'll be working with — it surfaces file-linked observations automatically.

### `update`

Call this when you need to **correct or expand an existing observation** by ID.

```
update(
  id: "01ABCDEF...",
  title: "JWT auth middleware — updated approach",
  content: "What: Updated to use RS256. Why: Better security than HS256 for distributed systems. Where: api/middleware/auth.go.",
  observation_type: "decision",
  kind: "semantic",
  tags: "jwt,auth,rs256"
)
```

### `forget`

Call this to **soft-delete an observation** that is no longer relevant.

```
forget(id: "01ABCDEF...")
```

The observation won't appear in recall but remains in the database (it can still be queried with `include_stale: true`).

### `invalidate`

Call this when you discover an observation is **factually wrong**. Provide a replacement when possible.

```
invalidate(
  observation_id: "01ABCDEF...",
  reason: "We switched from PostgreSQL to SQLite last month",
  replacement_title: "Database: SQLite with WAL mode",
  replacement_content: "What: Database is SQLite, not PostgreSQL. Why: Switched for single-file portability. Where: internal/db/. Learned: Migration completed 2026-03."
)
```

### `status`

Call this to check the health of the Neurox brain — layer counts, staleness, facts, providers.

```
status()
```

If the response includes `update_available`, inform the user that a newer version of Neurox is ready to install.

### `health_check`

Call this to get the brain power score (0-100%) with per-dimension breakdown and actionable recommendations.

```
health_check(days: 7)
```

If the response includes `update_available`, inform the user that a newer version of Neurox is ready to install.

### `consolidate`

Triggers an immediate consolidation cycle (decay → promote → dedup → contradict → reflect → evict). Neurox runs this automatically every 30 minutes, but you can trigger it manually after a heavy save session.

```
consolidate()
```

### `reflect`

Synthesizes Working-layer observations into high-level insights. Requires an LLM provider to be configured.

```
reflect(namespace: "myproject")
```

### `git_hook`

Report changed files from a git commit. Automatically marks linked observations as stale so they can be re-evaluated.

```
git_hook(
  changed_files: "src/auth.go,src/middleware.go",
  commit_sha: "b04b533",
  branch: "feat/jwt-v2"
)
```

### `backup`

Creates a safe, consistent point-in-time backup of the Neurox database using SQLite's online backup API. Works while the server is running.

```
backup(output: "/home/user/neurox-backup.db")
```

`output` is optional — defaults to `<db_path>.backup`. Call this before destructive operations (mass invalidations, curation runs, or major refactors).

### `curate`

Deep curation: uses an LLM to review a namespace, delete low-value noise, and recalibrate importance weights. Requires a curator LLM provider to be configured.

```
curate(namespace: "myproject", dry_run: true)
```

Always run with `dry_run: true` first to preview what will be removed. Then re-run without it to apply changes.

---

## Observation Types Reference

| Type | When to use | Example title |
|------|-------------|---------------|
| `decision` | Architectural or design choices | "Chose SQLite over PostgreSQL for portability" |
| `bugfix` | What broke and why | "Fixed N+1 query in user list with preload" |
| `discovery` | Learned something about the codebase | "Auth middleware runs before CORS" |
| `pattern` | Recurring conventions you've confirmed | "All stores use constructor injection" |
| `gotcha` | Traps and pitfalls | "Must run migrations before first query" |
| `config` | Environment and tool setup | "CI uses Go 1.26" |
| `preference` | User corrections and stated preferences | "User prefers table-driven tests" |
| `question` | Open questions for human review | "Should this package be split?" |

---

## Memory Kind Reference

| Kind | Decay rate | Use for |
|------|------------|---------|
| `episodic` | Fast (decays in days) | Session notes, temporary context, "what we did today" |
| `semantic` | Moderate (weeks) | Facts about the codebase, architecture, technology choices |
| `procedural` | Slow (months) | How-to knowledge, processes, bug patterns that recur |

When in doubt: bugfixes → `procedural`, facts → `semantic`, session events → `episodic`.

---

## Namespaces

Use the **project directory name** as namespace. For cross-project or user-level preferences, omit it or use `"default"`.

```
/home/user/projects/myapp  →  namespace: "myapp"
```

---

## Concrete Save Examples

### After making an architectural decision:

```
save(
  title: "Chose ULID over UUID for observation IDs",
  content: "What: All observation IDs use ULID format. Why: ULIDs are monotonically sortable and URL-safe, unlike UUIDs. Where: internal/observation/types.go. Learned: oklog/ulid library, use ulid.MustNew(ulid.Now(), rand) pattern.",
  observation_type: "decision",
  kind: "semantic",
  tags: "ulid,uuid,ids,architecture",
  files: "internal/observation/types.go",
  namespace: "myproject",
  confidence: 0.95
)
```

### After fixing a bug:

```
save(
  title: "Fixed missing migration causing test failures",
  content: "What: Tests failed with 'no such table: observations'. Why: Schema migration had not run before tests. Where: internal/db/schema.sql and test helpers. Learned: Always ensure db.Init() runs before any store operation in tests.",
  observation_type: "bugfix",
  kind: "procedural",
  tags: "sqlite,schema,migration,testing",
  files: "internal/db/init.go",
  namespace: "myproject"
)
```

### After discovering a pattern:

```
save(
  title: "Store structs accept *sql.DB and embed no state",
  content: "What: All store types (ObservationStore, FactStore, etc.) take *sql.DB in their constructor and hold no mutable state. Why: Enables safe concurrent use. Where: internal/observation/store.go, internal/facts/store.go. Learned: Pattern is consistent across all internal packages.",
  observation_type: "pattern",
  kind: "semantic",
  tags: "pattern,store,sql,concurrency,architecture",
  files: "internal/observation/store.go",
  namespace: "myproject"
)
```

### After learning a user preference:

```
save(
  title: "User prefers explicit error wrapping with fmt.Errorf",
  content: "What: User corrected me to use fmt.Errorf(\"context: %w\", err) instead of errors.New. Why: Preserves error chain for unwrapping with errors.Is and errors.As. Where: All Go files in the project. Learned: This is a hard requirement, not a suggestion.",
  observation_type: "preference",
  kind: "procedural",
  tags: "go,errors,wrapping,preference",
  namespace: "myproject",
  confidence: 0.95,
  retention: "durable"
)
```

---

## Retention Policy

- `durable` — eligible for promotion to Core layer (permanent long-term storage). Use for decisions, patterns, and important preferences.
- `operational` — stays in Working layer, never promoted to Core. Use for session notes, temporary context, in-progress work.

If omitted, Neurox auto-classifies based on observation type.

---

## Deep-Brain Search Protocol

For questions about the user, prior conversations, or cross-project knowledge:

1. First call `recall` with short keyword variants, no namespace filter
2. If the first result is inconclusive, run 2-3 more passes with alternate keywords
3. Try `observation_type: "preference"` for personal facts
4. Try `include_stale: true` when the information may be old
5. Only say you don't know after all passes still return nothing

---

## Session Workflow Summary

```
┌─ Start of session ──────────────────────────────┐
│  1. session_start(title, directory, namespace)  │
│  2. context(namespace, files)                   │
│  3. Read returned context before proceeding     │
└─────────────────────────────────────────────────┘

┌─ During work ───────────────────────────────────┐
│  Before answering: recall(keywords, namespace)  │
│  After decisions: save(decision)                │
│  After bugs: save(bugfix)                       │
│  After patterns: save(pattern/discovery)        │
│  After user corrections: save(preference)       │
│  On wrong info: invalidate(id, replacement)     │
└─────────────────────────────────────────────────┘

┌─ End of session ────────────────────────────────┐
│  session_end(session_id, Goal/Discoveries/      │
│              Accomplished/Next summary)         │
└─────────────────────────────────────────────────┘
```

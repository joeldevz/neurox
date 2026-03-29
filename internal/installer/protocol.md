<!-- neurox:protocol -->
## Neurox Persistent Memory — Protocol

### 1. Mindset

> *Remember first. Investigate second. Act third. Learn before closing.*

Every memory action follows this sequence. Before touching code, recall what is already known.
Before answering, check if this was already solved. Before closing, save what was learned.
This sequence is the operating principle behind every rule below.

---

### 2. Session Protocol (mandatory)

**At the top of every session — before reading files, before answering:**

```
neurox_session_start(title, directory, branch, namespace)
```

Read the returned context. It contains decisions, patterns, and preferences from prior sessions.
Do not skip this — it is the memory load step that makes all other recall more accurate.

**At the close of every session:**

```
neurox_session_end(session_id, summary)
```

Summary format: `Goal: / Discoveries: / Accomplished: / Next:`

If the session ends abruptly or is interrupted, still call `neurox_session_end` with what was done.

---

### 3. Three Reflexes — Proactive Saves (mandatory)

These fire automatically, without being asked. No confirmation needed.

**Reflex A — User fact or correction**

Trigger: the user corrects your approach, states a preference, or shares a personal fact.

```
neurox_save(
  observation_type: "preference", kind: "procedural",
  retention: "durable"
  // no namespace — personal facts are cross-project
)
```

**Reflex B — Architectural decision**

Trigger: any design, architecture, or technology decision is made.

```
neurox_save(
  observation_type: "decision", kind: "semantic",
  namespace: "<project>", retention: "durable"
)
```

**Reflex C — Bug fixed or pattern found**

Trigger: a bug is resolved → `observation_type: "bugfix"`, `kind: "procedural"`.
Trigger: a codebase pattern or convention is discovered → `observation_type: "pattern"` or `"discovery"`, `kind: "semantic"`.

**Concrete example — decision save:**

```
neurox_save(
  title: "Chose SQLite over PostgreSQL",
  content: "What: SQLite used for DB. Why: Single-file portability. Where: internal/db/. Learned: WAL mode required for concurrency.",
  observation_type: "decision", kind: "semantic",
  tags: "sqlite,database,architecture", namespace: "myproject",
  retention: "durable"
)
```

---

### 4. When to Search Memory

| Situation | Action |
|-----------|--------|
| Start of every conversation | `neurox_context(namespace, files)` — broad proactive load |
| Before answering technical questions | `neurox_recall(keywords, namespace)` |
| Before modifying familiar code | `neurox_recall(keywords, files: "path/to/file")` |
| Info seems wrong or outdated | `neurox_invalidate(id, reason, replacement_content)` |

Use short keyword queries, not long sentences. `neurox_recall("sqlite wal mode")` beats
`neurox_recall("what did we decide about the database configuration last week")`.

---

### 5. Deep Retrieval — When the First Pass Fails

If `neurox_recall` returns nothing useful, do NOT give up. Run the full deep-brain search:

1. **2-3 more passes** with alternate keyword variants — try synonyms, shorter terms, related concepts
2. **Try `observation_type: "preference"`** — for personal facts about the user (name, preferred tools, workflow habits)
3. **Try `include_stale: true`** — the information may be old but still correct
4. **Search without namespace** — the answer may live in cross-project or default memory

Only say you don't know after all four passes fail. Personal memory questions always require
the full deep-brain search before concluding no information exists.

---

### 6. Content Format

Every `neurox_save` content must follow this four-part format:

```
What: [what was done or discovered]
Why: [reason or motivation]
Where: [files, packages, or system areas affected]
Learned: [the key takeaway — what to remember next time]
```

Always fill these fields: `title`, `content`, `observation_type`, `kind`, `tags`, `namespace`.
Link source files with `files` when a memory is tied to specific code.

**Retention hints:**

- `retention: "durable"` — decisions, patterns, preferences, bugs (eligible for Core layer promotion)
- `retention: "operational"` — session notes, in-progress work, temporary context (never promoted)

**What NOT to save:**

- Trivial changes (typos, formatting tweaks)
- Information already in git history
- Temporary debugging steps with no lasting insight

---

### 7. Tool Reference

| Tool | When to call | Key params |
|------|-------------|------------|
| `neurox_session_start` | Top of every session, before any other action | `title`, `directory`, `branch`, `namespace` |
| `neurox_session_end` | Close of every session | `session_id`, `summary` (Goal/Discoveries/Accomplished/Next) |
| `neurox_save` | After any reflex trigger (preference, decision, bugfix, pattern) | `title`, `content`, `observation_type`, `kind`, `tags`, `namespace` |
| `neurox_recall` | Before answering technical questions; before editing familiar code | `query`, `namespace`, `files`, `observation_type`, `include_stale` |
| `neurox_context` | At session start; broad proactive memory load | `namespace`, `files`, `limit` |
| `neurox_update` | When an existing observation needs correction or enrichment | `id`, `title`, `content` |
| `neurox_forget` | Soft-delete noise or duplicate observations | `id` |
| `neurox_invalidate` | When stored information is wrong — marks stale, creates replacement | `observation_id`, `reason`, `replacement_content` |
| `neurox_status` | Check brain layer counts, staleness, LLM provider status | — |
| `neurox_health_check` | Diagnose how much of Neurox's potential is being used (0-100%) | `days` |
| `neurox_consolidate` | Force decay, promotion, dedup, contradiction detection, reflection | — |
| `neurox_reflect` | Synthesize related observations into high-level insights (needs LLM) | `namespace` |
| `neurox_git_hook` | Called by post-commit hook — marks file-linked observations stale | `changed_files`, `commit_sha`, `branch` |
| `neurox_backup` | Create a safe point-in-time backup of the database | `output` |
| `neurox_curate` | Deep curation: delete noise, recalibrate weights (needs curator LLM) | `namespace`, `dry_run` |

**Namespace rule:** Use the project directory name as namespace (e.g., `myproject`).
Personal preferences and cross-project facts: omit namespace or use `"default"`.
<!-- /neurox:protocol -->

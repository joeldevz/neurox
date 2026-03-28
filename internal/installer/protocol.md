## Neurox Persistent Memory — Protocol

You have access to Neurox MCP tools for persistent memory across sessions.
Use them **proactively** — do not wait for the user to ask.

### Session Protocol (mandatory)

- **Start of session**: Call `neurox_session_start` with title, directory, branch, and namespace (use project directory name).
- **Read returned context** before proceeding — it contains decisions, patterns, and preferences from prior sessions.
- **End of session**: Call `neurox_session_end` with a summary in Goal/Discoveries/Accomplished/Next format.

### Proactive Save Triggers (mandatory)

Call `neurox_save` **immediately** after any of these events:

| Event | observation_type | kind |
|-------|-----------------|------|
| Architecture or design decision made | `decision` | `semantic` |
| Bug fix completed (include root cause) | `bugfix` | `procedural` |
| Codebase pattern or convention discovered | `pattern` / `discovery` | `semantic` |
| User corrects your approach or states a preference | `preference` | `procedural` |
| Environment or tool configuration learned | `config` | `semantic` |
| Trap or pitfall encountered | `gotcha` | `procedural` |

**Content format**: `What: / Why: / Where: / Learned:`
**Always fill**: title, content, observation_type, kind, tags, files, namespace.

### When to Search Memory

1. **Start of every conversation**: Call `neurox_context` with the project namespace.
2. **Before answering technical questions**: Call `neurox_recall` with short keyword queries.
3. **Before modifying familiar code**: Call `neurox_recall` filtered by files.
4. **When information seems stale or wrong**: Call `neurox_invalidate` with a replacement.

### What NOT to Save

- Trivial changes (typos, formatting)
- Information already in git history
- Temporary debugging steps

### Namespace Convention

Use the project directory name as namespace (e.g., `myproject`).
For cross-project or personal preferences, omit namespace or use `"default"`.

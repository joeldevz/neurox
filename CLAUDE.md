# Neurox — Claude Code Configuration

## Project

Neurox is a brain-inspired memory engine for AI coding agents, written in Go. It implements a three-layer memory model (Buffer → Working → Core) with hybrid search (FTS5 + semantic), Ebbinghaus decay curves, and consolidation pipelines.

**Stack**: Go, SQLite 3 (WAL mode, FTS5, CGO via mattn/go-sqlite3), ULID (oklog/ulid), YAML config (gopkg.in/yaml.v3).

## Agent Roles

### Planner

**Role**: Interactive planning specialist. Turns task requests into concrete, execution-ready `PLAN.md`.

**Rules**:
1. Investigate before asking. Read the codebase first — go.mod, folder structure, existing packages, similar modules, tests, docs. Do not ask the user for information you can learn from the repository.
2. Ask in thematic blocks. 2-4 related questions at a time, not one giant list.
3. Cover both business and technical dimensions.
4. Recommend defaults when the user hasn't decided something important.
5. Confirm understanding before writing PLAN.md.
6. Avoid over-planning.

**Discovery Checklist** (run before asking questions):
1. Read `CONVENTIONS.md` if it exists
2. Read `go.mod` for dependencies and Go version
3. Read `PLAN.md` if it exists for current progress
4. Glob for packages similar to the requested feature
5. Read 1-2 existing tests for testing patterns
6. Read `internal/db/schema.sql` for the database schema
7. Read existing types, interfaces, or schemas near the area of change

**Question Flow**: BUSINESS → PRODUCT/OPERATIONS → TECHNICAL → DELIVERY

**Plan Output**:
```markdown
# Plan: [Task Title]

## Goal
## Business Context
## Technical Context

## Implementation Steps

### Step 1: [Short title]
- **What**: [Concrete unit of work]
- **Why**: [Purpose]
- **Where**: [Files/packages affected]
- **Acceptance**: [Observable completion criteria]
- **Status**: [ ] pending

## Verification
## Risks / Notes
```

**Step Quality**: Each step independently implementable and reviewable, dependency order, 3-8 steps for most tasks, explicit testable acceptance.

**Plan Templates** (adapt, don't follow rigidly):
- Package: new Go package (types, store, logic, tests)
- Bugfix: reproduce RED → fix GREEN → refactor
- Integration: interface, adapter, handlers, tests
- Refactor: safety net tests first, then incremental changes
- Feature: general features

### Manager

**Role**: Execution orchestrator. Reads PLAN.md, delegates one step at a time to coder, enforces human review loop.

**Rules**:
1. `PLAN.md` is law. Read it first on every run.
2. One step at a time. Never bundle multiple steps.
3. Delegate all code changes to coder. Manager may read/update PLAN.md but not application code.
4. Human review is mandatory after every implementation pass.
5. No auto-advance. Only mark done after explicit human approval.
6. Keep state visible. Update PLAN.md statuses.

**Status Model**: `[ ] pending` → `[~] in progress` → `[x] done` / `[!] needs fixes`

**Execution Loop**:
1. **READ AND SELECT**: Find next step ([!] before [ ]), update to [~]
2. **DELEGATE**: Launch coder with step title, what/why/where/acceptance, prior context, verification
3. **REPORT TO HUMAN**: Summarize step, files changed, verification result, risks. Request review. STOP.
4. **HANDLE FEEDBACK**: approved → [x] done; fixes needed → [!] needs fixes → delegate fixes → report again

**Escalation**: If same step fails repeatedly, explain blocker and ask human.

### Coder

**Role**: Implementation worker. Executes one bounded task to production quality.

**Rules**:
1. Read before writing. Inspect nearby code, packages, types, interfaces, tests, naming conventions.
2. Follow local conventions first. Match existing patterns.
3. Stay bounded. Only the requested step.
4. Keep types explicit. No interface{}/any unless truly necessary. Handle errors explicitly.
5. Prefer clean architecture. Reuse helpers, separate responsibilities, avoid duplication.

**Go Expectations**:
- Accept interfaces, return structs
- Error wrapping: `fmt.Errorf("context: %w", err)`
- Table-driven tests
- Focused cohesive packages
- Use existing patterns (Store structs for DB access, embedded SQL schemas)
- Respect internal/ boundary
- Use vendor/ — run `go mod vendor` after adding deps
- SQLite: prepared statements, proper transactions
- Test files co-located as `*_test.go`

**Self-Verification** (mandatory before returning success):
```bash
go build ./...
go vet ./...
go test ./...          # or targeted test runs
go test -race ./...    # for concurrency-sensitive code
```

Up to 3 autonomous fix attempts before returning failure.

**Result Contract**: Status, Files modified, Verification output, Notes.

## Commands

### /plan [task]
Agent: planner. Discovery → questions → confirm → write PLAN.md.

### /execute
Agent: manager. Read PLAN.md → delegate next step to coder → report → wait for review.

### /apply-feedback [feedback]
Agent: manager. Apply human corrections to current step. Delegate fixes to coder. Report. Wait for review.

### /status
Agent: manager. Read PLAN.md, report completed/in-progress/pending steps and next action.

### /diff
Agent: manager. Show annotated diff of current step changes with file summaries.

### /review
Agent: manager. Quality gate — check conventions, types, architecture, tests, imports. Run `go build`, `go vet`, `go test`. Report issues. Do NOT fix code.

### /test [target]
Agent: coder. Generate or run tests. Follow existing table-driven test patterns. Use in-memory SQLite for DB tests.

### /commit
Agent: manager. Create conventional commit: `<type>(<scope>): <description>`. Max 72 chars, imperative present tense, no period.

### /pr
Agent: manager. Create PR via `gh pr create` with Summary/Changes/Testing/Notes sections.

### /onboard
Agent: planner. Read-only codebase scan — go.mod, folder structure, schema, tests, config. Return summary.

### /plan-rewrite
Agent: planner. Review and improve existing PLAN.md. Validate against current codebase.

### /estimate
Agent: planner. T-shirt size estimate per step (XS ~5m, S ~15m, M ~30m, L ~1h, XL ~2h+). Table format with risks.

### /rollback [step]
Agent: manager. Undo last step's changes. ALWAYS ask confirmation first. Targeted file restores only, no `git reset --hard`.

### /docs [library/topic]
Agent: coder. Fetch live documentation via Context7 MCP. Focus on practical usage.

### /context [observation]
Agent: planner. Save discoveries/decisions to Engram persistent memory. Use `mem_search` to avoid duplicates.

## Commit Conventions

### Format: Conventional Commits
```
<type>(<scope>): <description>
```

### Types
| Type | When |
|---|---|
| `feat` | New functionality |
| `fix` | Bug fix |
| `refactor` | Code change without adding features or fixing bugs |
| `test` | Add or modify tests |
| `docs` | Documentation changes |
| `chore` | Maintenance (deps, config, scripts) |
| `style` | Formatting (no logic change) |
| `perf` | Performance improvement |
| `ci` | CI/CD changes |

### Scope
Use affected package name: `feat(recall): add tri-factor scoring`, `fix(observation): handle nil topic key`

### Rules
- First line: max 72 characters
- Imperative present tense in English: "add", "fix", "remove"
- No trailing period
- Add body after blank line if change needs explanation

### PR Body
```markdown
## Summary
- [1-3 bullets]

## Changes
- [significant changes]

## Testing
- [tests added/modified, manual verification]

## Notes
- [decisions, trade-offs, review notes]
```

## Plan Templates

### Bugfix
```markdown
# Plan: [Fix: bug description]
## Steps:
1. Reproduce bug (write failing test — RED)
2. Apply fix (test passes — GREEN)
3. Refactor if needed (clean up)
## Verification: go test ./..., go vet ./..., go build ./...
```

### New Package
```markdown
# Plan: [Package name]
## Steps:
1. Types and interfaces
2. Core logic / store implementation
3. Unit tests (table-driven)
4. Integration with existing packages
5. Verification
## Verification: go test ./..., go vet ./..., go build ./...
```

### Integration
```markdown
# Plan: [Integration with X]
## Steps:
1. Interface definition
2. DTOs / types for external data
3. Adapter implementation
4. Command/query handlers
5. Entry point (MCP tool, HTTP endpoint, CLI)
6. Tests
7. Wire up
## Verification: go test ./..., go vet ./..., go build ./...
```

### Refactor
```markdown
# Plan: [Refactor: description]
## Steps:
1. Ensure test coverage for affected code (safety net)
2. [First concrete improvement]
3. [Second concrete improvement]
N. Final verification — all tests pass, no behavior change
## Verification: go test ./..., go vet ./..., go build ./...
```

### Feature
```markdown
# Plan: [Feature name]
## Steps:
1. Domain types / models
2. Contracts (interfaces)
3. Core logic / handlers
4. Infrastructure (persistence, adapters)
5. Entry point (MCP tool, HTTP endpoint, CLI)
6. Tests
7. Wire up and final verification
## Verification: go test ./..., go vet ./..., go build ./...
```

# Plan: Documentation Sync — Align READMEs, concepts, and memory with current codebase

## Goal

Bring all user-facing documentation (README.md, README.es.md, docs/concepts.md) up to date with features, commands, tools, API routes, packages, and configuration that have been added since the docs were last touched. Also fix the stale version constant in `server.go` and invalidate outdated Neurox memory observations.

## Business Context

- **Problem**: The README documents 12 MCP tools (actual: 14), 11 CLI commands (actual: 16), and 15 internal packages (actual: 26). Three REST endpoints, the full `curator` and `consolidation` config sections, and several env vars are missing entirely. New major features (Brain Benchmark, Deep Curation) have no explanatory sections.
- **Users**: Anyone evaluating or onboarding to Neurox — developers, agent authors, and contributors.
- **Outcome**: A single source of truth in both languages, no undocumented features, and clean memory state.
- **Non-goals**: No code changes beyond the version fix. No new features.

## Technical Context

### Gaps identified (auditoría session 2026-03-28)

| Area | Documented | Actual | Delta |
|------|-----------|--------|-------|
| MCP Tools | 12 | 14 | +`health_check`, +`curate` |
| CLI Commands | 11 | 16 | +`curate`, `reembed`, `export`, `import`, `benchmark` |
| REST Endpoints | 14 | 17 | +`GET /api/v1/health-check`, `GET /api/v1/decay-timeline`, `GET /api/v1/stats/activity` |
| Internal Packages | 15 | 26 | +`curate/`, `health/`, `telemetry/`, `export/`, `benchmark/`, `classify/`, `installer/` |
| Config sections | 3 | 5 | +`curator`, `consolidation` |
| Env vars | 14 | 19+ | +`NEUROX_CURATOR_*` (5 vars) |
| Concepts (docs/concepts.md) | 6 | 9+ | +curation, brain benchmark, activation signals |

### Version mismatch
- `internal/mcp/server.go` line 10: `ServerVersion = "0.1.0"` should be `"0.1.16"` to match `main.go`

### Stale Neurox observations
- `01KM60RSD27Y1Y4Y54ACTVQAEW` — Project Structure says 20 packages, 11 commands, 12 tools (all wrong now)
- `01KM9MFARDCBJCMD95H25V2K78` — Bug: embed.Queue.Enqueue never called (fixed: `main.go` L630-635 wires it)

## Implementation Steps

### Step 1: Fix version constant in server.go
- **What**: Change `ServerVersion = "0.1.0"` → `"0.1.16"` in `internal/mcp/server.go` line 10
- **Why**: MCP clients see this version. It should match the binary version in `main.go`.
- **Where**: `internal/mcp/server.go`
- **Acceptance**:
  - `grep 'ServerVersion' internal/mcp/server.go` shows `"0.1.16"`
  - `CGO_ENABLED=1 go build -tags fts5 ./...` passes
- **Status**: [x] done (already at 0.1.16)

### Step 2: Update README.md — MCP Tools section

- **What**: Add `health_check` and `curate` to both the MCP Tools table and the MCP Tool Inputs table. Update the count reference from 12 → 14 if mentioned.
- **Why**: These tools exist in production and are registered in `server.go` but invisible to users reading the README.
- **Where**: `README.md` — sections "MCP Tools" and "MCP Tool Inputs"
- **Acceptance**:
  - MCP Tools table has 14 rows
  - MCP Tool Inputs table has 14 rows
  - `health_check` entry: `days` input
  - `curate` entry: `namespace`, `dry_run` inputs
  - No other section is modified
- **Status**: [x] done

### Step 3: Update README.md — CLI Reference section
- **What**: Add 5 missing commands to the CLI Reference table and update the CLI section code example block:
  - `curate` — Deep curation with LLM, flags: `--namespace`, `-n`, `--dry-run`
  - `reembed` — Re-embed all observations, no flags
  - `export` — Export as Markdown, flags: `--format`, `--output`, `--namespace`
  - `import` — Import .md files, flags: `--source`
  - `benchmark` — Brain benchmark suite, flags: `--scale`, `--category`, `--dimensions`, `--output`, `--output-html`, `--verbose`
  - Also add `update` as a listed command (even though implementation says "not yet implemented", the subcommand exists with `--yes` flag)
- **Why**: Users can't discover these features if they're not in the reference.
- **Where**: `README.md` — sections "CLI", "CLI Reference", "CLI Notes"
- **Acceptance**:
  - CLI Reference table has all 17 commands (mcp, serve, save, recall, context, invalidate, status, consolidate, curate, reembed, graph, benchmark, export, import, install, install-hook, config, update)
  - CLI code block includes example of `export`, `import`, and `benchmark`
  - CLI Notes updated with notes about curate, export, import, benchmark output
- **Status**: [x] done

### Step 4: Update README.md — REST API section
- **What**: Add 3 missing endpoints to the REST API listing and query params table:
  - `GET /api/v1/health-check` — Brain power score and dimension breakdown
  - `GET /api/v1/decay-timeline` — Average importance by layer per day
  - `GET /api/v1/stats/activity` — Tool call activity per day
- **Why**: HTTP clients need a complete API reference.
- **Where**: `README.md` — sections "REST API" and "REST Query Parameters"
- **Acceptance**:
  - REST route listing has all 17 routes
  - Query params table includes `health-check`, `decay-timeline`, `stats/activity`
  - `stats/activity` documents `?days=N` parameter
- **Status**: [x] done

### Step 5: Update README.md — Architecture, Config, Graceful Degradation, and new feature sections
- **What**: Multiple updates in the same file:
  1. **Architecture tree**: Add 7 missing packages (`curate/`, `health/`, `telemetry/`, `export/`, `benchmark/`, `classify/`, `installer/`) with one-line descriptions
  2. **Configuration**: Add `curator` and `consolidation` YAML blocks with field descriptions. Add 5 `NEUROX_CURATOR_*` env vars to the overrides table.
  3. **Graceful Degradation table**: Add row for `+ Curator LLM (remote)` → "Deep curation, higher-quality reflections"
  4. **New section "Deep Curation"** (after Knowledge Graph): Explain the curator concept — uses a large model (e.g. Gemini Flash) to review entire namespaces, delete noise, recalibrate importance weights. Available via `neurox curate` CLI and `curate` MCP tool. Supports dry-run mode and priorities.yaml for domain-specific weighting.
  5. **New section "Brain Benchmark"** (after Benchmark Results / LongMemEval): Explain the self-contained benchmark suite — 12 dimensions across 3 categories (Cognitive 45%, Performance 20%, Agent Simulation 35%), 3 scale tiers (small/medium/large = 1k/10k/100k observations), letter grading S/A/B/C/D/F, JSON and HTML report output. Available via `neurox benchmark`.
- **Why**: Major features like curation and benchmarking are completely undocumented. The architecture tree and config are stale.
- **Where**: `README.md` — multiple sections
- **Acceptance**:
  - Architecture tree lists all 26 packages alphabetically within `internal/`
  - Config YAML example includes all 5 sections (database, llm, embeddings, curator, consolidation)
  - Env vars table includes all `NEUROX_CURATOR_*` vars
  - Graceful Degradation table has 5 rows (nothing, +embeddings, +LLM, +remote API, +curator)
  - "Deep Curation" section exists with concept + CLI/MCP examples
  - "Brain Benchmark" section exists with dimensions table + example command
- **Status**: [x] done

### Step 6: Sync README.es.md with all README.md changes
- **What**: Apply the same structural changes from Steps 2-5 to the Spanish README, translating all new content.
- **Why**: Both READMEs must be identical in coverage. Spanish-speaking users get the same information.
- **Where**: `README.es.md`
- **Acceptance**:
  - Every table, section, and entry added to README.md exists in README.es.md
  - MCP Tools table: 14 rows
  - CLI Reference table: all commands
  - REST API: all 17 routes
  - Architecture tree: 26 packages
  - Config: includes curator and consolidation
  - New sections "Curacion Profunda" and "Brain Benchmark" present
  - Spanish prose is natural, not machine-translated
- **Status**: [x] done

### Step 7: Update docs/concepts.md — add 3 new concepts
- **What**: Add three new concept entries following the existing format (Definition / In practice / In Neurox):
  1. **Deep Curation** — what it is, why bulk review by a large model matters, how it works in Neurox (curator provider, priorities.yaml, dry-run, namespace scope, MCP tool + CLI)
  2. **Brain Benchmark** — what it measures, the 12 dimensions in 3 categories, scoring tiers (Base/Target/Elite/Beyond → 0-100 → letter grade S-F), scale configs, how to run it
  3. **Activation Signals** — what `activation_level` and `consolidation_strength` are, why importance alone is insufficient for memory-like retention, how they interact with decay/promotion/recall
- **Why**: These are core concepts that affect how users understand and configure the system.
- **Where**: `docs/concepts.md`
- **Acceptance**:
  - Three new sections with Definition / In practice / In Neurox structure
  - Each section references the relevant CLI commands, MCP tools, or config fields
  - "Further Reading" section updated if needed
- **Status**: [x] done

### Step 8: Invalidate stale Neurox observations and save updated project structure
- **What**:
  1. `invalidate` observation `01KM60RSD27Y1Y4Y54ACTVQAEW` (Project Structure) with reason "outdated counts" and create a replacement with current accurate counts (26 packages, 16 commands, 14 MCP tools, etc.)
  2. `invalidate` observation `01KM9MFARDCBJCMD95H25V2K78` (embed.Queue bug) with reason "bug was fixed — Enqueue is now wired in main.go saveQueue.OnPostSave"
  3. `save` a new observation documenting this documentation sync (what was updated, where)
- **Why**: Stale memories cause agents to give incorrect information about the project.
- **Where**: Neurox memory (namespace: neurox)
- **Acceptance**:
  - Old project structure observation is stale with replacement
  - Embed bug observation is invalidated
  - New observation captures the doc sync work
- **Status**: [!] partial — invalidations blocked by FTS5 error in running Neurox MCP binary. New observation saved OK (ID: 01KMT8QBA2GEBX192805JJBCH2). Rebuild binary with `CGO_ENABLED=1 go build -tags fts5 -o neurox .` and retry invalidations.

## Verification

```bash
# Build verification (ensures server.go change compiles)
CGO_ENABLED=1 go build -tags fts5 ./...
CGO_ENABLED=1 go vet -tags fts5 ./...

# Structural checks
grep -c '|' README.md    # sanity: table row counts increased
grep 'health_check' README.md README.es.md  # both mention health_check
grep 'curate' README.md README.es.md        # both mention curate
grep 'benchmark' README.md README.es.md     # both mention benchmark
grep 'reembed' README.md README.es.md       # both mention reembed
grep 'export' README.md README.es.md        # both mention export
grep 'curator' README.md README.es.md       # both mention curator config
grep 'Deep Curation' docs/concepts.md       # concept exists
grep 'Brain Benchmark' docs/concepts.md     # concept exists
grep 'Activation' docs/concepts.md          # concept exists
```

Manual: diff README.md and README.es.md section headers to confirm parity.

## Risks / Notes

- **README.es.md must stay natural Spanish** — not a mechanical translation. The coder should write it as if a Spanish-speaking developer authored it.
- **No code changes except `server.go` version** — this is purely a docs task. The coder must not refactor any Go code.
- **`update` command**: exists in code but prints "not yet implemented". Document it honestly with a note that it's coming.
- **Consolidation config**: the `buffer_to_working_threshold`, `working_to_core_threshold`, `core_recalibration_base`, `core_recalibration_type_bonus` fields exist in the struct but are advanced tuning — document them briefly or note they have sensible defaults.
- **Order of sections in README**: new sections (Deep Curation, Brain Benchmark) should go in logical order — Deep Curation after Knowledge Graph, Brain Benchmark after LongMemEval results.

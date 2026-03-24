# Plan: Fill the 5 Market Gaps — Neurox as the Definitive Memory Engine for Coding Agents

## Goal

Execute five strategic initiatives that together position Neurox as the category-defining tool for AI coding agent memory. Each gap is an opportunity no competitor has fully claimed. Most of the technical foundation already exists — this plan surfaces it, completes what's missing, and puts it in front of developers.

## Business Context

**What we already have (do not rebuild):**
- `neurox benchmark` — full 12-dimension cognitive+perf+agent benchmark suite (all steps done)
- `GET /api/v1/health-check` — brain power score with dimensions and recommendations
- `GET /api/v1/graph` — interactive vis-network graph visualization
- `GET /` — dashboard HTML (928-line dashboard.html already embedded)
- `GET /api/v1/stats/breakdown` — layer/type/kind/staleness breakdown
- `GET /api/v1/observations/browse` — filterable observation browser
- `internal/health/` — 561-line health check engine with telemetry dimensions
- `internal/graph/render.go` — 510-line graph renderer

**The 5 gaps to fill:**

| Gap | What it means for Neurox | Build vs. Already exists |
|-----|--------------------------|--------------------------|
| 🏆 Benchmark for forgetting | Public the benchmark suite as THE standard for agent memory evaluation | Exists internally — needs public blog post + shareable output |
| 📰 Newsletter / content voice | Own "memory for coding agents" as editorial topic | 0% built — content strategy only |
| 🎯 Coding agents niche | Make Neurox the go-to for Claude Code, Cursor, VS Code | Partial — needs per-client quickstart docs + Smithery listing |
| 📊 Observability dashboard | Let devs see how their agent's memory is doing | ~60% built — dashboard.html + health-check + graph — needs integration |
| 🏛️ Technical vocabulary | Define what "good memory" means — decay, layers, consolidation | 0% built — needs blog/paper-style writeup |

**Target users:** Developers using Claude Code, Cursor, or VS Code who want their AI coding agent to remember decisions, preferences, and project context across sessions.

## Technical Context

**Stack:** Go 1.23, SQLite WAL+FTS5, mark3labs/mcp-go, HTTP server on port 7438.

**Key existing packages:**
- `internal/benchmark/` — benchmark suite with `neurox benchmark --scale small|medium|large --category cognitive|performance|agent`
- `internal/health/health.go` — `Check()` returns `Report{Score, Grade, Dimensions, TopActions}`
- `internal/api/handlers.go` — `handleHealthCheck`, `handleGraph`, `handleBreakdown`, `handleBrowse`
- `internal/api/dashboard.html` — 928-line embedded HTML dashboard (already has status + browse)
- `internal/graph/render.go` — HTML graph with vis-network, filters, detail panel

**What's missing:**
- `GET /.well-known/mcp/server-card.json` — required for Smithery listing
- Per-client quickstart docs (Claude Desktop, Cursor, VS Code, Claude Code)
- Dashboard tab for health-check (brain power score + decay visualization)
- `neurox benchmark --output report.html` shareable HTML report
- `neurox export --format=markdown` for Markdown/Obsidian compatibility
- BFS graph traversal on `observation_links` (closes gap vs. Graphiti)
- Blog post: "The First Benchmark That Measures Forgetting"
- Skills SKILL.md file for agent installation

---

## Implementation Steps

### Step 1: Smithery + MCP Registry listing — distribution in <1 week
- **What**:
  1. Add `GET /.well-known/mcp/server-card.json` route to `internal/api/server.go`. Returns static JSON with all 13 MCP tools, their inputSchema, and server metadata. No logic needed — pure static JSON.
  2. Add `GET /.well-known/mcp/server-card.json` to `internal/mcp/` for the stdio server too (via HTTP wrapper if needed, or as a note in docs that it's only available when `neurox serve` is running).
  3. Write `smithery-listing.md` (internal doc) with the exact steps to submit to Smithery, Glama, MCP.so, and the official MCP registry. Include the server card URL, description, and category selection for each registry.
  4. Register on Smithery at `smithery.ai/servers/new` with the public HTTP URL.
  5. Submit to Glama at `glama.ai/mcp/servers` (button "Add Server").
  6. Submit to `mcp.so` via their submit flow.
- **Why**: Smithery is where Claude/Cursor users discover MCP servers. Friction score drops from 7 (manual JSON edit) to 2 (one-click). This is the highest-leverage distribution action available — takes days, not weeks.
- **Where**: `internal/api/server.go`, `internal/api/handlers.go` (new `handleServerCard`), docs/
- **Acceptance**:
  - `curl http://localhost:7438/.well-known/mcp/server-card.json` returns valid JSON with all 13 tools
  - JSON validates against Smithery's server card schema
  - Neurox appears in Smithery search for "memory coding agent"
  - Listed in Glama and MCP.so
- **Status**: [ ] pending

### Step 2: Per-client quickstart docs — eliminate setup abandonment
- **What**:
  1. Add `docs/` directory to the repo with these files (Markdown, linked from README):
     - `docs/claude-desktop.md` — step-by-step with screenshots placeholders, exact JSON config, verification prompt
     - `docs/cursor.md` — Settings > Developer > Edit Config, JSON snippet, verify with "what tools do you have?"
     - `docs/vscode.md` — `.vscode/mcp.json` format + `settings.json` approach
     - `docs/claude-code.md` — `~/.claude.json` config, the format Neurox already uses
     - `docs/opencode.md` — existing OpenCode config from README, cleaned up
  2. Each doc has exactly 3 sections: **Install** (one command), **Configure** (copy-paste JSON), **Verify** (one test prompt).
  3. Update `README.md` to link to each client doc with a "Quick Setup" table at the top.
  4. Add a `SKILL.md` file in the repo root (or `skills/neurox/SKILL.md`) that teaches agents when and how to use Neurox tools proactively — compatible with Basic Memory's `npx skills add` pattern and Claude Code's skills system.
- **Why**: The #1 cause of abandonment in competing tools is setup failure. Per-client docs with exact copy-paste config reduce this to near zero. The skills file teaches agents to use Neurox proactively (session_start on project open, save after key decisions, recall before answering questions).
- **Where**: `docs/`, `README.md`, `skills/neurox/SKILL.md`
- **Acceptance**:
  - Each client doc is < 300 words and has a working copy-paste JSON config block
  - Following the Claude Desktop doc results in Neurox tools appearing in <2 minutes
  - SKILL.md covers: when to call `session_start`, `save`, `recall`, `context`, `session_end`
  - README Quick Setup table links to all 5 client docs
- **Status**: [x] done

### Step 3: Observability dashboard — complete the brain health UI
- **What**: The health-check engine (`internal/health/health.go`), graph visualization (`internal/graph/`), and browse API all exist. What's missing is a unified dashboard that surfaces them as a coherent "Memory Health" view.

  1. **Expand `dashboard.html`** (currently 928 lines) with a new "Brain Health" tab that:
     - Calls `GET /api/v1/health-check` and renders the `Report` — overall score (0-100), letter grade, per-dimension bars with status icons (✓/⚠/✗), and top 3 recommendations
     - Shows a **decay timeline**: a chart (Chart.js via CDN) plotting average importance by layer (Buffer/Working/Core) over the last 30 days using `GET /api/v1/stats/breakdown`
     - Shows a **memory layer funnel**: Buffer → Working → Core counts with arrow flow (how many get promoted vs. evicted)
     - Shows **staleness heatmap**: observations colored by staleness (fresh/stale/revalidated/expired) across types

  2. **Add `GET /api/v1/decay-timeline`** endpoint in `handlers.go`:
     - Queries: `SELECT DATE(created_at) as day, layer, AVG(importance) FROM observations WHERE deleted_at IS NULL AND created_at >= datetime('now', '-30 days') GROUP BY day, layer ORDER BY day`
     - Returns JSON `{days: [...], buffer: [...], working: [...], core: [...]}` for the chart

  3. **Link the graph** from the dashboard: "View Knowledge Graph" button opens `GET /api/v1/graph` in a new tab (already built).

  4. **No new packages** — all logic lives in the existing API handler + dashboard HTML. Chart.js loaded via CDN (same pattern as vis-network in the graph page).

- **Why**: This closes Gap 4 (Observability Dashboard) which no competitor has. Devs can answer "what does my agent remember?", "what's being forgotten?", "is my memory healthy?" — visually, in a browser. It's also screenshot-able for blog posts and demos, which directly supports the community strategy.
- **Where**: `internal/api/dashboard.html`, `internal/api/handlers.go` (new `handleDecayTimeline`)
- **Acceptance**:
  - `neurox serve` → open browser → Brain Health tab visible
  - Health score renders with grade and per-dimension breakdown
  - Decay timeline chart shows 30 days of importance evolution across layers
  - Layer funnel shows Buffer/Working/Core counts with promotion stats
  - All data loads from existing API endpoints (no mocks)
  - Works without LLM configured (gracefully shows limited score with explanation)
- **Status**: [ ] pending

### Step 4: Shareable benchmark report + Markdown export — close the technical gaps
- **What**:

  **Part A — Shareable benchmark HTML report:**
  1. Add `--output-html report.html` flag to `neurox benchmark` CLI (`internal/benchmark/cli.go`)
  2. `internal/benchmark/report.go` generates a self-contained HTML file alongside the existing lipgloss terminal output:
     - Radar chart (Chart.js) showing all 12 dimensions
     - Per-dimension accordion with scenario details and pass/fail breakdown
     - Comparison table: Neurox scores vs. published competitor claims (mem0 LOCOMO 49.3%, Zep 51.6%, long-context baseline 84.6%) — with a note that methodology differs
     - Shareable: single HTML file, no external dependencies
  3. This HTML output is what goes into the blog post "The First Benchmark That Measures Forgetting"

  **Part B — Markdown export/import:**
  1. Add `neurox export --format=markdown --output=./brain-export/` CLI command in `main.go`
  2. New `internal/export/` package with `ExportMarkdown(ctx, db, namespace, outputDir)`:
     - Each observation → one `.md` file with YAML frontmatter (`id`, `type`, `layer`, `importance`, `tags`, `valid_from`, `namespace`) + content body
     - WikiLinks for `observation_links`: `- supersedes [[<title>]]`, `- relates_to [[<title>]]`
     - Compatible with Obsidian vault structure
  3. Add `neurox import --source=./brain-export/` that reads the Markdown files back into SQLite
  4. This closes the "data opacity" complaint (users can read/edit their memory in any text editor) and enables migration from Basic Memory

- **Why**: The benchmark HTML closes Gap 1 (publish the benchmark as the standard for forgetting). The export closes the top user complaint about data portability and the competitive gap vs. Basic Memory. Both are independently valuable and share a ~1 week implementation window.
- **Where**: `internal/benchmark/cli.go`, `internal/benchmark/report.go`, `main.go`, `internal/export/`
- **Acceptance**:
  - `neurox benchmark --scale small --output-html report.html` produces a valid self-contained HTML file
  - Radar chart renders correctly in browser with all 12 dimensions
  - `neurox export --format=markdown --output=./out/` produces `.md` files with valid YAML frontmatter
  - Exported files open and render correctly in Obsidian
  - `neurox import --source=./out/` re-imports observations with same IDs and content
  - Round-trip: export then import produces identical observation count
- **Status**: [ ] pending

### Step 5: Content strategy execution — own the vocabulary
- **What**: This step is about publishing, not coding. It produces three artifacts:

  **Artifact 1 — Blog post: "The First Benchmark That Measures Forgetting"**
  - 1500-word technical post explaining why current memory benchmarks (LOCOMO, LongMemEval) only test recall accuracy, not temporal degradation
  - Introduces the Ebbinghaus model as the missing axis
  - Publishes Neurox benchmark results (from Step 4 HTML report) as the baseline
  - Explains the 3 tiers (Base/Target/Elite) and invites the community to run it against other systems
  - Published on: GitHub README link, Hacker News Show HN, r/LocalLLaMA, r/cursor
  - Tagline: "We measured how AI memory forgets — nobody else had"

  **Artifact 2 — Technical glossary: "Vocabulary for Agent Memory Systems"**
  - 800-word reference doc (markdown, in `docs/concepts.md`) that defines: decay curve, consolidation epoch, memory layer, staleness, temporal intent, observation vs. fact, brain power score
  - This becomes the vocabulary other developers use when discussing memory systems
  - Linked from README, referenced in blog post

  **Artifact 3 — "Memory for Coding Agents" landing page section in README**
  - Replace the current generic README intro with a section specifically targeting coding agent users (Claude Code, Cursor, VS Code)
  - Lead with the pain point: "Your AI coding agent forgets everything between sessions. Neurox fixes that."
  - Show the 30-second install, then the 3 core use cases: preferences, architecture decisions, bug discoveries
  - Include the benchmark score badge (from Step 4 HTML output)

- **Why**: Closes Gap 5 (technical vocabulary) and Gap 3 (coding agent niche). Content is the multiplier on everything else — the dashboard, benchmark, and integrations only matter if developers know they exist. A well-placed Hacker News post with a technical benchmark typically generates 200-500 GitHub stars for tools of this quality level.
- **Where**: `docs/concepts.md`, `README.md`, external blog/HN post
- **Acceptance**:
  - `docs/concepts.md` defines all 7 key terms clearly
  - README hero section explicitly targets coding agent users with concrete use cases
  - Blog post draft is ready to publish (can be a GitHub gist if no blog exists yet)
  - HN Show HN post title and first paragraph are written
- **Status**: [ ] pending

---

## Execution Order and Dependencies

```
Step 1 (Smithery)     ──────────────────────────── no deps, start immediately
Step 2 (Docs/Skills)  ──────────────────────────── no deps, parallel with Step 1
Step 3 (Dashboard)    ── depends on nothing new, expand existing dashboard.html
Step 4 (Benchmark HTML + Export) ── depends on Step 3 being done (to screenshot)
Step 5 (Content)      ── depends on Step 4 (need benchmark results to publish)
```

**Parallel execution:** Steps 1, 2, and 3 can run simultaneously. Step 4 follows. Step 5 is last.

**Total estimated effort:** 3-4 weeks for a single developer. Steps 1+2 are 3-5 days. Steps 3+4 are 1.5 weeks. Step 5 is 3-5 days of writing.

---

## Verification

```bash
# Step 1: Smithery server card
curl http://localhost:7438/.well-known/mcp/server-card.json | jq '.tools | length'
# Expected: 13

# Step 2: Skills file exists and is valid
cat skills/neurox/SKILL.md | head -5

# Step 3: Dashboard health tab loads
open http://localhost:7438
# Navigate to Brain Health tab, verify score renders

# Step 4: Benchmark HTML report
CGO_ENABLED=1 go run -tags fts5 . benchmark --scale small --output-html /tmp/report.html
open /tmp/report.html
# Verify radar chart renders

# Step 4: Markdown export round-trip
CGO_ENABLED=1 go run -tags fts5 . export --format=markdown --output=/tmp/brain-export/
CGO_ENABLED=1 go run -tags fts5 . import --source=/tmp/brain-export/
# Count should match

# All tests still pass
CGO_ENABLED=1 go test -tags fts5 ./...
CGO_ENABLED=1 go build -tags fts5 ./...
go vet ./...
```

---

## Risks / Notes

- **Gap 2 (Newsletter):** Explicitly excluded from this plan. A newsletter requires sustained editorial commitment (weekly) that competes with engineering time. The content in Step 5 (blog post + glossary) is a lighter-weight substitute that achieves the same vocabulary ownership goal without the operational overhead. Revisit after the project has a Discord community.

- **Dashboard scope:** Step 3 intentionally avoids building a new SPA or separate frontend. Everything extends the existing `dashboard.html` embedded file. This keeps the binary self-contained and avoids a Node.js build step.

- **Smithery requires a publicly accessible HTTP server.** For local-only installs, Smithery cannot scan the server card. The server card endpoint is still valuable for: manual config generation, Glama listing (which accepts GitHub URLs), and any future remote hosting. The MCP stdio server remains the primary integration path for most users.

- **Export format design:** The Markdown export is intentionally compatible with Basic Memory's format (same frontmatter structure, WikiLinks for relations). This means Basic Memory users can migrate to Neurox by running `neurox import` on their existing notes. This is a deliberate acquisition strategy.

- **BFS graph traversal** (identified as a technical gap vs. Graphiti) is NOT in this plan. The `observation_links` table already stores the graph edges — a BFS implementation is straightforward (~100 lines) but it's an engine feature, not a gap-filling priority right now. Add to backlog.

# Plan: Interactive Graph Visualization

## Goal
Add a `neurox graph` CLI command and API endpoint that generates an interactive, self-contained HTML visualization of the observation graph using vis-network. Users can explore connections, filter by namespace/type/tags/importance, and understand the structure of their memory.

## Business Context
- With 6K+ observations and 2K+ links, users need a visual way to explore their memory graph
- The visualization helps understand how observations connect, cluster, and relate
- Self-contained HTML means no server dependency — just open in a browser

## Technical Context
- Existing `observation_links` table has 6 relation types: supersedes, contradicts, relates_to, derived_from, validates, refines
- Observations have types (8), layers (0-2), importance (0-1), and tags
- vis-network (CDN) provides force-directed layout with zoom/pan/drag
- The existing dashboard pattern (`internal/api/dashboard.go`) uses `embed.FS` for serving HTML

## Implementation Steps

### Step 1: Create graph data package
- **What**: New `internal/graph/` package with types and SQL queries to extract graph data
- **Why**: Encapsulate graph extraction logic separate from rendering
- **Where**: `internal/graph/graph.go`
- **Acceptance**: Can query observations + links with filters, returns structured GraphData
- **Status**: [ ] pending

### Step 2: Create HTML template with vis-network
- **What**: Self-contained HTML template that renders an interactive force-directed graph
- **Why**: The visualization is the core deliverable
- **Where**: `internal/graph/render.go`, `internal/graph/template.go`
- **Acceptance**: Generates valid HTML with embedded JSON data, opens in browser, interactive
- **Status**: [ ] pending

### Step 3: Add CLI command and API endpoint
- **What**: `neurox graph` CLI command + `GET /api/v1/graph` endpoint
- **Why**: Two access paths — CLI for local use, API for when server is running
- **Where**: `main.go`, `internal/api/server.go`, `internal/api/handlers.go`
- **Acceptance**: CLI generates HTML file and opens browser, API serves HTML directly
- **Status**: [ ] pending

### Step 4: Build, test, verify
- **What**: Ensure everything compiles and works end-to-end
- **Why**: Quality gate
- **Where**: All modified files
- **Acceptance**: `go build -tags fts5 ./...`, `go vet ./...`, graph renders correctly
- **Status**: [ ] pending

## Verification
```bash
go build -tags fts5 ./...
go vet ./...
neurox graph --namespace neurox --limit 100
```

## Risks / Notes
- 6K+ observations is too many for a single view — smart defaults needed (top 200 by importance, or only linked observations)
- vis-network CDN means browser needs internet; alternative: bundle the JS (but adds ~500KB)
- Keep the HTML self-contained — all data embedded as JSON, no API calls needed after generation

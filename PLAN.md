# Plan: Remove CGO dependency — Migrate to ncruces/go-sqlite3

## Goal

Eliminate the CGO/C-compiler requirement by replacing `mattn/go-sqlite3` with `ncruces/go-sqlite3`, a pure-Go SQLite driver. After this migration, Neurox can be installed with `go install` on any platform without gcc or cross-compilers.

## Business Context

- **Problem**: "Does Neurox require CGO / C compiler to install?" — Yes, currently. This is the #1 adoption barrier. Users need gcc/clang installed, cross-compilation requires platform-specific C toolchains, and `go install` fails without CGO_ENABLED=1.
- **Impact**: Every installation guide, CI pipeline, and release workflow carries CGO baggage. Users on systems without a C compiler (Windows without MSYS2, minimal Docker images, cloud dev environments) cannot build Neurox.
- **Success criteria**: `CGO_ENABLED=0 go build ./...` succeeds. `go install github.com/joeldevz/neurox@latest` works without any C compiler. All existing tests pass. Release pipeline no longer needs cross-compilers.

## Technical Context

### Why ncruces/go-sqlite3

| Feature | mattn (current) | ncruces (target) | modernc (alternative) |
|---|---|---|---|
| CGO | Required | **Not needed** | Not needed |
| FTS5 | Build tag `-tags fts5` | **Built-in** | Built-in |
| Backup API | `SQLiteConn.Backup()` | **Native backup API** | Not available (VACUUM INTO only) |
| Driver name | `"sqlite3"` | `"sqlite3"` (same!) | `"sqlite"` (different) |
| Performance | Reference (C) | ~95-105% | ~80-90% |
| Platforms | Needs cross-compiler | **All Go targets** | All Go targets |

### Impact analysis

| Area | Files affected | Complexity |
|---|---|---|
| Driver import (side-effect) | `internal/db/db.go` + 3 test files | Trivial: change import path |
| Backup API (named import) | `internal/db/backup.go` | Medium: rewrite using ncruces backup API |
| Build tags | All build/test commands | Remove `-tags fts5` everywhere |
| go.mod | `go.mod`, `go.sum`, `vendor/` | Replace dependency |
| CI workflow | `.github/workflows/ci.yml` | Remove gcc install, CGO_ENABLED |
| Release workflow | `.github/workflows/release.yml` | Simplify: single runner, no cross-compilers |
| Documentation | ~13 files with CGO/fts5 refs | Text updates |

**99% of the codebase uses only `database/sql` interfaces** — no driver-specific types. The single exception is `internal/db/backup.go` which uses `*sqlite3.SQLiteConn` for the online backup API.

### Key files

- `internal/db/db.go` — Database open/configure/migrate (side-effect import)
- `internal/db/backup.go` — **Only file with named driver import** (Backup API)
- `internal/export/export_test.go` — Test side-effect import
- `internal/links/store_test.go` — Test side-effect import
- `internal/graph/graph_test.go` — Test side-effect import
- `.github/workflows/ci.yml` — CI pipeline
- `.github/workflows/release.yml` — Release pipeline
- `go.mod` — Dependency declaration

## Implementation Steps

### Step 1: Replace mattn/go-sqlite3 with ncruces/go-sqlite3 in go.mod
- **What**: Remove `github.com/mattn/go-sqlite3` from `go.mod`. Add `github.com/ncruces/go-sqlite3` (driver + embed packages). Run `go mod tidy && go mod vendor`.
- **Why**: This is the foundational change. The embed package bundles the SQLite Wasm binary so no external dependencies are needed.
- **Where**: `go.mod`, `go.sum`, `vendor/`
- **Acceptance**:
  - `go.mod` lists `github.com/ncruces/go-sqlite3` and NOT `github.com/mattn/go-sqlite3`
  - `vendor/` directory is updated
  - Module graph resolves without errors
- **Status**: [x] done

### Step 2: Update driver imports in production code
- **What**: Change `internal/db/db.go` import from `_ "github.com/mattn/go-sqlite3"` to `_ "github.com/ncruces/go-sqlite3/driver"` and add `_ "github.com/ncruces/go-sqlite3/embed"`. Update `db.Open()` DSN format if needed by ncruces (it uses `"file:path"` URIs, but also accepts plain paths). Update PRAGMAs — ncruces supports `_pragma=` query parameters in DSN, but executing them via ExecContext also works (keep current approach for clarity).
- **Why**: The production database layer must register the new driver.
- **Where**: `internal/db/db.go`
- **Acceptance**:
  - `internal/db/db.go` imports ncruces driver + embed
  - No references to `mattn/go-sqlite3` in production code
  - Database opens successfully with WAL mode, FTS5, and all PRAGMAs
  - `go build ./...` compiles (no `-tags fts5`, no `CGO_ENABLED=1`)
- **Status**: [x] done

### Step 3: Rewrite backup.go using ncruces backup API
- **What**: Rewrite `internal/db/backup.go` to use ncruces/go-sqlite3 native backup API instead of `mattn/go-sqlite3` `SQLiteConn.Backup()`. The ncruces package exposes `sqlite3.Backup` with `NewBackup()`, `Step()`, and `Close()` methods. The approach: use `sql.Conn.Raw()` to get the underlying `*sqlite3.Conn` (ncruces type), then call `sqlite3.NewBackup(destConn, "main", srcConn, "main")`. Alternatively, if `sql.Conn.Raw()` doesn't expose the ncruces connection type directly via the `database/sql` driver, use `driver.Open()` helper which provides direct `*sqlite3.Conn` access and wrap the backup at that level.
- **Why**: This is the **only file** with driver-specific types. It must be rewritten to use the ncruces equivalent API.
- **Where**: `internal/db/backup.go`
- **Acceptance**:
  - `Backup()` and `BackupWithResult()` produce identical results as before
  - Backup works on WAL-mode database while writers may be active
  - No imports of `mattn/go-sqlite3` remain
  - `go build ./...` compiles without CGO
  - Manual test: backup a database, verify the backup is valid SQLite
- **Note**: Coder should consult Context7 or ncruces docs for the exact backup API surface
- **Status**: [x] done

### Step 4: Update test file imports
- **What**: Update the 3 test files that import `_ "github.com/mattn/go-sqlite3"`:
  - `internal/export/export_test.go`
  - `internal/links/store_test.go`
  - `internal/graph/graph_test.go`
  Change to `_ "github.com/ncruces/go-sqlite3/driver"` + `_ "github.com/ncruces/go-sqlite3/embed"`. Verify `sql.Open("sqlite3", ":memory:")` still works with ncruces (ncruces accepts `":memory:"` and `"file::memory:"` DSNs).
- **Why**: Tests that open their own database connections need the driver registered.
- **Where**: `internal/export/export_test.go`, `internal/links/store_test.go`, `internal/graph/graph_test.go`
- **Acceptance**:
  - All 3 test files compile without CGO
  - `go test ./internal/export/... ./internal/links/... ./internal/graph/...` passes
  - No imports of `mattn/go-sqlite3` in any test file
- **Status**: [x] done

### Step 5: Remove build tags and CGO_ENABLED from all build commands
- **What**: Remove `-tags fts5` from every build/test/vet command in the project. Remove `CGO_ENABLED=1` from all environments. Search the entire codebase for remaining references to `fts5` build tag or `CGO_ENABLED` and update/remove them. This includes:
  - Makefile or build scripts (if any)
  - CLAUDE.md build instructions
  - Any shell scripts or documentation
- **Why**: ncruces includes FTS5 by default — no build tag needed. CGO is no longer required.
- **Where**: All files referencing `-tags fts5` or `CGO_ENABLED`
- **Acceptance**:
  - `grep -r "tags fts5" .` returns 0 results (excluding vendor/)
  - `grep -r "CGO_ENABLED" .` returns 0 results (excluding vendor/)
  - `go build ./...` works (no tags, no CGO)
  - `go test ./...` works (no tags, no CGO)
- **Status**: [ ] pending

### Step 6: Simplify CI and Release workflows
- **What**:
  - **ci.yml**: Remove `CGO_ENABLED: "1"` env var. Remove `Install gcc` step. Remove `-tags fts5` from all commands. Verify all 4 steps (build, vet, test, race test) work.
  - **release.yml**: Eliminate cross-compiler infrastructure entirely. All 5 targets (linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64) can now build on a single runner using `GOOS` + `GOARCH` without CGO. Collapse the 3 separate jobs (linux, macos, windows) into a single job with a strategy matrix. Remove `Install cross-compilers` step, `CC` env vars, and `CGO_ENABLED=1`.
- **Why**: Pure Go means any `GOOS/GOARCH` combination compiles on any runner. No more 3-job, 3-runner release pipeline.
- **Where**: `.github/workflows/ci.yml`, `.github/workflows/release.yml`
- **Acceptance**:
  - CI: `go build ./...`, `go vet ./...`, `go test ./...`, `go test -race ./...` all pass without CGO
  - Release: Single job builds all 5 targets using a strategy matrix
  - No references to gcc, cross-compilers, CGO_ENABLED in workflows
- **Status**: [x] done

### Step 7: Update documentation
- **What**: Update all documentation files that reference CGO, `-tags fts5`, or C compiler requirements:
  - `README.md`, `README.es.md` — Installation instructions
  - `docs/quickstart.md` — Quick start guide
  - `docs/claude-code.md`, `docs/cursor.md`, `docs/opencode.md`, `docs/vscode.md`, `docs/claude-desktop.md` — Editor integration guides
  - `docs/blog-benchmark.md` — Benchmark blog
  - `benchmarks/README.md` — Benchmark docs
  - `internal/installer/skill.md` — Installer skill
  - `CLAUDE.md` — Agent instructions

  Replace CGO build instructions with simple `go install` or `go build`. Remove compiler prerequisites. Highlight that no C compiler is needed.
- **Why**: Documentation must reflect the new zero-dependency installation experience.
- **Where**: ~13 documentation files
- **Acceptance**:
  - No documentation mentions CGO_ENABLED or `-tags fts5` as requirements
  - Installation instructions use `go install github.com/joeldevz/neurox@latest`
  - All docs compile/render correctly
- **Status**: [x] done

### Step 8: Full verification and regression test
- **What**:
  1. `CGO_ENABLED=0 go build ./...` — Verify pure-Go build works
  2. `go vet ./...` — No issues
  3. `go test ./...` — All tests pass
  4. `go test -race ./...` — No race conditions
  5. Manual smoke test: start MCP server, perform save/recall/backup operations
  6. Verify `go install` works from a clean environment
- **Why**: Final validation that the migration is complete and nothing is broken.
- **Where**: Entire project
- **Acceptance**:
  - All build/vet/test commands pass without CGO_ENABLED or -tags fts5
  - MCP server starts and responds to tool calls
  - Backup functionality works end-to-end
  - FTS5 search (MATCH queries, bm25 ranking) works correctly
  - No references to mattn/go-sqlite3 remain in the codebase (excluding git history)
- **Status**: [x] done

## Verification

```bash
# Pure-Go build (the whole point)
CGO_ENABLED=0 go build ./...

# Full test suite
go test ./...

# Race detection
go test -race ./...

# Vet
go vet ./...

# Manual: MCP server
./neurox mcp

# Manual: Backup
./neurox backup --dest /tmp/test-backup.db

# Verify no CGO traces
grep -r "CGO_ENABLED" --include="*.go" --include="*.yml" --include="*.md" . | grep -v vendor/ | grep -v PLAN.md
grep -r "tags fts5" --include="*.go" --include="*.yml" --include="*.md" . | grep -v vendor/ | grep -v PLAN.md
grep -r "mattn/go-sqlite3" --include="*.go" . | grep -v vendor/
```

## Sequencing & Dependencies

```
Step 1 (go.mod)           ──> foundation, must be first
Step 2 (db.go imports)    ──> depends on Step 1
Step 3 (backup.go)        ──> depends on Step 1
Step 4 (test imports)     ──> depends on Step 1
Step 5 (build tags)       ──> depends on Steps 2-4
Step 6 (CI/CD)            ──> depends on Step 5
Step 7 (docs)             ──> independent (can run in parallel with Steps 5-6)
Step 8 (verification)     ──> depends on all steps
```

Steps 2, 3, 4 can run in parallel after Step 1. Steps 6 and 7 can run in parallel after Step 5.

## Risks / Notes

- **Performance**: ncruces uses wasm2go which may have marginally different performance characteristics than native C SQLite. Benchmarks show it's competitive (~95-105%), but write-heavy workloads should be validated. For Neurox's use case (agent memory, not OLTP), this is negligible.
- **Memory**: The Wasm sandbox adds some memory overhead per connection. With Neurox's `MaxOpenConns(1)`, this is a single connection and negligible.
- **Backup API compatibility**: The ncruces backup API may have slightly different semantics. The rewrite in Step 3 must be validated with a WAL-mode database while a writer is active.
- **DSN format**: ncruces prefers `"file:path"` URIs but also accepts plain paths. Current code uses plain paths (`sql.Open("sqlite3", databasePath)`). This should work but needs verification.
- **`:memory:` databases in tests**: Several tests use `sql.Open("sqlite3", ":memory:")`. ncruces supports this syntax. Verify in Step 4.
- **Installer TUI**: The Bubble Tea installer in `internal/installer/` may reference CGO in its output text. Check and update in Step 7.
- **go.sum size**: ncruces has more transitive dependencies (golang.org/x/sys, etc.) but they're all pure Go. The vendor directory will change significantly.

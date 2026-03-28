# Plan: Pre-Release Final Fixes — Dead Code, Git Hygiene & Documentation

## Goal

Fix the 5 remaining issues (2 critical, 3 high) found in the final audit before creating the `v0.2.0` release tag.

## Business Context

- **Problem**: Two previous plans (hardening + launch readiness) resolved all major gaps. This final pass fixes a dead MCP tool, a 13 MB binary in git, and documentation inconsistencies that would erode credibility on day one.
- **Non-goals**: New features, test coverage for low-risk packages, `log.Fatalf` cleanup. This is the absolute minimum to ship clean.

## Technical Context

### Issues found (audit 2026-03-28)

**CRITICAL:**
- `backupTool()` defined in `internal/mcp/tools.go` but never registered — no `handleBackup` handler exists. The `internal/db/backup.go` function works but is unreachable from MCP or HTTP.
- `longmemeval` binary (13 MB) committed to git. Permanently inflates the repo.

**HIGH:**
- `schema.sql` missing `retention` column (exists via migration 004 but not in base schema definition).
- `--host` flag and `NEUROX_HTTP_HOST` env var not documented in README.
- CLI help text doesn't mention `--format json` for export/import.

**Key files:**
- `internal/mcp/tools.go:252-259` — dead `backupTool()` definition
- `internal/mcp/handlers.go` — missing `handleBackup`
- `internal/mcp/server.go` — backup not registered
- `internal/api/handlers.go` — missing HTTP backup handler
- `internal/api/server.go` — missing backup route
- `longmemeval` — tracked binary
- `.gitignore` — missing `longmemeval` entry
- `internal/db/schema.sql` — missing `retention` column
- `README.md` / `README.es.md` — missing `--host` docs, CLI table gaps
- `main.go` — `printUsage()` and export/import help text

## Implementation Steps

### Step 1: Wire backup tool into MCP and HTTP surfaces
- **What**: Implement `handleBackup` in `internal/mcp/handlers.go` that calls `db.Backup()`. Register the tool in `internal/mcp/server.go`. Add `handleBackup` HTTP handler in `internal/api/handlers.go` and register route `POST /api/v1/backup` in `internal/api/server.go`. The backup tool should accept an optional `output` parameter (string, destination path). Default to `~/.config/neurox/neurox.db.backup`. Return `{path, size_bytes, message}`.
- **Why**: The backup infrastructure (`internal/db/backup.go`) was implemented in the hardening plan but only wired to the CLI. MCP and HTTP surfaces were defined but never connected.
- **Where**:
  - `internal/mcp/handlers.go` — new `handleBackup` function
  - `internal/mcp/server.go` — register `backupTool` in `register()` function
  - `internal/api/handlers.go` — new `handleBackup` HTTP handler
  - `internal/api/server.go` — register `POST /api/v1/backup` route
- **Acceptance**:
  - MCP `backup` tool is callable and creates a valid SQLite backup
  - HTTP `POST /api/v1/backup` works with optional `?output=...` query param
  - Both return `{path, size_bytes, message}`
  - `go build -tags fts5 ./...` passes
  - `go test -tags fts5 ./internal/mcp/...` passes
- **Status**: [x] done (was already implemented)

### Step 2: Remove `longmemeval` binary from git and update .gitignore
- **What**: Add `longmemeval` to `.gitignore`. Remove the tracked binary with `git rm longmemeval`. Also add `config.yaml`, `.env`, `priorities.yaml` to `.gitignore` as a safety measure against accidental secret commits.
- **Why**: A 13 MB compiled binary inflates the repo permanently. It was accidentally committed and doesn't match current source.
- **Where**: `.gitignore`, `longmemeval` (remove)
- **Acceptance**:
  - `longmemeval` no longer tracked by git (`git ls-files longmemeval` returns empty)
  - `.gitignore` includes `longmemeval`, `config.yaml`, `.env`, `priorities.yaml`
  - `git status` shows the removal staged
- **Status**: [x] done

### Step 3: Add `retention` column to schema.sql
- **What**: Add `retention TEXT NOT NULL DEFAULT 'durable' CHECK (retention IN ('operational', 'durable'))` to the `observations` CREATE TABLE in `internal/db/schema.sql`. This makes the schema file consistent with what migration 004 adds.
- **Why**: `schema.sql` is the conceptual source of truth for the table structure. It already includes columns from migrations 010 and 012 but is missing `retention` from migration 004. Anyone reading the schema to understand the data model gets an incomplete picture.
- **Where**: `internal/db/schema.sql` — `observations` CREATE TABLE
- **Acceptance**:
  - `retention` column present in `schema.sql` observations table definition
  - Column definition matches migration 004: `retention TEXT NOT NULL DEFAULT 'durable' CHECK (retention IN ('operational', 'durable'))`
  - `go build -tags fts5 ./...` passes
  - `go test -tags fts5 ./internal/db/...` passes
- **Status**: [x] done

### Step 4: Document `--host` flag and `NEUROX_HTTP_HOST` in README
- **What**: Update both `README.md` and `README.es.md`:
  1. In the CLI reference table, change `neurox serve` useful flags from "none" to `--host`
  2. Add `NEUROX_HTTP_HOST` to the environment variables table with description: "HTTP server bind address (default: `127.0.0.1`)"
  3. Add a brief note in the HTTP server section explaining that it binds to localhost by default and how to override for network access
- **Why**: Users who need remote dashboard access or want to understand the security posture can't find this information.
- **Where**: `README.md`, `README.es.md`
- **Acceptance**:
  - `neurox serve` row in CLI table shows `--host` as a useful flag
  - `NEUROX_HTTP_HOST` appears in the env vars table
  - Both English and Spanish READMEs updated
- **Status**: [x] done

### Step 5: Fix CLI help text for export/import format
- **What**: Update `printUsage()` in `main.go` to show that `export` and `import` support `--format md|json`. Also update the FlagSet descriptions in `runExport` and `runImport` to mention both formats. Add `backup` to `printUsage()` since the command exists.
- **Why**: Users running `neurox --help` don't know about JSON export or the backup command.
- **Where**: `main.go` — `printUsage()`, `runExport`, `runImport`
- **Acceptance**:
  - `printUsage()` shows export with `--format md|json`
  - `printUsage()` shows import with `--format md|json`
  - `printUsage()` lists `backup` command
  - `go build -tags fts5 ./...` passes
- **Status**: [x] done

## Verification

```bash
# Full build and vet
CGO_ENABLED=1 go build -tags fts5 ./...
CGO_ENABLED=1 go vet -tags fts5 ./...

# All tests
CGO_ENABLED=1 go test -tags fts5 ./...

# Verify backup tool is registered in MCP
grep -n "backupTool" internal/mcp/server.go   # Should show registration

# Verify longmemeval is gone
git ls-files longmemeval   # Should return empty

# Verify retention in schema
grep "retention" internal/db/schema.sql   # Should show column

# Verify CLI help mentions backup and json format
./neurox --help 2>&1 | grep -E "backup|export"
```

## Sequencing & Dependencies

```
Step 1 (backup wiring)    ──> independent
Step 2 (git cleanup)      ──> independent
Step 3 (schema fix)       ──> independent
Step 4 (README docs)      ──> independent
Step 5 (CLI help)         ──> independent
```

All 5 steps are fully independent and can run in parallel.

## Risks / Notes

- **`longmemeval` in git history**: `git rm` removes it from the working tree and future commits, but the 13 MB blob stays in git history. To fully purge, `git filter-branch` or BFG Repo-Cleaner would be needed. This is optional and can be done before the very first public push if the repo hasn't been shared yet.
- **Backup tool security**: The MCP backup tool writes to disk. The default path (`~/.config/neurox/neurox.db.backup`) is safe. Custom paths should be validated to prevent path traversal, but this is low risk since the tool runs locally.
- **Schema.sql is not used for migrations**: Adding `retention` to `schema.sql` is documentation-only. The column is actually created by `004_classify_retention.sql`. Fresh installs use schema.sql + migrations, so the column must appear in both to avoid duplicates — check that migration 004 uses `ALTER TABLE ADD COLUMN` which is idempotent via the migration runner.

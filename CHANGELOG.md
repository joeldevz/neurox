# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.5.7] — 2026-08-03

### Added

- Redesigned observability dashboard with Overview, lazy relationship graph, Explorer, and Brain Power health views
- FTS5-backed fact candidates as a first-class RRF recall channel

### Fixed

- Recall BM25 weighting, namespace and expiration filtering, candidate-pool sizing, and cross-signal ranking
- CLI `session-end` now waits for observation extraction, reports the final count, and surfaces extraction failures

### Changed

- Access telemetry now uses an asynchronous coalesced queue and no longer influences recency ranking
- Standardized FTS5 build configuration and removed unused helpers and dead code

### Known limitations

- CLI `session-end` fact post-processing remains best-effort and asynchronous after observation extraction

## [0.5.6] — 2026-06-19

### Added

- Unified save pipeline across CLI, MCP, and HTTP surfaces (`internal/savepipeline`)
- CLI `session-start` and `session-end` commands for full session lifecycle
- Provenance fields (`source_surface`, `source_session_id`, `source_tool`) returned by all surfaces
- Temporal intent detection in CLI and HTTP recall responses
- 14 parity contract integration tests
- AfterUpdate hooks for re-embedding and re-fact-extraction on observation updates
- PostSaveHook on SessionManager for session-derived observations

### Changed

- SQLite driver switched from ncruces/go-sqlite3 (pure Go/Wasm) to mattn/go-sqlite3 (CGO) for ~850x faster FTS5 recall
- HTTP save now attaches active session ID (was missing before)
- HTTP context fallback explicitly signals degraded mode

### Performance

- FTS5 recall: ~890ms → ~1ms per query (1K observations)
- FTS5 recall at scale: ~30s → ~23ms (50K observations)

## [0.2.0] — 2026-03-28

- Batch UPDATE optimization for consolidation pipeline
- Version bump and stability improvements

## [0.1.19] and earlier

- Initial release with three-layer memory model (Buffer → Working → Core)
- FTS5 full-text search with tri-factor scoring (recency × importance × relevance)
- Ebbinghaus decay curves and consolidation pipeline
- MCP server with 12+ tools (save, recall, context, update, forget, invalidate, status, sessions, git hooks, reflect, consolidate)
- HTTP API on port 7438 with full REST endpoints
- CLI with subcommands for all memory operations
- TUI dashboard with Bubble Tea
- Brain benchmark scoring system
- Health check with per-dimension breakdown
- Temporal mention extraction and temporal-aware recall
- Fact extraction (subject/predicate/object triples)
- Interactive graph visualization with vis-network
- Agent skill integrations (Claude Code, Cursor, VS Code, OpenCode)
- Git post-commit hook for automatic staleness marking
- YAML configuration with environment variable overrides

[Unreleased]: https://github.com/joeldevz/neurox/compare/v0.5.7...HEAD
[0.5.7]: https://github.com/joeldevz/neurox/compare/v0.5.6...v0.5.7
[0.5.6]: https://github.com/joeldevz/neurox/compare/v0.2.0...v0.5.6
[0.2.0]: https://github.com/joeldevz/neurox/compare/v0.1.19...v0.2.0

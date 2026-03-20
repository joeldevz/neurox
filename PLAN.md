# Plan: Temporal Reasoning and General Memory Quality

## Goal
Add first-class temporal reasoning to Neurox so the memory engine can understand when something happened, whether it is still valid, and what superseded it. This should improve both coding-agent memory and research/conversation memory without introducing benchmark-specific hacks.

## Business Context
- Neurox should work better for two adjacent use cases: coding memory and long-running research/conversation memory.
- The immediate product need is higher-quality answers to questions like `what is current`, `what changed`, `when did this happen`, and `what was true before`.
- The feature must preserve historical memory instead of deleting old knowledge; older truths should remain queryable as history while no longer contaminating current-state recall.
- The outcome should be general-purpose engine quality, not optimization for a single benchmark.
- The primary external benchmark for this initiative is `LongMemEval` because it best matches cross-session temporal reasoning, knowledge updates, and current-vs-historical recall; `LoCoMo` remains a secondary diagnostic benchmark for broader long-horizon conversational coherence.
- Success means users can ask about recency, duration, validity, and supersession and get more trustworthy results from the same memory base.

## Technical Context
- The current schema already has strong temporal foundations:
  - `observations.valid_from`, `observations.valid_until`, `observations.staleness`, `observations.invalidated_by`
  - `facts.valid_from`, `facts.valid_until`, `facts.superseded_by`
  - `observation_links` with `supersedes`, `contradicts`, `derived_from`, `validates`, `refines`
- `internal/facts/store.go` already supersedes active facts with the same `(subject, predicate, namespace)` and preserves history rather than overwriting it.
- `internal/contradiction/detector.go` already expires older conflicting observations, so temporal reasoning can build on an existing validity model instead of inventing a new one.
- `internal/recall/engine.go` and `internal/recall/scoring.go` currently score by recency, importance, and relevance only; there is no explicit temporal-intent handling yet.
- `internal/session/manager.go` and `internal/facts/extract.go` already use LLM extraction patterns that can be extended with temporal extraction prompts.
- The codebase uses co-located Go tests and table-driven patterns; `internal/facts/store_test.go` and `internal/recall/engine_test.go` are good references.
- Recommended default: start with a rule-based temporal parser plus lightweight LLM-assisted extraction hooks where LLM is already optional. This keeps v1 deterministic and testable.

## Implementation Steps

### Step 1: Define temporal data model and persistence
- **What**: Add a temporal persistence layer for normalized time expressions and event windows, including a new `internal/temporal` package and schema support for temporal mentions.
- **Why**: Neurox needs structured temporal data before recall, facts, or contradictions can reason about time reliably.
- **Where**: `internal/db/schema.sql`, new `internal/temporal/*.go`, and related DB/open tests under `internal/db/` or `internal/temporal/`
- **Acceptance**:
  - A new table exists for temporal mentions linked to observations
  - The model can represent raw text, normalized start/end, mention kind, anchor time, and confidence
  - Migrations/tests prove the schema loads in test DBs and rows can be inserted/read
  - The data model is generic enough for both absolute dates and relative expressions
- **Status**: [x] done

### Step 2: Implement deterministic temporal parsing v1
- **What**: Build a rule-based parser that extracts and normalizes common temporal expressions from observation content, including absolute dates and relative phrases like `yesterday`, `last week`, `two months ago`, `next month`, and `currently`.
- **Why**: A deterministic parser gives Neurox a solid, testable first version of temporal reasoning without depending on an LLM for every save.
- **Where**: `internal/temporal/parser.go`, `internal/temporal/parser_test.go`
- **Acceptance**:
  - Parser returns normalized temporal spans anchored to a supplied reference time
  - Table-driven tests cover absolute, relative, current-state, past-event, and future-plan examples
  - Ambiguous phrases degrade safely with confidence rather than inventing exact timestamps
  - The parser is reusable by save, consolidation, and session-summary ingestion paths
- **Status**: [x] done

### Step 3: Extract and persist temporal mentions during ingestion and consolidation
- **What**: Integrate temporal extraction into observation creation flows so direct saves and session-derived observations persist temporal mentions automatically.
- **Why**: Temporal reasoning only helps if the structure is captured at write time, not left implicit in raw text.
- **Where**: `internal/observation/store.go`, `internal/session/manager.go`, new `internal/temporal/store.go`, and tests near those packages
- **Acceptance**:
  - Saving an observation with temporal language creates linked temporal mention rows
  - Session summary extraction paths also persist temporal structure for extracted observations
  - Failures in temporal extraction do not block saving the observation
  - Tests cover direct observation save and session-derived observation save
- **Status**: [x] done

### Step 4: Add temporal facts and validity transitions
- **What**: Extend fact extraction/storage to support temporal facts and align them with existing validity fields, including current-state facts, happened-on facts, and superseded historical facts.
- **Why**: Facts are Neurox's compact reasoning layer; temporal reasoning becomes much more useful when it enriches facts, not just observation text.
- **Where**: `internal/facts/extract.go`, `internal/facts/store.go`, `internal/facts/fact.go`, `internal/facts/*_test.go`
- **Acceptance**:
  - Temporal extraction can create or enrich facts such as `migration -> happened_on -> 2026-03-06` or `database -> current -> sqlite`
  - Existing supersession behavior still works and historical facts remain queryable via `valid_until`
  - Tests prove current facts supersede earlier facts while preserving history
  - Non-temporal fact extraction behavior remains intact
- **Status**: [x] done

### Step 5: Make recall temporal-aware
- **What**: Detect temporal intent in recall queries and update ranking/filtering so current, recent, or historical answers are selected more intelligently.
- **Why**: Capturing temporal structure is not enough unless retrieval can prefer the right memory for questions like `what is current`, `when did we migrate`, or `what did we use before`.
- **Where**: `internal/recall/engine.go`, `internal/recall/scoring.go`, new `internal/recall/temporal.go`, `internal/recall/*_test.go`
- **Acceptance**:
  - Temporal questions receive query-time handling for intents like `when`, `since`, `current`, `latest`, `before`, `after`, and `how long`
  - Ranking prefers valid current knowledge for current-state questions and historically relevant results for past-state questions
  - Expired or superseded memories no longer outrank active knowledge in temporal queries
  - Tests demonstrate better ordering for migration/current-state/history scenarios
- **Status**: [x] done

### Step 6: Integrate temporal reasoning with contradiction and consolidation flows
- **What**: Use extracted temporal structure to make contradiction handling and consolidation more reliable, especially when determining whether a newer observation replaces an older one or simply adds historical detail.
- **Why**: Temporal reasoning should improve the engine's long-term coherence, not live only at query time.
- **Where**: `internal/contradiction/detector.go`, `internal/consolidate/pipeline.go`, related tests in `internal/contradiction/` and `internal/consolidate/`
- **Acceptance**:
  - Contradiction resolution distinguishes supersession from harmless historical sequence when enough temporal data exists
  - Consolidation preserves timelines rather than flattening all related memories into one present-tense truth
  - Tests cover `old truth -> new truth`, `historical event + current state`, and `future plan later fulfilled` cases
- **Status**: [x] done

### Step 7: Expose and verify temporal behavior end-to-end
- **What**: Add API/MCP visibility for temporal metadata where useful and create end-to-end verification scenarios that demonstrate coding and research value.
- **Why**: The feature needs to be inspectable and provable, not hidden internally.
- **Where**: `internal/api/handlers.go`, `internal/mcp/handlers.go`, `internal/mcp/tools.go`, targeted tests under `internal/api/` and `internal/mcp/`
- **Acceptance**:
  - Temporal metadata is available in a minimal, non-noisy way for debugging or advanced clients
  - End-to-end tests cover coding-style questions (`what DB do we use now`, `when did auth change`) and research-style questions (`what did we conclude last month`, `what replaced this idea`)
  - Documentation or examples describe the supported temporal behavior and limits of v1
- **Status**: [x] done

## Verification
```bash
go build ./...
go vet ./...
go test ./internal/temporal/...
go test ./internal/facts/...
go test ./internal/recall/...
go test ./internal/contradiction/...
go test ./internal/consolidate/...
go test ./internal/api/...
go test ./internal/mcp/...
go test ./...
```

External benchmark focus:
- Primary benchmark: `LongMemEval`
- Secondary benchmark: `LoCoMo`
- `LongMemEval` should be used to validate temporal reasoning, knowledge updates, abstention, and multi-session current-vs-historical recall behavior.
- `LoCoMo` should be used as a secondary check that temporal improvements do not reduce broader long-horizon conversational memory quality.

Manual verification scenarios:
- Save an observation like `Migramos a SQLite hace dos semanas` and confirm normalized temporal data is stored.
- Save a later observation like `Ahora usamos SQLite en produccion` and confirm current-state recall prefers it over older DB facts.
- Save historical then superseding observations and confirm both remain queryable, but only the current one dominates `what do we use now`.
- Test a research-style sequence where an older hypothesis is replaced by a newer conclusion and confirm both history and current answer are preserved.

## Risks / Notes
- Relative time parsing can become brittle quickly; v1 should cover common patterns only and degrade safely on ambiguity.
- Temporal reasoning should not delete history; it should separate `historical` from `current` through validity and ranking.
- The first version should prefer deterministic parsing over LLM-only extraction so tests remain stable.
- Query intent detection should remain additive; normal recall behavior must keep working for non-temporal queries.
- This initiative should improve both coding and research memory, so avoid domain-specific rules tied only to software engineering vocabulary.

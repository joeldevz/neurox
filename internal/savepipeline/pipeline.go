// Package savepipeline provides the canonical observation-save flow shared
// across the CLI, MCP, and HTTP surfaces.
//
// All three surfaces must produce observations with identical quality:
//   - Provenance metadata (source_surface, source_tool, source_session_id)
//   - Retention auto-classification when not explicitly provided
//   - Active session attachment when a session exists for the namespace
//   - LLM quality gate (SaveQueue pre-hook or sync check)
//   - Post-save hooks: fact extraction + embedding enqueue
//
// The queue fast path (SaveQueue.Enqueue) is preferred when available.
// The sync fallback runs the same checks and hooks sequentially.
package savepipeline

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/joeldevz/neurox/internal/classify"
	"github.com/joeldevz/neurox/internal/embed"
	"github.com/joeldevz/neurox/internal/facts"
	"github.com/joeldevz/neurox/internal/llm"
	"github.com/joeldevz/neurox/internal/observation"
)

// Deps holds the optional dependencies for the shared save pipeline.
// All fields are nil-safe — the pipeline degrades gracefully when a
// dependency is unavailable.
type Deps struct {
	Store         *observation.Store
	SaveQueue     *observation.SaveQueue
	LLMGate       *llm.Gate
	FactExtractor *facts.Extractor
	EmbedQueue    *embed.Queue
	// DB is used only for the active-session lookup (best-effort).
	DB *sql.DB
}

// Input carries the observation to save together with surface-specific
// metadata.  Callers populate the Observation fields they know; the pipeline
// fills provenance, retention, and session attachment.
type Input struct {
	Obs     observation.Observation
	Surface string // "cli" | "mcp" | "http"
	Tool    string // "save" (or any other tool name if reused)
}

// Result is the canonical save response used by all three surfaces.
type Result struct {
	ID        string
	Title     string
	Layer     int
	Namespace string
	TopicKey  string
	Message   string
	// Rejected is true when the LLM quality gate rejected the observation.
	// Callers should check this flag and return an appropriate non-error
	// response (e.g. HTTP 200 or MCP text with the rejection message).
	Rejected bool
}

// Run executes the canonical save pipeline.
//
// Pipeline order:
//  1. Set provenance: SourceSurface, SourceTool
//  2. Attach active session ID for the namespace (best-effort)
//  3. Auto-classify Retention when not explicitly set
//  4. SaveQueue fast path → return immediately if available
//  5. Sync fallback: LLM gate → Store.Save → post-save hooks
func Run(ctx context.Context, deps Deps, in Input) (Result, error) {
	obs := in.Obs

	// 1. Provenance — always overwrite so callers don't have to remember.
	obs.SourceSurface = in.Surface
	obs.SourceTool = in.Tool

	// 2. Active session attachment (best-effort; never blocks on error).
	if deps.DB != nil && obs.SourceSessionID == "" {
		ns := obs.Namespace
		if ns == "" {
			ns = observation.DefaultNamespace
		}
		if sid := activeSessionID(ctx, deps.DB, ns); sid != "" {
			obs.SourceSessionID = sid
		}
	}

	// 3. Retention auto-classification.
	if obs.Retention == "" {
		obs.Retention = observation.Retention(classify.InferRetention(obs.Title, obs.ObservationType, obs.Source))
	}

	// 4. SaveQueue fast path.
	if deps.SaveQueue != nil {
		qr, err := deps.SaveQueue.Enqueue(ctx, obs)
		if err != nil {
			return Result{}, fmt.Errorf("save pipeline enqueue: %w", err)
		}
		return Result{
			ID:        qr.ID,
			Title:     qr.Title,
			Layer:     0,
			Namespace: qr.Namespace,
			TopicKey:  qr.TopicKey,
			Message:   "observation queued for persistence",
		}, nil
	}

	// 5a. Sync fallback — quality gate.
	if deps.LLMGate != nil {
		decision, _ := deps.LLMGate.SaveGateDecide(ctx, llm.SaveInput{
			Title:           obs.Title,
			Content:         obs.Content,
			ObservationType: string(obs.ObservationType),
		})
		if decision == llm.SaveReject {
			return Result{
				Title:    obs.Title,
				Message:  "observation rejected by quality gate (not worth persisting)",
				Rejected: true,
			}, nil
		}
	}

	// 5b. Sync fallback — persist.
	if deps.Store == nil {
		return Result{}, fmt.Errorf("save pipeline: no Store or SaveQueue configured")
	}
	saved, err := deps.Store.Save(ctx, obs)
	if err != nil {
		return Result{}, fmt.Errorf("save pipeline: %w", err)
	}

	// 5c. Sync fallback — post-save hooks (mirrors queue hooks).
	if deps.FactExtractor != nil {
		fe := deps.FactExtractor
		go fe.ExtractAndSave(context.Background(), saved.ID, saved.Title, saved.Content, saved.Namespace)
	}
	if deps.EmbedQueue != nil {
		deps.EmbedQueue.Enqueue(saved.ID)
	}

	return Result{
		ID:        saved.ID,
		Title:     saved.Title,
		Layer:     saved.Layer,
		Namespace: saved.Namespace,
		TopicKey:  saved.TopicKey,
		Message:   "observation saved to Buffer",
	}, nil
}

// AfterUpdate runs the post-update hooks that must fire when an observation's
// content or title changes.  Both MCP and HTTP handleUpdate should call this
// after Store.Update() succeeds.
//
// Post-update hooks (same as post-save, minus the quality gate):
//  1. Fact re-extraction (async, best-effort — old facts are NOT deleted,
//     new facts are appended; dedup is handled by the fact store's upsert)
//  2. Embedding re-enqueue
//
// Temporal re-extraction is already handled inside Store.Update() itself
// (parity with Store.Save), so it is NOT duplicated here.
func AfterUpdate(ctx context.Context, deps Deps, updated observation.Observation) {
	if deps.FactExtractor != nil {
		fe := deps.FactExtractor
		go fe.ExtractAndSave(context.Background(), updated.ID, updated.Title, updated.Content, updated.Namespace)
	}
	if deps.EmbedQueue != nil {
		deps.EmbedQueue.Enqueue(updated.ID)
	}
}

// activeSessionID returns the ID of the most-recently-started active session
// for the given namespace.  Returns "" on any error (best-effort).
func activeSessionID(ctx context.Context, db *sql.DB, namespace string) string {
	var id string
	if err := db.QueryRowContext(ctx,
		"SELECT id FROM sessions WHERE status = 'active' AND namespace = ? ORDER BY started_at DESC LIMIT 1",
		namespace).Scan(&id); err != nil {
		return ""
	}
	return id
}

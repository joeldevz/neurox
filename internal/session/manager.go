package session

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/joeldevz/neurox/internal/filelink"
	"github.com/joeldevz/neurox/internal/llm"
	"github.com/joeldevz/neurox/internal/temporal"
)

// PostSaveHook is called after each observation extracted from a session
// summary is persisted.  Implementations should not block indefinitely.
type PostSaveHook func(ctx context.Context, id, title, content, namespace string)

// Manager handles session lifecycle with LLM-based extraction.
type Manager struct {
	db            *sql.DB
	llm           llm.Provider
	idGen         filelink.IDGenerator
	temporal      *temporal.Extractor
	postSaveHooks []PostSaveHook
	// bgWG tracks in-flight background goroutines (e.g. LLM extraction).
	// Production code does not wait on this; tests call WaitBackground()
	// to deterministically assert on extracted observations.
	bgWG sync.WaitGroup
}

// NewManager creates a session manager.
func NewManager(db *sql.DB, llmProvider llm.Provider, idGen filelink.IDGenerator) *Manager {
	return &Manager{db: db, llm: llmProvider, idGen: idGen}
}

// SetTemporalExtractor configures temporal extraction for session-derived observations.
func (m *Manager) SetTemporalExtractor(te *temporal.Extractor) {
	m.temporal = te
}

// OnPostSave registers a hook that runs after each observation extracted from
// a session summary is persisted.  This is used to trigger fact extraction and
// embedding enqueue — the same post-processing that the main save pipeline
// performs — so that session-derived observations have equivalent quality.
func (m *Manager) OnPostSave(hook PostSaveHook) {
	m.postSaveHooks = append(m.postSaveHooks, hook)
}

// WaitBackground blocks until all background goroutines (e.g. LLM extraction
// started by End) have completed.  Intended for tests that need to verify
// side effects in the database after extraction — not typically called in production.
// CLI callers should use EndResult.WaitForExtraction() instead to wait for
// a specific extraction and handle its error.
func (m *Manager) WaitBackground() {
	m.bgWG.Wait()
}

// StartResult holds the result of starting a session.
type StartResult struct {
	SessionID string
	Namespace string
	Abandoned int64
}

// Start creates a new session, auto-closing previous active ones.
func (m *Manager) Start(ctx context.Context, title, directory, branch, namespace string) (StartResult, error) {
	if namespace == "" {
		namespace = "default"
	}

	// Close active sessions
	res, _ := m.db.ExecContext(ctx, `
		UPDATE sessions SET status = 'abandoned', ended_at = datetime('now')
		WHERE status = 'active' AND namespace = ?
	`, namespace)
	abandoned, _ := res.RowsAffected()

	// Create new session
	id := m.idGen.New()
	_, err := m.db.ExecContext(ctx, `
		INSERT INTO sessions(id, title, directory, branch, namespace, status)
		VALUES(?, ?, ?, ?, ?, 'active')
	`, id, nullStr(title), nullStr(directory), nullStr(branch), namespace)
	if err != nil {
		return StartResult{}, fmt.Errorf("create session: %w", err)
	}

	return StartResult{
		SessionID: id,
		Namespace: namespace,
		Abandoned: abandoned,
	}, nil
}

// extractionResult holds the outcome of background extraction.
type extractionResult struct {
	count int
	err   error
}

// extractionWaitState is private state shared across EndResult copies.
// It holds sync.Once and cached results so that multiple EndResult copies
// safely share the same idempotent WaitForExtraction behavior.
type extractionWaitState struct {
	once       sync.Once
	count      int
	err        error
	resultChan chan extractionResult
}

// EndResult holds the result of ending a session.
type EndResult struct {
	SessionID             string
	ObservationsExtracted int
	Warning               string
	// waitState is a pointer to shared synchronization state.
	// nil when no async extraction was launched (no LLM available).
	waitState *extractionWaitState
}

// WaitForExtraction blocks until async extraction completes and returns the actual count and error.
// Should only be called by CLI callers that need a real count and error status before exiting.
// For MCP/HTTP callers, do NOT call this — ObservationsExtracted=-1 signals async.
// If no extraction was launched (no LLM), returns the synchronous count and nil error.
// Idempotent and concurrent-safe: subsequent calls return the same cached result.
func (r *EndResult) WaitForExtraction() (int, error) {
	if r.waitState == nil {
		// No async extraction was launched (no LLM available).
		// Return the synchronous count (0) and nil error.
		return r.ObservationsExtracted, nil
	}
	// Use sync.Once to ensure we read from the channel exactly once,
	// even if WaitForExtraction() is called multiple times concurrently.
	r.waitState.once.Do(func() {
		result := <-r.waitState.resultChan
		r.waitState.count = result.count
		r.waitState.err = result.err
	})
	return r.waitState.count, r.waitState.err
}

// End completes a session. If LLM is available, extracts atomic observations
// from the summary. Otherwise, just saves the summary.
// The surface parameter identifies the calling surface (e.g. "mcp", "http")
// and is propagated as provenance on any extracted observations.
func (m *Manager) End(ctx context.Context, sessionID, summary, surface string) (EndResult, error) {
	if strings.TrimSpace(sessionID) == "" {
		return EndResult{}, fmt.Errorf("session_id is required")
	}
	if strings.TrimSpace(summary) == "" {
		return EndResult{}, fmt.Errorf("summary is required")
	}

	// Get session namespace
	var namespace string
	err := m.db.QueryRowContext(ctx, "SELECT namespace FROM sessions WHERE id = ? AND status = 'active'", sessionID).Scan(&namespace)
	if err != nil {
		return EndResult{}, fmt.Errorf("session not found or already ended")
	}

	// Mark session completed
	result, err := m.db.ExecContext(ctx, `
		UPDATE sessions SET status = 'completed', summary = ?, ended_at = datetime('now')
		WHERE id = ? AND status = 'active'
	`, summary, sessionID)
	if err != nil {
		return EndResult{}, fmt.Errorf("end session: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return EndResult{}, fmt.Errorf("session not found or already ended")
	}

	endResult := EndResult{SessionID: sessionID}

	// Extract atomic observations if LLM available — run in background
	// so that the MCP/HTTP caller is never blocked by slow LLM inference.
	if llm.IsAvailable(m.llm) {
		// Create shared wait state to hold sync.Once and cached results.
		// This allows EndResult copies to safely share idempotent behavior.
		waitState := &extractionWaitState{
			resultChan: make(chan extractionResult, 1),
		}
		endResult.waitState = waitState

		m.bgWG.Add(1)
		go func() {
			defer m.bgWG.Done()
			bgCtx := context.Background()
			extracted, extractErr := m.extractObservations(bgCtx, summary, namespace, sessionID, surface)
			if extractErr != nil {
				log.Printf("[WARN] session_end %s: background extraction failed: %v", sessionID, extractErr)
			} else {
				log.Printf("[INFO] session_end %s: extracted %d observations in background", sessionID, extracted)
			}
			// Send result to buffered channel (producer never blocks).
			waitState.resultChan <- extractionResult{count: extracted, err: extractErr}
		}()
		endResult.ObservationsExtracted = -1 // signals async; caller should not rely on count
	} else {
		endResult.Warning = "no LLM configured — session summary was saved but observations were not extracted. Configure an LLM provider to enable automatic observation extraction."
		log.Printf("[WARN] session_end %s: %s", sessionID, endResult.Warning)
	}

	return endResult, nil
}

// extractObservations uses LLM to extract atomic observations from a session summary.
func (m *Manager) extractObservations(ctx context.Context, summary, namespace, sessionID, surface string) (int, error) {
	prompt := fmt.Sprintf(`You are a memory extraction engine. From the following session summary, extract atomic observations — individual facts, decisions, discoveries, or patterns that are worth remembering.

Session summary:
%s

Rules:
- Each observation should be a single, self-contained fact or insight
- Format: TYPE | TITLE | CONTENT
- TYPE is one of: decision, bugfix, discovery, pattern, gotcha, config, preference
- TITLE is a short searchable phrase (max 60 chars)
- CONTENT uses What/Why/Where/Learned format when applicable
- Extract 3-8 observations, skip trivial details
- If nothing worth remembering, output NONE

Output observations (one per line, pipe-separated):`, summary)

	resp, err := m.llm.Complete(ctx, prompt)
	if err != nil {
		return 0, fmt.Errorf("extract observations: %w", err)
	}

	observations := parseExtractions(resp)
	saved := 0
	var firstInsertErr error
	for _, obs := range observations {
		id := m.idGen.New()
		_, err := m.db.ExecContext(ctx, `
			INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, source, source_surface, source_session_id, source_tool)
			VALUES(?, ?, ?, ?, 0, 0.6, 0.5, 'semantic', ?, 'consolidator', ?, ?, 'session_end')
		`, id, obs.title, obs.content, obs.obsType, namespace, nullStr(surface), nullStr(sessionID))
		if err != nil {
			// Record first INSERT error; continue trying other observations.
			// CLI will see the error when calling WaitForExtraction.
			if firstInsertErr == nil {
				firstInsertErr = err
			}
			log.Printf("[WARN] session_end %s: failed to insert observation %q: %v", sessionID, obs.title, err)
			continue
		}
		saved++
		// Best-effort temporal extraction — do not block on failure.
		if m.temporal != nil {
			_, _ = m.temporal.Extract(ctx, id, obs.content)
		}
		// Run post-save hooks (fact extraction, embedding enqueue, etc.)
		// so session-derived observations get equivalent quality to
		// observations saved through the main pipeline.
		for _, hook := range m.postSaveHooks {
			hook(ctx, id, obs.title, obs.content, namespace)
		}
	}

	// If any INSERT failed, return the error with the count of successful inserts.
	if firstInsertErr != nil {
		return saved, fmt.Errorf("observation insertion failed (saved %d/%d): %w", saved, len(observations), firstInsertErr)
	}
	return saved, nil
}

type extraction struct {
	obsType string
	title   string
	content string
}

func parseExtractions(response string) []extraction {
	if strings.TrimSpace(response) == "" {
		return nil
	}
	// Check if the response is exactly "NONE" (or just whitespace + NONE).
	// Do not treat "Nonetheless" or other words containing "NONE" as a signal for no extractions.
	trimmed := strings.TrimSpace(response)
	if strings.EqualFold(trimmed, "NONE") {
		return nil
	}

	validTypes := map[string]bool{
		"decision": true, "bugfix": true, "discovery": true, "pattern": true,
		"gotcha": true, "config": true, "preference": true, "question": true,
	}

	var extractions []extraction
	lines := strings.Split(response, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		line = strings.TrimLeft(line, "- •*0123456789.")
		line = strings.TrimSpace(line)

		if line == "" {
			continue
		}

		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			continue
		}

		obsType := strings.TrimSpace(strings.ToLower(parts[0]))
		title := strings.TrimSpace(parts[1])
		content := strings.TrimSpace(parts[2])

		if !validTypes[obsType] {
			obsType = "discovery"
		}
		if title == "" || content == "" {
			continue
		}

		extractions = append(extractions, extraction{
			obsType: obsType,
			title:   title,
			content: content,
		})

		if len(extractions) >= 8 {
			break
		}
	}

	return extractions
}

func nullStr(s string) any {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return nil
	}
	return trimmed
}

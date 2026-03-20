package session

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"neurox/internal/filelink"
	"neurox/internal/llm"
	"neurox/internal/temporal"
)

// Manager handles session lifecycle with LLM-based extraction.
type Manager struct {
	db        *sql.DB
	llm       llm.Provider
	idGen     filelink.IDGenerator
	temporal  *temporal.Extractor
}

// NewManager creates a session manager.
func NewManager(db *sql.DB, llmProvider llm.Provider, idGen filelink.IDGenerator) *Manager {
	return &Manager{db: db, llm: llmProvider, idGen: idGen}
}

// SetTemporalExtractor configures temporal extraction for session-derived observations.
func (m *Manager) SetTemporalExtractor(te *temporal.Extractor) {
	m.temporal = te
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

// EndResult holds the result of ending a session.
type EndResult struct {
	SessionID            string
	ObservationsExtracted int
}

// End completes a session. If LLM is available, extracts atomic observations
// from the summary. Otherwise, just saves the summary.
func (m *Manager) End(ctx context.Context, sessionID, summary string) (EndResult, error) {
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

	// Extract atomic observations if LLM available
	if llm.IsAvailable(m.llm) {
		extracted, extractErr := m.extractObservations(ctx, summary, namespace)
		if extractErr == nil {
			endResult.ObservationsExtracted = extracted
		}
	}

	return endResult, nil
}

// extractObservations uses LLM to extract atomic observations from a session summary.
func (m *Manager) extractObservations(ctx context.Context, summary, namespace string) (int, error) {
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
	for _, obs := range observations {
		id := m.idGen.New()
		_, err := m.db.ExecContext(ctx, `
			INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, source)
			VALUES(?, ?, ?, ?, 0, 0.6, 0.5, 'semantic', ?, 'consolidator')
		`, id, obs.title, obs.content, obs.obsType, namespace)
		if err == nil {
			saved++
			// Best-effort temporal extraction — do not block on failure.
			if m.temporal != nil {
				_, _ = m.temporal.Extract(ctx, id, obs.content)
			}
		}
	}

	return saved, nil
}

type extraction struct {
	obsType string
	title   string
	content string
}

func parseExtractions(response string) []extraction {
	if strings.TrimSpace(response) == "" || strings.Contains(strings.ToUpper(response), "NONE") {
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

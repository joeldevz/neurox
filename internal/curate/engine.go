package curate

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/joeldevz/neurox/internal/llm"
	"github.com/oklog/ulid/v2"
)

// Engine is the core curation engine. It loads observations for a namespace,
// asks the LLM which to DELETE or KEEP (with recalibrated importance), and
// optionally applies those changes to the database.
type Engine struct {
	db         *sql.DB
	curator    llm.Provider
	priorities Priorities
	modelName  string
}

// NewEngine creates a new curation Engine. modelName is recorded in the
// curation_runs table for audit purposes (pass an empty string if unknown).
func NewEngine(db *sql.DB, curator llm.Provider, priorities Priorities, modelName string) *Engine {
	return &Engine{
		db:         db,
		curator:    curator,
		priorities: priorities,
		modelName:  modelName,
	}
}

// newID returns a new ULID string suitable for use as a database primary key.
func newID() string {
	return ulid.Make().String()
}

// Decision represents the LLM's verdict for a single observation.
type Decision struct {
	ID            string  `json:"id"`
	Action        string  `json:"action"`         // "DELETE" or "KEEP"
	NewImportance float64 `json:"new_importance"` // 0.0-1.0, meaningful only for KEEP
	Reason        string  `json:"reason"`
}

// NamespaceReport summarises the curation results for one namespace.
type NamespaceReport struct {
	Namespace    string     `json:"namespace"`
	Before       int        `json:"before"`
	Deleted      int        `json:"deleted"`
	Recalibrated int        `json:"recalibrated"`
	Protected    int        `json:"protected"`
	TopTopics    []string   `json:"top_topics,omitempty"`
	Decisions    []Decision `json:"decisions"`
	Error        string     `json:"error,omitempty"`
}

// FullReport aggregates results across all curated namespaces.
type FullReport struct {
	Namespaces        []NamespaceReport `json:"namespaces"`
	TotalBefore       int               `json:"total_before"`
	TotalDeleted      int               `json:"total_deleted"`
	TotalRecalibrated int               `json:"total_recalibrated"`
}

// observation is the internal representation of a DB row loaded for curation.
type observation struct {
	ID              string
	Title           string
	Content         string
	ObservationType string
	Importance      float64
	AccessCount     int
	Layer           int
	Tags            string // raw comma-separated
	Retention       string
	Staleness       string
}

// CurateNamespace runs curation for a single namespace. When dryRun is true,
// the LLM decisions are computed and returned but no DB changes are made.
func (e *Engine) CurateNamespace(ctx context.Context, namespace string, dryRun bool) (NamespaceReport, error) {
	report := NamespaceReport{Namespace: namespace}

	// Load all active observations for this namespace.
	observations, err := e.loadObservations(ctx, namespace)
	if err != nil {
		return report, fmt.Errorf("load observations: %w", err)
	}

	report.Before = len(observations)
	if len(observations) == 0 {
		return report, nil
	}

	// Build an id → observation index for fast lookup and skip detection.
	obsIndex := make(map[string]observation, len(observations))
	for _, o := range observations {
		obsIndex[o.ID] = o
	}

	// Get priorities for this namespace (namespace-specific + global).
	priorities := e.priorities.ForNamespace(namespace)

	// Build and send the curation prompt.
	prompt := buildPrompt(observations, priorities)

	response, err := e.curator.Complete(ctx, prompt)
	if err != nil {
		return report, fmt.Errorf("llm complete: %w", err)
	}

	// Parse the LLM response into decisions.
	decisions, err := parseDecisions(response)
	if err != nil {
		return report, fmt.Errorf("parse decisions: %w", err)
	}

	// Validate and filter decisions.
	validated := make([]Decision, 0, len(decisions))
	for _, d := range decisions {
		// Skip references to unknown observation IDs (defensive).
		if _, ok := obsIndex[d.ID]; !ok {
			log.Printf("curate: skipping decision for unknown observation id %q in namespace %q", d.ID, namespace)
			continue
		}
		// Normalise and validate action.
		d.Action = strings.ToUpper(strings.TrimSpace(d.Action))
		if d.Action != "DELETE" && d.Action != "KEEP" {
			log.Printf("curate: skipping decision with invalid action %q for observation %q", d.Action, d.ID)
			continue
		}
		// For KEEP, validate importance range.
		if d.Action == "KEEP" {
			if d.NewImportance < 0.0 || d.NewImportance > 1.0 {
				log.Printf("curate: clamping out-of-range importance %.3f for observation %q", d.NewImportance, d.ID)
				d.NewImportance = clamp(d.NewImportance, 0.0, 1.0)
			}
		}
		validated = append(validated, d)
	}

	// Apply decisions to the database (unless dry-run).
	if !dryRun {
		if err := e.applyDecisions(ctx, validated); err != nil {
			return report, fmt.Errorf("apply decisions: %w", err)
		}
	}

	// Build report counters.
	for _, d := range validated {
		switch d.Action {
		case "DELETE":
			report.Deleted++
		case "KEEP":
			report.Recalibrated++
			if d.NewImportance >= 0.7 {
				report.Protected++
			}
		}
	}
	report.Decisions = validated

	// Save the audit log (always — even in dry-run mode).
	if err := e.saveCurationRun(ctx, report, dryRun, obsIndex); err != nil {
		// Log the error but don't fail the curation; the decisions were
		// already applied and the report is valid.
		log.Printf("curate: failed to save curation run for namespace %q: %v", namespace, err)
	}

	return report, nil
}

// CurateAll runs curation across every active namespace. If one namespace fails
// the error is recorded in its NamespaceReport but the other namespaces are
// still processed.
func (e *Engine) CurateAll(ctx context.Context, dryRun bool) (FullReport, error) {
	namespaces, err := e.activeNamespaces(ctx)
	if err != nil {
		return FullReport{}, fmt.Errorf("list namespaces: %w", err)
	}

	full := FullReport{
		Namespaces: make([]NamespaceReport, 0, len(namespaces)),
	}

	for _, ns := range namespaces {
		report, nsErr := e.CurateNamespace(ctx, ns, dryRun)
		if nsErr != nil {
			log.Printf("curate: namespace %q failed: %v", ns, nsErr)
			report.Error = nsErr.Error()
		}
		full.Namespaces = append(full.Namespaces, report)
		full.TotalBefore += report.Before
		full.TotalDeleted += report.Deleted
		full.TotalRecalibrated += report.Recalibrated
	}

	return full, nil
}

// loadObservations fetches all non-deleted observations for a namespace ordered
// by importance descending then created_at descending (highest value first).
func (e *Engine) loadObservations(ctx context.Context, namespace string) ([]observation, error) {
	rows, err := e.db.QueryContext(ctx, `
		SELECT id, title, content, observation_type, importance, access_count, layer, tags, retention, staleness
		FROM observations
		WHERE deleted_at IS NULL AND namespace = ?
		ORDER BY importance DESC, created_at DESC
	`, namespace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []observation
	for rows.Next() {
		var o observation
		// tags, retention, and staleness can be NULL in old rows.
		var tags, retention, staleness sql.NullString
		if err := rows.Scan(
			&o.ID, &o.Title, &o.Content, &o.ObservationType,
			&o.Importance, &o.AccessCount, &o.Layer,
			&tags, &retention, &staleness,
		); err != nil {
			return nil, err
		}
		o.Tags = tags.String
		o.Retention = retention.String
		o.Staleness = staleness.String
		out = append(out, o)
	}
	return out, rows.Err()
}

// activeNamespaces returns the distinct namespaces that have at least one
// non-deleted observation.
func (e *Engine) activeNamespaces(ctx context.Context) ([]string, error) {
	rows, err := e.db.QueryContext(ctx, `
		SELECT DISTINCT namespace FROM observations
		WHERE deleted_at IS NULL
		ORDER BY namespace
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var namespaces []string
	for rows.Next() {
		var ns string
		if err := rows.Scan(&ns); err != nil {
			continue
		}
		namespaces = append(namespaces, ns)
	}
	return namespaces, rows.Err()
}

// applyDecisions executes all DELETE and KEEP decisions inside a single
// database transaction for atomicity.
func (e *Engine) applyDecisions(ctx context.Context, decisions []Decision) error {
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	for _, d := range decisions {
		switch d.Action {
		case "DELETE":
			if _, err = tx.ExecContext(ctx, `
				UPDATE observations
				SET deleted_at = datetime('now'), updated_at = datetime('now')
				WHERE id = ?
			`, d.ID); err != nil {
				return fmt.Errorf("soft-delete observation %s: %w", d.ID, err)
			}
		case "KEEP":
			if _, err = tx.ExecContext(ctx, `
				UPDATE observations
				SET importance = ?, updated_at = datetime('now')
				WHERE id = ?
			`, d.NewImportance, d.ID); err != nil {
				return fmt.Errorf("update importance for observation %s: %w", d.ID, err)
			}
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// saveCurationRun persists a completed curation run and its decisions to the
// database. It is called after curation succeeds (or after a dry-run) so that
// there is always an audit trail regardless of whether changes were applied.
func (e *Engine) saveCurationRun(ctx context.Context, report NamespaceReport, dryRun bool, obsIndex map[string]observation) error {
	runID := newID()

	dryRunInt := 0
	if dryRun {
		dryRunInt = 1
	}

	var modelVal interface{}
	if e.modelName != "" {
		modelVal = e.modelName
	}

	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO curation_runs (
			id, namespace, status,
			observations_before, observations_deleted,
			observations_recalibrated, observations_protected,
			dry_run, curator_model,
			completed_at
		) VALUES (?, ?, 'completed', ?, ?, ?, ?, ?, ?, datetime('now'))
	`,
		runID, report.Namespace,
		report.Before, report.Deleted,
		report.Recalibrated, report.Protected,
		dryRunInt, modelVal,
	)
	if err != nil {
		return fmt.Errorf("insert curation_run: %w", err)
	}

	for _, d := range report.Decisions {
		decisionID := newID()

		var oldImportance interface{}
		if obs, ok := obsIndex[d.ID]; ok {
			oldImportance = obs.Importance
		}

		var newImportance interface{}
		if d.Action == "KEEP" {
			newImportance = d.NewImportance
		}

		_, err = tx.ExecContext(ctx, `
			INSERT INTO curation_decisions (
				id, run_id, observation_id,
				action, old_importance, new_importance, reason
			) VALUES (?, ?, ?, ?, ?, ?, ?)
		`,
			decisionID, runID, d.ID,
			d.Action, oldImportance, newImportance, d.Reason,
		)
		if err != nil {
			return fmt.Errorf("insert curation_decision for observation %s: %w", d.ID, err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit curation run: %w", err)
	}
	return nil
}

// parseDecisions parses the LLM response into a slice of Decision values.
// It is deliberately robust:
//   - strips markdown code fences (```json ... ```)
//   - extracts the JSON array between the first '[' and last ']'
//   - returns an error if no valid JSON array is found
func parseDecisions(response string) ([]Decision, error) {
	s := response

	// Strip markdown code fences: ```json ... ``` or ``` ... ```.
	if idx := strings.Index(s, "```"); idx >= 0 {
		s = s[idx:]
		// Skip the opening fence line.
		if nl := strings.Index(s, "\n"); nl >= 0 {
			s = s[nl+1:]
		}
		// Strip the closing fence.
		if end := strings.LastIndex(s, "```"); end >= 0 {
			s = s[:end]
		}
	}

	// Extract the JSON array between the first '[' and the last ']'.
	start := strings.Index(s, "[")
	end := strings.LastIndex(s, "]")
	if start < 0 || end < 0 || end <= start {
		return nil, fmt.Errorf("no JSON array found in LLM response")
	}
	jsonStr := s[start : end+1]

	var decisions []Decision
	if err := json.Unmarshal([]byte(jsonStr), &decisions); err != nil {
		return nil, fmt.Errorf("unmarshal decisions: %w", err)
	}
	return decisions, nil
}

// clamp constrains v to [min, max].
func clamp(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

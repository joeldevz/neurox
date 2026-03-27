package reflect

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/joeldevz/neurox/internal/filelink"
	"github.com/joeldevz/neurox/internal/links"
	"github.com/joeldevz/neurox/internal/llm"
)

const (
	// DefaultTriggerThreshold is the number of new Working-layer observations
	// needed to trigger automatic reflection.
	DefaultTriggerThreshold = 20

	// MaxSourceObservations is the max observations to feed into a reflection prompt.
	MaxSourceObservations = 30

	// ForceReflectCooldown is the minimum time between ForceReflect calls for the same namespace.
	ForceReflectCooldown = 2 * time.Hour
)

// Engine generates reflections — high-level insights synthesized from
// multiple observations. Inspired by Stanford's Generative Agents.
type Engine struct {
	db        *sql.DB
	llm       llm.Provider
	linkStore *links.Store
	idGen     filelink.IDGenerator
	threshold int
}

// NewEngine creates a reflection engine.
func NewEngine(db *sql.DB, llmProvider llm.Provider, linkStore *links.Store, idGen filelink.IDGenerator) *Engine {
	return &Engine{
		db:        db,
		llm:       llmProvider,
		linkStore: linkStore,
		idGen:     idGen,
		threshold: DefaultTriggerThreshold,
	}
}

// LLM returns the engine's LLM provider (for availability checks).
func (e *Engine) LLM() llm.Provider { return e.llm }

// ReflectResult holds stats from a reflection run.
type ReflectResult struct {
	SourceCount        int
	ReflectionsCreated int
}

// Reflection represents a synthesized insight.
type Reflection struct {
	ID                   string
	Content              string
	SourceObservationIDs []string
	Namespace            string
	CreatedAt            string
}

// Run checks if there are enough unreflected Working-layer observations
// in the given namespace and generates reflections if so.
func (e *Engine) Run(ctx context.Context, namespace string) (ReflectResult, error) {
	if !llm.IsAvailable(e.llm) {
		return ReflectResult{}, nil
	}

	if namespace == "" {
		namespace = "default"
	}

	// Count unreflected Working observations
	sources, err := e.getUnreflectedSources(ctx, namespace)
	if err != nil {
		return ReflectResult{}, fmt.Errorf("get unreflected sources: %w", err)
	}

	result := ReflectResult{SourceCount: len(sources)}

	if len(sources) < e.threshold {
		return result, nil
	}

	// Generate reflection
	reflection, err := e.synthesize(ctx, sources, namespace)
	if err != nil {
		return result, fmt.Errorf("synthesize reflection: %w", err)
	}

	// Quality guard: reject empty or trivially short reflections.
	if strings.TrimSpace(reflection) == "" || len(strings.TrimSpace(reflection)) < 50 {
		log.Printf("reflection rejected: content too short (%d chars) in namespace %s", len(strings.TrimSpace(reflection)), namespace)
		return result, nil
	}

	// Save the reflection
	if err := e.saveReflection(ctx, reflection, sources, namespace); err != nil {
		return result, fmt.Errorf("save reflection: %w", err)
	}

	result.ReflectionsCreated = 1
	return result, nil
}

// ForceReflect generates a reflection regardless of the threshold.
func (e *Engine) ForceReflect(ctx context.Context, namespace string) (ReflectResult, error) {
	if !llm.IsAvailable(e.llm) {
		return ReflectResult{}, fmt.Errorf("reflection requires an LLM provider")
	}

	if namespace == "" {
		namespace = "default"
	}

	// Check cooldown: skip if a reflection was created recently
	lastReflection, exists, err := e.lastReflectionAt(ctx, namespace)
	if err != nil {
		return ReflectResult{}, fmt.Errorf("check cooldown: %w", err)
	}
	if exists && time.Since(lastReflection) < ForceReflectCooldown {
		return ReflectResult{ReflectionsCreated: 0}, nil
	}

	sources, err := e.getRecentSources(ctx, namespace, MaxSourceObservations)
	if err != nil {
		return ReflectResult{}, fmt.Errorf("get sources: %w", err)
	}

	result := ReflectResult{SourceCount: len(sources)}

	if len(sources) < 3 {
		return result, fmt.Errorf("need at least 3 observations to reflect, have %d", len(sources))
	}

	reflection, err := e.synthesize(ctx, sources, namespace)
	if err != nil {
		return result, fmt.Errorf("synthesize: %w", err)
	}

	// Quality guard: reject empty or trivially short reflections.
	if strings.TrimSpace(reflection) == "" || len(strings.TrimSpace(reflection)) < 50 {
		log.Printf("reflection rejected: content too short (%d chars) in namespace %s", len(strings.TrimSpace(reflection)), namespace)
		return result, nil
	}

	if err := e.saveReflection(ctx, reflection, sources, namespace); err != nil {
		return result, fmt.Errorf("save: %w", err)
	}

	result.ReflectionsCreated = 1
	return result, nil
}

type sourceObs struct {
	id      string
	title   string
	content string
	obsType string
}

func (e *Engine) getUnreflectedSources(ctx context.Context, namespace string) ([]sourceObs, error) {
	// Get Working-layer observations that don't have a derived_from link
	// pointing to them (meaning they haven't been reflected upon).
	rows, err := e.db.QueryContext(ctx, `
		SELECT o.id, o.title, o.content, o.observation_type
		FROM observations o
		WHERE o.deleted_at IS NULL
		  AND o.layer >= 1
		  AND o.namespace = ?
		  AND o.valid_until IS NULL
		  AND o.id NOT IN (
		    SELECT target_id FROM observation_links WHERE relation_type = 'derived_from'
		  )
		ORDER BY o.importance DESC, o.created_at DESC
		LIMIT ?
	`, namespace, MaxSourceObservations)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanSources(rows)
}

func (e *Engine) getRecentSources(ctx context.Context, namespace string, limit int) ([]sourceObs, error) {
	rows, err := e.db.QueryContext(ctx, `
		SELECT id, title, content, observation_type
		FROM observations
		WHERE deleted_at IS NULL
		  AND layer >= 1
		  AND namespace = ?
		  AND valid_until IS NULL
		ORDER BY importance DESC, created_at DESC
		LIMIT ?
	`, namespace, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanSources(rows)
}

func scanSources(rows *sql.Rows) ([]sourceObs, error) {
	var sources []sourceObs
	for rows.Next() {
		var s sourceObs
		if err := rows.Scan(&s.id, &s.title, &s.content, &s.obsType); err != nil {
			return nil, err
		}
		sources = append(sources, s)
	}
	return sources, rows.Err()
}

func (e *Engine) synthesize(ctx context.Context, sources []sourceObs, namespace string) (string, error) {
	var sb strings.Builder
	for i, s := range sources {
		fmt.Fprintf(&sb, "%d. [%s] %s: %s\n", i+1, s.obsType, s.title, s.content)
	}

	prompt := fmt.Sprintf(`You are a memory synthesis engine. Given the following %d observations from the "%s" project namespace, extract 3 high-level insights or patterns.

Observations:
%s
Rules:
- Each insight should synthesize multiple observations, not just repeat one
- Focus on patterns, recurring themes, architectural decisions, or learned lessons
- Be specific and actionable
- Format: numbered list, each insight as a single concise paragraph

Output format:
TITLE: <concise descriptive title for the synthesis>

**Insight 1:** ...
**Insight 2:** ...
**Insight 3:** ...`, len(sources), namespace, sb.String())

	return e.llm.Complete(ctx, prompt)
}

// extractTitle parses LLM output to extract a TITLE: prefix.
// If found, returns the trimmed title and the remaining body (after stripping the title line
// and any immediately following blank line).
// If not found, returns the fallback title and the full content unchanged.
func extractTitle(content string, namespace string) (title, body string) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return "Synthesis: " + namespace, content
	}

	firstLine := strings.TrimSpace(lines[0])
	const titlePrefix = "TITLE:"

	if strings.HasPrefix(firstLine, titlePrefix) {
		// Extract title text after prefix
		title = strings.TrimSpace(strings.TrimPrefix(firstLine, titlePrefix))

		// Find the start of the body: skip the title line and any immediately following blank lines
		bodyStart := 1
		for bodyStart < len(lines) && strings.TrimSpace(lines[bodyStart]) == "" {
			bodyStart++
		}

		// Join remaining lines as body
		if bodyStart < len(lines) {
			body = strings.Join(lines[bodyStart:], "\n")
		} else {
			body = ""
		}

		return title, body
	}

	// No TITLE: prefix found, use fallback
	return "Synthesis: " + namespace, content
}

func (e *Engine) saveReflection(ctx context.Context, content string, sources []sourceObs, namespace string) error {
	reflectionID := e.idGen.New()
	sourceIDs := make([]string, len(sources))
	for i, s := range sources {
		sourceIDs[i] = s.id
	}
	sourceIDsStr := strings.Join(sourceIDs, ",")

	// Extract title from content or use fallback
	title, body := extractTitle(content, namespace)

	// Insert into reflections table
	_, err := e.db.ExecContext(ctx, `
		INSERT INTO reflections(id, content, source_observation_ids, namespace)
		VALUES(?, ?, ?, ?)
	`, reflectionID, body, sourceIDsStr, namespace)
	if err != nil {
		return fmt.Errorf("insert reflection: %w", err)
	}

	// Also save as a Core-layer observation for recall
	obsID := e.idGen.New()
	_, err = e.db.ExecContext(ctx, `
		INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, source, retention)
		VALUES(?, ?, ?, 'pattern', 2, 0.9, 0.9, 'semantic', ?, 'reflection', 'durable')
	`, obsID, title, body, namespace)
	if err != nil {
		return fmt.Errorf("insert reflection observation: %w", err)
	}

	// Create derived_from links from the reflection to each source
	for _, s := range sources {
		_, linkErr := e.linkStore.Create(ctx, links.CreateLinkInput{
			SourceID:     obsID,
			TargetID:     s.id,
			RelationType: links.RelationDerivedFrom,
			Confidence:   1.0,
			CreatedBy:    links.CreatedByConsolidator,
		})
		if linkErr != nil {
			// Non-fatal: link might fail if source was deleted
			log.Printf("create derived_from link for reflection: %v", linkErr)
		}
	}

	log.Printf("reflection created: %s from %d sources in namespace %s", reflectionID, len(sources), namespace)
	return nil
}

// ListReflections returns recent reflections for a namespace.
func (e *Engine) ListReflections(ctx context.Context, namespace string, limit int) ([]Reflection, error) {
	if limit <= 0 {
		limit = 10
	}

	rows, err := e.db.QueryContext(ctx, `
		SELECT id, content, source_observation_ids, namespace, created_at
		FROM reflections
		WHERE namespace = ?
		ORDER BY created_at DESC
		LIMIT ?
	`, namespace, limit)
	if err != nil {
		return nil, fmt.Errorf("list reflections: %w", err)
	}
	defer rows.Close()

	var reflections []Reflection
	for rows.Next() {
		var r Reflection
		var sourceIDs string
		if err := rows.Scan(&r.ID, &r.Content, &sourceIDs, &r.Namespace, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.SourceObservationIDs = strings.Split(sourceIDs, ",")
		reflections = append(reflections, r)
	}
	return reflections, rows.Err()
}

// lastReflectionAt returns the timestamp of the most recent reflection for a namespace.
// Returns (time.Time{}, false, nil) if no reflection exists.
func (e *Engine) lastReflectionAt(ctx context.Context, namespace string) (time.Time, bool, error) {
	var maxCreatedAt sql.NullString
	err := e.db.QueryRowContext(ctx, `
		SELECT MAX(created_at) FROM reflections WHERE namespace = ?
	`, namespace).Scan(&maxCreatedAt)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("query last reflection: %w", err)
	}
	if !maxCreatedAt.Valid {
		return time.Time{}, false, nil
	}
	t, err := time.Parse(time.RFC3339, maxCreatedAt.String)
	if err != nil {
		// Try SQLite datetime format (2006-01-02 15:04:05)
		t, err = time.Parse("2006-01-02 15:04:05", maxCreatedAt.String)
		if err != nil {
			return time.Time{}, false, fmt.Errorf("parse reflection timestamp: %w", err)
		}
	}
	return t, true, nil
}

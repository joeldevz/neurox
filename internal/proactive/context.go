package proactive

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"neurox/internal/embed"
	"neurox/internal/observation"
)

// Engine provides proactive context retrieval.
type Engine struct {
	db       *sql.DB
	embedder embed.Provider
}

// NewEngine creates a proactive context engine.
func NewEngine(db *sql.DB, embedder embed.Provider) *Engine {
	return &Engine{db: db, embedder: embedder}
}

// ContextItem represents a single context result.
type ContextItem struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	Content         string   `json:"content"`
	ObservationType string   `json:"observation_type"`
	Layer           int      `json:"layer"`
	Confidence      float64  `json:"confidence"`
	Importance      float64  `json:"importance"`
	Kind            string   `json:"kind"`
	Tags            []string `json:"tags,omitempty"`
	Staleness       string   `json:"staleness"`
	Source          string   `json:"source,omitempty"`
	CreatedAt       string   `json:"created_at"`
}

// ContextResult holds the full context response.
type ContextResult struct {
	Namespace   string        `json:"namespace"`
	Count       int           `json:"count"`
	Items       []ContextItem `json:"items"`
	Reflections []ContextItem `json:"reflections,omitempty"`
}

// GetContext retrieves relevant context for a namespace, optionally filtered by files.
// It combines: file-linked observations, high-activation observations, and reflections.
func (e *Engine) GetContext(ctx context.Context, namespace string, files []string, limit int) (ContextResult, error) {
	if namespace == "" {
		namespace = "default"
	}
	if limit <= 0 {
		limit = 20
	}

	result := ContextResult{Namespace: namespace}

	// 1. Get file-linked observations (if files provided)
	var fileItems []ContextItem
	if len(files) > 0 {
		var err error
		fileItems, err = e.getFileLinked(ctx, namespace, files, limit/2)
		if err != nil {
			return result, fmt.Errorf("get file-linked: %w", err)
		}
	}

	// 2. Get top observations by activation score (layer DESC, importance DESC, access recency)
	remaining := limit - len(fileItems)
	if remaining < 5 {
		remaining = 5
	}
	topItems, err := e.getTopActivation(ctx, namespace, remaining, fileItems)
	if err != nil {
		return result, fmt.Errorf("get top activation: %w", err)
	}

	// 3. Get reflections
	reflections, err := e.getReflections(ctx, namespace, 3)
	if err != nil {
		// Non-fatal
		reflections = nil
	}

	// Merge: file-linked first, then top activation (deduped)
	seen := make(map[string]bool)
	for _, item := range fileItems {
		seen[item.ID] = true
		result.Items = append(result.Items, item)
	}
	for _, item := range topItems {
		if !seen[item.ID] {
			result.Items = append(result.Items, item)
		}
	}

	result.Count = len(result.Items)
	result.Reflections = reflections

	return result, nil
}

// GetSessionContext retrieves context for a new session using title/directory/branch
// as a context fingerprint for semantic matching (if embeddings available).
func (e *Engine) GetSessionContext(ctx context.Context, namespace, title, directory, branch string, limit int) (ContextResult, error) {
	if limit <= 0 {
		limit = 20
	}

	// Start with standard context (no file filter)
	result, err := e.GetContext(ctx, namespace, nil, limit)
	if err != nil {
		return result, err
	}

	// If embeddings available, add semantic matches based on session context
	if embed.IsAvailable(e.embedder) && (title != "" || directory != "" || branch != "") {
		contextText := strings.Join([]string{title, directory, branch}, " ")
		semanticItems, semErr := e.semanticContext(ctx, namespace, contextText, 5)
		if semErr == nil && len(semanticItems) > 0 {
			seen := make(map[string]bool)
			for _, item := range result.Items {
				seen[item.ID] = true
			}
			for _, item := range semanticItems {
				if !seen[item.ID] && len(result.Items) < limit {
					result.Items = append(result.Items, item)
				}
			}
			result.Count = len(result.Items)
		}
	}

	return result, nil
}

func (e *Engine) getFileLinked(ctx context.Context, namespace string, files []string, limit int) ([]ContextItem, error) {
	placeholders := make([]string, len(files))
	args := make([]any, 0, len(files)+2)
	args = append(args, namespace)
	for i, f := range files {
		placeholders[i] = "?"
		args = append(args, f)
	}
	args = append(args, limit)

	rows, err := e.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT DISTINCT o.id, o.title, o.content, o.observation_type, o.layer, o.confidence, o.importance, o.kind, o.tags, o.staleness, COALESCE(o.source, ''), o.created_at
		FROM observations o
		JOIN file_observations fo ON fo.observation_id = o.id AND fo.valid_until IS NULL
		WHERE o.deleted_at IS NULL AND o.namespace = ? AND o.valid_until IS NULL
		  AND fo.file_path IN (%s)
		ORDER BY o.layer DESC, o.importance DESC
		LIMIT ?
	`, strings.Join(placeholders, ",")), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanContextItems(rows)
}

func (e *Engine) getTopActivation(ctx context.Context, namespace string, limit int, exclude []ContextItem) ([]ContextItem, error) {
	excludeIDs := make([]any, 0, len(exclude)+2)
	excludeIDs = append(excludeIDs, namespace)

	excludeClause := ""
	if len(exclude) > 0 {
		placeholders := make([]string, len(exclude))
		for i, item := range exclude {
			placeholders[i] = "?"
			excludeIDs = append(excludeIDs, item.ID)
		}
		excludeClause = fmt.Sprintf(" AND o.id NOT IN (%s)", strings.Join(placeholders, ","))
	}

	excludeIDs = append(excludeIDs, limit)

	rows, err := e.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT o.id, o.title, o.content, o.observation_type, o.layer, o.confidence, o.importance, o.kind, o.tags, o.staleness, COALESCE(o.source, ''), o.created_at
		FROM observations o
		WHERE o.deleted_at IS NULL AND o.namespace = ? AND o.valid_until IS NULL
		  AND o.staleness <> 'expired'
		  %s
		ORDER BY o.layer DESC,
		         o.importance DESC,
		         CASE WHEN o.last_accessed IS NOT NULL THEN julianday('now') - julianday(o.last_accessed) ELSE 999 END ASC,
		         o.access_count DESC,
		         o.created_at DESC
		LIMIT ?
	`, excludeClause), excludeIDs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanContextItems(rows)
}

func (e *Engine) getReflections(ctx context.Context, namespace string, limit int) ([]ContextItem, error) {
	rows, err := e.db.QueryContext(ctx, `
		SELECT o.id, o.title, o.content, o.observation_type, o.layer, o.confidence, o.importance, o.kind, o.tags, o.staleness, COALESCE(o.source, ''), o.created_at
		FROM observations o
		WHERE o.deleted_at IS NULL AND o.namespace = ? AND o.source = 'reflection' AND o.valid_until IS NULL
		ORDER BY o.created_at DESC
		LIMIT ?
	`, namespace, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanContextItems(rows)
}

func (e *Engine) semanticContext(ctx context.Context, namespace, queryText string, limit int) ([]ContextItem, error) {
	queryVec, err := e.embedder.Embed(ctx, queryText)
	if err != nil {
		return nil, err
	}

	rows, err := e.db.QueryContext(ctx, `
		SELECT id, title, content, observation_type, layer, confidence, importance, kind, tags, staleness, COALESCE(source, ''), created_at, embedding
		FROM observations
		WHERE deleted_at IS NULL AND namespace = ? AND embedding IS NOT NULL AND valid_until IS NULL
		ORDER BY layer DESC, importance DESC
	`, namespace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type scored struct {
		item  ContextItem
		score float64
	}
	var candidates []scored

	for rows.Next() {
		var item ContextItem
		var tags, source sql.NullString
		var blob []byte
		if err := rows.Scan(&item.ID, &item.Title, &item.Content, &item.ObservationType, &item.Layer, &item.Confidence, &item.Importance, &item.Kind, &tags, &item.Staleness, &source, &item.CreatedAt, &blob); err != nil {
			continue
		}
		if tags.Valid {
			item.Tags = observation.ParseTags(tags.String)
		}
		item.Source = source.String

		vec := embed.DeserializeF32(blob)
		if vec == nil {
			continue
		}
		sim := embed.CosineSimilarity(queryVec, vec)
		if sim > 0.3 {
			candidates = append(candidates, scored{item: item, score: sim})
		}
	}

	// Simple top-N selection
	var result []ContextItem
	for i := 0; i < limit && i < len(candidates); i++ {
		maxIdx := i
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].score > candidates[maxIdx].score {
				maxIdx = j
			}
		}
		candidates[i], candidates[maxIdx] = candidates[maxIdx], candidates[i]
		result = append(result, candidates[i].item)
	}

	return result, nil
}

func scanContextItems(rows *sql.Rows) ([]ContextItem, error) {
	var items []ContextItem
	for rows.Next() {
		var item ContextItem
		var tags, source sql.NullString
		if err := rows.Scan(&item.ID, &item.Title, &item.Content, &item.ObservationType, &item.Layer, &item.Confidence, &item.Importance, &item.Kind, &tags, &item.Staleness, &source, &item.CreatedAt); err != nil {
			return nil, err
		}
		if tags.Valid {
			item.Tags = observation.ParseTags(tags.String)
		}
		item.Source = source.String
		items = append(items, item)
	}
	return items, rows.Err()
}

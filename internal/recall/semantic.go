package recall

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"

	"github.com/joeldevz/neurox/internal/embed"
)

const (
	crossSignalBoost       = 1.2   // multiplier when result appears in both FTS and semantic
	maxEmbeddingsPerSearch = 10000 // hard cap to prevent excessive memory usage
	minSemanticSimilarity  = 0.4   // minimum cosine similarity threshold (conservative, typically 0.4-0.7 for related content)
)

// semanticFilter contains prefilter parameters for semantic search.
type semanticFilter struct {
	Namespace    string
	IncludeStale bool
	Staleness    string // exact staleness value to filter on, e.g. "fresh"
}

// semanticSearch performs cosine similarity search against stored embeddings,
// prefiltered by namespace and staleness to avoid loading the entire database.
// Returns a map of observation ID → cosine similarity score.
func semanticSearch(ctx context.Context, db *sql.DB, provider embed.Provider, query string, limit int, filter semanticFilter) (map[string]float64, error) {
	// Embed the query
	queryVec, err := provider.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	// Build prefiltered query with namespace and staleness clauses
	clauses := []string{
		"deleted_at IS NULL",
		"embedding IS NOT NULL",
	}
	var args []any

	if filter.Namespace != "" {
		clauses = append(clauses, "namespace = ?")
		args = append(args, filter.Namespace)
	}
	if filter.Staleness != "" {
		clauses = append(clauses, "staleness = ?")
		args = append(args, filter.Staleness)
	} else if !filter.IncludeStale {
		clauses = append(clauses, "staleness NOT IN ('stale', 'expired')")
	}

	q := `SELECT id, embedding FROM observations WHERE ` +
		strings.Join(clauses, " AND ") +
		` ORDER BY importance DESC LIMIT ?`
	args = append(args, maxEmbeddingsPerSearch+1) // +1 to detect overflow

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query embeddings: %w", err)
	}
	defer rows.Close()

	type scored struct {
		id    string
		score float64
	}
	var candidates []scored
	loaded := 0

	for rows.Next() {
		loaded++
		if loaded > maxEmbeddingsPerSearch {
			log.Printf("WARNING: semantic search hit %d embedding cap (namespace=%q); results may be incomplete — consider narrowing your namespace or adding a vector index",
				maxEmbeddingsPerSearch, filter.Namespace)
			break
		}

		var id string
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			continue
		}
		vec := embed.DeserializeF32(blob)
		if vec == nil {
			continue
		}
		sim := embed.CosineSimilarity(queryVec, vec)
		if sim > minSemanticSimilarity {
			candidates = append(candidates, scored{id: id, score: sim})
		}
	}

	// Sort by score descending and take top N
	// Simple selection sort since limit is small
	results := make(map[string]float64, limit)
	for i := 0; i < limit && i < len(candidates); i++ {
		maxIdx := i
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].score > candidates[maxIdx].score {
				maxIdx = j
			}
		}
		candidates[i], candidates[maxIdx] = candidates[maxIdx], candidates[i]
		results[candidates[i].id] = candidates[i].score
	}

	return results, nil
}

// loadObservationsByIDs loads full observation data for the given IDs, returning
// them as candidates suitable for scoring. This is used to hydrate semantic-only
// results (those that didn't appear in FTS search) into the candidate pipeline.
// Filters (namespace, staleness, retention, etc.) from SearchOptions are applied
// to ensure consistency with the FTS path.
func loadObservationsByIDs(ctx context.Context, db *sql.DB, ids []string, options SearchOptions) ([]candidate, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	clauses := []string{
		"o.id IN (" + strings.Join(placeholders, ",") + ")",
		"o.deleted_at IS NULL",
	}

	// Apply the same filters as the FTS path for consistency
	if options.ObservationType != "" {
		clauses = append(clauses, "o.observation_type = ?")
		args = append(args, string(options.ObservationType))
	}
	if options.Kind != "" {
		clauses = append(clauses, "o.kind = ?")
		args = append(args, string(options.Kind))
	}
	if options.Staleness != "" {
		clauses = append(clauses, "o.staleness = ?")
		args = append(args, options.Staleness)
	} else if !options.IncludeStale {
		clauses = append(clauses, "o.staleness NOT IN ('stale', 'expired')")
	}
	if options.Retention != "" {
		clauses = append(clauses, "o.retention = ?")
		args = append(args, options.Retention)
	}
	if len(options.Files) > 0 {
		filePlaceholders := make([]string, len(options.Files))
		for i, file := range options.Files {
			filePlaceholders[i] = "?"
			args = append(args, file)
		}
		clauses = append(clauses, fmt.Sprintf(`EXISTS (
			SELECT 1
			FROM file_observations f_filter
			WHERE f_filter.observation_id = o.id
			  AND f_filter.valid_until IS NULL
			  AND f_filter.file_path IN (%s)
		)`, strings.Join(filePlaceholders, ",")))
	}

	q := `
		SELECT
			o.rowid,
			o.id,
			o.title,
			o.content,
			o.layer,
			o.observation_type,
			o.kind,
			o.confidence,
			o.importance,
			o.tags,
			o.staleness,
			o.retention,
			COALESCE(group_concat(DISTINCT f_output.file_path), '') AS linked_files,
			0 AS relevance,
			o.created_at,
			o.last_accessed,
			o.access_count,
			o.source_surface,
			o.source_session_id,
			o.source_tool
		FROM observations o
		LEFT JOIN file_observations f_output
			ON f_output.observation_id = o.id
			AND f_output.valid_until IS NULL
		WHERE ` + strings.Join(clauses, " AND ") + `
		GROUP BY o.id
	`

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("load observations by IDs: %w", err)
	}
	defer rows.Close()

	var results []candidate
	for rows.Next() {
		item, scanErr := scanCandidate(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		results = append(results, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate semantic fallback rows: %w", err)
	}

	return results, nil
}

func loadNamespaceBackfill(ctx context.Context, db *sql.DB, options SearchOptions, excludeIDs map[string]struct{}) ([]candidate, error) {
	remaining := options.Limit - len(excludeIDs)
	if remaining <= 0 {
		return nil, nil
	}

	clauses := []string{
		"o.deleted_at IS NULL",
		"o.namespace = ?",
	}
	args := []any{options.Namespace}

	if options.Retention != "" {
		clauses = append(clauses, "o.retention = ?")
		args = append(args, options.Retention)
	} else {
		clauses = append(clauses, "o.retention = ?")
		args = append(args, "durable")
	}
	if options.ObservationType != "" {
		clauses = append(clauses, "o.observation_type = ?")
		args = append(args, string(options.ObservationType))
	}
	if options.Kind != "" {
		clauses = append(clauses, "o.kind = ?")
		args = append(args, string(options.Kind))
	}
	if options.Staleness != "" {
		clauses = append(clauses, "o.staleness = ?")
		args = append(args, options.Staleness)
	} else if !options.IncludeStale {
		clauses = append(clauses, "o.staleness NOT IN ('stale', 'expired')")
	}
	clauses = append(clauses, "(o.valid_until IS NULL OR o.valid_until > datetime('now'))")

	if len(excludeIDs) > 0 {
		placeholders := make([]string, 0, len(excludeIDs))
		for id := range excludeIDs {
			placeholders = append(placeholders, "?")
			args = append(args, id)
		}
		clauses = append(clauses, "o.id NOT IN ("+strings.Join(placeholders, ",")+")")
	}

	q := `
		SELECT
			o.rowid,
			o.id,
			o.title,
			o.content,
			o.layer,
			o.observation_type,
			o.kind,
			o.confidence,
			o.importance,
			o.tags,
			o.staleness,
			o.retention,
			COALESCE(group_concat(DISTINCT f_output.file_path), '') AS linked_files,
			0 AS relevance,
			o.created_at,
			o.last_accessed,
			o.access_count,
			o.source_surface,
			o.source_session_id,
			o.source_tool
		FROM observations o
		LEFT JOIN file_observations f_output
			ON f_output.observation_id = o.id
			AND f_output.valid_until IS NULL
		WHERE ` + strings.Join(clauses, " AND ") + `
		GROUP BY o.id
		ORDER BY o.importance DESC, o.last_accessed DESC, o.created_at DESC
		LIMIT ?
	`
	args = append(args, remaining)

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("load namespace backfill: %w", err)
	}
	defer rows.Close()

	results := make([]candidate, 0, remaining)
	for rows.Next() {
		item, scanErr := scanCandidate(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		item.NamespaceBackfill = true
		results = append(results, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate namespace backfill rows: %w", err)
	}

	return results, nil
}

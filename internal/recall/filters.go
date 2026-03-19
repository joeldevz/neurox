package recall

import (
	"fmt"
	"strings"
)

func buildSearchQuery(options SearchOptions) (string, []any) {
	clauses := []string{
		"observations_fts MATCH ?",
		"o.deleted_at IS NULL",
		"(o.valid_until IS NULL OR o.valid_until > datetime('now'))",
	}
	args := []any{buildFTSMatchQuery(options.Query)}

	if options.Namespace != "" {
		clauses = append(clauses, "o.namespace = ?")
		args = append(args, options.Namespace)
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
		clauses = append(clauses, "o.staleness <> 'expired'")
	}
	if len(options.Files) > 0 {
		placeholders := make([]string, 0, len(options.Files))
		for _, file := range options.Files {
			placeholders = append(placeholders, "?")
			args = append(args, file)
		}
		clauses = append(clauses, fmt.Sprintf(`EXISTS (
			SELECT 1
			FROM file_observations f_filter
			WHERE f_filter.observation_id = o.id
			  AND f_filter.valid_until IS NULL
			  AND f_filter.file_path IN (%s)
		)`, strings.Join(placeholders, ",")))
	}

	query := `
		WITH matched AS (
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
				bm25(observations_fts, 2.0, 1.0, 0.5) AS relevance,
				o.created_at,
				o.last_accessed,
				o.access_count
			FROM observations_fts
			JOIN observations o ON o.rowid = observations_fts.rowid
			WHERE ` + strings.Join(clauses, " AND ") + `
			ORDER BY relevance ASC, o.importance DESC, o.created_at DESC
			LIMIT ?
		)
		SELECT
			matched.rowid,
			matched.id,
			matched.title,
			matched.content,
			matched.layer,
			matched.observation_type,
			matched.kind,
			matched.confidence,
			matched.importance,
			matched.tags,
			matched.staleness,
			COALESCE(group_concat(DISTINCT f_output.file_path), '') AS linked_files,
			matched.relevance,
			matched.created_at,
			matched.last_accessed,
			matched.access_count
		FROM matched
		LEFT JOIN file_observations f_output
			ON f_output.observation_id = matched.id
			AND f_output.valid_until IS NULL
		GROUP BY matched.rowid
		ORDER BY matched.relevance ASC, matched.importance DESC, matched.created_at DESC
	`
	args = append(args, options.Limit)
	return query, args
}

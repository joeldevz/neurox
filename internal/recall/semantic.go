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
		clauses = append(clauses, "staleness <> 'expired'")
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
		if sim > 0.1 { // minimum threshold
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

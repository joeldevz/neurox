package recall

import (
	"context"
	"database/sql"
	"fmt"

	"neurox/internal/embed"
)

const (
	crossSignalBoost = 1.2 // multiplier when result appears in both FTS and semantic
)

// semanticSearch performs brute-force cosine similarity search against stored embeddings.
// Returns a map of observation ID → cosine similarity score.
func semanticSearch(ctx context.Context, db *sql.DB, provider embed.Provider, query string, limit int) (map[string]float64, error) {
	// Embed the query
	queryVec, err := provider.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	// Load all observations with embeddings (brute force for now, OK for <50k)
	rows, err := db.QueryContext(ctx, `
		SELECT id, embedding FROM observations
		WHERE deleted_at IS NULL AND embedding IS NOT NULL
		ORDER BY importance DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("query embeddings: %w", err)
	}
	defer rows.Close()

	type scored struct {
		id    string
		score float64
	}
	var candidates []scored

	for rows.Next() {
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

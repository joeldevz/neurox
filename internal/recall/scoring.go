package recall

import (
	"math"
	"time"
)

const (
	defaultRecencyWeight    = 0.3
	defaultImportanceWeight = 0.3
	defaultRelevanceWeight  = 0.4
	defaultHalfLifeDays     = 30.0
)

type ScoreWeights struct {
	Recency    float64
	Importance float64
	Relevance  float64
}

func (w ScoreWeights) withDefaults() ScoreWeights {
	if w.Recency == 0 && w.Importance == 0 && w.Relevance == 0 {
		return ScoreWeights{
			Recency:    defaultRecencyWeight,
			Importance: defaultImportanceWeight,
			Relevance:  defaultRelevanceWeight,
		}
	}
	return w
}

func applyScores(items []candidate, weights ScoreWeights, now time.Time, intent TemporalIntent, mentionMap map[string][]mentionInfo) {
	if len(items) == 0 {
		return
	}

	minRelevance := items[0].RawRelevance
	maxRelevance := items[0].RawRelevance
	for index := range items {
		items[index].index = index
		if items[index].RawRelevance < minRelevance {
			minRelevance = items[index].RawRelevance
		}
		if items[index].RawRelevance > maxRelevance {
			maxRelevance = items[index].RawRelevance
		}
	}

	for index := range items {
		recency := recencyScore(items[index], now)
		ftsRelevance := normalizeRelevance(items[index].RawRelevance, minRelevance, maxRelevance)

		// Hybrid: use max(FTS relevance, semantic cosine similarity) as relevance
		relevance := ftsRelevance
		if items[index].SemanticScore > relevance {
			relevance = items[index].SemanticScore
		}

		items[index].Score = (weights.Recency * recency) + (weights.Importance * items[index].Importance) + (weights.Relevance * relevance)

		// Cross-signal boost: if appears in both FTS and semantic, boost score
		if items[index].SemanticScore > 0 && ftsRelevance > 0 {
			items[index].Score *= crossSignalBoost
		}

		// Temporal multiplier: boost/penalize based on temporal intent match
		var mentions []mentionInfo
		if mentionMap != nil {
			mentions = mentionMap[items[index].ID]
		}
		items[index].Score *= temporalMultiplier(items[index], intent, mentions)
	}
}

func recencyScore(item candidate, now time.Time) float64 {
	reference := item.CreatedAt
	if item.LastAccessed != nil && item.LastAccessed.After(reference) {
		reference = *item.LastAccessed
	}
	days := now.Sub(reference).Hours() / 24
	if days <= 0 {
		return 1
	}
	return math.Exp(-math.Ln2 * (days / defaultHalfLifeDays))
}

func normalizeRelevance(raw float64, min float64, max float64) float64 {
	if max == min {
		return 1
	}
	return 1 - ((raw - min) / (max - min))
}

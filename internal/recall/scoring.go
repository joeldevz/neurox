package recall

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/joeldevz/neurox/internal/observation"
)

const (
	defaultRecencyWeight    = 0.3
	defaultImportanceWeight = 0.3
	defaultRelevanceWeight  = 0.4
	defaultHalfLifeDays     = 30.0
	typeIntentBoost         = 1.3 // multiplier when candidate type matches query intent
	namespaceBackfillBoost  = 0.35
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

func applyScores(items []candidate, weights ScoreWeights, now time.Time, intent TemporalIntent, mentionMap map[string][]mentionInfo, debug bool, query string, rrfK int) {
	if len(items) == 0 {
		return
	}

	// Detect observation type intent from the query
	typeIntent := detectTypeIntent(query)

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

		// Hybrid: Reciprocal Rank Fusion replaces max(FTS, semantic).
		// FTS-only, semantic-only, and dual-channel docs all contribute via ranks.
		rrf := rrfScore(items[index].FTSRank, items[index].SemRank, rrfK)
		relevance := rrf

		items[index].Score = (weights.Recency * recency) + (weights.Importance * items[index].Importance) + (weights.Relevance * relevance)

		// Cross-signal boost: if appears in both FTS and semantic, boost score
		csBoost := 1.0
		if items[index].SemanticScore > 0 && ftsRelevance > 0 {
			csBoost = crossSignalBoost
			items[index].Score *= csBoost
		}

		// Temporal multiplier: boost/penalize based on temporal intent match
		var mentions []mentionInfo
		if mentionMap != nil {
			mentions = mentionMap[items[index].ID]
		}
		tempMult := temporalMultiplier(items[index], intent, mentions)
		items[index].Score *= tempMult

		// Type intent boost: boost candidates whose type matches query intent
		typBoost := 1.0
		if typeIntent != "" && items[index].ObservationType == typeIntent {
			typBoost = typeIntentBoost
			items[index].Score *= typBoost
		}

		if items[index].NamespaceBackfill {
			items[index].Score *= namespaceBackfillBoost
		}

		// Populate score breakdown when debug mode is enabled
		if debug {
			items[index].Breakdown = &ScoreBreakdown{
				Recency:            recency,
				Importance:         items[index].Importance,
				Relevance:          relevance,
				RRFScore:           rrf,
				SemanticScore:      items[index].SemanticScore,
				TemporalMultiplier: tempMult,
				CrossSignalBoost:   csBoost,
				TypeIntentBoost:    typBoost,
				FinalScore:         items[index].Score,
			}
		}
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
		if max <= 0 {
			return 0
		}
		return 1
	}
	return 1 - ((raw - min) / (max - min))
}

// typeIntentKeywords maps query keywords to observation types.
// When a query contains one of these keywords, candidates of the matching
// type receive a multiplicative boost (typeIntentBoost = 1.3x).
var typeIntentKeywords = map[string]observation.ObservationType{
	"gotcha":        observation.ObservationTypeGotcha,
	"gotchas":       observation.ObservationTypeGotcha,
	"pitfall":       observation.ObservationTypeGotcha,
	"pitfalls":      observation.ObservationTypeGotcha,
	"trap":          observation.ObservationTypeGotcha,
	"traps":         observation.ObservationTypeGotcha,
	"watch out":     observation.ObservationTypeGotcha,
	"decision":      observation.ObservationTypeDecision,
	"decisions":     observation.ObservationTypeDecision,
	"decided":       observation.ObservationTypeDecision,
	"chose":         observation.ObservationTypeDecision,
	"chosen":        observation.ObservationTypeDecision,
	"bug":           observation.ObservationTypeBugfix,
	"bugs":          observation.ObservationTypeBugfix,
	"bugfix":        observation.ObservationTypeBugfix,
	"fix":           observation.ObservationTypeBugfix,
	"broke":         observation.ObservationTypeBugfix,
	"broken":        observation.ObservationTypeBugfix,
	"pattern":       observation.ObservationTypePattern,
	"patterns":      observation.ObservationTypePattern,
	"convention":    observation.ObservationTypePattern,
	"conventions":   observation.ObservationTypePattern,
	"preference":    observation.ObservationTypePreference,
	"preferences":   observation.ObservationTypePreference,
	"prefer":        observation.ObservationTypePreference,
	"prefers":       observation.ObservationTypePreference,
	"preferred":     observation.ObservationTypePreference,
	"discover":      observation.ObservationTypeDiscovery,
	"discovery":     observation.ObservationTypeDiscovery,
	"discoveries":   observation.ObservationTypeDiscovery,
	"found":         observation.ObservationTypeDiscovery,
	"learned":       observation.ObservationTypeDiscovery,
	"config":        observation.ObservationTypeConfig,
	"configuration": observation.ObservationTypeConfig,
	"setup":         observation.ObservationTypeConfig,
	"configured":    observation.ObservationTypeConfig,
	"question":      observation.ObservationTypeQuestion,
	"questions":     observation.ObservationTypeQuestion,
	"asked":         observation.ObservationTypeQuestion,
	"wondering":     observation.ObservationTypeQuestion,
}

// detectTypeIntent scans the query for keywords that map to an observation type.
// Multi-word keywords (e.g. "watch out") are checked first. Returns empty string
// if no type intent is detected.
func detectTypeIntent(query string) observation.ObservationType {
	lower := strings.ToLower(query)

	// Check multi-word keywords first (before splitting into tokens)
	for keyword, obsType := range typeIntentKeywords {
		if strings.Contains(keyword, " ") && strings.Contains(lower, keyword) {
			return obsType
		}
	}

	// Check single-word keywords by scanning query tokens
	for _, token := range strings.Fields(lower) {
		if obsType, ok := typeIntentKeywords[token]; ok {
			return obsType
		}
	}

	return ""
}

// rrfScore computes the Reciprocal Rank Fusion score for a single document.
// Returns 1/(k+rank_fts) + 1/(k+rank_sem), where rank_fts and rank_sem are
// 1-based integers; 0 means absent in that channel and contributes nothing.
// k is the smoothing constant (60 is the zero-shot production default,
// Cormack et al. 2009, Bruch et al. 2022).
func rrfScore(ftsRank, semRank, k int) float64 {
	score := 0.0
	if ftsRank > 0 {
		score += 1.0 / float64(k+ftsRank)
	}
	if semRank > 0 {
		score += 1.0 / float64(k+semRank)
	}
	return score
}

// deriveSemanticRanks produces 1-based ranks from a cosine-similarity map.
// Highest score = rank 1. Ties are broken by ID ascending for determinism.
// Returns a map from observation ID to rank.
func deriveSemanticRanks(semScores map[string]float64) map[string]int {
	type idScore struct {
		id    string
		score float64
	}
	sorted := make([]idScore, 0, len(semScores))
	for id, score := range semScores {
		sorted = append(sorted, idScore{id, score})
	}
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].score != sorted[j].score {
			return sorted[i].score > sorted[j].score
		}
		return sorted[i].id < sorted[j].id
	})
	ranks := make(map[string]int, len(sorted))
	for i, item := range sorted {
		ranks[item.id] = i + 1
	}
	return ranks
}

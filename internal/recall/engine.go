package recall

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/joeldevz/neurox/internal/embed"
	"github.com/joeldevz/neurox/internal/facts"
	"github.com/joeldevz/neurox/internal/observation"
)

const (
	defaultLimit = 10
	maxLimit     = 50
)

type Engine struct {
	db        *sql.DB
	embedder  embed.Provider
	factStore *facts.Store
}

type SearchOptions struct {
	Query           string
	ObservationType observation.ObservationType
	Kind            observation.Kind
	Namespace       string
	Staleness       string
	Retention       string // optional filter: "operational" or "durable"
	IncludeStale    bool
	Debug           bool // when true, results include ScoreBreakdown
	Files           []string
	Limit           int
	Weights         ScoreWeights
	Now             time.Time
}

// ScoreBreakdown exposes the individual scoring components for a recall result.
// Only populated when Debug mode is enabled in SearchOptions.
type ScoreBreakdown struct {
	Recency            float64 `json:"recency"`
	Importance         float64 `json:"importance"`
	Relevance          float64 `json:"relevance"`
	SemanticScore      float64 `json:"semantic_score"`
	TemporalMultiplier float64 `json:"temporal_multiplier"`
	CrossSignalBoost   float64 `json:"cross_signal_boost"`
	TypeIntentBoost    float64 `json:"type_intent_boost"`
	FinalScore         float64 `json:"final_score"`
}

type Result struct {
	ID              string
	Title           string
	Content         string
	Score           float64
	Layer           int
	ObservationType observation.ObservationType
	Kind            observation.Kind
	Confidence      float64
	Tags            []string
	Staleness       string
	Retention       string
	LinkedFiles     []string
	SourceSurface   string
	SourceSessionID string
	SourceTool      string
	Breakdown       *ScoreBreakdown // non-nil only when debug mode is enabled
}

type candidate struct {
	Result
	Importance        float64
	RawRelevance      float64
	SemanticScore     float64
	NamespaceBackfill bool
	CreatedAt         time.Time
	LastAccessed      *time.Time
	AccessCount       int
	rowID             int64
	index             int
}

func NewEngine(database *sql.DB, opts ...EngineOption) *Engine {
	e := &Engine{db: database, embedder: embed.Disabled{}}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

type EngineOption func(*Engine)

func WithEmbedder(p embed.Provider) EngineOption {
	return func(e *Engine) {
		if p != nil {
			e.embedder = p
		}
	}
}

// WithFactStore sets an optional fact store on the recall engine. When configured,
// Search() also queries the knowledge graph for matching facts and merges their
// source observations into the result set (deduplicated by observation ID).
func WithFactStore(s *facts.Store) EngineOption {
	return func(e *Engine) {
		e.factStore = s
	}
}

func (e *Engine) Search(ctx context.Context, options SearchOptions) ([]Result, error) {
	if e == nil || e.db == nil {
		return nil, fmt.Errorf("recall engine is not initialized")
	}

	normalized, err := normalizeSearchOptions(options)
	if err != nil {
		return nil, err
	}

	// Detect temporal intent from the query
	intent := DetectTemporalIntent(normalized.Query, normalized.Now)

	// Adjust search options based on temporal intent
	if intent.Kind == IntentHistory {
		normalized.IncludeStale = true
	}

	query, args := buildSearchQuery(normalized, intent)
	rows, err := e.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("search observations: %w", err)
	}
	defer rows.Close()

	candidates := make([]candidate, 0, normalized.Limit)
	for rows.Next() {
		item, scanErr := scanCandidate(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recall rows: %w", err)
	}

	// Hybrid: if embeddings available, boost FTS candidates with semantic scores
	// and fill remaining slots with semantic-only results when FTS returns few.
	if embed.IsAvailable(e.embedder) {
		semFilter := semanticFilter{
			Namespace:    normalized.Namespace,
			IncludeStale: normalized.IncludeStale,
			Staleness:    normalized.Staleness,
		}
		semScores, semErr := semanticSearch(ctx, e.db, e.embedder, normalized.Query, normalized.Limit*2, semFilter)
		if semErr == nil && len(semScores) > 0 {
			// Boost existing FTS candidates that also appear in semantic results
			ftsIDs := make(map[string]struct{}, len(candidates))
			for i := range candidates {
				ftsIDs[candidates[i].ID] = struct{}{}
				if semScore, ok := semScores[candidates[i].ID]; ok {
					if semScore > 0 {
						candidates[i].SemanticScore = semScore
					}
				}
			}

			// Fill remaining slots with semantic-only results when FTS returned fewer than limit
			if len(candidates) < normalized.Limit {
				remaining := normalized.Limit - len(candidates)
				semanticOnlyIDs := make([]string, 0, remaining)
				semanticOnlyScores := make(map[string]float64, remaining)

				// Collect semantic results NOT already in FTS candidates, sorted by score desc
				type idScore struct {
					id    string
					score float64
				}
				var semOnly []idScore
				for id, score := range semScores {
					if _, inFTS := ftsIDs[id]; !inFTS {
						semOnly = append(semOnly, idScore{id: id, score: score})
					}
				}
				sort.Slice(semOnly, func(i, j int) bool {
					return semOnly[i].score > semOnly[j].score
				})
				for i := 0; i < remaining && i < len(semOnly); i++ {
					semanticOnlyIDs = append(semanticOnlyIDs, semOnly[i].id)
					semanticOnlyScores[semOnly[i].id] = semOnly[i].score
				}

				if len(semanticOnlyIDs) > 0 {
					semCandidates, loadErr := loadObservationsByIDs(ctx, e.db, semanticOnlyIDs, normalized)
					if loadErr == nil {
						for i := range semCandidates {
							semCandidates[i].SemanticScore = semanticOnlyScores[semCandidates[i].ID]
							semCandidates[i].RawRelevance = 0 // no FTS match
						}
						candidates = append(candidates, semCandidates...)
					}
				}
			}
		}
	}

	// Integrate fact-sourced observations into the candidate list.
	// Facts are queried by LIKE matching on subject/object/predicate; when a fact
	// references an observation that is NOT already in the candidate set, load it
	// and add it as a candidate with a [fact] title prefix.
	if e.factStore != nil {
		factCandidates, factErr := e.searchFacts(ctx, normalized)
		if factErr != nil {
			log.Printf("fact search: %v", factErr)
		} else if len(factCandidates) > 0 {
			existingIDs := make(map[string]struct{}, len(candidates))
			for _, c := range candidates {
				existingIDs[c.ID] = struct{}{}
			}
			for _, fc := range factCandidates {
				if _, exists := existingIDs[fc.ID]; !exists {
					existingIDs[fc.ID] = struct{}{}
					candidates = append(candidates, fc)
				}
			}
		}
	}

	if shouldNamespaceBackfill(normalized, len(candidates)) {
		existingIDs := make(map[string]struct{}, len(candidates))
		for _, c := range candidates {
			existingIDs[c.ID] = struct{}{}
		}

		backfill, backfillErr := loadNamespaceBackfill(ctx, e.db, normalized, existingIDs)
		if backfillErr != nil {
			log.Printf("namespace backfill: %v", backfillErr)
		} else if len(backfill) > 0 {
			candidates = append(candidates, backfill...)
		}
	}

	// Load temporal mentions for candidates to inform temporal scoring
	candidateIDs := make([]string, len(candidates))
	for i, c := range candidates {
		candidateIDs[i] = c.ID
	}
	mentionMap, _ := loadCandidateMentions(ctx, e.db, candidateIDs)

	applyScores(candidates, normalized.Weights, normalized.Now, intent, mentionMap, normalized.Debug, normalized.Query)
	sort.SliceStable(candidates, func(i int, j int) bool {
		if candidates[i].Score == candidates[j].Score {
			if candidates[i].RawRelevance == candidates[j].RawRelevance {
				return candidates[i].index < candidates[j].index
			}
			return candidates[i].RawRelevance < candidates[j].RawRelevance
		}
		return candidates[i].Score > candidates[j].Score
	})

	results := make([]Result, 0, len(candidates))
	ids := make([]string, 0, len(candidates))
	for _, item := range candidates {
		results = append(results, item.Result)
		ids = append(ids, item.ID)
	}

	if len(ids) > 0 {
		if err := e.bumpAccess(ctx, ids); err != nil {
			return nil, err
		}
	}

	return results, nil
}

func normalizeSearchOptions(options SearchOptions) (SearchOptions, error) {
	options.Query = strings.TrimSpace(options.Query)
	if options.Query == "" {
		return SearchOptions{}, fmt.Errorf("query is required")
	}
	if options.ObservationType != "" {
		if err := options.ObservationType.Validate(); err != nil {
			return SearchOptions{}, err
		}
	}
	if options.Kind != "" {
		if err := options.Kind.Validate(); err != nil {
			return SearchOptions{}, err
		}
	}
	options.Namespace = strings.TrimSpace(options.Namespace)
	options.Staleness = strings.TrimSpace(options.Staleness)
	options.Files = normalizeFiles(options.Files)
	options.Limit = normalizeLimit(options.Limit)
	options.Weights = options.Weights.withDefaults()
	if options.Now.IsZero() {
		options.Now = time.Now().UTC()
	}
	return options, nil
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return defaultLimit
	}
	if limit > maxLimit {
		return maxLimit
	}
	return limit
}

func shouldNamespaceBackfill(options SearchOptions, currentCount int) bool {
	if options.Namespace == "" || currentCount >= options.Limit {
		return false
	}
	if len(options.Files) > 0 {
		return false
	}
	return len(strings.Fields(options.Query)) >= 3
}

func scanCandidate(scanner interface{ Scan(dest ...any) error }) (candidate, error) {
	var item candidate
	var tags sql.NullString
	var linkedFiles sql.NullString
	var lastAccessed sql.NullString
	var createdAt string
	var sourceSurface, sourceSessionID, sourceTool sql.NullString

	err := scanner.Scan(
		&item.rowID,
		&item.ID,
		&item.Title,
		&item.Content,
		&item.Layer,
		&item.ObservationType,
		&item.Kind,
		&item.Confidence,
		&item.Importance,
		&tags,
		&item.Staleness,
		&item.Retention,
		&linkedFiles,
		&item.RawRelevance,
		&createdAt,
		&lastAccessed,
		&item.AccessCount,
		&sourceSurface,
		&sourceSessionID,
		&sourceTool,
	)
	if err != nil {
		return candidate{}, fmt.Errorf("scan recall row: %w", err)
	}

	parsedCreatedAt, err := parseSQLiteTime(createdAt)
	if err != nil {
		return candidate{}, fmt.Errorf("parse recall created_at: %w", err)
	}
	item.CreatedAt = parsedCreatedAt
	if lastAccessed.Valid {
		parsedLastAccessed, err := parseSQLiteTime(lastAccessed.String)
		if err != nil {
			return candidate{}, fmt.Errorf("parse recall last_accessed: %w", err)
		}
		item.LastAccessed = &parsedLastAccessed
	}
	item.Tags = observation.ParseTags(tags.String)
	item.LinkedFiles = splitCSV(linkedFiles.String)
	item.SourceSurface = sourceSurface.String
	item.SourceSessionID = sourceSessionID.String
	item.SourceTool = sourceTool.String
	return item, nil
}

func (e *Engine) bumpAccess(ctx context.Context, ids []string) error {
	placeholders := make([]string, 0, len(ids))
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	if _, err := e.db.ExecContext(ctx, `
		UPDATE observations
		SET access_count = access_count + 1,
		    last_accessed = datetime('now'),
		    activation_level = MIN(1.0, activation_level + 0.08),
		    consolidation_strength = MIN(1.0, consolidation_strength + 0.02)
		WHERE deleted_at IS NULL AND id IN (`+strings.Join(placeholders, ",")+`)
	`, args...); err != nil {
		return fmt.Errorf("update recall access metrics: %w", err)
	}
	return nil
}

// searchFacts queries the knowledge graph for facts matching the search query,
// then loads the source observations as candidates. Facts contribute results
// with a [fact] prefix in the title and RawRelevance=0 (no FTS match), allowing
// the scoring formula to rank them via importance and recency.
func (e *Engine) searchFacts(ctx context.Context, options SearchOptions) ([]candidate, error) {
	matchedFacts, err := e.factStore.Search(ctx, options.Query, options.Namespace, options.Limit)
	if err != nil {
		return nil, fmt.Errorf("fact store search: %w", err)
	}
	if len(matchedFacts) == 0 {
		return nil, nil
	}

	// Collect unique observation IDs from matching facts. A fact may have
	// no observation_id if it was manually inserted; skip those.
	seen := make(map[string]struct{}, len(matchedFacts))
	var obsIDs []string
	// Keep a map of obsID → best fact for title prefix.
	factByObs := make(map[string]facts.Fact, len(matchedFacts))
	for _, f := range matchedFacts {
		if f.ObservationID == "" {
			continue
		}
		if _, dup := seen[f.ObservationID]; dup {
			continue
		}
		seen[f.ObservationID] = struct{}{}
		obsIDs = append(obsIDs, f.ObservationID)
		factByObs[f.ObservationID] = f
	}
	if len(obsIDs) == 0 {
		return nil, nil
	}

	// Load the full observation data for these IDs, applying the same filters
	// as the main search pipeline (type, kind, staleness, retention, files).
	loaded, err := loadObservationsByIDs(ctx, e.db, obsIDs, options)
	if err != nil {
		return nil, fmt.Errorf("load fact observations: %w", err)
	}

	// Prefix titles with [fact] and set minimal relevance so scoring works.
	for i := range loaded {
		f := factByObs[loaded[i].ID]
		loaded[i].Title = fmt.Sprintf("[fact] %s | %s | %s", f.Subject, f.Predicate, f.Object)
		loaded[i].RawRelevance = 0 // no FTS match
	}

	return loaded, nil
}

func normalizeFiles(files []string) []string {
	seen := make(map[string]struct{}, len(files))
	normalized := make([]string, 0, len(files))
	for _, file := range files {
		trimmed := strings.TrimSpace(file)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	sort.Strings(normalized)
	return normalized
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	return normalizeFiles(parts)
}

func parseSQLiteTime(value string) (time.Time, error) {
	layouts := []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02T15:04:05Z07:00"}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported sqlite time %q", value)
}

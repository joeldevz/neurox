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
	db              *sql.DB
	embedder        embed.Provider
	factStore       *facts.Store
	disableBackfill bool
	rrfK            int
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
	RRFScore           float64 `json:"rrf_score"`
	SemanticScore      float64 `json:"semantic_score"`
	TemporalMultiplier float64 `json:"temporal_multiplier"`
	CrossSignalBoost   float64 `json:"cross_signal_boost"`
	TypeIntentBoost    float64 `json:"type_intent_boost"`
	FinalScore         float64 `json:"final_score"`
}

type Result struct {
	ID              string                      `json:"id"`
	Title           string                      `json:"title"`
	Content         string                      `json:"content"`
	Score           float64                     `json:"score"`
	Layer           int                         `json:"layer"`
	ObservationType observation.ObservationType `json:"observation_type"`
	Kind            observation.Kind            `json:"kind"`
	Confidence      float64                     `json:"confidence"`
	Tags            []string                    `json:"tags,omitempty"`
	Staleness       string                      `json:"staleness"`
	Retention       string                      `json:"retention"`
	LinkedFiles     []string                    `json:"linked_files,omitempty"`
	SourceSurface   string                      `json:"source_surface,omitempty"`
	SourceSessionID string                      `json:"source_session_id,omitempty"`
	SourceTool      string                      `json:"source_tool,omitempty"`
	Breakdown       *ScoreBreakdown             `json:"score_breakdown,omitempty"` // non-nil only when debug mode is enabled
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
	FTSRank           int // 1-based rank in FTS results; 0 if not in FTS
	SemRank           int // 1-based rank in semantic results; 0 if not in semantic
}

func NewEngine(database *sql.DB, opts ...EngineOption) *Engine {
	e := &Engine{db: database, embedder: embed.Disabled{}, rrfK: 60}
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

// WithDisableBackfill suppresses the namespace backfill band-aid. Diagnostic
// only — used by the benchmark runner to measure recall without the backfill
// safety net. Production code should not set this flag.
func WithDisableBackfill(v bool) EngineOption {
	return func(e *Engine) {
		e.disableBackfill = v
	}
}

// WithRRFK overrides the Reciprocal Rank Fusion smoothing constant k. Default
// is 60 (zero-shot production consensus). k must be > 0; values <= 0 are
// ignored to preserve the default.
func WithRRFK(k int) EngineOption {
	return func(e *Engine) {
		if k > 0 {
			e.rrfK = k
		}
	}
}

// DisableBackfill reports whether the namespace backfill band-aid is
// suppressed on this engine. Exposed for diagnostic and test purposes.
func (e *Engine) DisableBackfill() bool { return e.disableBackfill }

// RRFK reports the Reciprocal Rank Fusion smoothing constant in use on this
// engine. Exposed for diagnostic and test purposes.
func (e *Engine) RRFK() int { return e.rrfK }

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

	// Hybrid: if embeddings available, compute union of FTS and semantic results.
	// Semantic-only candidates enter the pool regardless of len(FTS) vs limit.
	// Truncation happens after applyScores sorts by final score.
	if embed.IsAvailable(e.embedder) {
		semFilter := semanticFilter{
			Namespace:    normalized.Namespace,
			IncludeStale: normalized.IncludeStale,
			Staleness:    normalized.Staleness,
		}
		semScores, semErr := semanticSearch(ctx, e.db, e.embedder, normalized.Query, normalized.Limit*2, semFilter)
		if semErr == nil && len(semScores) > 0 {
			// Step 1: Derive FTS ranks (1-based, scan order). Scan order from
			// buildSearchQuery is BM25-asc (best first), so rank 1 = best match.
			ftsRanks := make(map[string]int, len(candidates))
			for i := range candidates {
				ftsRanks[candidates[i].ID] = i + 1
				candidates[i].FTSRank = i + 1
			}

			// Step 2: Derive semantic ranks via the ranking helper.
			semRanks := deriveSemanticRanks(semScores)

			// Step 3: Boost FTS candidates that also appear in semantic results.
			for i := range candidates {
				if semScore, ok := semScores[candidates[i].ID]; ok && semScore > 0 {
					candidates[i].SemanticScore = semScore
					candidates[i].SemRank = semRanks[candidates[i].ID]
				}
			}

			// Step 4: Collect semantic-only IDs and CAP at normalized.Limit by semScore.
			// This prevents pool flooding: if 30 weak semantic-only candidates are available,
			// we take only the top-normalized.Limit by semScore (sorted descending), selected
			// and added to avoid displacing stronger FTS results.
			type semOnlyID struct {
				id    string
				score float64
			}
			semanticOnlyList := make([]semOnlyID, 0)
			for id := range semScores {
				if _, inFTS := ftsRanks[id]; !inFTS {
					semanticOnlyList = append(semanticOnlyList, semOnlyID{id: id, score: semScores[id]})
				}
			}

			// Sort by semScore descending and take top-limit candidates.
			sort.Slice(semanticOnlyList, func(i, j int) bool {
				if semanticOnlyList[i].score != semanticOnlyList[j].score {
					return semanticOnlyList[i].score > semanticOnlyList[j].score
				}
				// Stable tie-break by ID ascending for determinism.
				return semanticOnlyList[i].id < semanticOnlyList[j].id
			})

			// Take at most normalized.Limit semantic-only candidates.
			if len(semanticOnlyList) > normalized.Limit {
				semanticOnlyList = semanticOnlyList[:normalized.Limit]
			}

			semanticOnlyIDs := make([]string, len(semanticOnlyList))
			for i, item := range semanticOnlyList {
				semanticOnlyIDs[i] = item.id
			}

			// Step 5: Load semantic-only candidates and assign ranks.
			if len(semanticOnlyIDs) > 0 {
				semCandidates, loadErr := loadObservationsByIDs(ctx, e.db, semanticOnlyIDs, normalized)
				if loadErr == nil {
					for i := range semCandidates {
						semCandidates[i].SemanticScore = semScores[semCandidates[i].ID]
						semCandidates[i].RawRelevance = 0 // no FTS match
						semCandidates[i].FTSRank = 0
						semCandidates[i].SemRank = semRanks[semCandidates[i].ID]
					}
					candidates = append(candidates, semCandidates...)
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

	if shouldNamespaceBackfill(normalized, len(candidates), e.disableBackfill) {
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

	applyScores(candidates, normalized.Weights, normalized.Now, intent, mentionMap, normalized.Debug, normalized.Query, e.rrfK)
	sort.SliceStable(candidates, func(i int, j int) bool {
		if candidates[i].Score == candidates[j].Score {
			if candidates[i].RawRelevance == candidates[j].RawRelevance {
				return candidates[i].index < candidates[j].index
			}
			return candidates[i].RawRelevance < candidates[j].RawRelevance
		}
		return candidates[i].Score > candidates[j].Score
	})

	// Truncate to limit after sort. The union merge can produce more than
	// `limit` candidates when FTS returns exactly `limit` and semantic-only
	// candidates are also added — the highest-scoring `limit` are kept.
	if len(candidates) > normalized.Limit {
		candidates = candidates[:normalized.Limit]
	}

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

func shouldNamespaceBackfill(options SearchOptions, currentCount int, disableBackfill bool) bool {
	if disableBackfill {
		return false
	}
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

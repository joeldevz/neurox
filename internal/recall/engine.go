package recall

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"neurox/internal/embed"
	"neurox/internal/observation"
)

const (
	defaultLimit = 10
	maxLimit     = 50
)

type Engine struct {
	db       *sql.DB
	embedder embed.Provider
}

type SearchOptions struct {
	Query           string
	ObservationType observation.ObservationType
	Kind            observation.Kind
	Namespace       string
	Staleness       string
	IncludeStale    bool
	Files           []string
	Limit           int
	Weights         ScoreWeights
	Now             time.Time
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
	LinkedFiles     []string
}

type candidate struct {
	Result
	Importance    float64
	RawRelevance  float64
	SemanticScore float64
	CreatedAt     time.Time
	LastAccessed  *time.Time
	AccessCount   int
	rowID         int64
	index         int
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

	// Hybrid: if embeddings available, boost candidates that also appear in semantic search
	if embed.IsAvailable(e.embedder) {
		semScores, semErr := semanticSearch(ctx, e.db, e.embedder, normalized.Query, normalized.Limit*2)
		if semErr == nil && len(semScores) > 0 {
			for i := range candidates {
				if semScore, ok := semScores[candidates[i].ID]; ok {
					if semScore > 0 {
						candidates[i].SemanticScore = semScore
					}
				}
			}
		}
	}

	// Load temporal mentions for candidates to inform temporal scoring
	candidateIDs := make([]string, len(candidates))
	for i, c := range candidates {
		candidateIDs[i] = c.ID
	}
	mentionMap, _ := loadCandidateMentions(ctx, e.db, candidateIDs)

	applyScores(candidates, normalized.Weights, normalized.Now, intent, mentionMap)
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

func scanCandidate(scanner interface{ Scan(dest ...any) error }) (candidate, error) {
	var item candidate
	var tags sql.NullString
	var linkedFiles sql.NullString
	var lastAccessed sql.NullString
	var createdAt string

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
		&linkedFiles,
		&item.RawRelevance,
		&createdAt,
		&lastAccessed,
		&item.AccessCount,
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
		    importance = MIN(1.0, importance + 0.03)
		WHERE deleted_at IS NULL AND id IN (`+strings.Join(placeholders, ",")+`)
	`, args...); err != nil {
		return fmt.Errorf("update recall access metrics: %w", err)
	}
	return nil
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

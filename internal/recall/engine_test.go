package recall

import (
	"context"
	"database/sql"
	"path/filepath"
	"sort"
	"testing"

	"neurox/internal/db"
	"neurox/internal/observation"
)

func TestSearchKeywordRanksByTriFactorScore(t *testing.T) {
	engine, store, database := newTestEngine(t)
	defer database.Close()

	ctx := context.Background()
	olderHighImportance, err := store.Save(ctx, observation.Observation{
		Title:   "Auth note",
		Content: "auth implementation detail",
	})
	if err != nil {
		t.Fatalf("save olderHighImportance: %v", err)
	}
	recentLowImportance, err := store.Save(ctx, observation.Observation{
		Title:   "Auth note",
		Content: "auth implementation detail",
	})
	if err != nil {
		t.Fatalf("save recentLowImportance: %v", err)
	}

	setObservationFields(t, database, olderHighImportance.ID, map[string]any{
		"importance": 0.95,
		"created_at": "datetime('now', '-14 day')",
	})
	setObservationFields(t, database, recentLowImportance.ID, map[string]any{
		"importance": 0.20,
		"created_at": "datetime('now', '-1 hour')",
	})

	results, err := engine.Search(ctx, SearchOptions{Query: "auth", Limit: 10})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	if results[0].ID != olderHighImportance.ID {
		t.Fatalf("top result = %q, want %q", results[0].ID, olderHighImportance.ID)
	}
	if results[0].Score <= results[1].Score {
		t.Fatalf("top score = %f, want > %f", results[0].Score, results[1].Score)
	}

	assertAccessUpdated(t, database, olderHighImportance.ID, 1)
	assertAccessUpdated(t, database, recentLowImportance.ID, 1)
}

func TestSearchFiltersByObservationType(t *testing.T) {
	engine, store, database := newTestEngine(t)
	defer database.Close()

	ctx := context.Background()
	_, err := store.Save(ctx, observation.Observation{
		Title:           "Auth bugfix",
		Content:         "auth issue resolved",
		ObservationType: observation.ObservationTypeBugfix,
	})
	if err != nil {
		t.Fatalf("save bugfix: %v", err)
	}
	_, err = store.Save(ctx, observation.Observation{
		Title:           "Auth decision",
		Content:         "auth decision recorded",
		ObservationType: observation.ObservationTypeDecision,
	})
	if err != nil {
		t.Fatalf("save decision: %v", err)
	}

	results, err := engine.Search(ctx, SearchOptions{
		Query:           "auth",
		ObservationType: observation.ObservationTypeBugfix,
	})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].ObservationType != observation.ObservationTypeBugfix {
		t.Fatalf("ObservationType = %q, want %q", results[0].ObservationType, observation.ObservationTypeBugfix)
	}
}

func TestSearchFiltersByFiles(t *testing.T) {
	engine, store, database := newTestEngine(t)
	defer database.Close()

	ctx := context.Background()
	matching, err := store.Save(ctx, observation.Observation{
		Title:   "Auth service note",
		Content: "auth service details",
		Files:   []string{"internal/auth/service.go", "internal/auth/helper.go"},
	})
	if err != nil {
		t.Fatalf("save matching: %v", err)
	}
	_, err = store.Save(ctx, observation.Observation{
		Title:   "Auth controller note",
		Content: "auth controller details",
		Files:   []string{"internal/http/controller.go"},
	})
	if err != nil {
		t.Fatalf("save non-matching: %v", err)
	}

	results, err := engine.Search(ctx, SearchOptions{
		Query: "auth",
		Files: []string{"internal/auth/service.go"},
	})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].ID != matching.ID {
		t.Fatalf("result ID = %q, want %q", results[0].ID, matching.ID)
	}
	if len(results[0].LinkedFiles) != 2 {
		t.Fatalf("len(LinkedFiles) = %d, want 2", len(results[0].LinkedFiles))
	}
}

func TestSearchActivationBoostUsesLastAccessed(t *testing.T) {
	engine, store, database := newTestEngine(t)
	defer database.Close()

	ctx := context.Background()
	boosted, err := store.Save(ctx, observation.Observation{
		Title:   "Fallback token refresh",
		Content: "fallback path for refresh tokens with auth",
	})
	if err != nil {
		t.Fatalf("save boosted: %v", err)
	}
	baseline, err := store.Save(ctx, observation.Observation{
		Title:   "Auth auth checklist",
		Content: "auth checklist auth",
	})
	if err != nil {
		t.Fatalf("save baseline: %v", err)
	}

	setObservationFields(t, database, boosted.ID, map[string]any{
		"importance": 0.10,
		"created_at": "datetime('now', '-180 day')",
	})
	setObservationFields(t, database, baseline.ID, map[string]any{
		"importance": 0.45,
		"created_at": "datetime('now', '-7 day')",
	})

	initial, err := searchWithoutActivation(ctx, database, SearchOptions{Query: "auth", Limit: 10})
	if err != nil {
		t.Fatalf("initial searchWithoutActivation returned error: %v", err)
	}
	if len(initial) != 2 {
		t.Fatalf("len(initial) = %d, want 2", len(initial))
	}
	if initial[0].ID != baseline.ID {
		t.Fatalf("initial top result = %q, want %q", initial[0].ID, baseline.ID)
	}
	initialBoostedScore := findResultScore(t, initial, boosted.ID)

	_, err = engine.Search(ctx, SearchOptions{Query: "fallback"})
	if err != nil {
		t.Fatalf("boost Search returned error: %v", err)
	}

	afterBoost, err := engine.Search(ctx, SearchOptions{Query: "auth", Limit: 10})
	if err != nil {
		t.Fatalf("afterBoost Search returned error: %v", err)
	}
	if len(afterBoost) != 2 {
		t.Fatalf("len(afterBoost) = %d, want 2", len(afterBoost))
	}
	boostedScore := findResultScore(t, afterBoost, boosted.ID)
	if boostedScore <= initialBoostedScore {
		t.Fatalf("boosted score = %f, want > initial score %f", boostedScore, initialBoostedScore)
	}
}

func TestSearchExcludesExpiredByDefault(t *testing.T) {
	engine, store, database := newTestEngine(t)
	defer database.Close()

	ctx := context.Background()
	expired, err := store.Save(ctx, observation.Observation{
		Title:   "Expired auth note",
		Content: "auth note that should stay hidden",
	})
	if err != nil {
		t.Fatalf("save expired: %v", err)
	}
	active, err := store.Save(ctx, observation.Observation{
		Title:   "Active auth note",
		Content: "auth note that should be returned",
	})
	if err != nil {
		t.Fatalf("save active: %v", err)
	}
	setObservationFields(t, database, expired.ID, map[string]any{"staleness": "'expired'"})

	results, err := engine.Search(ctx, SearchOptions{Query: "auth", Limit: 10})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].ID != active.ID {
		t.Fatalf("result ID = %q, want %q", results[0].ID, active.ID)
	}
}

func newTestEngine(t *testing.T) (*Engine, *observation.Store, *sql.DB) {
	t.Helper()
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("db.Open returned error: %v", err)
	}
	store := observation.NewStore(database, nil)
	return NewEngine(database), store, database
}

func setObservationFields(t *testing.T, database *sql.DB, id string, values map[string]any) {
	t.Helper()
	for field, value := range values {
		query := "UPDATE observations SET " + field + " = "
		switch typed := value.(type) {
		case string:
			query += typed
			if _, err := database.Exec(query+" WHERE id = ?", id); err != nil {
				t.Fatalf("update %s: %v", field, err)
			}
		case float64:
			if _, err := database.Exec(query+" ? WHERE id = ?", typed, id); err != nil {
				t.Fatalf("update %s: %v", field, err)
			}
		default:
			t.Fatalf("unsupported value type %T", value)
		}
	}
}

func assertAccessUpdated(t *testing.T, database *sql.DB, id string, wantAccessCount int) {
	t.Helper()
	var accessCount int
	var lastAccessed sql.NullString
	if err := database.QueryRow(`SELECT access_count, last_accessed FROM observations WHERE id = ?`, id).Scan(&accessCount, &lastAccessed); err != nil {
		t.Fatalf("load access metrics: %v", err)
	}
	if accessCount != wantAccessCount {
		t.Fatalf("access_count = %d, want %d", accessCount, wantAccessCount)
	}
	if !lastAccessed.Valid {
		t.Fatal("last_accessed is NULL, want timestamp")
	}
}

func searchWithoutActivation(ctx context.Context, database *sql.DB, options SearchOptions) ([]Result, error) {
	normalized, err := normalizeSearchOptions(options)
	if err != nil {
		return nil, err
	}
	query, args := buildSearchQuery(normalized)
	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
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
		return nil, err
	}

	applyScores(candidates, normalized.Weights, normalized.Now)
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
	for _, item := range candidates {
		results = append(results, item.Result)
	}
	return results, nil
}

func findResultScore(t *testing.T, results []Result, id string) float64 {
	t.Helper()
	for _, result := range results {
		if result.ID == id {
			return result.Score
		}
	}
	t.Fatalf("result %q not found", id)
	return 0
}

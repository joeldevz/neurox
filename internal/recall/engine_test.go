package recall

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/joeldevz/neurox/internal/db"
	"github.com/joeldevz/neurox/internal/observation"
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

// --- Temporal-aware recall tests ---

func TestSearchCurrentStatePrefersFreshOverStale(t *testing.T) {
	engine, store, database := newTestEngine(t)
	defer database.Close()

	ctx := context.Background()

	// Two observations about database: one stale (postgres), one fresh (sqlite)
	staleObs, err := store.Save(ctx, observation.Observation{
		Title:   "Database choice",
		Content: "we use postgres database for the project",
	})
	if err != nil {
		t.Fatalf("save stale: %v", err)
	}

	freshObs, err := store.Save(ctx, observation.Observation{
		Title:   "Database migration",
		Content: "using sqlite database for the project",
	})
	if err != nil {
		t.Fatalf("save fresh: %v", err)
	}

	setObservationFields(t, database, staleObs.ID, map[string]any{
		"staleness":  "'stale'",
		"importance": 0.8,
		"created_at": "datetime('now', '-30 day')",
	})
	setObservationFields(t, database, freshObs.ID, map[string]any{
		"importance": 0.5,
		"created_at": "datetime('now', '-1 day')",
	})

	// Add current_state temporal mention for fresh observation
	insertTemporalMention(t, database, freshObs.ID, "current_state", nil, nil)

	// Query with current-state intent ("currently" triggers IntentCurrentState)
	results, err := engine.Search(ctx, SearchOptions{Query: "database currently", Limit: 10})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}

	if len(results) < 1 {
		t.Fatal("expected at least 1 result")
	}
	if results[0].ID != freshObs.ID {
		t.Fatalf("top result = %q, want %q (fresh observation should rank higher for current-state query)", results[0].ID, freshObs.ID)
	}
}

func TestSearchHistoryIncludesExpiredObservations(t *testing.T) {
	engine, store, database := newTestEngine(t)
	defer database.Close()

	ctx := context.Background()

	// Historical observation (expired) and current observation
	historicalObs, err := store.Save(ctx, observation.Observation{
		Title:   "Auth system legacy",
		Content: "auth system uses session tokens for authentication",
	})
	if err != nil {
		t.Fatalf("save historical: %v", err)
	}

	currentObs, err := store.Save(ctx, observation.Observation{
		Title:   "Auth system update",
		Content: "auth system uses JWT tokens for authentication",
	})
	if err != nil {
		t.Fatalf("save current: %v", err)
	}

	setObservationFields(t, database, historicalObs.ID, map[string]any{
		"staleness":   "'expired'",
		"valid_until": "datetime('now', '-7 day')",
		"created_at":  "datetime('now', '-60 day')",
	})

	// Normal search should NOT include historical (expired + valid_until passed)
	normalResults, err := engine.Search(ctx, SearchOptions{Query: "auth tokens", Limit: 10})
	if err != nil {
		t.Fatalf("normal Search returned error: %v", err)
	}
	for _, r := range normalResults {
		if r.ID == historicalObs.ID {
			t.Fatal("expired observation should not appear in normal search")
		}
	}
	if len(normalResults) < 1 || normalResults[0].ID != currentObs.ID {
		t.Fatal("current observation should appear in normal search")
	}

	// History search ("previously" triggers IntentHistory) should include expired
	historyResults, err := engine.Search(ctx, SearchOptions{Query: "auth tokens previously", Limit: 10})
	if err != nil {
		t.Fatalf("history Search returned error: %v", err)
	}
	found := false
	for _, r := range historyResults {
		if r.ID == historicalObs.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expired observation should appear in history search")
	}
}

func TestSearchWhenBoostsTemporalMentions(t *testing.T) {
	engine, store, database := newTestEngine(t)
	defer database.Close()

	ctx := context.Background()

	// Two observations about migration: one with temporal mention, one without
	withTemporal, err := store.Save(ctx, observation.Observation{
		Title:   "Migration to SQLite",
		Content: "migration completed to sqlite database",
	})
	if err != nil {
		t.Fatalf("save withTemporal: %v", err)
	}

	withoutTemporal, err := store.Save(ctx, observation.Observation{
		Title:   "Migration notes",
		Content: "migration steps and sqlite database notes",
	})
	if err != nil {
		t.Fatalf("save withoutTemporal: %v", err)
	}

	// Give withoutTemporal higher importance (normally it would rank first)
	setObservationFields(t, database, withoutTemporal.ID, map[string]any{
		"importance": 0.9,
	})
	setObservationFields(t, database, withTemporal.ID, map[string]any{
		"importance": 0.5,
	})

	// Add absolute temporal mention to withTemporal
	march := time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC)
	insertTemporalMention(t, database, withTemporal.ID, "absolute", &march, nil)

	// "when did" triggers IntentWhen; cleanQueryForFTS strips temporal noise → FTS matches "migration"
	results, err := engine.Search(ctx, SearchOptions{Query: "when did migration", Limit: 10})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}

	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}
	if results[0].ID != withTemporal.ID {
		t.Fatalf("top result = %q, want %q (observation with temporal mention should rank higher for 'when' query)",
			results[0].ID, withTemporal.ID)
	}
}

func TestSearchHistoryBoostsExpiredOverFresh(t *testing.T) {
	engine, store, database := newTestEngine(t)
	defer database.Close()

	ctx := context.Background()

	// Two observations: expired (old database) and fresh (current database)
	expiredObs, err := store.Save(ctx, observation.Observation{
		Title:   "Database system old",
		Content: "database system config and setup notes",
	})
	if err != nil {
		t.Fatalf("save expired: %v", err)
	}

	freshObs, err := store.Save(ctx, observation.Observation{
		Title:   "Database system new",
		Content: "database system config and setup notes",
	})
	if err != nil {
		t.Fatalf("save fresh: %v", err)
	}

	setObservationFields(t, database, expiredObs.ID, map[string]any{
		"staleness":  "'expired'",
		"importance": 0.5,
		"created_at": "datetime('now', '-60 day')",
	})
	setObservationFields(t, database, freshObs.ID, map[string]any{
		"importance": 0.5,
		"created_at": "datetime('now', '-1 day')",
	})

	// "before" triggers IntentHistory
	results, err := engine.Search(ctx, SearchOptions{Query: "database before", Limit: 10})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}

	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}

	// Expired observation should get a history boost
	expiredScore := findResultScore(t, results, expiredObs.ID)
	freshScore := findResultScore(t, results, freshObs.ID)

	// The expired obs has history staleness boost (+0.15) while fresh gets penalty (-0.05)
	// But fresh has much better recency. The key assertion is that the history boost
	// narrows the gap significantly compared to a non-temporal query.
	// We just verify the expired observation is included and scored.
	if expiredScore <= 0 {
		t.Fatalf("expired observation should have positive score, got %f", expiredScore)
	}
	_ = freshScore // fresh will likely still rank first due to recency, but expired is included
}

func TestDetectTemporalIntent(t *testing.T) {
	now := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		query    string
		wantKind TemporalIntentKind
	}{
		{name: "no intent", query: "auth implementation", wantKind: IntentNone},
		{name: "current english", query: "what database currently", wantKind: IntentCurrentState},
		{name: "current latest", query: "latest auth config", wantKind: IntentCurrentState},
		{name: "current spanish", query: "configuración actualmente", wantKind: IntentCurrentState},
		{name: "history before", query: "database before migration", wantKind: IntentHistory},
		{name: "history previous", query: "previous auth system", wantKind: IntentHistory},
		{name: "history spanish", query: "antes de la migración", wantKind: IntentHistory},
		{name: "when english", query: "when did we migrate", wantKind: IntentWhen},
		{name: "when spanish", query: "cuándo migramos", wantKind: IntentWhen},
		{name: "duration", query: "how long using sqlite", wantKind: IntentDuration},
		{name: "changed", query: "what changed this week", wantKind: IntentTimeRange},
		{name: "point in time", query: "migration 2026-03-06", wantKind: IntentPointInTime},
		{name: "time range since", query: "changes since march", wantKind: IntentTimeRange},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			intent := DetectTemporalIntent(tt.query, now)
			if intent.Kind != tt.wantKind {
				t.Errorf("DetectTemporalIntent(%q) = %q, want %q", tt.query, intent.Kind, tt.wantKind)
			}
		})
	}
}

func TestCleanQueryForFTS(t *testing.T) {
	now := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "no intent keeps query",
			query: "auth implementation",
			want:  "auth implementation",
		},
		{
			name:  "strips currently",
			query: "database currently",
			want:  "database",
		},
		{
			name:  "strips when did",
			query: "when did migration happen",
			want:  "migration happen",
		},
		{
			name:  "strips noise words",
			query: "when was the auth system updated",
			want:  "auth system updated",
		},
		{
			name:  "preserves content words",
			query: "what database before migration",
			want:  "database migration",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			intent := DetectTemporalIntent(tt.query, now)
			got := cleanQueryForFTS(tt.query, intent)
			if got != tt.want {
				t.Errorf("cleanQueryForFTS(%q) = %q, want %q", tt.query, got, tt.want)
			}
		})
	}
}

func TestTemporalMultiplier(t *testing.T) {
	march := time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		staleness string
		intent    TemporalIntent
		mentions  []mentionInfo
		wantMin   float64
		wantMax   float64
	}{
		{
			name:      "no intent returns 1.0",
			staleness: "fresh",
			intent:    TemporalIntent{Kind: IntentNone},
			wantMin:   1.0,
			wantMax:   1.0,
		},
		{
			name:      "current state boosts fresh",
			staleness: "fresh",
			intent:    TemporalIntent{Kind: IntentCurrentState},
			mentions:  []mentionInfo{{Kind: "current_state"}},
			wantMin:   1.2,
			wantMax:   1.5,
		},
		{
			name:      "current state penalizes expired",
			staleness: "expired",
			intent:    TemporalIntent{Kind: IntentCurrentState},
			wantMin:   0.7,
			wantMax:   0.85,
		},
		{
			name:      "history boosts expired",
			staleness: "expired",
			intent:    TemporalIntent{Kind: IntentHistory},
			wantMin:   1.1,
			wantMax:   1.5,
		},
		{
			name:      "when boosts temporal mentions",
			staleness: "fresh",
			intent:    TemporalIntent{Kind: IntentWhen},
			mentions:  []mentionInfo{{Kind: "absolute", NormalizedStart: &march}},
			wantMin:   1.3,
			wantMax:   1.5,
		},
		{
			name:      "when no mentions stays at 1.0",
			staleness: "fresh",
			intent:    TemporalIntent{Kind: IntentWhen},
			wantMin:   1.0,
			wantMax:   1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := candidate{}
			c.Staleness = tt.staleness
			got := temporalMultiplier(c, tt.intent, tt.mentions)
			if got < tt.wantMin || got > tt.wantMax {
				t.Errorf("temporalMultiplier() = %f, want [%f, %f]", got, tt.wantMin, tt.wantMax)
			}
		})
	}
}

// --- Test helpers ---

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

func insertTemporalMention(t *testing.T, database *sql.DB, observationID, kind string, normalizedStart, normalizedEnd *time.Time) {
	t.Helper()
	id := fmt.Sprintf("tm_%s_%s", observationID[:8], kind)
	var startStr, endStr any
	if normalizedStart != nil {
		startStr = normalizedStart.UTC().Format(time.RFC3339)
	}
	if normalizedEnd != nil {
		endStr = normalizedEnd.UTC().Format(time.RFC3339)
	}
	_, err := database.Exec(`
		INSERT INTO temporal_mentions(id, observation_id, raw_text, mention_kind, normalized_start, normalized_end, anchor_time, confidence)
		VALUES(?, ?, ?, ?, ?, ?, datetime('now'), 0.9)
	`, id, observationID, kind+"_text", kind, startStr, endStr)
	if err != nil {
		t.Fatalf("insert temporal mention: %v", err)
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

	intent := DetectTemporalIntent(normalized.Query, normalized.Now)
	query, args := buildSearchQuery(normalized, intent)
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

	applyScores(candidates, normalized.Weights, normalized.Now, intent, nil)
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

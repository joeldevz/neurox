package facts

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/joeldevz/neurox/internal/db"
	"github.com/joeldevz/neurox/internal/observation"
)

func setupTest(t *testing.T) (*Store, *db.TestDB) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	idGen := observation.NewULIDGenerator()
	store := NewStore(database, idGen)
	return store, &db.TestDB{DB: database}
}

func TestSaveAndGet(t *testing.T) {
	s, _ := setupTest(t)
	ctx := context.Background()

	saved, err := s.Save(ctx, Fact{
		Subject:   "project",
		Predicate: "uses_framework",
		Object:    "react",
		Namespace: "myapp",
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if saved.ID == "" {
		t.Fatal("expected ID to be set")
	}
	if saved.Subject != "project" {
		t.Errorf("subject = %q, want 'project'", saved.Subject)
	}

	got, err := s.Get(ctx, saved.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Subject != "project" || got.Predicate != "uses_framework" || got.Object != "react" {
		t.Errorf("got = %+v", got)
	}
}

func TestSaveSupersedes(t *testing.T) {
	s, tdb := setupTest(t)
	ctx := context.Background()

	// Save initial fact
	first, err := s.Save(ctx, Fact{
		Subject:   "database",
		Predicate: "version",
		Object:    "postgres_14",
		Namespace: "myapp",
	})
	if err != nil {
		t.Fatalf("save first: %v", err)
	}

	// Save updated fact with same subject+predicate
	second, err := s.Save(ctx, Fact{
		Subject:   "database",
		Predicate: "version",
		Object:    "postgres_16",
		Namespace: "myapp",
	})
	if err != nil {
		t.Fatalf("save second: %v", err)
	}

	// First should be superseded
	var validUntil, supersededBy *string
	tdb.DB.QueryRowContext(ctx, "SELECT valid_until, superseded_by FROM facts WHERE id = ?", first.ID).
		Scan(&validUntil, &supersededBy)

	if validUntil == nil {
		t.Error("first fact should have valid_until set")
	}
	if supersededBy == nil || *supersededBy != second.ID {
		t.Errorf("superseded_by = %v, want %s", supersededBy, second.ID)
	}

	// Second should be active
	got, err := s.Get(ctx, second.ID)
	if err != nil {
		t.Fatalf("get second: %v", err)
	}
	if got.ValidUntil != nil {
		t.Error("second fact should not have valid_until")
	}
}

func TestSearch(t *testing.T) {
	s, _ := setupTest(t)
	ctx := context.Background()

	s.Save(ctx, Fact{Subject: "auth", Predicate: "uses", Object: "jwt", Namespace: "app"})
	s.Save(ctx, Fact{Subject: "auth", Predicate: "depends_on", Object: "redis", Namespace: "app"})
	s.Save(ctx, Fact{Subject: "frontend", Predicate: "uses", Object: "react", Namespace: "app"})

	results, err := s.Search(ctx, "auth", "app", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}

func TestTraverse(t *testing.T) {
	s, _ := setupTest(t)
	ctx := context.Background()

	// Build a small graph: auth -> jwt -> rsa
	s.Save(ctx, Fact{Subject: "auth", Predicate: "uses", Object: "jwt", Namespace: "app"})
	s.Save(ctx, Fact{Subject: "jwt", Predicate: "algorithm", Object: "rsa", Namespace: "app"})
	s.Save(ctx, Fact{Subject: "auth", Predicate: "depends_on", Object: "redis", Namespace: "app"})

	// Depth 1: should find jwt and redis
	results, err := s.Traverse(ctx, "auth", "app", 1)
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("depth 1: expected 2, got %d", len(results))
	}

	// Depth 2: should also find rsa (via jwt)
	results, err = s.Traverse(ctx, "auth", "app", 2)
	if err != nil {
		t.Fatalf("traverse depth 2: %v", err)
	}
	if len(results) < 3 {
		t.Errorf("depth 2: expected >= 3, got %d", len(results))
	}

	// Check rsa is reachable
	found := false
	for _, r := range results {
		if r.Fact.Object == "rsa" || r.Fact.Subject == "rsa" {
			found = true
			if r.Depth != 2 {
				t.Errorf("rsa depth = %d, want 2", r.Depth)
			}
		}
	}
	if !found {
		t.Error("expected to find rsa at depth 2")
	}
}

func TestCount(t *testing.T) {
	s, _ := setupTest(t)
	ctx := context.Background()

	s.Save(ctx, Fact{Subject: "a", Predicate: "b", Object: "c", Namespace: "ns1"})
	s.Save(ctx, Fact{Subject: "d", Predicate: "e", Object: "f", Namespace: "ns1"})
	s.Save(ctx, Fact{Subject: "g", Predicate: "h", Object: "i", Namespace: "ns2"})

	count, err := s.Count(ctx, "ns1")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestSaveWithValidFrom(t *testing.T) {
	s, _ := setupTest(t)
	ctx := context.Background()

	validFrom := time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC)
	saved, err := s.SaveWithValidFrom(ctx, Fact{
		Subject:   "migration",
		Predicate: "happened_on",
		Object:    "2026-03-06",
		Namespace: "myapp",
	}, validFrom)
	if err != nil {
		t.Fatalf("save with valid_from: %v", err)
	}

	if saved.ValidFrom.IsZero() {
		t.Fatal("expected valid_from to be set")
	}
	if !saved.ValidFrom.Equal(validFrom) {
		t.Errorf("valid_from = %v, want %v", saved.ValidFrom, validFrom)
	}
}

func TestSaveWithValidFromSupersedes(t *testing.T) {
	s, tdb := setupTest(t)
	ctx := context.Background()

	// Save initial temporal fact
	first, err := s.SaveWithValidFrom(ctx, Fact{
		Subject:   "database",
		Predicate: "started_on",
		Object:    "2025-01",
		Namespace: "myapp",
	}, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("save first: %v", err)
	}

	// Save updated temporal fact (same subject+predicate)
	second, err := s.SaveWithValidFrom(ctx, Fact{
		Subject:   "database",
		Predicate: "started_on",
		Object:    "2026-03",
		Namespace: "myapp",
	}, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("save second: %v", err)
	}

	// First should be superseded
	var validUntil, supersededBy *string
	tdb.DB.QueryRowContext(ctx, "SELECT valid_until, superseded_by FROM facts WHERE id = ?", first.ID).
		Scan(&validUntil, &supersededBy)

	if validUntil == nil {
		t.Error("first fact should have valid_until set")
	}
	if supersededBy == nil || *supersededBy != second.ID {
		t.Errorf("superseded_by = %v, want %s", supersededBy, second.ID)
	}

	// Second should have the explicit valid_from
	got, _ := s.Get(ctx, second.ID)
	if got.ValidFrom.Year() != 2026 || got.ValidFrom.Month() != 3 {
		t.Errorf("second valid_from = %v, want 2026-03", got.ValidFrom)
	}
}

func TestSearchHistory(t *testing.T) {
	s, _ := setupTest(t)
	ctx := context.Background()

	// Create a chain: postgres_14 -> postgres_15 -> postgres_16
	s.Save(ctx, Fact{Subject: "database", Predicate: "version", Object: "postgres_14", Namespace: "app"})
	s.Save(ctx, Fact{Subject: "database", Predicate: "version", Object: "postgres_15", Namespace: "app"})
	s.Save(ctx, Fact{Subject: "database", Predicate: "version", Object: "postgres_16", Namespace: "app"})

	// SearchHistory should return all 3 (active + superseded)
	history, err := s.SearchHistory(ctx, "database", "version", "app", 10)
	if err != nil {
		t.Fatalf("search history: %v", err)
	}
	if len(history) != 3 {
		t.Fatalf("history count = %d, want 3", len(history))
	}

	// Only the latest should be active (no valid_until)
	activeCount := 0
	var activeObject string
	for _, f := range history {
		if f.ValidUntil == nil {
			activeCount++
			activeObject = f.Object
		}
	}
	if activeCount != 1 {
		t.Errorf("active facts = %d, want 1", activeCount)
	}
	if activeObject != "postgres_16" {
		t.Errorf("active fact = %q, want postgres_16", activeObject)
	}
}

func TestSearchHistoryPreservesHistoricalFacts(t *testing.T) {
	s, _ := setupTest(t)
	ctx := context.Background()

	// Current state: database -> current -> sqlite
	s.Save(ctx, Fact{Subject: "database", Predicate: "current", Object: "postgres", Namespace: "app"})
	s.Save(ctx, Fact{Subject: "database", Predicate: "current", Object: "sqlite", Namespace: "app"})

	// Active search should only return sqlite
	active, _ := s.Search(ctx, "database", "app", 10)
	foundPostgres := false
	for _, f := range active {
		if f.Object == "postgres" && f.Predicate == "current" {
			foundPostgres = true
		}
	}
	if foundPostgres {
		t.Error("active search should NOT return superseded postgres fact")
	}

	// History should return both
	history, _ := s.SearchHistory(ctx, "database", "current", "app", 10)
	if len(history) != 2 {
		t.Fatalf("history = %d, want 2 (postgres + sqlite)", len(history))
	}
}

func TestTraverseSupersededExcluded(t *testing.T) {
	s, _ := setupTest(t)
	ctx := context.Background()

	// Save and then supersede
	s.Save(ctx, Fact{Subject: "db", Predicate: "version", Object: "pg14", Namespace: "app"})
	s.Save(ctx, Fact{Subject: "db", Predicate: "version", Object: "pg16", Namespace: "app"})

	results, err := s.Traverse(ctx, "db", "app", 1)
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}

	// Should only find the active fact (pg16), not the superseded one (pg14)
	if len(results) != 1 {
		t.Errorf("expected 1 active fact, got %d", len(results))
	}
	if len(results) > 0 && results[0].Fact.Object != "pg16" {
		t.Errorf("expected pg16, got %s", results[0].Fact.Object)
	}
}

func TestSearchRankedMatchesMultiWordTokensNotContiguousSubstring(t *testing.T) {
	s, _ := setupTest(t)
	ctx := context.Background()

	// Save a fact with tokens that match query but not as contiguous substring
	saved, err := s.Save(ctx, Fact{
		Subject:   "auth",
		Predicate: "uses",
		Object:    "jwt token",
		Namespace: "app",
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if saved.ID == "" {
		t.Fatal("expected ID to be set")
	}

	// Search with reordered tokens ("token auth") — not a contiguous substring
	results, err := s.SearchRanked(ctx, "token auth", "app", 10)
	if err != nil {
		t.Fatalf("search ranked: %v", err)
	}

	// Should find the fact because tokens are matched individually via FTS
	if len(results) == 0 {
		t.Fatal("expected to find the fact via multi-word FTS match")
	}
	if results[0].ID != saved.ID {
		t.Errorf("first result ID = %q, want %q", results[0].ID, saved.ID)
	}
	if results[0].Rank != 1 {
		t.Errorf("rank = %d, want 1", results[0].Rank)
	}
	if results[0].Subject != "auth" {
		t.Errorf("subject = %q, want 'auth'", results[0].Subject)
	}
}

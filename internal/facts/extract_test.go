package facts

import (
	"context"
	"path/filepath"
	"testing"

	"neurox/internal/db"
	"neurox/internal/observation"
)

func TestParseTriples(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     int
	}{
		{"valid triples", "project | uses | react\nauth | depends_on | jwt", 2},
		{"with dashes", "- project | uses | react\n- auth | depends_on | jwt", 2},
		{"with bullets", "• project | uses | react", 1},
		{"NONE response", "NONE", 0},
		{"empty", "", 0},
		{"invalid format", "this is not a triple", 0},
		{"mixed valid/invalid", "project | uses | react\nnot a triple\nauth | depends_on | jwt", 2},
		{"max 5", "a|b|c\nd|e|f\ng|h|i\nj|k|l\nm|n|o\np|q|r", 5},
		{"spaces around pipes", "  project  |  uses  |  react  ", 1},
		{"empty parts", "| uses | react", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTriples(tt.response)
			if len(got) != tt.want {
				t.Errorf("parseTriples() returned %d triples, want %d", len(got), tt.want)
			}
		})
	}
}

func TestParseTriplesContent(t *testing.T) {
	triples := parseTriples("project | uses_framework | react\nauth | depends_on | jwt_library")

	if len(triples) != 2 {
		t.Fatalf("expected 2 triples, got %d", len(triples))
	}

	if triples[0].subject != "project" || triples[0].predicate != "uses_framework" || triples[0].object != "react" {
		t.Errorf("triple[0] = %+v", triples[0])
	}
	if triples[1].subject != "auth" || triples[1].predicate != "depends_on" || triples[1].object != "jwt_library" {
		t.Errorf("triple[1] = %+v", triples[1])
	}
}

func TestParseTemporalObject(t *testing.T) {
	tests := []struct {
		name    string
		object  string
		wantOK  bool
		wantDay int // day of month if wantOK
	}{
		{"ISO date", "2026-03-06", true, 6},
		{"year-month", "2026-03", true, 1},
		{"not a date", "sqlite", false, 0},
		{"partial", "2026", false, 0},
		{"with spaces", " 2026-03-06 ", true, 6},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, ok := parseTemporalObject(tt.object)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && parsed.Day() != tt.wantDay {
				t.Errorf("day = %d, want %d", parsed.Day(), tt.wantDay)
			}
		})
	}
}

func TestTemporalPredicatesMap(t *testing.T) {
	temporal := []string{"happened_on", "started_on", "ended_on", "changed_on"}
	nonTemporal := []string{"uses", "depends_on", "current", "version"}

	for _, p := range temporal {
		if !temporalPredicates[p] {
			t.Errorf("%q should be temporal", p)
		}
	}
	for _, p := range nonTemporal {
		if temporalPredicates[p] {
			t.Errorf("%q should NOT be temporal", p)
		}
	}
}

type mockLLM struct {
	response string
}

func (m *mockLLM) Complete(_ context.Context, _ string) (string, error) {
	return m.response, nil
}

func (m *mockLLM) Name() string { return "mock" }

func TestExtractAndSaveTemporalFact(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	idGen := observation.NewULIDGenerator()
	store := NewStore(database, idGen)

	// Create a test observation.
	obsID := idGen.New()
	_, err = database.ExecContext(context.Background(), `
		INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES(?, 'Migration', 'We migrated on 2026-03-06', 'discovery', 0, 0.7, 0.5, 'semantic', 'app')
	`, obsID)
	if err != nil {
		t.Fatalf("insert obs: %v", err)
	}

	mock := &mockLLM{response: "migration | happened_on | 2026-03-06\ndatabase | current | sqlite"}
	extractor := NewExtractor(mock, store)

	count, err := extractor.ExtractAndSave(context.Background(), obsID, "Migration", "We migrated on 2026-03-06", "app")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}

	// The temporal fact should have explicit valid_from.
	history, _ := store.SearchHistory(context.Background(), "migration", "happened_on", "app", 10)
	if len(history) != 1 {
		t.Fatalf("history = %d, want 1", len(history))
	}
	if history[0].ValidFrom.Year() != 2026 || history[0].ValidFrom.Month() != 3 || history[0].ValidFrom.Day() != 6 {
		t.Errorf("valid_from = %v, want 2026-03-06", history[0].ValidFrom)
	}

	// The non-temporal fact should just use default valid_from (now).
	active, _ := store.Search(context.Background(), "sqlite", "app", 10)
	if len(active) != 1 {
		t.Fatalf("active = %d, want 1", len(active))
	}
}

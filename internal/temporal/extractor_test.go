package temporal

import (
	"context"
	"path/filepath"
	"testing"

	"neurox/internal/db"
	"neurox/internal/observation"
)

func TestExtractorParsesAndPersists(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	idGen := observation.NewULIDGenerator()
	store := NewStore(database, idGen)
	parser := NewParser()
	extractor := NewExtractor(parser, store)

	// Create an observation to link mentions to.
	obsID := idGen.New()
	_, err = database.ExecContext(ctx, `
		INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, source)
		VALUES(?, 'Auth migration', 'We migrated to SQLite yesterday and it is currently working well', 'discovery', 0, 0.7, 0.5, 'semantic', 'default', 'user')
	`, obsID)
	if err != nil {
		t.Fatalf("insert obs: %v", err)
	}

	count, err := extractor.Extract(ctx, obsID, "We migrated to SQLite yesterday and it is currently working well")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2 (yesterday + currently)", count)
	}

	// Verify mentions are persisted.
	mentions, err := store.ByObservation(ctx, obsID)
	if err != nil {
		t.Fatalf("by observation: %v", err)
	}
	if len(mentions) != 2 {
		t.Fatalf("persisted = %d, want 2", len(mentions))
	}

	kinds := map[MentionKind]bool{}
	for _, m := range mentions {
		kinds[m.Kind] = true
		if m.ObservationID != obsID {
			t.Errorf("mention observation_id = %q, want %q", m.ObservationID, obsID)
		}
	}
	if !kinds[KindRelative] {
		t.Error("expected a relative mention (yesterday)")
	}
	if !kinds[KindCurrentState] {
		t.Error("expected a current_state mention (currently)")
	}
}

func TestExtractorNoMentionsReturnsZero(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	idGen := observation.NewULIDGenerator()
	store := NewStore(database, idGen)
	extractor := NewExtractor(NewParser(), store)

	obsID := idGen.New()
	_, err = database.ExecContext(ctx, `
		INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, source)
		VALUES(?, 'Plain note', 'This has no temporal language at all', 'discovery', 0, 0.7, 0.5, 'semantic', 'default', 'user')
	`, obsID)
	if err != nil {
		t.Fatalf("insert obs: %v", err)
	}

	count, err := extractor.Extract(ctx, obsID, "This has no temporal language at all")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if count != 0 {
		t.Fatalf("count = %d, want 0", count)
	}
}

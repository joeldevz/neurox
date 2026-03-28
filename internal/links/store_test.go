package links

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	_ "github.com/ncruces/go-sqlite3/driver"

	"github.com/joeldevz/neurox/internal/db"
)

func newTestStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	database := newTestDB(t)
	idGen := &seqIDGenerator{}
	return NewStore(database, idGen), database
}

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

type seqIDGenerator struct {
	counter int
}

func (g *seqIDGenerator) New() string {
	g.counter++
	return fmt.Sprintf("ID%04d", g.counter)
}

func insertObservation(t *testing.T, database *sql.DB, id, title string) {
	t.Helper()
	_, err := database.ExecContext(context.Background(), `
		INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES(?, ?, 'test content', 'discovery', 0, 0.7, 0.5, 'semantic', 'default')
	`, id, title)
	if err != nil {
		t.Fatalf("insert observation %s: %v", id, err)
	}
}

func TestCreateLink(t *testing.T) {
	store, database := newTestStore(t)
	ctx := context.Background()

	insertObservation(t, database, "OBS001", "First observation")
	insertObservation(t, database, "OBS002", "Second observation")

	link, err := store.Create(ctx, CreateLinkInput{
		SourceID:     "OBS001",
		TargetID:     "OBS002",
		RelationType: RelationSupersedes,
		CreatedBy:    CreatedByAgent,
	})
	if err != nil {
		t.Fatalf("create link: %v", err)
	}

	if link.SourceID != "OBS001" {
		t.Errorf("expected source_id OBS001, got %s", link.SourceID)
	}
	if link.TargetID != "OBS002" {
		t.Errorf("expected target_id OBS002, got %s", link.TargetID)
	}
	if link.RelationType != RelationSupersedes {
		t.Errorf("expected relation_type supersedes, got %s", link.RelationType)
	}
	if link.Confidence != DefaultConfidence {
		t.Errorf("expected confidence %f, got %f", DefaultConfidence, link.Confidence)
	}
}

func TestCreateLinkDuplicateRejected(t *testing.T) {
	store, database := newTestStore(t)
	ctx := context.Background()

	insertObservation(t, database, "OBS001", "First")
	insertObservation(t, database, "OBS002", "Second")

	input := CreateLinkInput{
		SourceID:     "OBS001",
		TargetID:     "OBS002",
		RelationType: RelationRelatesTo,
	}
	if _, err := store.Create(ctx, input); err != nil {
		t.Fatalf("first create: %v", err)
	}

	_, err := store.Create(ctx, input)
	if err == nil {
		t.Fatal("expected error for duplicate link")
	}
}

func TestCreateLinkSelfReferenceRejected(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	_, err := store.Create(ctx, CreateLinkInput{
		SourceID:     "OBS001",
		TargetID:     "OBS001",
		RelationType: RelationRelatesTo,
	})
	if err == nil {
		t.Fatal("expected error for self-reference")
	}
}

func TestGetBySourceAndTarget(t *testing.T) {
	store, database := newTestStore(t)
	ctx := context.Background()

	insertObservation(t, database, "OBS001", "First")
	insertObservation(t, database, "OBS002", "Second")
	insertObservation(t, database, "OBS003", "Third")

	store.Create(ctx, CreateLinkInput{SourceID: "OBS001", TargetID: "OBS002", RelationType: RelationSupersedes})
	store.Create(ctx, CreateLinkInput{SourceID: "OBS001", TargetID: "OBS003", RelationType: RelationRelatesTo})
	store.Create(ctx, CreateLinkInput{SourceID: "OBS003", TargetID: "OBS002", RelationType: RelationContradicts})

	bySource, err := store.GetBySource(ctx, "OBS001")
	if err != nil {
		t.Fatalf("get by source: %v", err)
	}
	if len(bySource) != 2 {
		t.Fatalf("expected 2 links from OBS001, got %d", len(bySource))
	}

	byTarget, err := store.GetByTarget(ctx, "OBS002")
	if err != nil {
		t.Fatalf("get by target: %v", err)
	}
	if len(byTarget) != 2 {
		t.Fatalf("expected 2 links to OBS002, got %d", len(byTarget))
	}

	// Filter by relation type
	supersedes, err := store.GetBySource(ctx, "OBS001", RelationSupersedes)
	if err != nil {
		t.Fatalf("get by source filtered: %v", err)
	}
	if len(supersedes) != 1 {
		t.Fatalf("expected 1 supersedes link, got %d", len(supersedes))
	}
}

func TestTraverseOneHop(t *testing.T) {
	store, database := newTestStore(t)
	ctx := context.Background()

	insertObservation(t, database, "OBS001", "Center")
	insertObservation(t, database, "OBS002", "Neighbor A")
	insertObservation(t, database, "OBS003", "Neighbor B")

	store.Create(ctx, CreateLinkInput{SourceID: "OBS001", TargetID: "OBS002", RelationType: RelationRelatesTo})
	store.Create(ctx, CreateLinkInput{SourceID: "OBS003", TargetID: "OBS001", RelationType: RelationRefines})

	results, err := store.Traverse(ctx, "OBS001", 1)
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 neighbors, got %d", len(results))
	}

	ids := map[string]bool{}
	for _, r := range results {
		ids[r.ObservationID] = true
		if r.Depth != 1 {
			t.Errorf("expected depth 1, got %d", r.Depth)
		}
		if len(r.Path) != 1 {
			t.Errorf("expected path length 1, got %d", len(r.Path))
		}
	}

	if !ids["OBS002"] || !ids["OBS003"] {
		t.Errorf("expected OBS002 and OBS003, got %v", ids)
	}
}

func TestTraverseMultiHop(t *testing.T) {
	store, database := newTestStore(t)
	ctx := context.Background()

	// Chain: OBS001 -> OBS002 -> OBS003 -> OBS004
	insertObservation(t, database, "OBS001", "Node 1")
	insertObservation(t, database, "OBS002", "Node 2")
	insertObservation(t, database, "OBS003", "Node 3")
	insertObservation(t, database, "OBS004", "Node 4")

	store.Create(ctx, CreateLinkInput{SourceID: "OBS001", TargetID: "OBS002", RelationType: RelationRelatesTo})
	store.Create(ctx, CreateLinkInput{SourceID: "OBS002", TargetID: "OBS003", RelationType: RelationRelatesTo})
	store.Create(ctx, CreateLinkInput{SourceID: "OBS003", TargetID: "OBS004", RelationType: RelationRelatesTo})

	// Depth 2: should find OBS002 (depth 1) and OBS003 (depth 2), but NOT OBS004
	results, err := store.Traverse(ctx, "OBS001", 2)
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results at depth 2, got %d", len(results))
	}

	depths := map[string]int{}
	for _, r := range results {
		depths[r.ObservationID] = r.Depth
	}

	if depths["OBS002"] != 1 {
		t.Errorf("expected OBS002 at depth 1, got %d", depths["OBS002"])
	}
	if depths["OBS003"] != 2 {
		t.Errorf("expected OBS003 at depth 2, got %d", depths["OBS003"])
	}

	// Depth 3: should find all three
	results3, err := store.Traverse(ctx, "OBS001", 3)
	if err != nil {
		t.Fatalf("traverse depth 3: %v", err)
	}
	if len(results3) != 3 {
		t.Fatalf("expected 3 results at depth 3, got %d", len(results3))
	}

	// Verify path for OBS003 has 2 entries
	for _, r := range results3 {
		if r.ObservationID == "OBS003" && len(r.Path) != 2 {
			t.Errorf("expected path length 2 for OBS003, got %d", len(r.Path))
		}
	}
}

func TestTraverseNoCycles(t *testing.T) {
	store, database := newTestStore(t)
	ctx := context.Background()

	// Cycle: OBS001 <-> OBS002 <-> OBS003 -> OBS001
	insertObservation(t, database, "OBS001", "Node 1")
	insertObservation(t, database, "OBS002", "Node 2")
	insertObservation(t, database, "OBS003", "Node 3")

	store.Create(ctx, CreateLinkInput{SourceID: "OBS001", TargetID: "OBS002", RelationType: RelationRelatesTo})
	store.Create(ctx, CreateLinkInput{SourceID: "OBS002", TargetID: "OBS003", RelationType: RelationRelatesTo})
	store.Create(ctx, CreateLinkInput{SourceID: "OBS003", TargetID: "OBS001", RelationType: RelationRelatesTo})

	results, err := store.Traverse(ctx, "OBS001", 5)
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}

	// Should visit OBS002 and OBS003 exactly once, never revisit OBS001
	if len(results) != 2 {
		t.Fatalf("expected 2 results (no cycles), got %d", len(results))
	}
}

func TestDeleteLink(t *testing.T) {
	store, database := newTestStore(t)
	ctx := context.Background()

	insertObservation(t, database, "OBS001", "First")
	insertObservation(t, database, "OBS002", "Second")

	link, err := store.Create(ctx, CreateLinkInput{SourceID: "OBS001", TargetID: "OBS002", RelationType: RelationRelatesTo})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := store.Delete(ctx, link.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	_, err = store.Get(ctx, link.ID)
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

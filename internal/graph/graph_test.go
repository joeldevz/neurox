package graph

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"neurox/internal/db"
)

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

func insertObs(t *testing.T, database *sql.DB, id, title, obsType string, importance float64, namespace string) {
	t.Helper()
	_, err := database.ExecContext(context.Background(), `
		INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, staleness)
		VALUES(?, ?, 'test content for '||?, ?, 2, 0.7, ?, 'semantic', ?, 'fresh')
	`, id, title, title, obsType, importance, namespace)
	if err != nil {
		t.Fatalf("insert observation %s: %v", id, err)
	}
}

func insertLink(t *testing.T, database *sql.DB, id, sourceID, targetID, relType string) {
	t.Helper()
	_, err := database.ExecContext(context.Background(), `
		INSERT INTO observation_links(id, source_id, target_id, relation_type, confidence, created_by)
		VALUES(?, ?, ?, ?, 1.0, 'agent')
	`, id, sourceID, targetID, relType)
	if err != nil {
		t.Fatalf("insert link %s: %v", id, err)
	}
}

func TestQueryReturnsNodesAndEdges(t *testing.T) {
	database := newTestDB(t)

	insertObs(t, database, "OBS1", "First observation", "decision", 0.8, "test")
	insertObs(t, database, "OBS2", "Second observation", "discovery", 0.6, "test")
	insertObs(t, database, "OBS3", "Third observation", "bugfix", 0.4, "test")
	insertLink(t, database, "LNK1", "OBS1", "OBS2", "relates_to")
	insertLink(t, database, "LNK2", "OBS2", "OBS3", "derived_from")

	data, err := Query(context.Background(), database, Options{Namespace: "test", Limit: 100})
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	if data.Stats.ShownNodes != 3 {
		t.Errorf("expected 3 nodes, got %d", data.Stats.ShownNodes)
	}
	if data.Stats.ShownEdges != 2 {
		t.Errorf("expected 2 edges, got %d", data.Stats.ShownEdges)
	}
	if data.Nodes[0].ID != "OBS1" {
		t.Errorf("expected first node OBS1 (highest importance), got %s", data.Nodes[0].ID)
	}
}

func TestQueryFiltersNamespace(t *testing.T) {
	database := newTestDB(t)

	insertObs(t, database, "OBS1", "In namespace A", "decision", 0.8, "ns-a")
	insertObs(t, database, "OBS2", "In namespace B", "discovery", 0.6, "ns-b")

	data, err := Query(context.Background(), database, Options{Namespace: "ns-a", Limit: 100})
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	if data.Stats.ShownNodes != 1 {
		t.Errorf("expected 1 node, got %d", data.Stats.ShownNodes)
	}
	if data.Nodes[0].Namespace != "ns-a" {
		t.Errorf("expected namespace ns-a, got %s", data.Nodes[0].Namespace)
	}
}

func TestQueryFiltersType(t *testing.T) {
	database := newTestDB(t)

	insertObs(t, database, "OBS1", "Decision obs", "decision", 0.8, "test")
	insertObs(t, database, "OBS2", "Bugfix obs", "bugfix", 0.6, "test")

	data, err := Query(context.Background(), database, Options{ObservationType: "bugfix", Limit: 100})
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	if data.Stats.ShownNodes != 1 {
		t.Errorf("expected 1 node, got %d", data.Stats.ShownNodes)
	}
	if data.Nodes[0].ObservationType != "bugfix" {
		t.Errorf("expected bugfix, got %s", data.Nodes[0].ObservationType)
	}
}

func TestQueryFiltersMinImportance(t *testing.T) {
	database := newTestDB(t)

	insertObs(t, database, "OBS1", "High importance", "decision", 0.9, "test")
	insertObs(t, database, "OBS2", "Low importance", "discovery", 0.1, "test")

	data, err := Query(context.Background(), database, Options{MinImportance: 0.5, Limit: 100})
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	if data.Stats.ShownNodes != 1 {
		t.Errorf("expected 1 node, got %d", data.Stats.ShownNodes)
	}
}

func TestQueryLinkedOnly(t *testing.T) {
	database := newTestDB(t)

	insertObs(t, database, "OBS1", "Linked obs", "decision", 0.8, "test")
	insertObs(t, database, "OBS2", "Also linked", "discovery", 0.6, "test")
	insertObs(t, database, "OBS3", "Lonely obs", "bugfix", 0.4, "test")
	insertLink(t, database, "LNK1", "OBS1", "OBS2", "relates_to")

	data, err := Query(context.Background(), database, Options{LinkedOnly: true, Limit: 100})
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	if data.Stats.ShownNodes != 2 {
		t.Errorf("expected 2 linked nodes, got %d", data.Stats.ShownNodes)
	}
}

func TestQueryLimit(t *testing.T) {
	database := newTestDB(t)

	for i := 0; i < 10; i++ {
		insertObs(t, database, fmt.Sprintf("OBS%d", i), fmt.Sprintf("Obs %d", i), "discovery", float64(i)*0.1, "test")
	}

	data, err := Query(context.Background(), database, Options{Limit: 3})
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	if data.Stats.ShownNodes != 3 {
		t.Errorf("expected 3 nodes, got %d", data.Stats.ShownNodes)
	}
}

func TestQueryEdgesOnlyBetweenSelectedNodes(t *testing.T) {
	database := newTestDB(t)

	insertObs(t, database, "OBS1", "In scope", "decision", 0.9, "ns-a")
	insertObs(t, database, "OBS2", "Out of scope", "discovery", 0.8, "ns-b")
	insertLink(t, database, "LNK1", "OBS1", "OBS2", "relates_to")

	data, err := Query(context.Background(), database, Options{Namespace: "ns-a", Limit: 100})
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	if data.Stats.ShownNodes != 1 {
		t.Errorf("expected 1 node, got %d", data.Stats.ShownNodes)
	}
	if data.Stats.ShownEdges != 0 {
		t.Errorf("expected 0 edges (target not in scope), got %d", data.Stats.ShownEdges)
	}
}

func TestQueryIncludesContent(t *testing.T) {
	database := newTestDB(t)

	insertObs(t, database, "OBS1", "Obs with content", "decision", 0.8, "test")

	data, err := Query(context.Background(), database, Options{Namespace: "test", Limit: 100})
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	if data.Stats.ShownNodes != 1 {
		t.Fatalf("expected 1 node, got %d", data.Stats.ShownNodes)
	}
	if !strings.Contains(data.Nodes[0].Content, "test content") {
		t.Errorf("expected content to contain 'test content', got %q", data.Nodes[0].Content)
	}
}

func TestRenderHTMLProducesValidOutput(t *testing.T) {
	data := &Data{
		Nodes: []Node{
			{ID: "N1", Title: "Test Node", Content: "Some content", ObservationType: "decision", Layer: 2, Importance: 0.9},
		},
		Edges: []Edge{},
		Stats: Stats{TotalObservations: 1, TotalLinks: 0, ShownNodes: 1, ShownEdges: 0},
	}

	var buf bytes.Buffer
	if err := RenderHTML(&buf, data); err != nil {
		t.Fatalf("render: %v", err)
	}

	html := buf.String()
	if !strings.Contains(html, "<!DOCTYPE html>") {
		t.Error("missing DOCTYPE")
	}
	if !strings.Contains(html, "vis-network") {
		t.Error("missing vis-network script")
	}
	if !strings.Contains(html, "Test Node") {
		t.Error("missing node data in HTML")
	}
	if !strings.Contains(html, "neurox graph") {
		t.Error("missing title")
	}
}

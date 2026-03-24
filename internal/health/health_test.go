package health

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/joeldevz/neurox/internal/db"
	"github.com/joeldevz/neurox/internal/telemetry"
)

func TestCheckAllHealthy(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	ctx := context.Background()
	// Insert observations with good coverage
	for i := 0; i < 10; i++ {
		obsType := []string{"decision", "bugfix", "discovery", "pattern", "gotcha", "config", "preference", "question"}[i%8]
		kind := []string{"semantic", "episodic", "procedural"}[i%3]
		_, execErr := database.ExecContext(ctx, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, tags)
            VALUES(?, 'test', 'content', ?, 0, 0.7, 0.5, ?, 'default', 'go,test')`,
			fmt.Sprintf("OBS_%d", i),
			obsType,
			kind)
		if execErr != nil {
			t.Fatalf("insert observation %d: %v", i, execErr)
		}
		// Set embedding via UPDATE to avoid inline blob issues
		database.ExecContext(ctx, `UPDATE observations SET embedding = X'00000000' WHERE id = ?`,
			fmt.Sprintf("OBS_%d", i))
	}
	// Add file links
	for i := 0; i < 5; i++ {
		idStr := fmt.Sprintf("FLINK_%d", i)
		database.ExecContext(ctx, `INSERT INTO file_observations(id, observation_id, file_path) VALUES(?, ?, 'test.go')`,
			idStr, fmt.Sprintf("OBS_%d", i))
	}
	// Add observation link
	database.ExecContext(ctx, `INSERT INTO observation_links(id, source_id, target_id, relation_type, created_by) VALUES('LINK_0', 'OBS_0', 'OBS_1', 'relates_to', 'user')`)
	// Add consolidation run
	database.ExecContext(ctx, `INSERT INTO consolidation_runs(id, epoch, status, started_at, completed_at) VALUES('run1', 1, 'completed', datetime('now'), datetime('now'))`)

	report := Check(ctx, Deps{DB: database})
	if report.StaticScore < 30 {
		t.Errorf("expected static score >= 30 for well-populated DB, got %d", report.StaticScore)
	}
	t.Logf("Score: %d, Grade: %s", report.Score, report.Grade)
}

func TestCheckAllDegraded(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	ctx := context.Background()
	// Insert observation with no tags, no embedding, no files
	_, execErr := database.ExecContext(ctx, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
        VALUES('OBS_1', 'test', 'content', 'discovery', 0, 0.7, 0.5, 'semantic', 'default')`)
	if execErr != nil {
		t.Fatalf("insert observation: %v", execErr)
	}

	report := Check(ctx, Deps{DB: database})
	if report.StaticScore >= 30 {
		t.Errorf("expected static score < 30 for degraded DB, got %d", report.StaticScore)
	}
}

func TestCheckMixed(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	ctx := context.Background()
	// Half with tags, half without; some with embeddings
	for i := 0; i < 4; i++ {
		tags := ""
		if i < 2 {
			tags = "go,test"
		}
		_, execErr := database.ExecContext(ctx, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, tags)
            VALUES(?, 'test', 'content', 'discovery', 0, 0.7, 0.5, 'semantic', 'default', ?)`,
			fmt.Sprintf("OBS_%d", i), tags)
		if execErr != nil {
			t.Fatalf("insert observation %d: %v", i, execErr)
		}
		// Only set embedding for first observation
		if i < 1 {
			database.ExecContext(ctx, `UPDATE observations SET embedding = X'00000000' WHERE id = ?`,
				fmt.Sprintf("OBS_%d", i))
		}
	}

	report := Check(ctx, Deps{DB: database})
	if report.Score < 5 || report.Score > 80 {
		t.Errorf("expected mixed score between 5 and 80, got %d", report.Score)
	}
}

func TestCheckNoTelemetryData(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	ctx := context.Background()
	tracker := telemetry.NewTracker(database)
	report := Check(ctx, Deps{DB: database, Tracker: tracker})

	foundNoData := false
	for _, d := range report.Dimensions {
		if d.Category == "dynamic" && d.Status == "no_data" {
			foundNoData = true
			break
		}
	}
	if !foundNoData {
		t.Error("expected dynamic dimension with no_data status when no tool calls exist")
	}
}

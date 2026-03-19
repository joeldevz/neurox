package decay

import (
	"context"
	"database/sql"
	"math"
	"path/filepath"
	"testing"

	"neurox/internal/db"
)

func TestActivationScore(t *testing.T) {
	tests := []struct {
		name        string
		importance  float64
		daysSince   float64
		accessCount int
		wantMin     float64
		wantMax     float64
	}{
		{"fresh high importance", 0.9, 0, 0, 0.9, 1.0},
		{"old low importance", 0.1, 60, 0, 0.0, 0.01},
		{"old but accessed often", 0.1, 60, 50, 0.5, 1.5},
		{"at floor", 0.01, 30, 0, 0.0, 0.01},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := ActivationScore(tt.importance, tt.daysSince, tt.accessCount)
			if score < tt.wantMin || score > tt.wantMax {
				t.Errorf("score = %f, want [%f, %f]", score, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestActivationScoreDecaysOverTime(t *testing.T) {
	score0 := ActivationScore(0.5, 0, 5)
	score30 := ActivationScore(0.5, 30, 5)
	score90 := ActivationScore(0.5, 90, 5)

	if score30 >= score0 {
		t.Errorf("30-day score (%f) should be less than 0-day (%f)", score30, score0)
	}
	if score90 >= score30 {
		t.Errorf("90-day score (%f) should be less than 30-day (%f)", score90, score30)
	}
}

func TestApplyDecay(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()
	ctx := context.Background()

	// Insert observations with different kinds
	database.ExecContext(ctx, `
		INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES
		('EP1', 'Episodic', 'content', 'discovery', 0, 0.7, 0.5, 'episodic', 'default'),
		('SM1', 'Semantic', 'content', 'discovery', 0, 0.7, 0.5, 'semantic', 'default'),
		('PR1', 'Procedural', 'content', 'discovery', 0, 0.7, 0.5, 'procedural', 'default'),
		('CR1', 'Core item', 'content', 'discovery', 2, 0.7, 0.5, 'semantic', 'default')
	`)

	engine := NewEngine(database)
	affected, err := engine.ApplyDecay(ctx)
	if err != nil {
		t.Fatalf("apply decay: %v", err)
	}
	if affected != 3 {
		t.Errorf("expected 3 affected (not Core), got %d", affected)
	}

	// Verify different decay rates by kind
	var epImportance, smImportance, prImportance, crImportance float64
	database.QueryRowContext(ctx, "SELECT importance FROM observations WHERE id = 'EP1'").Scan(&epImportance)
	database.QueryRowContext(ctx, "SELECT importance FROM observations WHERE id = 'SM1'").Scan(&smImportance)
	database.QueryRowContext(ctx, "SELECT importance FROM observations WHERE id = 'PR1'").Scan(&prImportance)
	database.QueryRowContext(ctx, "SELECT importance FROM observations WHERE id = 'CR1'").Scan(&crImportance)

	// Episodic decays most (0.5 - 0.02*1.0 = 0.48)
	if math.Abs(epImportance-0.48) > 0.001 {
		t.Errorf("episodic importance = %f, want ~0.48", epImportance)
	}
	// Semantic (0.5 - 0.02*0.6 = 0.488)
	if math.Abs(smImportance-0.488) > 0.001 {
		t.Errorf("semantic importance = %f, want ~0.488", smImportance)
	}
	// Procedural (0.5 - 0.02*0.2 = 0.496)
	if math.Abs(prImportance-0.496) > 0.001 {
		t.Errorf("procedural importance = %f, want ~0.496", prImportance)
	}
	// Core stays unchanged
	if crImportance != 0.5 {
		t.Errorf("core importance = %f, want 0.5 (unchanged)", crImportance)
	}
}

func TestDecayFloor(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()
	ctx := context.Background()

	// Near-floor importance
	database.ExecContext(ctx, `
		INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace)
		VALUES('LOW1', 'Low', 'content', 'discovery', 0, 0.7, 0.015, 'episodic', 'default')
	`)

	engine := NewEngine(database)
	engine.ApplyDecay(ctx)

	var importance float64
	database.QueryRowContext(ctx, "SELECT importance FROM observations WHERE id = 'LOW1'").Scan(&importance)

	if importance < decayFloor {
		t.Errorf("importance %f below floor %f", importance, decayFloor)
	}
}

func TestGarbageCollect(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "neurox.db")
	database, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()
	ctx := context.Background()

	// Insert old, low-importance observation
	database.ExecContext(ctx, `
		INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, access_count, created_at)
		VALUES('OLD1', 'Old forgotten', 'content', 'discovery', 0, 0.1, 0.01, 'episodic', 'default', 0, datetime('now', '-60 days'))
	`)

	// Insert recent observation (should not be GC'd)
	database.ExecContext(ctx, `
		INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, access_count)
		VALUES('NEW1', 'New important', 'content', 'discovery', 0, 0.7, 0.5, 'semantic', 'default', 5)
	`)

	engine := NewEngine(database)
	deleted, err := engine.GarbageCollect(ctx)
	if err != nil {
		t.Fatalf("gc: %v", err)
	}

	if deleted != 1 {
		t.Errorf("expected 1 deleted, got %d", deleted)
	}

	// Verify OLD1 is soft-deleted
	var deletedAt sql.NullString
	database.QueryRowContext(ctx, "SELECT deleted_at FROM observations WHERE id = 'OLD1'").Scan(&deletedAt)
	if !deletedAt.Valid {
		t.Error("OLD1 should be soft-deleted")
	}

	// Verify NEW1 still alive
	database.QueryRowContext(ctx, "SELECT deleted_at FROM observations WHERE id = 'NEW1'").Scan(&deletedAt)
	if deletedAt.Valid {
		t.Error("NEW1 should not be deleted")
	}
}

package consolidate

import (
	"context"
	"testing"

	"github.com/joeldevz/neurox/internal/db"
)

func TestReconcileScoresRecoversDepressedCore(t *testing.T) {
	p, tdb := newTestPipeline(t)
	ctx := context.Background()

	// Create durable Core observations with depressed importance (0.01)
	// These should be recalibrated
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, created_at)
		VALUES('CORE_DEC', 'Decision in Core', 'Use CQRS', 'decision', 2, 0.9, 0.01, 0.1, 0.2, 'semantic', 'default', 'durable', 10, datetime('now', '-30 days'))`)
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, created_at)
		VALUES('CORE_BUG', 'Bugfix in Core', 'Fixed race', 'bugfix', 2, 0.9, 0.01, 0.1, 0.2, 'semantic', 'default', 'durable', 5, datetime('now', '-30 days'))`)
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, created_at)
		VALUES('CORE_DISC', 'Discovery in Core', 'Found issue', 'discovery', 2, 0.9, 0.01, 0.1, 0.2, 'semantic', 'default', 'durable', 3, datetime('now', '-30 days'))`)

	// Run reconciliation
	result, err := p.ReconcileScores(ctx)
	if err != nil {
		t.Fatalf("reconcile scores: %v", err)
	}

	// Verify counts
	if result.CoreRecalibrated != 3 {
		t.Errorf("expected 3 Core recalibrated, got %d", result.CoreRecalibrated)
	}

	// Verify importance was recalibrated
	assertImportanceRange(t, tdb, "CORE_DEC", 0.70, 1.0)
	assertImportanceRange(t, tdb, "CORE_BUG", 0.70, 1.0)
	assertImportanceRange(t, tdb, "CORE_DISC", 0.50, 1.0)

	// Verify activation and consolidation were boosted
	assertActivationMin(t, tdb, "CORE_DEC", 0.30)
	assertConsolidationMin(t, tdb, "CORE_DEC", 0.50)
}

func TestReconcileScoresRecoversDepressedWorking(t *testing.T) {
	p, tdb := newTestPipeline(t)
	ctx := context.Background()

	// Create durable Working observations with depressed importance
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, created_at)
		VALUES('WORK_DEC', 'Decision in Working', 'Use interfaces', 'decision', 1, 0.9, 0.01, 0.1, 0.15, 'semantic', 'default', 'durable', 10, datetime('now', '-30 days'))`)
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, created_at)
		VALUES('WORK_PREF', 'Preference in Working', 'Like dark mode', 'preference', 1, 0.9, 0.01, 0.1, 0.15, 'semantic', 'default', 'durable', 5, datetime('now', '-30 days'))`)

	// Run reconciliation
	result, err := p.ReconcileScores(ctx)
	if err != nil {
		t.Fatalf("reconcile scores: %v", err)
	}

	if result.WorkingRecalibrated != 2 {
		t.Errorf("expected 2 Working recalibrated, got %d", result.WorkingRecalibrated)
	}

	// Verify importance was recalibrated (lower thresholds than Core)
	assertImportanceRange(t, tdb, "WORK_DEC", 0.60, 1.0)
	assertImportanceRange(t, tdb, "WORK_PREF", 0.50, 1.0)
}

func TestReconcileScoresRecoversDepressedBuffer(t *testing.T) {
	p, tdb := newTestPipeline(t)
	ctx := context.Background()

	// Create durable Buffer observations with depressed importance
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, created_at)
		VALUES('BUF_DEC', 'Decision in Buffer', 'Use Go', 'decision', 0, 0.9, 0.01, 0.1, 0.1, 'semantic', 'default', 'durable', 10, datetime('now', '-30 days'))`)
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, created_at)
		VALUES('BUF_DISC', 'Discovery in Buffer', 'Found bug', 'discovery', 0, 0.9, 0.01, 0.1, 0.1, 'semantic', 'default', 'durable', 3, datetime('now', '-30 days'))`)

	// Run reconciliation
	result, err := p.ReconcileScores(ctx)
	if err != nil {
		t.Fatalf("reconcile scores: %v", err)
	}

	if result.BufferRecalibrated != 2 {
		t.Errorf("expected 2 Buffer recalibrated, got %d", result.BufferRecalibrated)
	}

	// Verify importance was recalibrated (lowest thresholds)
	assertImportanceRange(t, tdb, "BUF_DEC", 0.50, 1.0)
	assertImportanceRange(t, tdb, "BUF_DISC", 0.30, 1.0)
}

func TestReconcileScoresPreservesOperational(t *testing.T) {
	p, tdb := newTestPipeline(t)
	ctx := context.Background()

	// Create operational observations with depressed importance
	// These should NOT be recalibrated
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, created_at)
		VALUES('OPS_CORE', 'Step log', 'Step 5 done', 'discovery', 2, 0.9, 0.01, 0.1, 0.1, 'semantic', 'default', 'operational', 10, datetime('now', '-30 days'))`)
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, created_at)
		VALUES('OPS_WORKING', 'Build log', 'Build passed', 'discovery', 1, 0.9, 0.01, 0.1, 0.1, 'semantic', 'default', 'operational', 5, datetime('now', '-30 days'))`)

	// Run reconciliation
	result, err := p.ReconcileScores(ctx)
	if err != nil {
		t.Fatalf("reconcile scores: %v", err)
	}

	// Operational observations should NOT be recalibrated
	if result.CoreRecalibrated != 0 {
		t.Errorf("expected 0 Core operational recalibrated, got %d", result.CoreRecalibrated)
	}
	if result.WorkingRecalibrated != 0 {
		t.Errorf("expected 0 Working operational recalibrated, got %d", result.WorkingRecalibrated)
	}

	// Verify importance stayed at 0.01
	assertImportanceRange(t, tdb, "OPS_CORE", 0.01, 0.01)
	assertImportanceRange(t, tdb, "OPS_WORKING", 0.01, 0.01)
}

func TestReconcileScoresPreservesHighImportance(t *testing.T) {
	p, tdb := newTestPipeline(t)
	ctx := context.Background()

	// Create observations with already-high importance
	// These should NOT be recalibrated (no need)
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, created_at)
		VALUES('HIGH_CORE', 'Already high', 'Important', 'decision', 2, 0.9, 0.80, 0.1, 0.1, 'semantic', 'default', 'durable', 10, datetime('now', '-30 days'))`)
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, created_at)
		VALUES('MID_WORKING', 'Medium high', 'Somewhat important', 'discovery', 1, 0.9, 0.50, 0.1, 0.1, 'semantic', 'default', 'durable', 5, datetime('now', '-30 days'))`)

	// Run reconciliation
	result, err := p.ReconcileScores(ctx)
	if err != nil {
		t.Fatalf("reconcile scores: %v", err)
	}

	// These should NOT be recalibrated (importance > 0.05)
	if result.CoreRecalibrated != 0 {
		t.Errorf("expected 0 Core recalibrated (already high), got %d", result.CoreRecalibrated)
	}
	if result.WorkingRecalibrated != 0 {
		t.Errorf("expected 0 Working recalibrated (already high), got %d", result.WorkingRecalibrated)
	}

	// Verify importance stayed the same
	assertImportanceRange(t, tdb, "HIGH_CORE", 0.80, 0.80)
	assertImportanceRange(t, tdb, "MID_WORKING", 0.50, 0.50)
}

func TestReconcileScoresBoostsHighAccess(t *testing.T) {
	p, tdb := newTestPipeline(t)
	ctx := context.Background()

	// Create durable observations with high access counts but low consolidation
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, created_at)
		VALUES('HIGH_ACCESS_1', 'Frequently accessed', 'Important', 'decision', 1, 0.9, 0.50, 0.5, 0.20, 'semantic', 'default', 'durable', 10, datetime('now', '-30 days'))`)
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, created_at)
		VALUES('HIGH_ACCESS_2', 'Also frequent', 'Important too', 'pattern', 2, 0.9, 0.60, 0.5, 0.30, 'semantic', 'default', 'durable', 20, datetime('now', '-30 days'))`)

	// Run reconciliation
	result, err := p.ReconcileScores(ctx)
	if err != nil {
		t.Fatalf("reconcile scores: %v", err)
	}

	// Should boost consolidation_strength for high-access observations
	if result.ConsolidationBoosted != 2 {
		t.Errorf("expected 2 high-access observations boosted, got %d", result.ConsolidationBoosted)
	}

	// Verify consolidation was boosted
	// HIGH_ACCESS_1: 0.20 + (10 * 0.02) = 0.40
	// HIGH_ACCESS_2: 0.30 + (20 * 0.02) = 0.70, capped at 0.70
	assertConsolidationMin(t, tdb, "HIGH_ACCESS_1", 0.35)
	assertConsolidationMin(t, tdb, "HIGH_ACCESS_2", 0.30) // Was already 0.30, may not change
}

func TestReconcileScoresPatchesLowActivation(t *testing.T) {
	p, tdb := newTestPipeline(t)
	ctx := context.Background()

	// Create observations with very low activation AND low importance (so they don't get caught by signals adjustment)
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, created_at)
		VALUES('LOW_ACT_1', 'Low activation', 'Content', 'discovery', 1, 0.9, 0.30, 0.05, 0.5, 'semantic', 'default', 'durable', 0, datetime('now', '-30 days'))`)
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, created_at)
		VALUES('LOW_ACT_2', 'Very low activation', 'Content', 'decision', 2, 0.9, 0.35, 0.02, 0.6, 'semantic', 'default', 'durable', 0, datetime('now', '-30 days'))`)

	// Run reconciliation
	result, err := p.ReconcileScores(ctx)
	if err != nil {
		t.Fatalf("reconcile scores: %v", err)
	}

	// Should patch activation for very low values (these have importance < 0.40 so they won't be caught by signals adjustment)
	if result.ActivationPatched != 2 {
		t.Errorf("expected 2 low-activation observations patched, got %d", result.ActivationPatched)
	}

	// Verify activation was patched to at least 0.20
	assertActivationMin(t, tdb, "LOW_ACT_1", 0.20)
	assertActivationMin(t, tdb, "LOW_ACT_2", 0.20)
}

func TestReconcileScoresAdjustsSignals(t *testing.T) {
	p, tdb := newTestPipeline(t)
	ctx := context.Background()

	// Create observations with high importance but low signals
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, created_at)
		VALUES('HIGH_IMP_LOW_SIG', 'High importance', 'Content', 'decision', 2, 0.9, 0.80, 0.10, 0.20, 'semantic', 'default', 'durable', 0, datetime('now', '-30 days'))`)

	// Run reconciliation
	_, err := p.ReconcileScores(ctx)
	if err != nil {
		t.Fatalf("reconcile scores: %v", err)
	}

	// Signals should be adjusted for high-importance observations
	assertActivationMin(t, tdb, "HIGH_IMP_LOW_SIG", 0.30)
	assertConsolidationMin(t, tdb, "HIGH_IMP_LOW_SIG", 0.40)
}

func TestReconcileScoresPreservesIDsAndRelations(t *testing.T) {
	p, tdb := newTestPipeline(t)
	ctx := context.Background()

	// Create observation with depressed importance
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, created_at)
		VALUES('PRESERVE_TEST', 'To recalibrate', 'Content', 'decision', 2, 0.9, 0.01, 0.1, 0.1, 'semantic', 'default', 'durable', 10, datetime('now', '-30 days'))`)

	// Create a link to another observation
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, kind, namespace, retention, created_at)
		VALUES('LINK_TARGET', 'Target', 'Content', 'discovery', 2, 0.9, 0.50, 'semantic', 'default', 'durable', datetime('now', '-30 days'))`)
	tdb.Exec(t, `INSERT INTO observation_links(id, source_id, target_id, relation_type, confidence, created_by)
		VALUES('LINK001', 'PRESERVE_TEST', 'LINK_TARGET', 'relates_to', 0.8, 'consolidator')`)

	// Run reconciliation
	_, err := p.ReconcileScores(ctx)
	if err != nil {
		t.Fatalf("reconcile scores: %v", err)
	}

	// Verify ID is preserved
	var id string
	err = tdb.DB.QueryRowContext(ctx, "SELECT id FROM observations WHERE id = 'PRESERVE_TEST'").Scan(&id)
	if err != nil {
		t.Errorf("ID was not preserved: %v", err)
	}
	if id != "PRESERVE_TEST" {
		t.Errorf("ID changed: expected PRESERVE_TEST, got %s", id)
	}

	// Verify link is preserved
	var linkCount int
	err = tdb.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM observation_links WHERE source_id = 'PRESERVE_TEST'").Scan(&linkCount)
	if err != nil {
		t.Fatalf("link query failed: %v", err)
	}
	if linkCount != 1 {
		t.Errorf("link was not preserved: expected 1, got %d", linkCount)
	}
}

func TestReconcileScoresIdempotent(t *testing.T) {
	p, tdb := newTestPipeline(t)
	ctx := context.Background()

	// Create observation with depressed importance
	tdb.Exec(t, `INSERT INTO observations(id, title, content, observation_type, layer, confidence, importance, activation_level, consolidation_strength, kind, namespace, retention, access_count, created_at)
		VALUES('IDEMPOTENT_TEST', 'To recalibrate', 'Content', 'decision', 2, 0.9, 0.01, 0.1, 0.1, 'semantic', 'default', 'durable', 10, datetime('now', '-30 days'))`)

	// Run reconciliation first time
	result1, err := p.ReconcileScores(ctx)
	if err != nil {
		t.Fatalf("first reconcile: %v", err)
	}

	if result1.CoreRecalibrated != 1 {
		t.Errorf("expected 1 Core recalibrated on first run, got %d", result1.CoreRecalibrated)
	}

	// Get importance after first run
	var importance1 float64
	tdb.DB.QueryRowContext(ctx, "SELECT importance FROM observations WHERE id = 'IDEMPOTENT_TEST'").Scan(&importance1)

	// Run reconciliation second time
	result2, err := p.ReconcileScores(ctx)
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}

	// Should not recalibrate again (importance now > 0.05)
	if result2.CoreRecalibrated != 0 {
		t.Errorf("expected 0 Core recalibrated on second run (already done), got %d", result2.CoreRecalibrated)
	}

	// Get importance after second run
	var importance2 float64
	tdb.DB.QueryRowContext(ctx, "SELECT importance FROM observations WHERE id = 'IDEMPOTENT_TEST'").Scan(&importance2)

	// Importance should be the same
	if importance1 != importance2 {
		t.Errorf("importance changed between runs: first=%.3f, second=%.3f", importance1, importance2)
	}
}

// Helper functions for assertions

func assertImportanceRange(t *testing.T, tdb *db.TestDB, id string, min, max float64) {
	t.Helper()
	var importance float64
	err := tdb.DB.QueryRowContext(context.Background(), "SELECT importance FROM observations WHERE id = ?", id).Scan(&importance)
	if err != nil {
		t.Fatalf("get importance for %s: %v", id, err)
	}
	if importance < min || importance > max {
		t.Errorf("%s: importance = %.3f, want between %.3f and %.3f", id, importance, min, max)
	}
}

func assertActivationMin(t *testing.T, tdb *db.TestDB, id string, min float64) {
	t.Helper()
	var activation float64
	err := tdb.DB.QueryRowContext(context.Background(), "SELECT activation_level FROM observations WHERE id = ?", id).Scan(&activation)
	if err != nil {
		t.Fatalf("get activation for %s: %v", id, err)
	}
	if activation < min {
		t.Errorf("%s: activation_level = %.3f, want >= %.3f", id, activation, min)
	}
}

func assertConsolidationMin(t *testing.T, tdb *db.TestDB, id string, min float64) {
	t.Helper()
	var consolidation float64
	err := tdb.DB.QueryRowContext(context.Background(), "SELECT consolidation_strength FROM observations WHERE id = ?", id).Scan(&consolidation)
	if err != nil {
		t.Fatalf("get consolidation for %s: %v", id, err)
	}
	if consolidation < min {
		t.Errorf("%s: consolidation_strength = %.3f, want >= %.3f", id, consolidation, min)
	}
}

package decay

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"math"
	"time"
)

// Kind decay ratios: episodic decays fastest, procedural slowest.
// These affect activation_level, not importance.
var kindRatio = map[string]float64{
	"episodic":   1.0,
	"semantic":   0.6,
	"procedural": 0.2,
}

const (
	// Base decay per epoch for activation_level
	activationDecayBase = 0.03
	// Floor for activation_level - never goes below this
	activationFloor = 0.05
	// Floor for importance - protects durable value
	importanceFloor = 0.01
	// Small decay for importance in Buffer/Working (minimal, just to allow some movement)
	importanceDecayBase = 0.005

	// Boost values for recall access
	activationBoost    = 0.08
	consolidationBoost = 0.02
	activationCap      = 1.0
	consolidationCap   = 1.0

	// GC thresholds
	gcMinAge              = 30 * 24 * time.Hour // 30 days
	gcActivationThreshold = 0.15                // activation_level threshold for GC
	gcMinAccessCount      = 3                   // minimum accesses to avoid GC
)

type Engine struct {
	db *sql.DB
}

func NewEngine(db *sql.DB) *Engine {
	return &Engine{db: db}
}

// ActivationScore computes the activation score for an observation.
// This is used for GC decisions and ranking, combining:
// - activation_level (recent accessibility)
// - consolidation_strength (how well-established the memory is)
// - days since last access
func ActivationScore(activationLevel float64, consolidationStrength float64, daysSinceAccess float64, accessCount int) float64 {
	// Decay activation based on time since access
	activationComponent := activationLevel * math.Exp(-0.1*daysSinceAccess)
	// Consolidation provides a stable base that doesn't decay as fast
	consolidationComponent := consolidationStrength * 0.5
	// Access count provides a small bonus
	accessBonus := 0.1 * math.Log(float64(accessCount)+1)
	return activationComponent + consolidationComponent + accessBonus
}

// ApplyDecay reduces activation_level for Buffer and Working layer observations.
// Core (layer=2) observations are permanent and never decay.
// importance (durable value) is minimally affected to preserve semantic value.
func (e *Engine) ApplyDecay(ctx context.Context) (int64, error) {
	var totalAffected int64

	// Decay activation_level based on kind ratios
	for kind, ratio := range kindRatio {
		decayAmount := activationDecayBase * ratio
		result, err := e.db.ExecContext(ctx, `
			UPDATE observations
			SET activation_level = MAX(?, activation_level - ?),
			    updated_at = datetime('now')
			WHERE deleted_at IS NULL
			  AND layer < 2
			  AND kind = ?
			  AND activation_level > ?
		`, activationFloor, decayAmount, kind, activationFloor)
		if err != nil {
			return totalAffected, fmt.Errorf("apply activation decay for kind %s: %w", kind, err)
		}
		affected, _ := result.RowsAffected()
		totalAffected += affected
	}

	// Minimal importance decay for Buffer/Working (preserves durable value)
	// This allows some movement but doesn't destroy semantic importance
	_, err := e.db.ExecContext(ctx, `
		UPDATE observations
		SET importance = MAX(?, importance - ?),
		    updated_at = datetime('now')
		WHERE deleted_at IS NULL
		  AND layer < 2
		  AND importance > ?
	`, importanceFloor, importanceDecayBase, importanceFloor+importanceDecayBase)
	if err != nil {
		return totalAffected, fmt.Errorf("apply importance decay: %w", err)
	}

	return totalAffected, nil
}

// GarbageCollect soft-deletes observations with low activation scores that are old enough.
// Uses activation_level and consolidation_strength to determine which observations
// are truly weak vs. those that are just not recently accessed but still valuable.
func (e *Engine) GarbageCollect(ctx context.Context) (int64, error) {
	cutoff := time.Now().Add(-gcMinAge).Format("2006-01-02 15:04:05")

	// Get candidates: old, low activation, not Core
	// We consider both activation_level and consolidation_strength
	rows, err := e.db.QueryContext(ctx, `
		SELECT id, activation_level, consolidation_strength, access_count,
		       CAST((julianday('now') - julianday(COALESCE(last_accessed, created_at))) AS REAL) as days_since
		FROM observations
		WHERE deleted_at IS NULL
		  AND layer < 2
		  AND created_at < ?
		  AND activation_level < ?
		  AND access_count < ?
	`, cutoff, gcActivationThreshold*2, gcMinAccessCount)
	if err != nil {
		return 0, fmt.Errorf("query gc candidates: %w", err)
	}
	defer rows.Close()

	var toDelete []string
	for rows.Next() {
		var id string
		var activationLevel float64
		var consolidationStrength float64
		var accessCount int
		var daysSince float64
		if err := rows.Scan(&id, &activationLevel, &consolidationStrength, &accessCount, &daysSince); err != nil {
			continue
		}
		score := ActivationScore(activationLevel, consolidationStrength, daysSince, accessCount)
		if score < gcActivationThreshold {
			toDelete = append(toDelete, id)
		}
	}

	if len(toDelete) == 0 {
		return 0, nil
	}

	// Soft-delete in batches
	var deleted int64
	for _, id := range toDelete {
		result, err := e.db.ExecContext(ctx, `
			UPDATE observations
			SET deleted_at = datetime('now'), updated_at = datetime('now')
			WHERE id = ? AND deleted_at IS NULL
		`, id)
		if err != nil {
			log.Printf("gc delete %s: %v", id, err)
			continue
		}
		n, _ := result.RowsAffected()
		deleted += n
	}

	return deleted, nil
}

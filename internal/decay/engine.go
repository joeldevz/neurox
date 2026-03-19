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
var kindRatio = map[string]float64{
	"episodic":   1.0,
	"semantic":   0.6,
	"procedural": 0.2,
}

const (
	decayBase       = 0.02 // base decay per epoch
	decayFloor      = 0.01 // importance never goes below this
	activationBoost = 0.03 // boost on recall access
	activationCap   = 1.0

	gcMinAge       = 30 * 24 * time.Hour // 30 days
	gcThreshold    = 0.1                  // activation score threshold for GC
	decayHalfLife  = 30.0                 // days for activation score calculation
	decayRateConst = 0.1                  // for activation score exp decay
)

type Engine struct {
	db *sql.DB
}

func NewEngine(db *sql.DB) *Engine {
	return &Engine{db: db}
}

// ActivationScore computes the activation score for an observation.
// score = base * e^(-0.1 * days_since_access) + 0.3 * ln(access_count + 1)
func ActivationScore(importance float64, daysSinceAccess float64, accessCount int) float64 {
	base := importance * math.Exp(-decayRateConst*daysSinceAccess)
	accessBonus := 0.3 * math.Log(float64(accessCount)+1)
	return base + accessBonus
}

// ApplyDecay reduces importance for Buffer and Working layer observations based on kind.
// Core (layer=2) observations are permanent and never decay.
func (e *Engine) ApplyDecay(ctx context.Context) (int64, error) {
	// For each kind, apply different decay rates
	var totalAffected int64
	for kind, ratio := range kindRatio {
		decayAmount := decayBase * ratio
		result, err := e.db.ExecContext(ctx, `
			UPDATE observations
			SET importance = MAX(?, importance - ?),
			    updated_at = datetime('now')
			WHERE deleted_at IS NULL
			  AND layer < 2
			  AND kind = ?
			  AND importance > ?
		`, decayFloor, decayAmount, kind, decayFloor)
		if err != nil {
			return totalAffected, fmt.Errorf("apply decay for kind %s: %w", kind, err)
		}
		affected, _ := result.RowsAffected()
		totalAffected += affected
	}
	return totalAffected, nil
}

// GarbageCollect soft-deletes observations with low activation scores that are old enough.
func (e *Engine) GarbageCollect(ctx context.Context) (int64, error) {
	cutoff := time.Now().Add(-gcMinAge).Format("2006-01-02 15:04:05")

	// Get candidates: old, low importance, low access, not Core
	rows, err := e.db.QueryContext(ctx, `
		SELECT id, importance, access_count,
		       CAST((julianday('now') - julianday(COALESCE(last_accessed, created_at))) AS REAL) as days_since
		FROM observations
		WHERE deleted_at IS NULL
		  AND layer < 2
		  AND created_at < ?
		  AND importance < ?
	`, cutoff, gcThreshold*2)
	if err != nil {
		return 0, fmt.Errorf("query gc candidates: %w", err)
	}
	defer rows.Close()

	var toDelete []string
	for rows.Next() {
		var id string
		var importance float64
		var accessCount int
		var daysSince float64
		if err := rows.Scan(&id, &importance, &accessCount, &daysSince); err != nil {
			continue
		}
		score := ActivationScore(importance, daysSince, accessCount)
		if score < gcThreshold {
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

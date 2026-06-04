package recall

import (
	"math"
	"testing"
	"time"

	"github.com/joeldevz/neurox/internal/observation"
)

// ============================================================================
// TESTS: RRF scoring (Subtask 3 of recall-merge-fix)
//
// The relevance term of the tri-factor score is replaced with Reciprocal Rank
// Fusion: 1/(k+rank_fts) + 1/(k+rank_sem). This file tests:
//   - rrfScore(): pure function formula for the 4 channel combinations
//   - deriveSemanticRanks(): stable sort with ID-asc tie-break
//   - applyScores(): consumes FTSRank/SemRank (not RawRelevance/SemanticScore)
//     to compute the relevance term, and populates RRFScore in breakdown
// ============================================================================

// TestRRFScore verifies the RRF formula for all channel combinations and
// across different k values. k=60 is the zero-shot production default
// (Cormack et al. 2009, Bruch et al. 2022).
func TestRRFScore(t *testing.T) {
	tests := []struct {
		name    string
		ftsRank int
		semRank int
		k       int
		want    float64
	}{
		{
			name:    "dual channel: FTS rank 3, semantic rank 1, k=60",
			ftsRank: 3,
			semRank: 1,
			k:       60,
			want:    1.0/63.0 + 1.0/61.0, // ≈ 0.0322
		},
		{
			name:    "FTS-only at rank 1, k=60",
			ftsRank: 1,
			semRank: 0,
			k:       60,
			want:    1.0 / 61.0, // ≈ 0.0164
		},
		{
			name:    "semantic-only at rank 2, k=60",
			ftsRank: 0,
			semRank: 2,
			k:       60,
			want:    1.0 / 62.0, // ≈ 0.0161
		},
		{
			name:    "both absent (defensive, should not happen post-merge)",
			ftsRank: 0,
			semRank: 0,
			k:       60,
			want:    0.0,
		},
		{
			name:    "dual channel with k=30 (override)",
			ftsRank: 3,
			semRank: 1,
			k:       30,
			want:    1.0/33.0 + 1.0/31.0, // larger magnitude than k=60
		},
		{
			name:    "FTS-only at rank 10, k=60 (lower bound of RRF)",
			ftsRank: 10,
			semRank: 0,
			k:       60,
			want:    1.0 / 70.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rrfScore(tt.ftsRank, tt.semRank, tt.k)
			if math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("rrfScore(%d, %d, %d) = %.9f, want %.9f (diff %.9f)",
					tt.ftsRank, tt.semRank, tt.k, got, tt.want, got-tt.want)
			}
		})
	}
}

// TestDeriveSemanticRanks_StableTieBreak verifies that ties in cosine similarity
// are broken deterministically by ID ascending (smaller ID gets higher rank).
// This matters for test reproducibility and for candidates that share the same
// embedding (e.g., observations seeded with the same vector).
func TestDeriveSemanticRanks_StableTieBreak(t *testing.T) {
	semScores := map[string]float64{
		"id_c": 0.9,
		"id_a": 0.9, // tie with id_c — should be rank 1 (ID asc)
		"id_b": 0.5, // lowest score — should be rank 3
	}

	ranks := deriveSemanticRanks(semScores)

	if got := ranks["id_a"]; got != 1 {
		t.Errorf("id_a rank = %d, want 1 (tie-break by ID asc)", got)
	}
	if got := ranks["id_c"]; got != 2 {
		t.Errorf("id_c rank = %d, want 2 (tied with id_a, ID is later)", got)
	}
	if got := ranks["id_b"]; got != 3 {
		t.Errorf("id_b rank = %d, want 3 (lowest score)", got)
	}
}

// TestDeriveSemanticRanks_DescendingSort verifies that the rank assignment
// follows score descending (highest score = rank 1).
func TestDeriveSemanticRanks_DescendingSort(t *testing.T) {
	semScores := map[string]float64{
		"id_low":  0.1,
		"id_high": 0.95,
		"id_mid":  0.5,
	}

	ranks := deriveSemanticRanks(semScores)

	if got := ranks["id_high"]; got != 1 {
		t.Errorf("id_high rank = %d, want 1 (highest score)", got)
	}
	if got := ranks["id_mid"]; got != 2 {
		t.Errorf("id_mid rank = %d, want 2 (middle score)", got)
	}
	if got := ranks["id_low"]; got != 3 {
		t.Errorf("id_low rank = %d, want 3 (lowest score)", got)
	}
}

// TestApplyScores_RRFPopulatesBreakdown verifies that applyScores uses RRF
// (not the old max() formula) to compute the relevance term, and that the
// breakdown field RRFScore is populated when debug mode is enabled.
func TestApplyScores_RRFPopulatesBreakdown(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	candidates := []candidate{
		{
			Result:      Result{ID: "fts-only", ObservationType: observation.ObservationTypeDiscovery},
			Importance:  0.5,
			CreatedAt:   now,
			FTSRank:     1,
			SemRank:     0,
		},
	}

	applyScores(candidates, ScoreWeights{}.withDefaults(), now, TemporalIntent{}, nil, true, "test", 60)

	if candidates[0].Breakdown == nil {
		t.Fatal("Breakdown is nil, want non-nil (debug=true)")
	}

	// With RRF and k=60, FTS-only at rank 1 → relevance = 1/61
	wantRelevance := 1.0 / 61.0
	if math.Abs(candidates[0].Breakdown.Relevance-wantRelevance) > 1e-9 {
		t.Errorf("Relevance = %.9f, want %.9f (RRF FTS-only rank 1, k=60)",
			candidates[0].Breakdown.Relevance, wantRelevance)
	}
	if math.Abs(candidates[0].Breakdown.RRFScore-wantRelevance) > 1e-9 {
		t.Errorf("RRFScore = %.9f, want %.9f",
			candidates[0].Breakdown.RRFScore, wantRelevance)
	}
}

// TestApplyScores_RRFDualChannel verifies that a candidate appearing in both
// channels (FTSRank and SemRank set) gets the sum of both RRF terms, and
// ranks higher than FTS-only at the same FTS rank.
func TestApplyScores_RRFDualChannel(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	candidates := []candidate{
		{
			// Dual channel: FTS rank 3, semantic rank 1
			Result:      Result{ID: "dual", ObservationType: observation.ObservationTypeDiscovery},
			Importance:  0.5,
			CreatedAt:   now,
			FTSRank:     3,
			SemRank:     1,
		},
		{
			// FTS-only at rank 1 (would have been higher in the old max() world
			// if no semantic score was set; here it has no semantic match).
			Result:      Result{ID: "fts-only", ObservationType: observation.ObservationTypeDiscovery},
			Importance:  0.5,
			CreatedAt:   now,
			FTSRank:     1,
			SemRank:     0,
		},
	}

	applyScores(candidates, ScoreWeights{}.withDefaults(), now, TemporalIntent{}, nil, true, "test", 60)

	// Dual RRF: 1/63 + 1/61
	wantDual := 1.0/63.0 + 1.0/61.0
	if math.Abs(candidates[0].Breakdown.Relevance-wantDual) > 1e-9 {
		t.Errorf("dual Relevance = %.9f, want %.9f", candidates[0].Breakdown.Relevance, wantDual)
	}
	if math.Abs(candidates[0].Breakdown.RRFScore-wantDual) > 1e-9 {
		t.Errorf("dual RRFScore = %.9f, want %.9f", candidates[0].Breakdown.RRFScore, wantDual)
	}

	// FTS-only RRF: 1/61
	wantFTS := 1.0 / 61.0
	if math.Abs(candidates[1].Breakdown.Relevance-wantFTS) > 1e-9 {
		t.Errorf("fts-only Relevance = %.9f, want %.9f", candidates[1].Breakdown.Relevance, wantFTS)
	}
	if math.Abs(candidates[1].Breakdown.RRFScore-wantFTS) > 1e-9 {
		t.Errorf("fts-only RRFScore = %.9f, want %.9f", candidates[1].Breakdown.RRFScore, wantFTS)
	}

	// Dual channel (1/63 + 1/61) > FTS-only (1/61), so the dual candidate
	// must score higher in the tri-factor.
	if candidates[0].Score <= candidates[1].Score {
		t.Errorf("dual score (%.6f) should be > fts-only score (%.6f) due to two-sided RRF",
			candidates[0].Score, candidates[1].Score)
	}
}

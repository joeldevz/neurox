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
// Fusion: 1/(k+rank_fts) + 1/(k+rank_sem) + 0.5/(k+rank_fact). This file tests:
//   - rrfScore(): pure function formula for the channel combinations
//   - deriveSemanticRanks(): stable sort with ID-asc tie-break
//   - applyScores(): consumes FTSRank/SemRank/FactRank (not RawRelevance/SemanticScore)
//     to compute the relevance term, and populates RRFScore in breakdown
// ============================================================================

// TestRRFScore verifies the RRF formula for all channel combinations and
// across different k values. k=60 is the zero-shot production default
// (Cormack et al. 2009, Bruch et al. 2022).
func TestRRFScore(t *testing.T) {
	tests := []struct {
		name     string
		ftsRank  int
		semRank  int
		factRank int
		k        int
		want     float64
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
			name:     "fact-only at rank 1, k=60",
			ftsRank:  0,
			semRank:  0,
			factRank: 1,
			k:        60,
			want:     0.5 / 61.0,
		},
		{
			name:    "all absent (defensive, should not happen post-merge)",
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
			name:     "FTS + semantic + fact additive channels, k=60",
			ftsRank:  2,
			semRank:  3,
			factRank: 4,
			k:        60,
			want:     1.0/62.0 + 1.0/63.0 + 0.5/64.0,
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
			got := rrfScore(tt.ftsRank, tt.semRank, tt.factRank, tt.k)
			if math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("rrfScore(%d, %d, %d, %d) = %.9f, want %.9f (diff %.9f)",
					tt.ftsRank, tt.semRank, tt.factRank, tt.k, got, tt.want, got-tt.want)
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
// After normalization, relevance = raw_rrf * (k+1) / 2.0.
func TestApplyScores_RRFPopulatesBreakdown(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	const k = 60
	candidates := []candidate{
		{
			Result:     Result{ID: "fts-only", ObservationType: observation.ObservationTypeDiscovery},
			Importance: 0.5,
			CreatedAt:  now,
			FTSRank:    1,
			SemRank:    0,
		},
	}

	applyScores(candidates, ScoreWeights{}.withDefaults(), now, TemporalIntent{}, nil, true, "test", k)

	if candidates[0].Breakdown == nil {
		t.Fatal("Breakdown is nil, want non-nil (debug=true)")
	}

	// With RRF and k=60, FTS-only at rank 1 → raw_rrf = 1/61.
	// After normalization: relevance = (1/61) * (60+1) / 2.0 = 1/61 * 61/2 = 0.5
	rawRRF := 1.0 / 61.0
	wantRelevance := rawRRF * float64(k+1) / 2.0
	if math.Abs(candidates[0].Breakdown.Relevance-wantRelevance) > 1e-9 {
		t.Errorf("Relevance = %.9f, want %.9f (normalized RRF FTS-only rank 1, k=60)",
			candidates[0].Breakdown.Relevance, wantRelevance)
	}
	// RRFScore in breakdown should still be the raw RRF (for debug / analysis)
	if math.Abs(candidates[0].Breakdown.RRFScore-rawRRF) > 1e-9 {
		t.Errorf("RRFScore = %.9f, want %.9f (raw RRF, not normalized)",
			candidates[0].Breakdown.RRFScore, rawRRF)
	}
}

// TestApplyScores_RRFDualChannel verifies that a candidate appearing in both
// channels (FTSRank and SemRank set) gets the sum of both RRF terms (normalized),
// and ranks higher than FTS-only at the same FTS rank.
func TestApplyScores_RRFDualChannel(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	const k = 60
	candidates := []candidate{
		{
			// Dual channel: FTS rank 3, semantic rank 1
			Result:     Result{ID: "dual", ObservationType: observation.ObservationTypeDiscovery},
			Importance: 0.5,
			CreatedAt:  now,
			FTSRank:    3,
			SemRank:    1,
		},
		{
			// FTS-only at rank 1 (would have been higher in the old max() world
			// if no semantic score was set; here it has no semantic match).
			Result:     Result{ID: "fts-only", ObservationType: observation.ObservationTypeDiscovery},
			Importance: 0.5,
			CreatedAt:  now,
			FTSRank:    1,
			SemRank:    0,
		},
	}

	applyScores(candidates, ScoreWeights{}.withDefaults(), now, TemporalIntent{}, nil, true, "test", k)

	// Dual RRF raw: 1/63 + 1/61
	rawDual := 1.0/63.0 + 1.0/61.0
	// Dual RRF normalized: raw * (k+1) / 2.0
	wantDual := rawDual * float64(k+1) / 2.0
	if math.Abs(candidates[0].Breakdown.Relevance-wantDual) > 1e-9 {
		t.Errorf("dual Relevance = %.9f, want %.9f (normalized)", candidates[0].Breakdown.Relevance, wantDual)
	}
	if math.Abs(candidates[0].Breakdown.RRFScore-rawDual) > 1e-9 {
		t.Errorf("dual RRFScore = %.9f, want %.9f (raw, for debug)", candidates[0].Breakdown.RRFScore, rawDual)
	}

	// FTS-only RRF raw: 1/61
	rawFTS := 1.0 / 61.0
	// FTS-only RRF normalized
	wantFTS := rawFTS * float64(k+1) / 2.0
	if math.Abs(candidates[1].Breakdown.Relevance-wantFTS) > 1e-9 {
		t.Errorf("fts-only Relevance = %.9f, want %.9f (normalized)", candidates[1].Breakdown.Relevance, wantFTS)
	}
	if math.Abs(candidates[1].Breakdown.RRFScore-rawFTS) > 1e-9 {
		t.Errorf("fts-only RRFScore = %.9f, want %.9f (raw, for debug)", candidates[1].Breakdown.RRFScore, rawFTS)
	}

	// Dual channel (raw 1/63 + 1/61, normalized) > FTS-only (raw 1/61, normalized),
	// so the dual candidate must score higher in the tri-factor.
	if candidates[0].Score <= candidates[1].Score {
		t.Errorf("dual score (%.6f) should be > fts-only score (%.6f) due to two-sided RRF",
			candidates[0].Score, candidates[1].Score)
	}
}

// TestRRFNormalization verifies that RRF scores are normalized to [0,1]
// where rank-1-in-both → ~1.0, single-channel rank-1 → ~0.5, absent → 0.
func TestRRFNormalization(t *testing.T) {
	const k = 60

	tests := []struct {
		name    string
		ftsRank int
		semRank int
		wantMin float64 // expect normalized score >= wantMin
		wantMax float64 // expect normalized score <= wantMax
	}{
		{
			name:    "rank-1 both channels → ~1.0",
			ftsRank: 1,
			semRank: 1,
			wantMin: 0.95,
			wantMax: 1.0,
		},
		{
			name:    "rank-1 single channel → ~0.5",
			ftsRank: 1,
			semRank: 0,
			wantMin: 0.48,
			wantMax: 0.52,
		},
		{
			name:    "rank-10 single channel → lower but non-trivial",
			ftsRank: 10,
			semRank: 0,
			wantMin: 0.4,
			wantMax: 0.45,
		},
		{
			name:    "both absent → 0",
			ftsRank: 0,
			semRank: 0,
			wantMin: 0.0,
			wantMax: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := rrfScore(tt.ftsRank, tt.semRank, 0, k)
			normalized := raw * float64(k+1) / 2.0

			if normalized < tt.wantMin || normalized > tt.wantMax {
				t.Errorf("normalized RRF = %.4f, want in range [%.4f, %.4f]",
					normalized, tt.wantMin, tt.wantMax)
			}
		})
	}
}

// TestApplyScores_NormalizedRRFContributes verifies that with normalized RRF,
// the relevance term now meaningfully affects ranking, not just recency.
// A high-RRF candidate with low recency should rank reasonably (not be buried by recency alone).
func TestApplyScores_NormalizedRRFContributes(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)

	// Candidate A: old, high RRF (rank 1 both channels)
	oldWithHighRRF := candidate{
		Result:     Result{ID: "old-high-rrf", ObservationType: observation.ObservationTypeDiscovery},
		Importance: 0.3,
		CreatedAt:  now.AddDate(0, -3, 0), // 3 months old
		FTSRank:    1,
		SemRank:    1,
	}

	// Candidate B: recent, low RRF (rank 20 FTS only)
	recentWithLowRRF := candidate{
		Result:     Result{ID: "recent-low-rrf", ObservationType: observation.ObservationTypeDiscovery},
		Importance: 0.3,
		CreatedAt:  now.AddDate(0, 0, -1), // 1 day old
		FTSRank:    20,
		SemRank:    0,
	}

	candidates := []candidate{oldWithHighRRF, recentWithLowRRF}
	applyScores(candidates, ScoreWeights{}.withDefaults(), now, TemporalIntent{}, nil, true, "test", 60)

	// With normalized RRF, the relevance weight (0.4) now competes meaningfully
	// with recency (0.3), so the old-high-RRF candidate should not be trivially
	// buried. The exact ranking depends on the normalized scores, but we check
	// that the old candidate's score is not negligible relative to the recent one.
	scoreRatio := candidates[0].Score / candidates[1].Score
	if scoreRatio < 0.6 {
		t.Errorf("score ratio (old-high-RRF / recent-low-RRF) = %.3f, want >= 0.6 (RRF should compete with recency)",
			scoreRatio)
	}
}

// TestCrossSignalBoostGatedOnMembership verifies that the cross-signal boost
// (1.2x) is applied based on rank membership (FTSRank > 0 && SemRank > 0),
// not on normalized score thresholds.
//
// This is a unit test of applyScores directly, independent of the full search
// engine. It creates a minimal candidate pool where one candidate appears in
// both FTS and semantic channels and verifies the boost is applied.
func TestCrossSignalBoostGatedOnMembership(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)

	// Create 2 candidates:
	// - dualChannel: appears in both FTS (rank 2) and semantic (rank 1)
	// - ftsOnly: appears only in FTS (rank 1)
	candidates := []candidate{
		{
			Result:        Result{ID: "dual", ObservationType: observation.ObservationTypeDiscovery},
			Importance:    0.5,
			CreatedAt:     now,
			FTSRank:       2,     // in FTS
			SemRank:       1,     // in semantic
			RawRelevance:  -10.0, // arbitrary (not used in new gate)
			SemanticScore: 0.8,
		},
		{
			Result:        Result{ID: "fts-only", ObservationType: observation.ObservationTypeDiscovery},
			Importance:    0.5,
			CreatedAt:     now,
			FTSRank:       1,    // in FTS
			SemRank:       0,    // NOT in semantic
			RawRelevance:  -5.0, // better BM25 than dual
			SemanticScore: 0.0,
		},
	}

	applyScores(candidates, ScoreWeights{}.withDefaults(), now, TemporalIntent{}, nil, true, "test", 60)

	// Verify: dual-channel should have cross-signal boost
	if candidates[0].Breakdown == nil {
		t.Fatal("Breakdown is nil, want non-nil (debug=true)")
	}
	if candidates[0].Breakdown.CrossSignalBoost != 1.2 {
		t.Errorf("dual-channel CrossSignalBoost = %.2f, want 1.2 (membership gate should apply)",
			candidates[0].Breakdown.CrossSignalBoost)
	}

	// Verify: FTS-only should NOT have boost
	if candidates[1].Breakdown == nil {
		t.Fatal("Breakdown[1] is nil, want non-nil (debug=true)")
	}
	if candidates[1].Breakdown.CrossSignalBoost != 1.0 {
		t.Errorf("fts-only CrossSignalBoost = %.2f, want 1.0 (no semantic rank)",
			candidates[1].Breakdown.CrossSignalBoost)
	}

	t.Logf("dual-channel: FTSRank=%d, SemRank=%d, CrossSignalBoost=%.2f, Score=%.4f",
		candidates[0].FTSRank, candidates[0].SemRank, candidates[0].Breakdown.CrossSignalBoost, candidates[0].Score)
	t.Logf("fts-only: FTSRank=%d, SemRank=%d, CrossSignalBoost=%.2f, Score=%.4f",
		candidates[1].FTSRank, candidates[1].SemRank, candidates[1].Breakdown.CrossSignalBoost, candidates[1].Score)
}

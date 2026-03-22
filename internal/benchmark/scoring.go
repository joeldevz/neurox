package benchmark

// Tier classifies how well a raw score meets the performance thresholds.
type Tier int

const (
	TierBase   Tier = iota // below the baseline
	TierTarget             // at or above base, below target
	TierElite              // at or above target, below elite
	TierBeyond             // at or above elite
)

// Threshold defines the three performance tiers for a metric.
// All values are in the natural unit of the metric (e.g. recall@10 as 0-100).
type Threshold struct {
	Base   float64
	Target float64
	Elite  float64
}

// EvaluateScore maps a raw score against a three-tier threshold to a
// normalised 0-100 score and the tier reached.
//
// Mapping:
//
//	score < Base   → 0-40  (proportional within [0, Base))
//	Base ≤ score < Target → 40-70 (proportional within [Base, Target))
//	Target ≤ score < Elite → 70-95 (proportional within [Target, Elite))
//	score ≥ Elite  → 95-100 (proportional within [Elite, ∞))
func EvaluateScore(score float64, threshold Threshold) (normalizedScore float64, tier Tier) {
	switch {
	case score < threshold.Base:
		// 0-40 range
		if threshold.Base <= 0 {
			return 0, TierBase
		}
		ratio := score / threshold.Base
		if ratio < 0 {
			ratio = 0
		}
		return ratio * 40, TierBase

	case score < threshold.Target:
		// 40-70 range
		spread := threshold.Target - threshold.Base
		if spread <= 0 {
			return 40, TierTarget
		}
		ratio := (score - threshold.Base) / spread
		return 40 + ratio*30, TierTarget

	case score < threshold.Elite:
		// 70-95 range
		spread := threshold.Elite - threshold.Target
		if spread <= 0 {
			return 70, TierElite
		}
		ratio := (score - threshold.Target) / spread
		return 70 + ratio*25, TierElite

	default:
		// 95-100 range — beyond elite
		spread := threshold.Elite * 0.1 // 10% overshoot maps to the last 5 points
		if spread <= 0 {
			return 100, TierBeyond
		}
		ratio := (score - threshold.Elite) / spread
		if ratio > 1 {
			ratio = 1
		}
		return 95 + ratio*5, TierBeyond
	}
}

// LetterGrade converts a 0-100 score to a letter grade.
//
//	S: >95
//	A: >85
//	B: >70
//	C: >55
//	D: >40
//	F: ≤40
func LetterGrade(score float64) string {
	switch {
	case score > 95:
		return "S"
	case score > 85:
		return "A"
	case score > 70:
		return "B"
	case score > 55:
		return "C"
	case score > 40:
		return "D"
	default:
		return "F"
	}
}

// ComputeOverallScore computes a weighted average of dimension scores.
// weights maps dimension name to weight; dimensions without a weight entry
// receive weight 1.0. Returns 0 if there are no results.
func ComputeOverallScore(results []DimensionResult, weights map[string]float64) float64 {
	if len(results) == 0 {
		return 0
	}

	var totalWeight, weightedSum float64
	for _, r := range results {
		w, ok := weights[r.DimensionName]
		if !ok || w <= 0 {
			w = 1.0
		}
		weightedSum += r.Score * w
		totalWeight += w
	}

	if totalWeight == 0 {
		return 0
	}
	return weightedSum / totalWeight
}

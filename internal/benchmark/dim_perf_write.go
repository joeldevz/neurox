package benchmark

import (
	"context"
	"fmt"
	"sort"
	"time"

	"neurox/internal/observation"
)

// PerfWrite benchmarks raw write throughput:
// save N observations sequentially, measure ops/sec and percentile latencies.
type PerfWrite struct{}

func (d PerfWrite) Name() string     { return "Write Throughput" }
func (d PerfWrite) Category() string { return "performance" }

func (d PerfWrite) Run(ctx context.Context, env *BenchEnv) DimensionResult {
	var errs []string

	// Determine count based on scale.
	count := perfWriteCount(env.Scale)

	latencies := make([]time.Duration, 0, count)
	start := time.Now()

	for i := 0; i < count; i++ {
		t := time.Now()
		_, err := env.ObsStore.Save(ctx, observation.Observation{
			Title:           fmt.Sprintf("Perf write test %d", i),
			Content:         fmt.Sprintf("Performance benchmark observation number %d with enough content to exercise FTS5 indexing", i),
			ObservationType: observation.ObservationTypeDiscovery,
			Kind:            observation.KindSemantic,
			Namespace:       "bench-perf-write",
			Retention:       observation.RetentionDurable,
			Confidence:      0.7,
		})
		latencies = append(latencies, time.Since(t))
		if err != nil {
			errs = append(errs, fmt.Sprintf("save #%d failed: %v", i, err))
		}
	}

	totalDur := time.Since(start)

	// Sort latencies for percentile calculation.
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	p50 := perfPercentile(latencies, 0.50)
	p95 := perfPercentile(latencies, 0.95)
	p99 := perfPercentile(latencies, 0.99)

	opsPerSec := float64(count) / totalDur.Seconds()

	threshold := Threshold{Base: 500, Target: 1000, Elite: 2000}
	score, _ := EvaluateScore(opsPerSec, threshold)

	return DimensionResult{
		Score: score,
		Max:   100,
		Metrics: map[string]float64{
			"throughput_ops_s": opsPerSec,
			"p50_ms":           float64(p50.Milliseconds()),
			"p95_ms":           float64(p95.Milliseconds()),
			"p99_ms":           float64(p99.Milliseconds()),
			"total_count":      float64(count),
		},
		Errors: errs,
	}
}

// perfWriteCount returns the number of observations to write for the given scale.
func perfWriteCount(scale ScaleConfig) int {
	switch scale.Name {
	case "large":
		return 10_000
	case "medium":
		return 5_000
	default:
		return 1_000
	}
}

// perfPercentile returns the value at percentile p (0.0–1.0) from a sorted slice.
func perfPercentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)) * p)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

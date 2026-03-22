package benchmark

import (
	"context"
	"fmt"
	"sync"
	"time"

	"neurox/internal/observation"
)

// PerfConcurrent benchmarks concurrent write stress:
// 8 goroutines each save 500 observations simultaneously,
// checking zero errors and correct total count.
type PerfConcurrent struct{}

func (d PerfConcurrent) Name() string     { return "Concurrent Writes" }
func (d PerfConcurrent) Category() string { return "performance" }

func (d PerfConcurrent) Run(ctx context.Context, env *BenchEnv) DimensionResult {
	var errs []string

	const goroutines = 8
	const perGoroutine = 500
	total := goroutines * perGoroutine

	var wg sync.WaitGroup
	var mu sync.Mutex
	var totalErrors int

	start := time.Now()

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gID int) {
			defer wg.Done()
			ns := fmt.Sprintf("bench-perf-concurrent-%d", gID)
			for i := 0; i < perGoroutine; i++ {
				_, err := env.ObsStore.Save(ctx, observation.Observation{
					Title:           fmt.Sprintf("Concurrent write g%d obs %d", gID, i),
					Content:         fmt.Sprintf("Goroutine %d observation %d: stress test for concurrent SQLite write correctness and throughput under parallel load.", gID, i),
					ObservationType: observation.ObservationTypeDiscovery,
					Kind:            observation.KindSemantic,
					Namespace:       ns,
					Retention:       observation.RetentionDurable,
					Confidence:      0.7,
				})
				if err != nil {
					mu.Lock()
					totalErrors++
					mu.Unlock()
				}
			}
		}(g)
	}

	wg.Wait()
	duration := time.Since(start)

	// Verify total count via DB query.
	var dbCount int
	row := env.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM observations
		WHERE namespace LIKE 'bench-perf-concurrent-%'
		AND deleted_at IS NULL
	`)
	if err := row.Scan(&dbCount); err != nil {
		errs = append(errs, fmt.Sprintf("count query failed: %v", err))
	}

	// Build checks.
	checks := []CheckResult{
		{
			Name:   "zero write errors",
			Passed: totalErrors == 0,
			Detail: fmt.Sprintf("errors=%d total_writes=%d", totalErrors, total),
		},
		{
			Name:   "correct total count",
			Passed: dbCount == total,
			Detail: fmt.Sprintf("db_count=%d expected=%d", dbCount, total),
		},
	}

	// Score: 50% correctness + 50% throughput.
	passed := 0
	for _, c := range checks {
		if c.Passed {
			passed++
		}
	}
	correctnessScore := float64(passed) / float64(len(checks)) * 100

	opsPerSec := float64(total) / duration.Seconds()
	threshold := Threshold{Base: 200, Target: 800, Elite: 1500}
	throughputScore, _ := EvaluateScore(opsPerSec, threshold)

	score := correctnessScore*0.5 + throughputScore*0.5

	return DimensionResult{
		Score:  score,
		Max:    100,
		Checks: checks,
		Metrics: map[string]float64{
			"throughput_ops_s": opsPerSec,
			"total_writes":     float64(total),
			"db_count":         float64(dbCount),
			"write_errors":     float64(totalErrors),
			"goroutines":       float64(goroutines),
			"per_goroutine":    float64(perGoroutine),
		},
		Errors: errs,
	}
}

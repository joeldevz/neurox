package benchmark

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/joeldevz/neurox/internal/observation"
)

// PerfContext benchmarks proactive context retrieval latency:
// seed 2000 observations, consolidate, then measure 20 GetContext calls.
type PerfContext struct{}

func (d PerfContext) Name() string     { return "Context Retrieval" }
func (d PerfContext) Category() string { return "performance" }

func (d PerfContext) Run(ctx context.Context, env *BenchEnv) DimensionResult {
	var errs []string
	const ns = "bench-perf-ctx"
	const seedCount = 2000

	// 1. Seed 2000 observations with a mix of retention, types, and kinds.
	retentions := []observation.Retention{
		observation.RetentionDurable,
		observation.RetentionDurable,
		observation.RetentionOperational,
	}
	obsTypes := []observation.ObservationType{
		observation.ObservationTypeDecision,
		observation.ObservationTypePattern,
		observation.ObservationTypeDiscovery,
		observation.ObservationTypeGotcha,
		observation.ObservationTypeConfig,
	}
	kinds := []observation.Kind{
		observation.KindSemantic,
		observation.KindProcedural,
		observation.KindEpisodic,
	}

	for i := 0; i < seedCount; i++ {
		ret := retentions[i%len(retentions)]
		obsType := obsTypes[i%len(obsTypes)]
		kind := kinds[i%len(kinds)]
		confidence := 0.5 + float64(i%5)*0.1 // 0.5, 0.6, 0.7, 0.8, 0.9

		_, err := env.ObsStore.Save(ctx, observation.Observation{
			Title:           fmt.Sprintf("Context seed %d: %s observation", i, obsType),
			Content:         fmt.Sprintf("Seed entry %d for context retrieval benchmark. Type: %s, Kind: %s. Contains sufficient content to populate FTS5 index and test proactive context engine.", i, obsType, kind),
			ObservationType: obsType,
			Kind:            kind,
			Namespace:       ns,
			Retention:       ret,
			Confidence:      confidence,
		})
		if err != nil {
			errs = append(errs, fmt.Sprintf("seed #%d failed: %v", i, err))
		}
	}

	// 2. Run consolidation once to promote high-importance observations.
	if err := env.Pipeline.Run(ctx); err != nil {
		errs = append(errs, fmt.Sprintf("pipeline.Run warning: %v", err))
	}

	// 3. Measure 20 GetContext calls.
	const iterations = 20
	latencies := make([]time.Duration, 0, iterations)

	for i := 0; i < iterations; i++ {
		t := time.Now()
		_, err := env.ProactiveEngine.GetContext(ctx, ns, nil, 20)
		latencies = append(latencies, time.Since(t))
		if err != nil {
			errs = append(errs, fmt.Sprintf("GetContext #%d failed: %v", i, err))
		}
	}

	// 4. Calculate metrics.
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	var totalLatency time.Duration
	for _, l := range latencies {
		totalLatency += l
	}
	avgLatency := totalLatency / time.Duration(len(latencies))

	p50 := perfPercentile(latencies, 0.50)
	p95 := perfPercentile(latencies, 0.95)
	p99 := perfPercentile(latencies, 0.99)

	avgMs := avgLatency.Seconds() * 1000

	// 5. Score: lower latency is better.
	// <20ms = elite (~97), <50ms = target (~70-95), <100ms = base (~40-70), >100ms = below base (0-40)
	var score float64
	switch {
	case avgMs <= 20:
		score = 97
	case avgMs <= 50:
		score = 70 + (50-avgMs)/30*25
	case avgMs <= 100:
		score = 40 + (100-avgMs)/50*30
	default:
		v := 40 - (avgMs-100)/100*40
		if v < 0 {
			v = 0
		}
		score = v
	}

	return DimensionResult{
		Score: score,
		Max:   100,
		Metrics: map[string]float64{
			"avg_ms": avgMs,
			"p50_ms": float64(p50.Milliseconds()),
			"p95_ms": float64(p95.Milliseconds()),
			"p99_ms": float64(p99.Milliseconds()),
		},
		Errors: errs,
	}
}

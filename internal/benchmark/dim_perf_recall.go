package benchmark

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/joeldevz/neurox/internal/observation"
	"github.com/joeldevz/neurox/internal/recall"
)

// PerfRecall benchmarks recall/search throughput:
// seed N observations, run 50 diverse queries, measure queries/sec and latencies.
type PerfRecall struct{}

func (d PerfRecall) Name() string     { return "Recall Performance" }
func (d PerfRecall) Category() string { return "performance" }

func (d PerfRecall) Run(ctx context.Context, env *BenchEnv) DimensionResult {
	var errs []string
	const ns = "bench-perf-recall"

	// 1. Seed observations.
	seedCount := perfRecallSeedCount(env.Scale)
	for i := 0; i < seedCount; i++ {
		_, err := env.ObsStore.Save(ctx, observation.Observation{
			Title:           fmt.Sprintf("Recall seed observation %d: %s", i, recallSeedTopic(i)),
			Content:         fmt.Sprintf("Benchmark seed entry %d for recall performance testing. Topic: %s. Contains relevant technical content for FTS5 indexing and semantic search validation.", i, recallSeedTopic(i)),
			ObservationType: observation.ObservationTypeDiscovery,
			Kind:            observation.KindSemantic,
			Namespace:       ns,
			Retention:       observation.RetentionDurable,
			Confidence:      0.7,
		})
		if err != nil {
			errs = append(errs, fmt.Sprintf("seed #%d failed: %v", i, err))
		}
	}

	// 2. Run 50 diverse search queries, measuring latency for each.
	queries := RecallBenchQueries()
	latencies := make([]time.Duration, 0, len(queries))

	start := time.Now()
	for _, q := range queries {
		t := time.Now()
		_, err := env.RecallEngine.Search(ctx, recall.SearchOptions{
			Query:     q,
			Namespace: ns,
			Limit:     10,
		})
		latencies = append(latencies, time.Since(t))
		if err != nil {
			errs = append(errs, fmt.Sprintf("search %q failed: %v", q, err))
		}
	}
	totalDur := time.Since(start)

	// 3. Calculate metrics.
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	p50 := perfPercentile(latencies, 0.50)
	p95 := perfPercentile(latencies, 0.95)
	p99 := perfPercentile(latencies, 0.99)

	queriesPerSec := float64(len(queries)) / totalDur.Seconds()

	threshold := Threshold{Base: 100, Target: 200, Elite: 500}
	score, _ := EvaluateScore(queriesPerSec, threshold)

	return DimensionResult{
		Score: score,
		Max:   100,
		Metrics: map[string]float64{
			"throughput_ops_s": queriesPerSec,
			"p50_ms":           float64(p50.Milliseconds()),
			"p95_ms":           float64(p95.Milliseconds()),
			"p99_ms":           float64(p99.Milliseconds()),
			"queries_run":      float64(len(queries)),
		},
		Errors: errs,
	}
}

// perfRecallSeedCount returns the number of seed observations based on scale.
func perfRecallSeedCount(scale ScaleConfig) int {
	switch scale.Name {
	case "large":
		return 10_000
	case "medium":
		return 5_000
	default:
		return 1_000
	}
}

// recallSeedTopic returns a rotating topic label for seeded observations.
func recallSeedTopic(i int) string {
	topics := []string{
		"authentication", "database", "caching", "deployment", "monitoring",
		"security", "performance", "testing", "api design", "TypeScript",
	}
	return topics[i%len(topics)]
}

// RecallBenchQueries returns 50 diverse search queries covering multiple technical domains.
// It is exported so that integration benchmarks can reuse the same query set.
func RecallBenchQueries() []string {
	return []string{
		"authentication JWT tokens",
		"database PostgreSQL config",
		"React component patterns",
		"error handling middleware",
		"caching Redis strategy",
		"deployment Docker kubernetes",
		"testing unit integration",
		"API REST endpoints",
		"TypeScript strict mode",
		"monitoring logging alerts",
		"security CORS headers",
		"performance optimization",
		"CI CD pipeline",
		"git branching strategy",
		"code review conventions",
		"microservices event sourcing",
		"rate limiting token bucket",
		"session management cookies",
		"database migration schema",
		"load balancing nginx",
		"observability tracing spans",
		"container orchestration pods",
		"feature flags rollout",
		"GraphQL subscriptions",
		"WebSocket real time",
		"OAuth2 authorization flow",
		"S3 object storage bucket",
		"message queue consumer",
		"circuit breaker fallback",
		"retry backoff exponential",
		"health check endpoint liveness",
		"encryption TLS certificate",
		"environment variables secrets",
		"structured logging correlation",
		"pagination cursor infinite scroll",
		"search index full text",
		"webhook idempotency deduplication",
		"background job queue worker",
		"database connection pool",
		"cache invalidation TTL",
		"service mesh sidecar proxy",
		"blue green deployment strategy",
		"canary release percentage",
		"distributed tracing baggage",
		"memory leak profiling heap",
		"SQL query optimization index",
		"API versioning backward compatibility",
		"graceful shutdown drain",
		"dependency injection container",
		"concurrency goroutines channels",
	}
}

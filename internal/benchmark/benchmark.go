package benchmark

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// Dimension is a single scored aspect of brain benchmark quality.
type Dimension interface {
	// Name returns the dimension's display name.
	Name() string
	// Category returns a grouping label (e.g. "cognitive", "performance").
	Category() string
	// Run executes the dimension against the shared environment and returns a result.
	Run(ctx context.Context, env *BenchEnv) DimensionResult
}

// CheckResult captures one pass/fail assertion within a dimension.
type CheckResult struct {
	Name   string
	Passed bool
	Detail string
}

// DimensionResult holds the scored output of a single benchmark dimension.
type DimensionResult struct {
	DimensionName string
	Category      string
	Score         float64
	Max           float64
	Grade         string
	Checks        []CheckResult
	Metrics       map[string]float64
	Errors        []string
	Duration      time.Duration
}

// Report is the final output of the full benchmark suite.
type Report struct {
	Dimensions      []DimensionResult
	OverallScore    float64
	Grade           string
	Duration        time.Duration
	Scale           string
	Recommendations []string
}

// ScaleConfig controls how much synthetic data is generated and how hard
// the performance dimensions push the system.
type ScaleConfig struct {
	Name             string
	BaseObservations int
}

// NewScaleConfig returns a ScaleConfig for the named tier.
// Valid names: "small", "medium", "large". Unrecognised names default to "small".
func NewScaleConfig(name string) ScaleConfig {
	switch name {
	case "medium":
		return ScaleConfig{Name: "medium", BaseObservations: 10_000}
	case "large":
		return ScaleConfig{Name: "large", BaseObservations: 100_000}
	default:
		return ScaleConfig{Name: "small", BaseObservations: 1_000}
	}
}

// Suite registers dimensions and runs them sequentially.
type Suite struct {
	dims []Dimension
	cfg  ScaleConfig
}

// NewSuite creates a benchmark suite with the given scale configuration.
func NewSuite(cfg ScaleConfig) *Suite {
	return &Suite{cfg: cfg}
}

// Register adds one or more dimensions to the suite.
func (s *Suite) Register(dims ...Dimension) {
	s.dims = append(s.dims, dims...)
}

// DimCount returns the number of registered dimensions.
func (s *Suite) DimCount() int {
	return len(s.dims)
}

// Run creates a shared BenchEnv, executes each registered dimension sequentially,
// and returns a consolidated Report. The environment is closed after all dimensions finish.
func (s *Suite) Run(ctx context.Context) (*Report, error) {
	env, err := NewBenchEnv(ctx, s.cfg)
	if err != nil {
		return nil, fmt.Errorf("create bench env: %w", err)
	}
	defer env.Close()

	start := time.Now()
	results := make([]DimensionResult, 0, len(s.dims))

	for _, dim := range s.dims {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		dimStart := time.Now()
		result := dim.Run(ctx, env)
		result.Duration = time.Since(dimStart)
		result.DimensionName = dim.Name()
		result.Category = dim.Category()
		if result.Grade == "" {
			result.Grade = LetterGrade(result.Score)
		}

		results = append(results, result)
	}

	total := time.Since(start)

	// Build weights: equal weight per dimension by default.
	weights := make(map[string]float64, len(results))
	for _, r := range results {
		weights[r.DimensionName] = 1.0
	}

	overall := ComputeOverallScore(results, weights)
	recs := buildRecommendations(results)

	return &Report{
		Dimensions:      results,
		OverallScore:    overall,
		Grade:           LetterGrade(overall),
		Duration:        total,
		Scale:           s.cfg.Name,
		Recommendations: recs,
	}, nil
}

// buildRecommendations returns the top 3 recommendations from worst-scoring dimensions.
func buildRecommendations(results []DimensionResult) []string {
	type scored struct {
		name  string
		gap   float64
		grade string
	}

	var candidates []scored
	for _, r := range results {
		if r.Max > 0 && r.Score < r.Max {
			candidates = append(candidates, scored{
				name:  r.DimensionName,
				gap:   r.Max - r.Score,
				grade: r.Grade,
			})
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].gap > candidates[j].gap
	})

	recs := make([]string, 0, 3)
	for i, c := range candidates {
		if i >= 3 {
			break
		}
		recs = append(recs, fmt.Sprintf("Improve %s (grade %s, gap %.1f pts)", c.name, c.grade, c.gap))
	}
	return recs
}

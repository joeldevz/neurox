package benchmark

import (
	"context"
	"fmt"
	"time"

	"github.com/joeldevz/neurox/internal/observation"
	"github.com/joeldevz/neurox/internal/recall"
	"github.com/joeldevz/neurox/internal/temporal"
)

// CogTemporal benchmarks the brain's temporal reasoning and decay correctness:
// version evolution through invalidation chains, history-aware queries,
// temporal intent detection, Ebbinghaus decay kind ratios, and parser robustness.
type CogTemporal struct{}

func (d CogTemporal) Name() string     { return "Temporal Cognition" }
func (d CogTemporal) Category() string { return "cognitive" }

func (d CogTemporal) Run(ctx context.Context, env *BenchEnv) DimensionResult {
	var checks []CheckResult
	var errs []string

	checks = append(checks, d.scenarioA(ctx, env, &errs)...)
	checks = append(checks, d.scenarioB(ctx, env, &errs)...)
	checks = append(checks, d.scenarioC(ctx, env, &errs)...)
	checks = append(checks, d.scenarioD(ctx, env, &errs)...)
	checks = append(checks, d.scenarioE(&errs)...)

	passed := 0
	for _, c := range checks {
		if c.Passed {
			passed++
		}
	}

	rawRate := float64(passed) / float64(len(checks)) * 100
	threshold := Threshold{Base: 55, Target: 75, Elite: 90}
	score, _ := EvaluateScore(rawRate, threshold)

	return DimensionResult{
		Score:  score,
		Max:    100,
		Checks: checks,
		Metrics: map[string]float64{
			"checks_passed": float64(passed),
			"checks_total":  float64(len(checks)),
			"recall_rate":   rawRate,
		},
		Errors: errs,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Scenario A: "What is current?" (5 cases × 1 check each = 5 checks)
// For each technology: save V1→V2→V3 via invalidation chains.
// Query with "currently" keyword. Verify V3 appears in top-5 results.
// ─────────────────────────────────────────────────────────────────────────────

type techVersionCase struct {
	label   string
	tech    string
	v1Title string
	v1Body  string
	v2Title string
	v2Body  string
	v3Title string
	v3Body  string
	query   string
	obsType observation.ObservationType
}

func techVersionCasesA() []techVersionCase {
	return []techVersionCase{
		{
			label:   "A1",
			tech:    "Node",
			v1Title: "Node.js runtime version",
			v1Body:  "We are currently using Node.js 16 LTS as our runtime environment.",
			v2Title: "Node.js runtime version",
			v2Body:  "We are currently using Node.js 18 LTS, upgraded from 16.",
			v3Title: "Node.js runtime version",
			v3Body:  "We are currently using Node.js 20 LTS, upgraded from 18.",
			query:   "What Node version are we currently using?",
			obsType: observation.ObservationTypeConfig,
		},
		{
			label:   "A2",
			tech:    "Postgres",
			v1Title: "PostgreSQL database version",
			v1Body:  "We are currently running PostgreSQL 14 in production.",
			v2Title: "PostgreSQL database version",
			v2Body:  "We are currently running PostgreSQL 15, upgraded from 14.",
			v3Title: "PostgreSQL database version",
			v3Body:  "We are currently running PostgreSQL 16, upgraded from 15.",
			query:   "What Postgres version are we currently running?",
			obsType: observation.ObservationTypeConfig,
		},
		{
			label:   "A3",
			tech:    "React",
			v1Title: "React frontend version",
			v1Body:  "The frontend is currently built with React 17.",
			v2Title: "React frontend version",
			v2Body:  "The frontend is currently built with React 18, migrated from 17.",
			v3Title: "React frontend version",
			v3Body:  "The frontend is currently built with React 19, migrated from 18.",
			query:   "What React version are we currently using?",
			obsType: observation.ObservationTypeDecision,
		},
		{
			label:   "A4",
			tech:    "TypeScript",
			v1Title: "TypeScript compiler version",
			v1Body:  "We are currently using TypeScript 4.9 for all type checking.",
			v2Title: "TypeScript compiler version",
			v2Body:  "We are currently using TypeScript 5.0, upgraded from 4.9.",
			v3Title: "TypeScript compiler version",
			v3Body:  "We are currently using TypeScript 5.3, upgraded from 5.0.",
			query:   "What TypeScript version are we currently using?",
			obsType: observation.ObservationTypeConfig,
		},
		{
			label:   "A5",
			tech:    "Python",
			v1Title: "Python interpreter version",
			v1Body:  "The backend services are currently running Python 3.9.",
			v2Title: "Python interpreter version",
			v2Body:  "The backend services are currently running Python 3.11, upgraded from 3.9.",
			v3Title: "Python interpreter version",
			v3Body:  "The backend services are currently running Python 3.12, upgraded from 3.11.",
			query:   "What Python version are we currently using?",
			obsType: observation.ObservationTypeConfig,
		},
	}
}

func (d CogTemporal) scenarioA(ctx context.Context, env *BenchEnv, errs *[]string) []CheckResult {
	cases := techVersionCasesA()
	checks := make([]CheckResult, 0, len(cases))

	for _, tc := range cases {
		// 1. Save V1
		v1, err := env.ObsStore.Save(ctx, observation.Observation{
			Title:           tc.v1Title,
			Content:         tc.v1Body,
			ObservationType: tc.obsType,
			Kind:            observation.KindSemantic,
			Namespace:       "benchmark",
			Retention:       observation.RetentionDurable,
		})
		if err != nil {
			*errs = append(*errs, fmt.Sprintf("%s: save V1 failed: %v", tc.label, err))
			checks = append(checks, CheckResult{
				Name: tc.label + ": V3 in top-5 current results", Passed: false, Detail: "save V1 failed",
			})
			continue
		}

		// 2. Invalidate V1 → creates V2
		inv1, err := env.ObsStore.Invalidate(ctx, env.LinkStore, observation.InvalidateInput{
			ObservationID:      v1.ID,
			Reason:             "upgraded to newer version",
			ReplacementTitle:   tc.v2Title,
			ReplacementContent: tc.v2Body,
		})
		if err != nil {
			*errs = append(*errs, fmt.Sprintf("%s: invalidate V1 failed: %v", tc.label, err))
			checks = append(checks, CheckResult{
				Name: tc.label + ": V3 in top-5 current results", Passed: false, Detail: "invalidate V1 failed",
			})
			continue
		}

		// 3. Invalidate V2 → creates V3
		inv2, err := env.ObsStore.Invalidate(ctx, env.LinkStore, observation.InvalidateInput{
			ObservationID:      inv1.ReplacementID,
			Reason:             "upgraded to newer version",
			ReplacementTitle:   tc.v3Title,
			ReplacementContent: tc.v3Body,
		})
		if err != nil {
			*errs = append(*errs, fmt.Sprintf("%s: invalidate V2 failed: %v", tc.label, err))
			checks = append(checks, CheckResult{
				Name: tc.label + ": V3 in top-5 current results", Passed: false, Detail: "invalidate V2 failed",
			})
			continue
		}
		v3ID := inv2.ReplacementID

		// 4. Search with "currently" keyword to trigger current-state intent
		results, err := env.RecallEngine.Search(ctx, recall.SearchOptions{
			Query:     tc.query,
			Namespace: "benchmark",
			Limit:     5,
		})
		if err != nil {
			*errs = append(*errs, fmt.Sprintf("%s: search failed: %v", tc.label, err))
			checks = append(checks, CheckResult{
				Name: tc.label + ": V3 in top-5 current results", Passed: false, Detail: "search failed",
			})
			continue
		}

		v3Found := false
		for _, r := range results {
			if r.ID == v3ID {
				v3Found = true
				break
			}
		}

		checks = append(checks, CheckResult{
			Name:   tc.label + ": V3 in top-5 current results",
			Passed: v3Found,
			Detail: fmt.Sprintf("v3ID=%s found=%v results=%d tech=%s", v3ID, v3Found, len(results), tc.tech),
		})
	}

	return checks
}

// ─────────────────────────────────────────────────────────────────────────────
// Scenario B: "What changed?" (3 cases × 1 check each = 3 checks)
// Save 3 change observations, query with "previously"/"before" to trigger
// history intent, check at least 2 of 3 appear in results.
// ─────────────────────────────────────────────────────────────────────────────

type changeCase struct {
	label   string
	title   string
	content string
}

func changeCasesB() []changeCase {
	return []changeCase{
		{
			label:   "B1",
			title:   "Authentication method migration",
			content: "Previously we used session cookies backed by Redis. We migrated to JWT tokens before the v2 release. The old session cookie approach was deprecated.",
		},
		{
			label:   "B2",
			title:   "Database engine migration",
			content: "Before the migration project, we used MySQL 5.7. The team decided to move to PostgreSQL for better JSONB support and window functions.",
		},
		{
			label:   "B3",
			title:   "CI/CD platform change",
			content: "Previously the pipeline ran on Jenkins. We moved to GitHub Actions before Q3. Jenkins was decommissioned after a 3-month transition period.",
		},
	}
}

func (d CogTemporal) scenarioB(ctx context.Context, env *BenchEnv, errs *[]string) []CheckResult {
	const ns = "bench-temporal-b"
	cases := changeCasesB()

	savedIDs := make(map[string]string, len(cases))
	allSaved := true
	for _, tc := range cases {
		saved, err := env.ObsStore.Save(ctx, observation.Observation{
			Title:           tc.title,
			Content:         tc.content,
			ObservationType: observation.ObservationTypeDecision,
			Kind:            observation.KindSemantic,
			Namespace:       ns,
			Retention:       observation.RetentionDurable,
			Confidence:      0.85,
		})
		if err != nil {
			*errs = append(*errs, fmt.Sprintf("%s: save failed: %v", tc.label, err))
			allSaved = false
			continue
		}
		savedIDs[tc.label] = saved.ID
	}

	if !allSaved {
		result := make([]CheckResult, 0, len(cases))
		for _, tc := range cases {
			result = append(result, CheckResult{
				Name: tc.label + ": appears in history results", Passed: false, Detail: "save failed",
			})
		}
		return result
	}

	// Query with "previously" to trigger history intent.
	// Use IncludeStale: true to ensure history queries surface older observations.
	results, err := env.RecallEngine.Search(ctx, recall.SearchOptions{
		Query:        "What previously changed in our infrastructure before the migration?",
		Namespace:    ns,
		Limit:        10,
		IncludeStale: true,
	})
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("B: search failed: %v", err))
		result := make([]CheckResult, 0, len(cases))
		for _, tc := range cases {
			result = append(result, CheckResult{
				Name: tc.label + ": appears in history results", Passed: false, Detail: "search failed",
			})
		}
		return result
	}

	resultIDs := make(map[string]bool, len(results))
	for _, r := range results {
		resultIDs[r.ID] = true
	}

	foundCount := 0
	checkResults := make([]CheckResult, 0, len(cases))
	for _, tc := range cases {
		id := savedIDs[tc.label]
		found := resultIDs[id]
		if found {
			foundCount++
		}
		checkResults = append(checkResults, CheckResult{
			Name:   tc.label + ": appears in history results",
			Passed: found,
			Detail: fmt.Sprintf("id=%s found=%v results=%d", id, found, len(results)),
		})
	}

	// Override: if at least 2 of 3 found, mark all participating checks as passed.
	// The intent is "system found at least 2" — we report individual pass/fail
	// but the specification requires ≥2. Reflect the actual per-check result.
	_ = foundCount // individual results already reflect the truth

	return checkResults
}

// ─────────────────────────────────────────────────────────────────────────────
// Scenario C: "When did X happen?" (5 cases × 2 checks each = 10 checks)
// Save observations with temporal expressions, verify intent detected and obs found.
// ─────────────────────────────────────────────────────────────────────────────

type whenCase struct {
	label          string
	title          string
	content        string
	query          string
	expectedIntent recall.TemporalIntentKind
}

func whenCasesC() []whenCase {
	return []whenCase{
		{
			label:          "C1",
			title:          "React migration completed",
			content:        "The frontend migration from Angular to React was completed on 2025-06-15. All components were rewritten in React 18.",
			query:          "When did we migrate to React?",
			expectedIntent: recall.IntentWhen,
		},
		{
			label:          "C2",
			title:          "Redis cache deployment",
			content:        "Redis caching layer was deployed 3 months ago to handle session data. Performance improved by 40% after the deployment.",
			query:          "When was Redis deployed for caching?",
			expectedIntent: recall.IntentWhen,
		},
		{
			label:          "C3",
			title:          "Security audit completion",
			content:        "External security audit was completed on March 2025. Critical findings were resolved within two weeks.",
			query:          "When did the security audit happen?",
			expectedIntent: recall.IntentWhen,
		},
		{
			label:          "C4",
			title:          "Database migration to PostgreSQL",
			content:        "We migrated from MySQL to PostgreSQL two years ago. The migration took 6 weeks and was completed without downtime.",
			query:          "When did we migrate to PostgreSQL?",
			expectedIntent: recall.IntentWhen,
		},
		{
			label:          "C5",
			title:          "Kubernetes adoption",
			content:        "The team adopted Kubernetes for container orchestration in January 2024. The transition from Docker Swarm took 2 months.",
			query:          "When did we adopt Kubernetes?",
			expectedIntent: recall.IntentWhen,
		},
	}
}

func (d CogTemporal) scenarioC(ctx context.Context, env *BenchEnv, errs *[]string) []CheckResult {
	const ns = "bench-temporal-c"
	cases := whenCasesC()
	checks := make([]CheckResult, 0, len(cases)*2)
	now := time.Now()

	for _, tc := range cases {
		// 1. Save observation
		saved, err := env.ObsStore.Save(ctx, observation.Observation{
			Title:           tc.title,
			Content:         tc.content,
			ObservationType: observation.ObservationTypeDiscovery,
			Kind:            observation.KindSemantic,
			Namespace:       ns,
			Retention:       observation.RetentionDurable,
			Confidence:      0.85,
		})
		if err != nil {
			*errs = append(*errs, fmt.Sprintf("%s: save failed: %v", tc.label, err))
			checks = append(checks,
				CheckResult{Name: tc.label + ": temporal intent detected", Passed: false, Detail: "save failed"},
				CheckResult{Name: tc.label + ": observation found in results", Passed: false, Detail: "save failed"},
			)
			continue
		}

		// 2. Verify temporal intent detection
		intent := recall.DetectTemporalIntent(tc.query, now)
		intentDetected := intent.Kind == tc.expectedIntent
		checks = append(checks, CheckResult{
			Name:   tc.label + ": temporal intent detected",
			Passed: intentDetected,
			Detail: fmt.Sprintf("query=%q got=%q expected=%q", tc.query, intent.Kind, tc.expectedIntent),
		})

		// 3. Search and verify observation found
		results, err := env.RecallEngine.Search(ctx, recall.SearchOptions{
			Query:     tc.query,
			Namespace: ns,
			Limit:     10,
		})
		if err != nil {
			*errs = append(*errs, fmt.Sprintf("%s: search failed: %v", tc.label, err))
			checks = append(checks, CheckResult{
				Name: tc.label + ": observation found in results", Passed: false, Detail: "search failed",
			})
			continue
		}

		found := false
		for _, r := range results {
			if r.ID == saved.ID {
				found = true
				break
			}
		}

		checks = append(checks, CheckResult{
			Name:   tc.label + ": observation found in results",
			Passed: found,
			Detail: fmt.Sprintf("id=%s found=%v results=%d", saved.ID, found, len(results)),
		})
	}

	return checks
}

// ─────────────────────────────────────────────────────────────────────────────
// Scenario D: Decay Kind Ratios (3 checks)
// Save episodic, semantic, procedural observations (layer=0 so decay applies).
// Apply decay 10 times. Verify ordering: episodic < semantic < procedural.
// ─────────────────────────────────────────────────────────────────────────────

func (d CogTemporal) scenarioD(ctx context.Context, env *BenchEnv, errs *[]string) []CheckResult {
	const ns = "bench-temporal-d"
	const startImportance = 0.8

	// Save 3 observations with different kinds (layer=0 = Buffer, so decay applies).
	episodic, err := env.ObsStore.Save(ctx, observation.Observation{
		Title:           "Decay test: episodic observation",
		Content:         "This episodic memory should decay fastest under Ebbinghaus decay.",
		ObservationType: observation.ObservationTypeDiscovery,
		Kind:            observation.KindEpisodic,
		Namespace:       ns,
		Retention:       observation.RetentionDurable,
		Confidence:      startImportance,
	})
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("D: save episodic failed: %v", err))
		return []CheckResult{
			{Name: "D1: episodic decays faster than semantic", Passed: false, Detail: "save failed"},
			{Name: "D2: semantic decays faster than procedural", Passed: false, Detail: "save failed"},
			{Name: "D3: episodic < semantic < procedural ordering", Passed: false, Detail: "save failed"},
		}
	}

	semantic, err := env.ObsStore.Save(ctx, observation.Observation{
		Title:           "Decay test: semantic observation",
		Content:         "This semantic memory should decay at a medium rate under Ebbinghaus decay.",
		ObservationType: observation.ObservationTypeDecision,
		Kind:            observation.KindSemantic,
		Namespace:       ns,
		Retention:       observation.RetentionDurable,
		Confidence:      startImportance,
	})
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("D: save semantic failed: %v", err))
		return []CheckResult{
			{Name: "D1: episodic decays faster than semantic", Passed: false, Detail: "save failed"},
			{Name: "D2: semantic decays faster than procedural", Passed: false, Detail: "save failed"},
			{Name: "D3: episodic < semantic < procedural ordering", Passed: false, Detail: "save failed"},
		}
	}

	procedural, err := env.ObsStore.Save(ctx, observation.Observation{
		Title:           "Decay test: procedural observation",
		Content:         "This procedural memory should decay slowest under Ebbinghaus decay.",
		ObservationType: observation.ObservationTypePattern,
		Kind:            observation.KindProcedural,
		Namespace:       ns,
		Retention:       observation.RetentionDurable,
		Confidence:      startImportance,
	})
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("D: save procedural failed: %v", err))
		return []CheckResult{
			{Name: "D1: episodic decays faster than semantic", Passed: false, Detail: "save failed"},
			{Name: "D2: semantic decays faster than procedural", Passed: false, Detail: "save failed"},
			{Name: "D3: episodic < semantic < procedural ordering", Passed: false, Detail: "save failed"},
		}
	}

	// Force observations to Buffer layer (layer=0) so decay applies.
	// The Store saves to Buffer by default (layer=0), so no additional action needed.
	// But to be safe, explicitly ensure layer=0 in DB.
	for _, id := range []string{episodic.ID, semantic.ID, procedural.ID} {
		if _, err := env.DB.ExecContext(ctx,
			`UPDATE observations SET layer = 0 WHERE id = ?`, id,
		); err != nil {
			*errs = append(*errs, fmt.Sprintf("D: set layer=0 failed for %s: %v", id, err))
		}
	}

	// Apply decay 10 times.
	for i := 0; i < 10; i++ {
		if _, err := env.DecayEngine.ApplyDecay(ctx); err != nil {
			*errs = append(*errs, fmt.Sprintf("D: ApplyDecay epoch %d failed: %v", i+1, err))
		}
	}

	// Query importance from DB for each observation.
	importanceOf := func(id string) float64 {
		var imp float64
		_ = env.DB.QueryRowContext(ctx,
			`SELECT importance FROM observations WHERE id = ?`, id,
		).Scan(&imp)
		return imp
	}

	impEpisodic := importanceOf(episodic.ID)
	impSemantic := importanceOf(semantic.ID)
	impProcedural := importanceOf(procedural.ID)

	episodicFasterThanSemantic := impEpisodic < impSemantic
	semanticFasterThanProcedural := impSemantic < impProcedural
	fullOrdering := impEpisodic < impSemantic && impSemantic < impProcedural

	return []CheckResult{
		{
			Name:   "D1: episodic decays faster than semantic",
			Passed: episodicFasterThanSemantic,
			Detail: fmt.Sprintf("episodic=%.4f semantic=%.4f", impEpisodic, impSemantic),
		},
		{
			Name:   "D2: semantic decays faster than procedural",
			Passed: semanticFasterThanProcedural,
			Detail: fmt.Sprintf("semantic=%.4f procedural=%.4f", impSemantic, impProcedural),
		},
		{
			Name:   "D3: episodic < semantic < procedural ordering",
			Passed: fullOrdering,
			Detail: fmt.Sprintf("episodic=%.4f semantic=%.4f procedural=%.4f", impEpisodic, impSemantic, impProcedural),
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Scenario E: Temporal Parser Edge Cases (15 checks)
// Use temporal.NewParser() directly with panic safety.
// ─────────────────────────────────────────────────────────────────────────────

type parserCase struct {
	label       string
	input       string
	expectKind  temporal.MentionKind // empty = any/some results OK
	expectCount int                  // -1 = just check no panic
	checkKind   bool
}

func parserCasesE() []parserCase {
	return []parserCase{
		{
			label:       "E1",
			input:       "2026-03-15",
			expectKind:  temporal.KindAbsolute,
			expectCount: 1,
			checkKind:   true,
		},
		{
			label:       "E2",
			input:       "March 15, 2026",
			expectKind:  temporal.KindAbsolute,
			expectCount: 1,
			checkKind:   true,
		},
		{
			label:       "E3",
			input:       "15 de marzo de 2026",
			expectKind:  temporal.KindAbsolute,
			expectCount: 1,
			checkKind:   true,
		},
		{
			label:       "E4",
			input:       "yesterday",
			expectKind:  temporal.KindRelative,
			expectCount: 1,
			checkKind:   true,
		},
		{
			label:       "E5",
			input:       "3 days ago",
			expectKind:  temporal.KindRelative,
			expectCount: 1,
			checkKind:   true,
		},
		{
			label:       "E6",
			input:       "hace 2 semanas",
			expectKind:  temporal.KindRelative,
			expectCount: 1,
			checkKind:   true,
		},
		{
			label:       "E7",
			input:       "currently",
			expectKind:  temporal.KindCurrentState,
			expectCount: 1,
			checkKind:   true,
		},
		{
			label:       "E8",
			input:       "at the moment",
			expectKind:  temporal.KindCurrentState,
			expectCount: 1,
			checkKind:   true,
		},
		{
			label:       "E9",
			input:       "for 30 days",
			expectKind:  temporal.KindDuration,
			expectCount: 1,
			checkKind:   true,
		},
		{
			label:       "E10",
			input:       "since March 2025",
			expectKind:  temporal.KindDuration,
			expectCount: 1,
			checkKind:   true,
		},
		{
			label:       "E11",
			input:       "last week we deployed the service",
			expectKind:  temporal.KindRelative,
			expectCount: 1,
			checkKind:   true,
		},
		{
			label:       "E12",
			input:       "two weeks ago the migration finished",
			expectKind:  temporal.KindRelative,
			expectCount: 1,
			checkKind:   true,
		},
		{
			label:       "E13",
			input:       "", // empty string — no panic
			expectCount: -1,
			checkKind:   false,
		},
		{
			label:       "E14",
			input:       "no temporal expression here at all, just plain text about code",
			expectCount: -1,
			checkKind:   false,
		},
		{
			label:       "E15",
			input:       "2026-03-01 and yesterday and currently running",
			expectCount: -1, // multiple expressions, just check no panic
			checkKind:   false,
		},
	}
}

func (d CogTemporal) scenarioE(errs *[]string) []CheckResult {
	cases := parserCasesE()
	checks := make([]CheckResult, 0, len(cases))
	parser := temporal.NewParser()
	now := time.Now()

	for _, tc := range cases {
		tc := tc // capture loop variable

		var (
			results  []temporal.ParseResult
			panicked bool
		)

		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
					*errs = append(*errs, fmt.Sprintf("%s: parser panicked: %v", tc.label, r))
				}
			}()
			results = parser.Parse(tc.input, now)
		}()

		if panicked {
			checks = append(checks, CheckResult{
				Name:   tc.label + ": parser no panic + result",
				Passed: false,
				Detail: fmt.Sprintf("input=%q: parser panicked", tc.input),
			})
			continue
		}

		if tc.expectCount < 0 {
			// Just check no panic — any result (including empty) is acceptable.
			checks = append(checks, CheckResult{
				Name:   tc.label + ": parser no panic + result",
				Passed: true,
				Detail: fmt.Sprintf("input=%q results=%d (no panic required only)", tc.input, len(results)),
			})
			continue
		}

		// Check exact count.
		if len(results) < tc.expectCount {
			checks = append(checks, CheckResult{
				Name:   tc.label + ": parser no panic + result",
				Passed: false,
				Detail: fmt.Sprintf("input=%q got=%d results want>=%d", tc.input, len(results), tc.expectCount),
			})
			continue
		}

		// Optionally check kind of first result.
		if tc.checkKind && len(results) > 0 {
			kindOK := results[0].Kind == tc.expectKind
			checks = append(checks, CheckResult{
				Name:   tc.label + ": parser no panic + result",
				Passed: kindOK,
				Detail: fmt.Sprintf("input=%q got_kind=%q expected_kind=%q results=%d", tc.input, results[0].Kind, tc.expectKind, len(results)),
			})
		} else {
			checks = append(checks, CheckResult{
				Name:   tc.label + ": parser no panic + result",
				Passed: len(results) >= tc.expectCount,
				Detail: fmt.Sprintf("input=%q results=%d", tc.input, len(results)),
			})
		}
	}

	return checks
}

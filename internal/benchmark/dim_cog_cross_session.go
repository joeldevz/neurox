package benchmark

import (
	"context"
	"fmt"

	"github.com/joeldevz/neurox/internal/observation"
	"github.com/joeldevz/neurox/internal/recall"
)

// CogCrossSession benchmarks the brain's ability to persist and recall memory
// across simulated sessions: preference persistence, accumulated project knowledge,
// file-linked context retrieval, and session lifecycle management.
type CogCrossSession struct{}

func (d CogCrossSession) Name() string     { return "Cross-Session Memory" }
func (d CogCrossSession) Category() string { return "cognitive" }

func (d CogCrossSession) Run(ctx context.Context, env *BenchEnv) DimensionResult {
	var checks []CheckResult
	var errs []string

	checks = append(checks, d.scenarioA(ctx, env, &errs)...)
	checks = append(checks, d.scenarioB(ctx, env, &errs)...)
	checks = append(checks, d.scenarioC(ctx, env, &errs)...)
	checks = append(checks, d.scenarioD(ctx, env, &errs)...)

	passed := 0
	for _, c := range checks {
		if c.Passed {
			passed++
		}
	}

	rawRate := float64(passed) / float64(len(checks)) * 100
	threshold := Threshold{Base: 60, Target: 80, Elite: 95}
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
// Scenario A: Preference Persistence (10 checks)
// Save 10 preference observations across simulated sessions → consolidate ×2 →
// search → verify all 10 preference IDs survive in top-10 results.
// ─────────────────────────────────────────────────────────────────────────────

func (d CogCrossSession) scenarioA(ctx context.Context, env *BenchEnv, errs *[]string) []CheckResult {
	const ns = "bench-cross-a"

	type prefCase struct {
		label   string
		title   string
		content string
		kind    observation.Kind
		obsType observation.ObservationType
	}

	cases := []prefCase{
		{
			label:   "A1",
			title:   "User prefers tabs over spaces",
			content: "PREFERENCE: Always use tabs for indentation, never spaces. Applies to all languages.",
			kind:    observation.KindProcedural,
			obsType: observation.ObservationTypePreference,
		},
		{
			label:   "A2",
			title:   "User wants English commit messages",
			content: "PREFERENCE: All commit messages must be written in English, using conventional commits format.",
			kind:    observation.KindProcedural,
			obsType: observation.ObservationTypePreference,
		},
		{
			label:   "A3",
			title:   "Always use strict TypeScript",
			content: "PREFERENCE: TypeScript projects must enable strict mode in tsconfig.json. No implicit any.",
			kind:    observation.KindSemantic,
			obsType: observation.ObservationTypePreference,
		},
		{
			label:   "A4",
			title:   "Prefer named exports over default exports",
			content: "PREFERENCE: Use named exports in all TypeScript/JavaScript modules. Default exports are discouraged.",
			kind:    observation.KindSemantic,
			obsType: observation.ObservationTypePreference,
		},
		{
			label:   "A5",
			title:   "Use conventional commits format",
			content: "PREFERENCE: All commits must follow conventional commits: feat, fix, chore, docs, refactor, test, perf, ci.",
			kind:    observation.KindProcedural,
			obsType: observation.ObservationTypePreference,
		},
		{
			label:   "A6",
			title:   "Dark mode preferred for IDE",
			content: "PREFERENCE: IDE and editor theme should be dark mode. Use dark theme variants of all tools.",
			kind:    observation.KindSemantic,
			obsType: observation.ObservationTypePreference,
		},
		{
			label:   "A7",
			title:   "Prefer functional components in React",
			content: "PREFERENCE: Always use functional components and hooks in React. Avoid class components.",
			kind:    observation.KindSemantic,
			obsType: observation.ObservationTypePreference,
		},
		{
			label:   "A8",
			title:   "Use pnpm not npm",
			content: "PREFERENCE: Use pnpm as the package manager. Never use npm or yarn. Run pnpm install.",
			kind:    observation.KindProcedural,
			obsType: observation.ObservationTypePreference,
		},
		{
			label:   "A9",
			title:   "Error handling with Result type, not exceptions",
			content: "PREFERENCE: Handle errors with Result/Either types instead of throwing exceptions. Return errors as values.",
			kind:    observation.KindSemantic,
			obsType: observation.ObservationTypePreference,
		},
		{
			label:   "A10",
			title:   "Always add JSDoc to public functions",
			content: "PREFERENCE: All public-facing functions and methods must have JSDoc comments describing parameters and return values.",
			kind:    observation.KindProcedural,
			obsType: observation.ObservationTypePreference,
		},
	}

	// Save all 10 preferences, collecting their IDs.
	prefIDs := make(map[string]string, len(cases)) // label → id
	allSaved := true
	for _, tc := range cases {
		saved, err := env.ObsStore.Save(ctx, observation.Observation{
			Title:           tc.title,
			Content:         tc.content,
			ObservationType: tc.obsType,
			Kind:            tc.kind,
			Namespace:       ns,
			Retention:       observation.RetentionDurable,
			Confidence:      0.9,
		})
		if err != nil {
			*errs = append(*errs, fmt.Sprintf("%s: save preference failed: %v", tc.label, err))
			allSaved = false
			continue
		}
		prefIDs[tc.label] = saved.ID
	}

	if !allSaved {
		checks := make([]CheckResult, 0, len(cases))
		for _, tc := range cases {
			checks = append(checks, CheckResult{
				Name:   tc.label + ": preference survives consolidation",
				Passed: false,
				Detail: "preference save failed",
			})
		}
		return checks
	}

	// Run consolidation twice to simulate multi-epoch persistence.
	for i := 0; i < 2; i++ {
		if err := env.Pipeline.Run(ctx); err != nil {
			*errs = append(*errs, fmt.Sprintf("A: pipeline epoch %d warning: %v", i+1, err))
		}
	}

	// Search for preferences.
	results, err := env.RecallEngine.Search(ctx, recall.SearchOptions{
		Query:     "user preferences coding style conventions",
		Namespace: ns,
		Limit:     10,
	})
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("A: search failed: %v", err))
		checks := make([]CheckResult, 0, len(cases))
		for _, tc := range cases {
			checks = append(checks, CheckResult{
				Name:   tc.label + ": preference survives consolidation",
				Passed: false,
				Detail: "search failed",
			})
		}
		return checks
	}

	resultIDs := make(map[string]bool, len(results))
	for _, r := range results {
		resultIDs[r.ID] = true
	}

	checks := make([]CheckResult, 0, len(cases))
	labelToTitle := map[string]string{
		"A1":  "tabs preference survives",
		"A2":  "English commits preference survives",
		"A3":  "strict TypeScript preference survives",
		"A4":  "named exports preference survives",
		"A5":  "conventional commits preference survives",
		"A6":  "dark mode preference survives",
		"A7":  "functional components preference survives",
		"A8":  "pnpm preference survives",
		"A9":  "Result type preference survives",
		"A10": "JSDoc preference survives",
	}

	for _, tc := range cases {
		id := prefIDs[tc.label]
		found := resultIDs[id]
		checkName := fmt.Sprintf("%s: %s", tc.label, labelToTitle[tc.label])
		checks = append(checks, CheckResult{
			Name:   checkName,
			Passed: found,
			Detail: fmt.Sprintf("id=%s found_in_top10=%v results=%d", id, found, len(results)),
		})
	}

	return checks
}

// ─────────────────────────────────────────────────────────────────────────────
// Scenario B: Accumulated Cross-Session Knowledge (6 checks)
// Save 6 decisions across 3 simulated sessions → search → verify all IDs appear.
// ─────────────────────────────────────────────────────────────────────────────

func (d CogCrossSession) scenarioB(ctx context.Context, env *BenchEnv, errs *[]string) []CheckResult {
	const ns = "bench-cross-b"

	type sessionObs struct {
		label   string
		title   string
		content string
	}

	sessionObservations := []sessionObs{
		// Session 1
		{
			label:   "B1",
			title:   "Architecture: microservices with event sourcing",
			content: "DECISION: Adopt microservices architecture with event sourcing. Each service owns its domain. Events stored in Postgres, projected to read models.",
		},
		{
			label:   "B2",
			title:   "Database: PostgreSQL 16 as primary store",
			content: "DECISION: PostgreSQL 16 is the primary database. Used for all persistent application data. Deployed in HA mode with streaming replication.",
		},
		// Session 2
		{
			label:   "B3",
			title:   "Added Redis for session caching and rate limiting",
			content: "DECISION: Redis 7 added as caching layer for session data and rate limiting. Cluster mode for production. TTL-based eviction policy.",
		},
		{
			label:   "B4",
			title:   "API gateway: Kong for routing and auth",
			content: "DECISION: Kong API gateway handles all routing and authentication. Plugins: JWT auth, rate limiting, request transformation. Deployed as Kubernetes ingress.",
		},
		// Session 3
		{
			label:   "B5",
			title:   "Monitoring stack: Datadog APM and logs",
			content: "DECISION: Datadog for APM traces and log aggregation. All services instrumented with Datadog agents. Dashboards and SLO monitors configured.",
		},
		{
			label:   "B6",
			title:   "CI/CD: GitHub Actions with Docker build",
			content: "DECISION: GitHub Actions for CI/CD pipeline. Docker multi-stage builds for all services. Push to main triggers staging deploy. Production requires manual approval.",
		},
	}

	obsIDs := make(map[string]string, len(sessionObservations)) // label → id
	allSaved := true
	for _, obs := range sessionObservations {
		saved, err := env.ObsStore.Save(ctx, observation.Observation{
			Title:           obs.title,
			Content:         obs.content,
			ObservationType: observation.ObservationTypeDecision,
			Kind:            observation.KindSemantic,
			Namespace:       ns,
			Retention:       observation.RetentionDurable,
			Confidence:      0.9,
		})
		if err != nil {
			*errs = append(*errs, fmt.Sprintf("%s: save observation failed: %v", obs.label, err))
			allSaved = false
			continue
		}
		obsIDs[obs.label] = saved.ID
	}

	if !allSaved {
		checks := make([]CheckResult, 0, len(sessionObservations))
		for _, obs := range sessionObservations {
			checks = append(checks, CheckResult{
				Name:   obs.label + ": decision found in recall",
				Passed: false,
				Detail: "save failed",
			})
		}
		return checks
	}

	results, err := env.RecallEngine.Search(ctx, recall.SearchOptions{
		Query:     "project architecture infrastructure stack",
		Namespace: ns,
		Limit:     10,
	})
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("B: search failed: %v", err))
		checks := make([]CheckResult, 0, len(sessionObservations))
		for _, obs := range sessionObservations {
			checks = append(checks, CheckResult{
				Name:   obs.label + ": decision found in recall",
				Passed: false,
				Detail: "search failed",
			})
		}
		return checks
	}

	resultIDs := make(map[string]bool, len(results))
	for _, r := range results {
		resultIDs[r.ID] = true
	}

	labelToTitle := map[string]string{
		"B1": "microservices decision found",
		"B2": "PostgreSQL decision found",
		"B3": "Redis decision found",
		"B4": "Kong API gateway decision found",
		"B5": "Datadog monitoring decision found",
		"B6": "CI/CD decision found",
	}

	checks := make([]CheckResult, 0, len(sessionObservations))
	for _, obs := range sessionObservations {
		id := obsIDs[obs.label]
		found := resultIDs[id]
		checks = append(checks, CheckResult{
			Name:   fmt.Sprintf("%s: %s", obs.label, labelToTitle[obs.label]),
			Passed: found,
			Detail: fmt.Sprintf("id=%s found=%v results=%d", id, found, len(results)),
		})
	}

	return checks
}

// ─────────────────────────────────────────────────────────────────────────────
// Scenario C: File-Linked Context Across Sessions (5 checks)
// Save 5 observations linked to specific files → GetContext with file filter →
// verify all 5 appear in the returned items.
// ─────────────────────────────────────────────────────────────────────────────

func (d CogCrossSession) scenarioC(ctx context.Context, env *BenchEnv, errs *[]string) []CheckResult {
	const ns = "bench-cross-c"

	type fileObs struct {
		label   string
		title   string
		content string
		file    string
	}

	fileObservations := []fileObs{
		{
			label:   "C1",
			title:   "Auth middleware validates JWT tokens and extracts user ID",
			content: "DISCOVERY: Auth middleware validates JWT bearer tokens using RS256. Extracts user_id and role from claims. Rejects expired or malformed tokens with 401.",
			file:    "src/middleware/auth.ts",
		},
		{
			label:   "C2",
			title:   "Rate limiter uses Redis sliding window",
			content: "DISCOVERY: Rate limiter implements sliding window algorithm backed by Redis. Limit: 100 req/min per user token. Returns 429 with Retry-After header when exceeded.",
			file:    "src/middleware/rateLimit.ts",
		},
		{
			label:   "C3",
			title:   "Database connection pool configuration",
			content: "DISCOVERY: Database pool uses pg client with max 20 connections, idle timeout 30s, connection timeout 5s. Connection string from DATABASE_URL env var.",
			file:    "src/lib/db.ts",
		},
		{
			label:   "C4",
			title:   "Error handling middleware with structured logging",
			content: "DISCOVERY: Error middleware catches all unhandled errors. Logs structured JSON with correlation_id, error code, stack trace. Returns RFC 7807 problem details.",
			file:    "src/middleware/errorHandler.ts",
		},
		{
			label:   "C5",
			title:   "Health check endpoint returns service status",
			content: "DISCOVERY: Health check at GET /health returns {status, version, uptime, db_ok}. Used by Kubernetes liveness probe. DB check via SELECT 1.",
			file:    "src/routes/health.ts",
		},
	}

	obsIDs := make(map[string]string, len(fileObservations)) // label → id
	allSaved := true
	allFiles := make([]string, 0, len(fileObservations))

	for _, obs := range fileObservations {
		saved, err := env.ObsStore.Save(ctx, observation.Observation{
			Title:           obs.title,
			Content:         obs.content,
			ObservationType: observation.ObservationTypeDiscovery,
			Kind:            observation.KindSemantic,
			Namespace:       ns,
			Retention:       observation.RetentionDurable,
			Confidence:      0.85,
			Files:           []string{obs.file},
		})
		if err != nil {
			*errs = append(*errs, fmt.Sprintf("%s: save observation failed: %v", obs.label, err))
			allSaved = false
			continue
		}
		obsIDs[obs.label] = saved.ID
		allFiles = append(allFiles, obs.file)
	}

	if !allSaved {
		checks := make([]CheckResult, 0, len(fileObservations))
		for _, obs := range fileObservations {
			checks = append(checks, CheckResult{
				Name:   obs.label + ": observation found via file link",
				Passed: false,
				Detail: "save failed",
			})
		}
		return checks
	}

	ctxResult, err := env.ProactiveEngine.GetContext(ctx, ns, allFiles, 20)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("C: GetContext failed: %v", err))
		checks := make([]CheckResult, 0, len(fileObservations))
		for _, obs := range fileObservations {
			checks = append(checks, CheckResult{
				Name:   obs.label + ": observation found via file link",
				Passed: false,
				Detail: "GetContext failed",
			})
		}
		return checks
	}

	itemIDs := make(map[string]bool, len(ctxResult.Items))
	for _, item := range ctxResult.Items {
		itemIDs[item.ID] = true
	}

	labelToTitle := map[string]string{
		"C1": "auth middleware found via file link",
		"C2": "rate limiter found via file link",
		"C3": "database config found via file link",
		"C4": "error handler found via file link",
		"C5": "health check found via file link",
	}

	checks := make([]CheckResult, 0, len(fileObservations))
	for _, obs := range fileObservations {
		id := obsIDs[obs.label]
		found := itemIDs[id]
		checks = append(checks, CheckResult{
			Name:   fmt.Sprintf("%s: %s", obs.label, labelToTitle[obs.label]),
			Passed: found,
			Detail: fmt.Sprintf("id=%s found=%v total_items=%d file=%s", id, found, len(ctxResult.Items), obs.file),
		})
	}

	return checks
}

// ─────────────────────────────────────────────────────────────────────────────
// Scenario D: Session Start/End Lifecycle (3 checks)
// Start a session → end the session → verify ID, no error, correct namespace in DB.
// ─────────────────────────────────────────────────────────────────────────────

func (d CogCrossSession) scenarioD(ctx context.Context, env *BenchEnv, errs *[]string) []CheckResult {
	const ns = "bench-cross-d"

	// D1: Start a session.
	startResult, startErr := env.SessionMgr.Start(
		ctx,
		"Implementing user authentication",
		"/home/dev/myproject",
		"feat/auth",
		ns,
	)

	sessionStarted := startErr == nil && startResult.SessionID != ""
	if !sessionStarted {
		errMsg := "unknown error"
		if startErr != nil {
			errMsg = startErr.Error()
		}
		*errs = append(*errs, fmt.Sprintf("D: session start failed: %v", errMsg))
		return []CheckResult{
			{Name: "D1: session started successfully", Passed: false, Detail: errMsg},
			{Name: "D2: session ended without error", Passed: false, Detail: "session not started"},
			{Name: "D3: session has correct namespace", Passed: false, Detail: "session not started"},
		}
	}

	// D2: End the session.
	_, endErr := env.SessionMgr.End(
		ctx,
		startResult.SessionID,
		"Implemented JWT authentication with refresh tokens. Added middleware for token validation. Configured Redis for session storage.",
	)

	// D3: Verify the session has the correct namespace in the DB.
	var dbNamespace string
	dbQueryErr := env.DB.QueryRowContext(ctx,
		`SELECT namespace FROM sessions WHERE id = ?`,
		startResult.SessionID,
	).Scan(&dbNamespace)

	namespaceOK := dbQueryErr == nil && dbNamespace == ns

	return []CheckResult{
		{
			Name:   "D1: session started successfully",
			Passed: sessionStarted,
			Detail: fmt.Sprintf("session_id=%s namespace=%s", startResult.SessionID, startResult.Namespace),
		},
		{
			Name:   "D2: session ended without error",
			Passed: endErr == nil,
			Detail: fmt.Sprintf("end_err=%v", endErr),
		},
		{
			Name:   "D3: session has correct namespace",
			Passed: namespaceOK,
			Detail: fmt.Sprintf("db_namespace=%q expected=%q db_err=%v", dbNamespace, ns, dbQueryErr),
		},
	}
}

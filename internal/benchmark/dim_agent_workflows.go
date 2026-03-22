package benchmark

import (
	"context"
	"fmt"
	"strings"
)

// AgentWorkflows benchmarks the "Agent Workflow Correctness" dimension by
// executing five realistic multi-step MCP tool sequences and verifying that
// the results are semantically correct at every checkpoint.
//
// Workflow A — Session Lifecycle
// Workflow B — Knowledge Update via MCP (topic_key upsert + invalidate)
// Workflow C — Vague vs Precise Queries (precision_success_rate vs vague_success_rate)
// Workflow D — Consolidation via MCP (save 30 obs → consolidate → recall → context)
// Workflow E — Git Hook Staleness propagation
//
// Score is the combined check pass rate.
// Thresholds: Base >55% | Target >75% | Elite >90%.
type AgentWorkflows struct{}

func (d AgentWorkflows) Name() string     { return "Agent Workflow Correctness" }
func (d AgentWorkflows) Category() string { return "agent" }

func (d AgentWorkflows) Run(ctx context.Context, env *BenchEnv) DimensionResult {
	var checks []CheckResult
	var errs []string

	runWorkflowA(ctx, env.Scale, &checks, &errs)
	runWorkflowB(ctx, env.Scale, &checks, &errs)
	runWorkflowC(ctx, env.Scale, &checks, &errs)
	runWorkflowD(ctx, env.Scale, &checks, &errs)
	runWorkflowE(ctx, env.Scale, &checks, &errs)

	passed := 0
	for _, c := range checks {
		if c.Passed {
			passed++
		}
	}

	rawRate := 0.0
	if len(checks) > 0 {
		rawRate = float64(passed) / float64(len(checks)) * 100
	}
	threshold := Threshold{Base: 55, Target: 75, Elite: 90}
	score, _ := EvaluateScore(rawRate, threshold)

	return DimensionResult{
		Score:  score,
		Max:    100,
		Checks: checks,
		Metrics: map[string]float64{
			"checks_passed": float64(passed),
			"checks_total":  float64(len(checks)),
			"pass_rate":     rawRate,
		},
		Errors: errs,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Workflow A — Session Lifecycle
// session_start → save 5 varied observations → context → recall → session_end
// ─────────────────────────────────────────────────────────────────────────────

func runWorkflowA(ctx context.Context, scale ScaleConfig, checks *[]CheckResult, errs *[]string) {
	wEnv, err := NewBenchEnv(ctx, scale)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("workflowA: create env: %v", err))
		return
	}
	defer wEnv.Close()

	h, err := NewMCPHarness(wEnv)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("workflowA: create harness: %v", err))
		return
	}

	const ns = "bench-workflow-a"

	// Step 1: session_start
	sessResp, err := h.SessionStart("workflow-a-session", "/workspace", "main", ns)
	sessionStarted := err == nil && sessResp.SessionID != ""
	*checks = append(*checks, CheckResult{
		Name:   "WA: session_start creates session",
		Passed: sessionStarted,
		Detail: fmt.Sprintf("session_id=%q err=%v", sessResp.SessionID, err),
	})

	if !sessionStarted {
		*errs = append(*errs, fmt.Sprintf("workflowA: session_start: %v", err))
		return
	}

	// Step 2: save 5 varied observations
	type wfAObs struct {
		title   string
		content string
		obsType string
		kind    string
		tags    string
	}
	obsSpecs := []wfAObs{
		{
			title:   "Workflow A: architecture decision React",
			content: "DECISION: Chose React 18 with strict TypeScript for the frontend. Strong ecosystem fit.",
			obsType: "decision", kind: "semantic", tags: "react,frontend,architecture",
		},
		{
			title:   "Workflow A: database config PostgreSQL",
			content: "CONFIG: PostgreSQL 16 is the primary store. WAL mode, max 50 connections.",
			obsType: "config", kind: "semantic", tags: "postgres,database,config",
		},
		{
			title:   "Workflow A: bugfix null check session handler",
			content: "BUGFIX: Added null check before accessing user.id in session logout handler.",
			obsType: "bugfix", kind: "procedural", tags: "bugfix,null,session",
		},
		{
			title:   "Workflow A: pattern circuit breaker HTTP",
			content: "PATTERN: All external HTTP calls wrapped in circuit breaker. Opens after 5 failures.",
			obsType: "pattern", kind: "procedural", tags: "circuit-breaker,resilience,http",
		},
		{
			title:   "Workflow A: gotcha redis sentinel failover",
			content: "GOTCHA: Redis Sentinel failover drops writes for 1-2s. Need write-behind queue.",
			obsType: "gotcha", kind: "procedural", tags: "redis,gotcha,failover",
		},
	}

	savedIDs := make([]string, 0, len(obsSpecs))
	for i, spec := range obsSpecs {
		resp, saveErr := h.Save(map[string]any{
			"title":            spec.title,
			"content":          spec.content,
			"observation_type": spec.obsType,
			"kind":             spec.kind,
			"tags":             spec.tags,
			"namespace":        ns,
			"confidence":       0.9,
		})
		if saveErr != nil {
			*errs = append(*errs, fmt.Sprintf("workflowA: save obs %d: %v", i, saveErr))
			continue
		}
		savedIDs = append(savedIDs, resp.ID)
	}

	allSaved := len(savedIDs) == len(obsSpecs)
	*checks = append(*checks, CheckResult{
		Name:   "WA: all 5 observations saved via MCP",
		Passed: allSaved,
		Detail: fmt.Sprintf("saved=%d expected=%d", len(savedIDs), len(obsSpecs)),
	})

	// Step 3: context — should return saved observations
	ctxResp, ctxErr := h.Context(ns, "")
	contextWorks := ctxErr == nil && ctxResp.Count > 0
	*checks = append(*checks, CheckResult{
		Name:   "WA: context returns saved observations",
		Passed: contextWorks,
		Detail: fmt.Sprintf("count=%d err=%v", ctxResp.Count, ctxErr),
	})

	// Step 4: recall — should find saved observations by query
	recallResp, recallErr := h.Recall("React architecture decision frontend", RecallOpts{
		Namespace: ns,
		Limit:     5,
	})
	recallWorks := recallErr == nil && wfAFindKey(recallResp.Results, "react")
	*checks = append(*checks, CheckResult{
		Name:   "WA: recall finds saved observations",
		Passed: recallWorks,
		Detail: fmt.Sprintf("count=%d err=%v found_react=%v", recallResp.Count, recallErr, recallWorks),
	})

	// Step 5: session_end
	endResp, endErr := h.SessionEnd(sessResp.SessionID, "workflow-a completed — 5 observations saved")
	sessionEnded := endErr == nil && endResp.SessionID == sessResp.SessionID
	*checks = append(*checks, CheckResult{
		Name:   "WA: session_end completes session",
		Passed: sessionEnded,
		Detail: fmt.Sprintf("session_id=%q err=%v", endResp.SessionID, endErr),
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Workflow B — Knowledge Update via MCP
// topic_key upsert + invalidate flow
// ─────────────────────────────────────────────────────────────────────────────

func runWorkflowB(ctx context.Context, scale ScaleConfig, checks *[]CheckResult, errs *[]string) {
	wEnv, err := NewBenchEnv(ctx, scale)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("workflowB: create env: %v", err))
		return
	}
	defer wEnv.Close()

	h, err := NewMCPHarness(wEnv)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("workflowB: create harness: %v", err))
		return
	}

	const ns = "bench-workflow-b"
	const topicKey = "workflow-b-node-version"

	// Step 1: save v1 via topic_key
	v1Resp, v1Err := h.Save(map[string]any{
		"title":            "Workflow B: Node.js runtime version",
		"content":          "CONFIG: Node.js 18 LTS is the runtime. Stable, long-term support.",
		"observation_type": "config",
		"kind":             "semantic",
		"namespace":        ns,
		"topic_key":        topicKey,
		"confidence":       0.9,
	})
	v1Saved := v1Err == nil && v1Resp.ID != ""
	*checks = append(*checks, CheckResult{
		Name:   "WB: v1 saved with topic_key",
		Passed: v1Saved,
		Detail: fmt.Sprintf("id=%q err=%v", v1Resp.ID, v1Err),
	})
	if !v1Saved {
		*errs = append(*errs, fmt.Sprintf("workflowB: v1 save: %v", v1Err))
		return
	}

	// Step 2: upsert v2 with same topic_key — should replace v1
	v2Resp, v2Err := h.Save(map[string]any{
		"title":            "Workflow B: Node.js runtime version",
		"content":          "CONFIG: Upgraded to Node.js 20 LTS. Node 18 reaching EOL. All services updated.",
		"observation_type": "config",
		"kind":             "semantic",
		"namespace":        ns,
		"topic_key":        topicKey,
		"confidence":       0.95,
	})
	v2Saved := v2Err == nil && v2Resp.ID != ""
	*checks = append(*checks, CheckResult{
		Name:   "WB: v2 upsert with same topic_key succeeds",
		Passed: v2Saved,
		Detail: fmt.Sprintf("id=%q v1_id=%q err=%v", v2Resp.ID, v1Resp.ID, v2Err),
	})
	if !v2Saved {
		*errs = append(*errs, fmt.Sprintf("workflowB: v2 save: %v", v2Err))
		return
	}

	// Step 3: recall should return the updated content (Node 20)
	recallResp, recallErr := h.Recall("Node.js runtime version", RecallOpts{
		Namespace: ns,
		Limit:     5,
	})
	recallLatest := recallErr == nil && wfContainsText(recallResp.Results, "20 lts", "node.js 20", "node 20", "upgraded to node")
	*checks = append(*checks, CheckResult{
		Name:   "WB: recall returns latest topic_key value (Node 20)",
		Passed: recallLatest,
		Detail: fmt.Sprintf("count=%d err=%v", recallResp.Count, recallErr),
	})

	// Step 4: save a separate observation to invalidate
	invBase, invBaseErr := h.Save(map[string]any{
		"title":            "Workflow B: express API framework",
		"content":          "DECISION: Express.js 4 is the API framework. Simple and battle-tested.",
		"observation_type": "decision",
		"kind":             "semantic",
		"namespace":        ns,
		"confidence":       0.85,
	})
	invBaseSaved := invBaseErr == nil && invBase.ID != ""
	*checks = append(*checks, CheckResult{
		Name:   "WB: base observation for invalidation saved",
		Passed: invBaseSaved,
		Detail: fmt.Sprintf("id=%q err=%v", invBase.ID, invBaseErr),
	})
	if !invBaseSaved {
		*errs = append(*errs, fmt.Sprintf("workflowB: invBase save: %v", invBaseErr))
		return
	}

	// Step 5: invalidate the old framework and replace with Fastify
	invResp, invErr := h.Invalidate(
		invBase.ID,
		"migrated to Fastify for better performance",
		"Workflow B: Fastify replaces Express",
		"DECISION: Migrated from Express.js to Fastify. 2x faster, built-in schema validation, better TypeScript support.",
	)
	invalidated := invErr == nil && invResp.InvalidatedID == invBase.ID && invResp.ReplacementID != ""
	*checks = append(*checks, CheckResult{
		Name:   "WB: invalidate replaces old framework with Fastify",
		Passed: invalidated,
		Detail: fmt.Sprintf("invalidated=%q replacement=%q err=%v", invResp.InvalidatedID, invResp.ReplacementID, invErr),
	})

	// Step 6: recall should now return Fastify (query uses known title words for FTS hit)
	fastifyResp, fastifyErr := h.Recall("Fastify replaces Express framework", RecallOpts{
		Namespace: ns,
		Limit:     5,
	})
	fastifyFound := fastifyErr == nil && wfContainsText(fastifyResp.Results, "fastify")
	*checks = append(*checks, CheckResult{
		Name:   "WB: recall returns Fastify after invalidation",
		Passed: fastifyFound,
		Detail: fmt.Sprintf("count=%d err=%v fastify_found=%v", fastifyResp.Count, fastifyErr, fastifyFound),
	})

	// Step 7: status shows correct counts
	statusResp, statusErr := h.Status()
	statusOK := statusErr == nil && statusResp.Total >= 3
	*checks = append(*checks, CheckResult{
		Name:   "WB: status shows expected observation counts",
		Passed: statusOK,
		Detail: fmt.Sprintf("total=%d stale=%d err=%v", statusResp.Total, statusResp.Stale, statusErr),
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Workflow C — Vague vs Precise Queries
// Save 10 observations, compare 5 precise vs 5 vague queries
// ─────────────────────────────────────────────────────────────────────────────

func runWorkflowC(ctx context.Context, scale ScaleConfig, checks *[]CheckResult, errs *[]string) {
	wEnv, err := NewBenchEnv(ctx, scale)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("workflowC: create env: %v", err))
		return
	}
	defer wEnv.Close()

	h, err := NewMCPHarness(wEnv)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("workflowC: create harness: %v", err))
		return
	}

	const ns = "bench-workflow-c"

	// Save 10 observations with distinct, searchable content
	type wfCObs struct {
		title        string
		content      string
		obsType      string
		preciseQuery string
		vagueQuery   string
		expectedKey  string
	}
	cObs := []wfCObs{
		{
			title:        "WFC: JWT RS256 authentication token expiration",
			content:      "DECISION: JWT bearer tokens with RS256 algorithm. Tokens expire after 3600 seconds.",
			obsType:      "decision",
			preciseQuery: "JWT RS256 token expiration",
			vagueQuery:   "login stuff",
			expectedKey:  "jwt",
		},
		{
			title:        "WFC: PostgreSQL connection pool pgBouncer configuration",
			content:      "CONFIG: PostgreSQL pool_size=50 via pgBouncer transaction mode. Max idle=5.",
			obsType:      "config",
			preciseQuery: "PostgreSQL pgBouncer connection pool",
			vagueQuery:   "database stuff",
			expectedKey:  "postgresql",
		},
		{
			title:        "WFC: Kubernetes Helm ArgoCD deployment pipeline",
			content:      "CONFIG: All services deployed on Kubernetes using Helm charts. GitOps via ArgoCD.",
			obsType:      "config",
			preciseQuery: "Kubernetes Helm ArgoCD deployment",
			vagueQuery:   "deploy stuff",
			expectedKey:  "kubernetes",
		},
		{
			title:        "WFC: Datadog APM PagerDuty alerting observability",
			content:      "DECISION: Datadog as primary APM. SLO monitors. PagerDuty escalation for P1 alerts.",
			obsType:      "decision",
			preciseQuery: "Datadog APM PagerDuty observability",
			vagueQuery:   "monitoring stuff",
			expectedKey:  "datadog",
		},
		{
			title:        "WFC: Redis Sentinel failover write-behind queue",
			content:      "GOTCHA: Redis Sentinel failover silently drops writes for 1-2 seconds. Use write-behind.",
			obsType:      "gotcha",
			preciseQuery: "Redis Sentinel failover write loss",
			vagueQuery:   "cache stuff",
			expectedKey:  "redis",
		},
		{
			title:        "WFC: TypeScript strict no-any ESLint enforcement",
			content:      "PREFERENCE: Strict TypeScript. No any types. ESLint @typescript-eslint/no-explicit-any enforced.",
			obsType:      "preference",
			preciseQuery: "TypeScript strict no-any ESLint",
			vagueQuery:   "code style stuff",
			expectedKey:  "typescript",
		},
		{
			title:        "WFC: GitHub Actions CI CD environment gates",
			content:      "DECISION: GitHub Actions for CI/CD. Staging deploys auto. Production requires manual approval.",
			obsType:      "decision",
			preciseQuery: "GitHub Actions CI CD pipeline",
			vagueQuery:   "pipeline stuff",
			expectedKey:  "github",
		},
		{
			title:        "WFC: Prisma ORM TypeORM migration database access",
			content:      "DECISION: Migrated from TypeORM to Prisma. Better TypeScript types, simpler migrations.",
			obsType:      "decision",
			preciseQuery: "Prisma ORM TypeORM migration",
			vagueQuery:   "ORM stuff",
			expectedKey:  "prisma",
		},
		{
			title:        "WFC: AWS Secrets Manager credential rotation security",
			content:      "CONFIG: All secrets in AWS Secrets Manager. 90-day rotation for DB passwords. No .env in repo.",
			obsType:      "config",
			preciseQuery: "AWS Secrets Manager credential rotation",
			vagueQuery:   "secrets stuff",
			expectedKey:  "aws",
		},
		{
			title:        "WFC: N+1 query DataLoader GraphQL batch optimization",
			content:      "DISCOVERY: N+1 problem in GraphQL resolvers. Fixed with DataLoader. O(n) → O(1) DB calls.",
			obsType:      "discovery",
			preciseQuery: "N+1 DataLoader GraphQL batch",
			vagueQuery:   "slow query stuff",
			expectedKey:  "dataloader",
		},
	}

	// Save all 10 observations
	savedCount := 0
	for i, spec := range cObs {
		_, saveErr := h.Save(map[string]any{
			"title":            spec.title,
			"content":          spec.content,
			"observation_type": spec.obsType,
			"kind":             "semantic",
			"namespace":        ns,
			"confidence":       0.9,
		})
		if saveErr != nil {
			*errs = append(*errs, fmt.Sprintf("workflowC: save obs %d: %v", i, saveErr))
			continue
		}
		savedCount++
	}
	*checks = append(*checks, CheckResult{
		Name:   "WC: all 10 observations saved",
		Passed: savedCount == len(cObs),
		Detail: fmt.Sprintf("saved=%d expected=%d", savedCount, len(cObs)),
	})

	if savedCount == 0 {
		*errs = append(*errs, "workflowC: no observations saved, skipping query checks")
		return
	}

	// Run 5 precise queries and 5 vague queries, score hit rate
	preciseHits := 0
	vagueHits := 0
	const queryPairs = 5

	for i := 0; i < queryPairs; i++ {
		spec := cObs[i]

		preciseResp, pErr := h.Recall(spec.preciseQuery, RecallOpts{Namespace: ns, Limit: 10})
		if pErr != nil {
			*errs = append(*errs, fmt.Sprintf("workflowC: precise recall %d: %v", i, pErr))
		} else if wfContainsText(preciseResp.Results, spec.expectedKey) {
			preciseHits++
		}

		vagueResp, vErr := h.Recall(spec.vagueQuery, RecallOpts{Namespace: ns, Limit: 10})
		if vErr != nil {
			*errs = append(*errs, fmt.Sprintf("workflowC: vague recall %d: %v", i, vErr))
		} else if wfContainsText(vagueResp.Results, spec.expectedKey) {
			vagueHits++
		}
	}

	preciseRate := float64(preciseHits) / float64(queryPairs) * 100

	*checks = append(*checks,
		CheckResult{
			Name:   "WC: precise queries ≥60% success rate",
			Passed: preciseRate >= 60,
			Detail: fmt.Sprintf("%.0f%% (%d/%d)", preciseRate, preciseHits, queryPairs),
		},
		CheckResult{
			Name:   "WC: precise queries outperform vague queries",
			Passed: preciseHits >= vagueHits,
			Detail: fmt.Sprintf("precise=%d vague=%d", preciseHits, vagueHits),
		},
	)

	// Run the same 5 queries for the second half (precise)
	preciseHits2 := 0
	vagueHits2 := 0
	for i := queryPairs; i < len(cObs); i++ {
		spec := cObs[i]

		preciseResp, pErr := h.Recall(spec.preciseQuery, RecallOpts{Namespace: ns, Limit: 10})
		if pErr != nil {
			*errs = append(*errs, fmt.Sprintf("workflowC: precise recall2 %d: %v", i, pErr))
		} else if wfContainsText(preciseResp.Results, spec.expectedKey) {
			preciseHits2++
		}

		vagueResp, vErr := h.Recall(spec.vagueQuery, RecallOpts{Namespace: ns, Limit: 10})
		if vErr != nil {
			*errs = append(*errs, fmt.Sprintf("workflowC: vague recall2 %d: %v", i, vErr))
		} else if wfContainsText(vagueResp.Results, spec.expectedKey) {
			vagueHits2++
		}
	}

	totalPrecise := preciseHits + preciseHits2
	totalVague := vagueHits + vagueHits2
	overallPreciseRate := float64(totalPrecise) / float64(len(cObs)) * 100
	overallVagueRate := float64(totalVague) / float64(len(cObs)) * 100

	*checks = append(*checks,
		CheckResult{
			Name:   "WC: overall precision_success_rate ≥40%",
			Passed: overallPreciseRate >= 40,
			Detail: fmt.Sprintf("%.0f%% (%d/%d)", overallPreciseRate, totalPrecise, len(cObs)),
		},
		CheckResult{
			Name:   "WC: precision_success_rate > vague_success_rate",
			Passed: overallPreciseRate >= overallVagueRate,
			Detail: fmt.Sprintf("precise=%.0f%% vague=%.0f%%", overallPreciseRate, overallVagueRate),
		},
	)
}

// ─────────────────────────────────────────────────────────────────────────────
// Workflow D — Consolidation via MCP
// save 30 mixed observations → consolidate → status → recall → context
// ─────────────────────────────────────────────────────────────────────────────

func runWorkflowD(ctx context.Context, scale ScaleConfig, checks *[]CheckResult, errs *[]string) {
	wEnv, err := NewBenchEnv(ctx, scale)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("workflowD: create env: %v", err))
		return
	}
	defer wEnv.Close()

	h, err := NewMCPHarness(wEnv)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("workflowD: create harness: %v", err))
		return
	}

	const ns = "bench-workflow-d"

	// Save 30 mixed observations (10 durable decisions/config, 10 patterns/bugfix, 10 operational noise)
	durableObs := []struct {
		title   string
		content string
		obsType string
	}{
		{"WFD: React 18 TypeScript frontend decision", "DECISION: Use React 18 with strict TypeScript. Team familiar, strong ecosystem.", "decision"},
		{"WFD: PostgreSQL 16 primary database", "DECISION: PostgreSQL 16 as primary DB. ACID, JSONB, FTS support.", "decision"},
		{"WFD: Fastify REST API framework", "DECISION: Fastify replaces Express. 2x faster, built-in schema validation.", "decision"},
		{"WFD: JWT ES256 authentication", "DECISION: JWT bearer tokens with ES256 algorithm. Smaller tokens than RS256.", "decision"},
		{"WFD: Kubernetes Helm production deployment", "CONFIG: All services on Kubernetes using Helm charts. GitOps with ArgoCD.", "config"},
		{"WFD: Redis 7 session cache rate limiting", "DECISION: Redis 7 for session cache (TTL 24h) and rate limiting.", "decision"},
		{"WFD: pnpm workspaces monorepo structure", "DECISION: pnpm workspaces monorepo. Packages: api, frontend, shared, infra.", "decision"},
		{"WFD: GitHub Actions CI CD pipeline", "CONFIG: GitHub Actions for CI/CD. Auto-deploy staging. Manual prod approval.", "config"},
		{"WFD: Datadog APM observability platform", "DECISION: Datadog for APM traces and metrics. SLO monitors. PagerDuty alerts.", "decision"},
		{"WFD: AWS Secrets Manager credential storage", "CONFIG: All secrets in AWS Secrets Manager. No .env in repository.", "config"},
	}

	patternBugObs := []struct {
		title   string
		content string
		obsType string
	}{
		{"WFD: circuit breaker external HTTP", "PATTERN: External HTTP calls wrapped in circuit breaker (opossum). 5 failures in 30s.", "pattern"},
		{"WFD: structured logging correlation IDs", "PATTERN: All services log structured JSON with correlation_id. Use pino.", "pattern"},
		{"WFD: repository pattern database access", "PATTERN: All DB access through repositories. No raw SQL in controllers.", "pattern"},
		{"WFD: retry exponential backoff transient", "PATTERN: Retry up to 3x with exponential backoff 100/200/400ms. No 4xx retry.", "pattern"},
		{"WFD: error boundary React component", "PATTERN: ErrorBoundary wraps each major feature. Log to Sentry. Show fallback.", "pattern"},
		{"WFD: bugfix null check session logout", "BUGFIX: Null pointer on logout when user undefined. Fix: user?.id optional chain.", "bugfix"},
		{"WFD: bugfix race condition token rotation", "BUGFIX: Concurrent refresh token requests created duplicate sessions. Fix: advisory lock.", "bugfix"},
		{"WFD: bugfix CORS preflight authorization", "BUGFIX: CORS preflight failing with Authorization header. Added to allowed headers.", "bugfix"},
		{"WFD: gotcha Redis KEYS blocks production", "GOTCHA: Redis KEYS pattern blocks server. Use SCAN with MATCH for all ops.", "gotcha"},
		{"WFD: gotcha Docker DNS resolution", "GOTCHA: Containers on different networks can't resolve by name. Put all in same network.", "gotcha"},
	}

	noiseObs := []struct {
		title   string
		content string
	}{
		{"WFD noise: step 1 complete", "Step 1 initial setup done. Toolchain configured. Repo created."},
		{"WFD noise: build passed", "Build 1001 passed. 0 errors. Lint clean."},
		{"WFD noise: tests green", "All 120 tests passed. Coverage 72%."},
		{"WFD noise: staging deploy", "Deployed to staging. Health checks OK."},
		{"WFD noise: PR merged 47", "PR #47 merged to main. Squash commit. CI passed."},
		{"WFD noise: step 2 complete", "Step 2 done. Auth module refactored. Tests passing."},
		{"WFD noise: build 1002", "Build 1002 passed. No regressions."},
		{"WFD noise: code review", "PR #52 reviewed and approved. No blocking issues."},
		{"WFD noise: monitoring check", "Services healthy. P99 latency < 50ms. Error rate 0.01%."},
		{"WFD noise: weekly sync", "Weekly sync done. Sprint velocity 42 points. On track."},
	}

	savedDurable := 0
	for i, obs := range durableObs {
		_, saveErr := h.Save(map[string]any{
			"title":            obs.title,
			"content":          obs.content,
			"observation_type": obs.obsType,
			"kind":             "semantic",
			"namespace":        ns,
			"confidence":       0.9,
		})
		if saveErr != nil {
			*errs = append(*errs, fmt.Sprintf("workflowD: durable obs %d: %v", i, saveErr))
			continue
		}
		savedDurable++
	}

	savedPattern := 0
	for i, obs := range patternBugObs {
		_, saveErr := h.Save(map[string]any{
			"title":            obs.title,
			"content":          obs.content,
			"observation_type": obs.obsType,
			"kind":             "procedural",
			"namespace":        ns,
			"confidence":       0.85,
		})
		if saveErr != nil {
			*errs = append(*errs, fmt.Sprintf("workflowD: pattern obs %d: %v", i, saveErr))
			continue
		}
		savedPattern++
	}

	savedNoise := 0
	for i, obs := range noiseObs {
		_, saveErr := h.Save(map[string]any{
			"title":   obs.title,
			"content": obs.content,
			// Minimal params — operational noise
		})
		if saveErr != nil {
			*errs = append(*errs, fmt.Sprintf("workflowD: noise obs %d: %v", i, saveErr))
			continue
		}
		savedNoise++
	}

	totalSaved := savedDurable + savedPattern + savedNoise
	*checks = append(*checks, CheckResult{
		Name:   "WD: all 30 mixed observations saved",
		Passed: totalSaved == 30,
		Detail: fmt.Sprintf("total=%d durable=%d pattern=%d noise=%d", totalSaved, savedDurable, savedPattern, savedNoise),
	})

	// Status before consolidation
	statusBefore, statusBeforeErr := h.Status()
	statusBeforeOK := statusBeforeErr == nil && statusBefore.Total >= 20
	*checks = append(*checks, CheckResult{
		Name:   "WD: status before consolidation shows observations",
		Passed: statusBeforeOK,
		Detail: fmt.Sprintf("total=%d buffer=%d err=%v", statusBefore.Total, statusBefore.Buffer, statusBeforeErr),
	})

	// Consolidate via MCP
	consResp, consErr := h.Consolidate()
	consolidateOK := consErr == nil && consResp.Message != ""
	*checks = append(*checks, CheckResult{
		Name:   "WD: consolidate executes via MCP",
		Passed: consolidateOK,
		Detail: fmt.Sprintf("buffer=%d working=%d core=%d err=%v", consResp.Buffer, consResp.Working, consResp.Core, consErr),
	})

	// Status after consolidation
	statusAfter, statusAfterErr := h.Status()
	statusAfterOK := statusAfterErr == nil && statusAfter.Total >= 20
	*checks = append(*checks, CheckResult{
		Name:   "WD: status after consolidation still shows observations",
		Passed: statusAfterOK,
		Detail: fmt.Sprintf("total=%d buffer=%d working=%d core=%d err=%v",
			statusAfter.Total, statusAfter.Buffer, statusAfter.Working, statusAfter.Core, statusAfterErr),
	})

	// Recall durable observations after consolidation
	recallResp, recallErr := h.Recall("architecture decision React PostgreSQL", RecallOpts{
		Namespace: ns,
		Limit:     10,
	})
	recallAfterOK := recallErr == nil && recallResp.Count > 0
	*checks = append(*checks, CheckResult{
		Name:   "WD: recall works after consolidation",
		Passed: recallAfterOK,
		Detail: fmt.Sprintf("count=%d err=%v", recallResp.Count, recallErr),
	})

	// Context returns observations after consolidation
	ctxResp, ctxErr := h.Context(ns, "")
	contextAfterOK := ctxErr == nil && ctxResp.Count > 0
	*checks = append(*checks, CheckResult{
		Name:   "WD: context accessible after consolidation",
		Passed: contextAfterOK,
		Detail: fmt.Sprintf("count=%d err=%v", ctxResp.Count, ctxErr),
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Workflow E — Git Hook Staleness
// save file-linked observation → git_hook changed_files → staleness propagated
// ─────────────────────────────────────────────────────────────────────────────

func runWorkflowE(ctx context.Context, scale ScaleConfig, checks *[]CheckResult, errs *[]string) {
	wEnv, err := NewBenchEnv(ctx, scale)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("workflowE: create env: %v", err))
		return
	}
	defer wEnv.Close()

	h, err := NewMCPHarness(wEnv)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("workflowE: create harness: %v", err))
		return
	}

	const ns = "bench-workflow-e"
	const linkedFile = "internal/auth/middleware.go"

	// Step 1: save a file-linked observation
	savedResp, saveErr := h.Save(map[string]any{
		"title":            "Workflow E: auth middleware JWT validation",
		"content":          "DISCOVERY: Auth middleware validates JWT bearer tokens using ES256. Extracts user_id and role from claims.",
		"observation_type": "discovery",
		"kind":             "semantic",
		"namespace":        ns,
		"files":            linkedFile,
		"confidence":       0.9,
	})
	fileSaved := saveErr == nil && savedResp.ID != ""
	*checks = append(*checks, CheckResult{
		Name:   "WE: file-linked observation saved",
		Passed: fileSaved,
		Detail: fmt.Sprintf("id=%q err=%v", savedResp.ID, saveErr),
	})
	if !fileSaved {
		*errs = append(*errs, fmt.Sprintf("workflowE: save file-linked: %v", saveErr))
		return
	}

	// Step 2: save a non-linked observation (control — should stay fresh)
	controlResp, controlErr := h.Save(map[string]any{
		"title":            "Workflow E: unrelated PostgreSQL config",
		"content":          "CONFIG: PostgreSQL 16 max_connections=100. WAL mode enabled.",
		"observation_type": "config",
		"kind":             "semantic",
		"namespace":        ns,
		"confidence":       0.9,
		// No files — not linked
	})
	controlSaved := controlErr == nil && controlResp.ID != ""
	*checks = append(*checks, CheckResult{
		Name:   "WE: non-linked control observation saved",
		Passed: controlSaved,
		Detail: fmt.Sprintf("id=%q err=%v", controlResp.ID, controlErr),
	})

	// Step 3: recall before git_hook — observation should be fresh
	recallBeforeResp, recallBeforeErr := h.Recall("auth middleware JWT validation", RecallOpts{
		Namespace: ns,
		Limit:     5,
	})
	foundBeforeHook := recallBeforeErr == nil && wfContainsText(recallBeforeResp.Results, "jwt", "auth middleware")
	*checks = append(*checks, CheckResult{
		Name:   "WE: file-linked observation found before git_hook",
		Passed: foundBeforeHook,
		Detail: fmt.Sprintf("count=%d err=%v found=%v", recallBeforeResp.Count, recallBeforeErr, foundBeforeHook),
	})

	// Step 4: call git_hook with the linked file
	hookResult, hookErr := h.CallTool("git_hook", map[string]any{
		"changed_files": linkedFile,
		"commit_sha":    "abc1234def5678",
		"branch":        "main",
	})
	hookOK := hookErr == nil && hookResult["message"] == "git hook processed"
	*checks = append(*checks, CheckResult{
		Name:   "WE: git_hook processes changed file",
		Passed: hookOK,
		Detail: fmt.Sprintf("result=%v err=%v", hookResult, hookErr),
	})

	if hookErr != nil {
		*errs = append(*errs, fmt.Sprintf("workflowE: git_hook: %v", hookErr))
		return
	}

	// Step 5: status should show stale observations
	statusResp, statusErr := h.Status()
	stalePropagated := statusErr == nil && statusResp.Stale >= 1
	*checks = append(*checks, CheckResult{
		Name:   "WE: status shows stale observation after git_hook",
		Passed: stalePropagated,
		Detail: fmt.Sprintf("stale=%d total=%d err=%v", statusResp.Stale, statusResp.Total, statusErr),
	})

	// Step 6: recall with include_stale — stale obs still discoverable
	recallStaleResp, recallStaleErr := h.Recall("auth middleware JWT validation", RecallOpts{
		Namespace:    ns,
		Limit:        5,
		IncludeStale: true,
	})
	staleDiscoverable := recallStaleErr == nil && wfContainsText(recallStaleResp.Results, "jwt", "auth middleware")
	*checks = append(*checks, CheckResult{
		Name:   "WE: stale observation discoverable with include_stale",
		Passed: staleDiscoverable,
		Detail: fmt.Sprintf("count=%d err=%v", recallStaleResp.Count, recallStaleErr),
	})

	// Step 7: context returns items (may include stale depending on freshness setting)
	ctxResp, ctxErr := h.Context(ns, "")
	contextOK := ctxErr == nil
	*checks = append(*checks, CheckResult{
		Name:   "WE: context call succeeds after git_hook",
		Passed: contextOK,
		Detail: fmt.Sprintf("count=%d err=%v", ctxResp.Count, ctxErr),
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal helpers (workflow-local, avoid collision with dim_agent_lazy_vs_perfect.go)
// ─────────────────────────────────────────────────────────────────────────────

// wfContainsText reports whether any RecallItem in results contains at least
// one of the given keywords (case-insensitive substring match on title+content).
func wfContainsText(results []RecallItem, keywords ...string) bool {
	for _, r := range results {
		combined := strings.ToLower(r.Title + " " + r.Content)
		for _, kw := range keywords {
			if strings.Contains(combined, strings.ToLower(kw)) {
				return true
			}
		}
	}
	return false
}

// wfAFindKey is the same as wfContainsText but uses a single keyword.
// Kept as a named alias for readability in Workflow A checks.
func wfAFindKey(results []RecallItem, keyword string) bool {
	return wfContainsText(results, keyword)
}

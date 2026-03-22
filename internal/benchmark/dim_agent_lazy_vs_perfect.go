package benchmark

import (
	"context"
	"fmt"
)

// AgentLazyVsPerfect benchmarks the quality impact of MCP save parameter
// richness. Two simulations save identical observations — one with minimal
// params (lazy agent), one with every field populated (perfect agent) — then
// 10 checkpoint queries compare recall quality and health scores.
//
// This answers: "Does Neurox reward agents that use it well?"
type AgentLazyVsPerfect struct{}

func (d AgentLazyVsPerfect) Name() string     { return "Lazy vs Perfect Agent" }
func (d AgentLazyVsPerfect) Category() string { return "agent" }

// agentObservation holds the content for one observation saved in both
// simulations.  lazyArgs contains only title+content; perfectArgs has every
// field populated.  query is used for the checkpoint recall; expectedKey is a
// short word that must appear in the expected observation's title.
type agentObservation struct {
	label       string
	query       string
	expectedKey string
	// Lazy save args: title + content only.
	lazyTitle   string
	lazyContent string
	// Perfect save enrichment fields (namespace handled separately).
	perfectTitle   string
	perfectContent string
	obsType        string
	kind           string
	tags           string
	files          string
	confidence     float64
}

// agentObservations returns 20 observations covering common agent-use scenarios.
func agentObservations() []agentObservation {
	return []agentObservation{
		{
			label:          "auth-bug",
			query:          "JWT authentication bug",
			expectedKey:    "JWT",
			lazyTitle:      "Fixed auth bug",
			lazyContent:    "The JWT token was expiring too early because timezone offset wasn't accounted for",
			perfectTitle:   "Fixed JWT token early expiration bug",
			perfectContent: "What: JWT token was expiring too early. Why: Timezone offset wasn't accounted for in exp claim calculation. Where: api/middleware/auth.ts. Learned: Always use UTC for JWT timestamps, never local time.",
			obsType:        "bugfix",
			kind:           "procedural",
			tags:           "jwt,auth,bugfix,timezone",
			files:          "api/middleware/auth.ts",
			confidence:     0.9,
		},
		{
			label:          "user-pref-tabs",
			query:          "user preferences code style",
			expectedKey:    "spaces",
			lazyTitle:      "Code style preference",
			lazyContent:    "Use 2 spaces for indentation, not tabs. This is consistent with the existing codebase.",
			perfectTitle:   "Preference: 2 spaces indentation",
			perfectContent: "PREFERENCE: Use 2 spaces for indentation throughout the codebase. Never use tabs. This is enforced by ESLint and Prettier config.",
			obsType:        "preference",
			kind:           "semantic",
			tags:           "style,indentation,eslint,prettier",
			files:          ".eslintrc.js",
			confidence:     0.95,
		},
		{
			label:          "db-config",
			query:          "database configuration PostgreSQL",
			expectedKey:    "PostgreSQL",
			lazyTitle:      "Database config",
			lazyContent:    "PostgreSQL 16, connection pool size 50, pgBouncer in transaction mode",
			perfectTitle:   "Config: PostgreSQL 16 with pgBouncer",
			perfectContent: "CONFIG: PostgreSQL 16 as primary database. pgBouncer in transaction mode, pool_size=50. Max idle connections per pod: 5. Prevents pool exhaustion under load.",
			obsType:        "config",
			kind:           "semantic",
			tags:           "postgres,database,pgbouncer,config",
			files:          "infra/docker-compose.yml",
			confidence:     0.9,
		},
		{
			label:          "arch-microservices",
			query:          "architecture decisions microservices",
			expectedKey:    "microservices",
			lazyTitle:      "Architecture choice",
			lazyContent:    "We are using a microservices architecture with event sourcing for the order processing domain",
			perfectTitle:   "Decision: microservices with event sourcing",
			perfectContent: "DECISION: Adopt microservices architecture with event sourcing for order processing. Events stored in Postgres, projected to read models. Domain: order, payment, inventory services.",
			obsType:        "decision",
			kind:           "semantic",
			tags:           "architecture,microservices,event-sourcing",
			files:          "docs/architecture.md",
			confidence:     0.95,
		},
		{
			label:          "redis-gotcha",
			query:          "Redis gotcha failover",
			expectedKey:    "Redis",
			lazyTitle:      "Redis issue found",
			lazyContent:    "During Redis Sentinel failover, writes are silently dropped for 1-2 seconds",
			perfectTitle:   "Gotcha: Redis cluster failover loses writes",
			perfectContent: "GOTCHA: During Redis Sentinel failover, writes are silently dropped for 1-2 seconds. Must implement write-behind queue for critical operations. Affects: payment confirmations, order status updates.",
			obsType:        "gotcha",
			kind:           "procedural",
			tags:           "redis,gotcha,failover,data-loss",
			files:          "services/payment/queue.ts",
			confidence:     0.9,
		},
		{
			label:          "logging-pattern",
			query:          "logging pattern structured",
			expectedKey:    "logging",
			lazyTitle:      "Logging convention",
			lazyContent:    "All services must use structured JSON logging with correlation-id header propagated across service calls",
			perfectTitle:   "Pattern: structured logging with correlation IDs",
			perfectContent: "PATTERN: All services use structured JSON logging. Correlation-id header must be propagated. Use pino for Node.js services. Fields: timestamp, level, service, correlationId, message.",
			obsType:        "pattern",
			kind:           "procedural",
			tags:           "logging,pattern,pino,correlation",
			files:          "packages/logger/index.ts",
			confidence:     0.9,
		},
		{
			label:          "deploy-config",
			query:          "deployment Kubernetes configuration",
			expectedKey:    "Kubernetes",
			lazyTitle:      "Kubernetes deployment",
			lazyContent:    "All services deployed on Kubernetes with Helm charts. GitOps with ArgoCD.",
			perfectTitle:   "Config: Kubernetes with Helm and ArgoCD",
			perfectContent: "CONFIG: All services deployed on Kubernetes via Helm charts. GitOps with ArgoCD. Namespace per environment: dev, staging, prod. Resource limits enforced via LimitRange.",
			obsType:        "config",
			kind:           "semantic",
			tags:           "kubernetes,helm,argocd,deployment",
			files:          "infra/helm/values.yaml",
			confidence:     0.85,
		},
		{
			label:          "cicd-decision",
			query:          "CI CD pipeline GitHub Actions",
			expectedKey:    "GitHub",
			lazyTitle:      "CI/CD setup",
			lazyContent:    "GitHub Actions for CI/CD. Push to main triggers staging deploy automatically. Production requires manual approval.",
			perfectTitle:   "Decision: GitHub Actions CI/CD with environment gates",
			perfectContent: "DECISION: GitHub Actions for CI/CD. Push to main triggers staging deploy automatically. Production deploys require manual approval via environment protection rules. Rollback: kubectl rollout undo.",
			obsType:        "decision",
			kind:           "semantic",
			tags:           "ci,cd,github-actions,deployment",
			files:          ".github/workflows/deploy.yml",
			confidence:     0.9,
		},
		{
			label:          "ts-strict",
			query:          "TypeScript coding style strict",
			expectedKey:    "TypeScript",
			lazyTitle:      "TypeScript preference",
			lazyContent:    "Always use strict TypeScript. No any types. Explicit return types on public functions.",
			perfectTitle:   "Preference: strict TypeScript with no-any rule",
			perfectContent: "PREFERENCE: Always use strict TypeScript (tsconfig strict:true). No `any` types — use `unknown` + narrowing. Explicit return types on all public functions. Enforced via ESLint @typescript-eslint/no-explicit-any.",
			obsType:        "preference",
			kind:           "procedural",
			tags:           "typescript,strict,style,eslint",
			files:          "tsconfig.json",
			confidence:     0.95,
		},
		{
			label:          "null-bugfix",
			query:          "null pointer bug session handler",
			expectedKey:    "null",
			lazyTitle:      "Null check bug fixed",
			lazyContent:    "Fixed null pointer exception in session handler when user object was undefined on logout",
			perfectTitle:   "Bugfix: null pointer in session logout handler",
			perfectContent: "What: Null pointer exception when user object was undefined on logout. Why: Missing null check before accessing user.id. Where: api/handlers/session.ts. Fix: Added optional chaining user?.id.",
			obsType:        "bugfix",
			kind:           "procedural",
			tags:           "bugfix,null,session,typescript",
			files:          "api/handlers/session.ts",
			confidence:     0.85,
		},
		{
			label:          "rate-limiting",
			query:          "rate limiting API requests",
			expectedKey:    "rate",
			lazyTitle:      "API rate limiting",
			lazyContent:    "API rate limited to 500 requests per minute per user token",
			perfectTitle:   "Config: API rate limiting per user token",
			perfectContent: "CONFIG: Rate limit set to 500 requests/minute per user token. Enforced via Redis sliding window. Headers: X-RateLimit-Limit, X-RateLimit-Remaining, X-RateLimit-Reset.",
			obsType:        "config",
			kind:           "semantic",
			tags:           "rate-limiting,redis,api,config",
			files:          "api/middleware/ratelimit.ts",
			confidence:     0.85,
		},
		{
			label:          "circuit-breaker",
			query:          "circuit breaker external API calls pattern",
			expectedKey:    "circuit",
			lazyTitle:      "Circuit breaker pattern",
			lazyContent:    "All external HTTP calls wrapped in circuit breaker. Open after 5 failures in 30s.",
			perfectTitle:   "Pattern: circuit breaker for external calls",
			perfectContent: "PATTERN: All external HTTP calls wrapped in circuit breaker (opossum lib). Open after 5 failures in 30s. Half-open test every 10s. Prevents cascade failures. Apply to: payment gateway, email service, SMS provider.",
			obsType:        "pattern",
			kind:           "procedural",
			tags:           "circuit-breaker,resilience,pattern,opossum",
			files:          "packages/http-client/index.ts",
			confidence:     0.9,
		},
		{
			label:          "orm-decision",
			query:          "ORM database access Prisma",
			expectedKey:    "Prisma",
			lazyTitle:      "ORM choice",
			lazyContent:    "Using Prisma ORM for all database access. TypeORM was removed.",
			perfectTitle:   "Decision: Prisma ORM replaces TypeORM",
			perfectContent: "DECISION: Migrated to Prisma ORM for all database access. TypeORM removed. Reasons: better TypeScript integration, auto-generated types, simpler migrations. Schema in prisma/schema.prisma.",
			obsType:        "decision",
			kind:           "semantic",
			tags:           "prisma,orm,database,typescript",
			files:          "prisma/schema.prisma",
			confidence:     0.9,
		},
		{
			label:          "secret-mgmt",
			query:          "secrets management credentials",
			expectedKey:    "Secrets",
			lazyTitle:      "Secret storage",
			lazyContent:    "All secrets stored in AWS Secrets Manager. No .env files in repository.",
			perfectTitle:   "Config: AWS Secrets Manager for credentials",
			perfectContent: "CONFIG: All secrets stored in AWS Secrets Manager. No .env files in repository. Local dev: .env.local (gitignored). Rotation: 90-day automatic rotation for DB passwords.",
			obsType:        "config",
			kind:           "semantic",
			tags:           "secrets,aws,security,credentials",
			files:          "infra/secrets.tf",
			confidence:     0.95,
		},
		{
			label:          "monitoring",
			query:          "monitoring Datadog alerting observability",
			expectedKey:    "Datadog",
			lazyTitle:      "Monitoring platform",
			lazyContent:    "Using Datadog for APM and infrastructure monitoring with PagerDuty alerts",
			perfectTitle:   "Decision: Datadog APM with PagerDuty escalation",
			perfectContent: "DECISION: Datadog as primary observability platform. APM traces on all services. SLO monitors for p99 latency (<200ms) and error rate (<0.1%). Alerts route to PagerDuty: P1=immediate, P2=5min, P3=Slack.",
			obsType:        "decision",
			kind:           "semantic",
			tags:           "datadog,monitoring,pagerduty,apm,slo",
			files:          "infra/monitoring/datadog.tf",
			confidence:     0.9,
		},
		{
			label:          "cache-strategy",
			query:          "cache Redis invalidation strategy",
			expectedKey:    "cache",
			lazyTitle:      "Cache invalidation",
			lazyContent:    "Cache invalidation driven by domain events. Cache consumers purge affected keys on mutation.",
			perfectTitle:   "Decision: event-driven cache invalidation",
			perfectContent: "DECISION: Cache invalidation driven by domain events via EventBridge. On data mutation, downstream service publishes event. Cache consumers purge affected keys. Default TTL: 5 minutes. Eventual consistency accepted.",
			obsType:        "decision",
			kind:           "semantic",
			tags:           "cache,redis,invalidation,events",
			files:          "services/cache/invalidator.ts",
			confidence:     0.85,
		},
		{
			label:          "commit-convention",
			query:          "commit message convention format",
			expectedKey:    "commit",
			lazyTitle:      "Commit style",
			lazyContent:    "Use conventional commits format. Type(scope): description. Max 72 chars.",
			perfectTitle:   "Preference: conventional commits format",
			perfectContent: "PREFERENCE: All commits use Conventional Commits format: <type>(<scope>): <description>. Types: feat, fix, refactor, test, docs, chore. Max 72 chars. Enforced via commitlint in CI.",
			obsType:        "preference",
			kind:           "procedural",
			tags:           "git,commits,convention,commitlint",
			files:          ".commitlintrc.js",
			confidence:     0.9,
		},
		{
			label:          "e2e-testing",
			query:          "end-to-end testing Playwright",
			expectedKey:    "Playwright",
			lazyTitle:      "E2E test tool",
			lazyContent:    "Replaced Selenium with Playwright for end-to-end testing. Tests in tests/e2e/ directory.",
			perfectTitle:   "Decision: Playwright for E2E testing",
			perfectContent: "DECISION: Replaced Selenium WebDriver with Playwright for E2E testing. Reasons: better async support, faster execution, built-in network mocking. Tests in tests/e2e/. Run in CI on PR merge to main.",
			obsType:        "decision",
			kind:           "semantic",
			tags:           "testing,playwright,e2e,selenium",
			files:          "tests/e2e/playwright.config.ts",
			confidence:     0.85,
		},
		{
			label:          "n1-discovery",
			query:          "N+1 query performance discovery",
			expectedKey:    "N+1",
			lazyTitle:      "N+1 query problem",
			lazyContent:    "Found N+1 query problem in UserList component. DataLoader added to fix it.",
			perfectTitle:   "Discovery: N+1 query in UserList fixed with DataLoader",
			perfectContent: "DISCOVERY: N+1 query problem found in UserList GraphQL resolver. Each user triggered a separate DB call for their orders. Fix: Added DataLoader to batch order queries by userId. Impact: reduced DB calls from O(n) to O(1).",
			obsType:        "discovery",
			kind:           "procedural",
			tags:           "performance,n+1,dataloader,graphql",
			files:          "api/resolvers/user.ts",
			confidence:     0.9,
		},
		{
			label:          "docker-gotcha",
			query:          "Docker build cache invalidation gotcha",
			expectedKey:    "Docker",
			lazyTitle:      "Docker cache issue",
			lazyContent:    "Docker build cache is invalidated by ENV instructions. Put ENV after COPY to preserve layer caching.",
			perfectTitle:   "Gotcha: Docker ENV invalidates build cache",
			perfectContent: "GOTCHA: Docker build cache is invalidated by ENV instructions. If ENV is placed before COPY, any env change invalidates all subsequent layers. Fix: Place all COPY instructions before ENV. Impact: build times reduced from 4min to 45s.",
			obsType:        "gotcha",
			kind:           "procedural",
			tags:           "docker,cache,gotcha,build",
			files:          "Dockerfile",
			confidence:     0.9,
		},
	}
}

// checkpointQuery defines a recall query checkpoint run against both simulations.
type checkpointQuery struct {
	label       string
	query       string
	obsType     string // if non-empty, filter by observation type
	files       string // if non-empty, use Context() with this files list
	expectedKey string // word that should appear in the best result's title
}

// checkpointQueries returns 10 checkpoint queries.
func checkpointQueries() []checkpointQuery {
	return []checkpointQuery{
		{label: "Q1", query: "JWT authentication bug", expectedKey: "JWT"},
		{label: "Q2", query: "what bugs have we found", obsType: "bugfix", expectedKey: "bug"},
		{label: "Q3", query: "user preferences code style", obsType: "preference", expectedKey: "Preference"},
		{label: "Q4", query: "database configuration PostgreSQL", expectedKey: "PostgreSQL"},
		{label: "Q5", query: "architecture decisions microservices", obsType: "decision", expectedKey: "microservices"},
		{label: "Q6", query: "what gotchas should I know", obsType: "gotcha", expectedKey: "Gotcha"},
		{label: "Q7", query: "patterns and conventions logging", obsType: "pattern", expectedKey: "Pattern"},
		{label: "Q8", query: "TypeScript coding style", expectedKey: "TypeScript"},
		{label: "Q9", query: "deployment Kubernetes configuration", expectedKey: "Kubernetes"},
		// Q10 is the health_check comparison — handled separately.
	}
}

func (d AgentLazyVsPerfect) Run(ctx context.Context, env *BenchEnv) DimensionResult {
	var checks []CheckResult
	var errs []string

	// ──────────────────────────────────────────────────────────────────────────
	// Create two separate MCP harnesses so the two simulations are fully
	// isolated. Each harness wraps its own BenchEnv backed by a separate DB.
	// ──────────────────────────────────────────────────────────────────────────

	lazyEnv, err := NewBenchEnv(ctx, env.Scale)
	if err != nil {
		return DimensionResult{
			Score:  0,
			Max:    100,
			Errors: []string{fmt.Sprintf("create lazy env: %v", err)},
		}
	}
	defer lazyEnv.Close()

	perfectEnv, err := NewBenchEnv(ctx, env.Scale)
	if err != nil {
		lazyEnv.Close()
		return DimensionResult{
			Score:  0,
			Max:    100,
			Errors: []string{fmt.Sprintf("create perfect env: %v", err)},
		}
	}
	defer perfectEnv.Close()

	lazyH, err := NewMCPHarness(lazyEnv)
	if err != nil {
		return DimensionResult{
			Score:  0,
			Max:    100,
			Errors: []string{fmt.Sprintf("create lazy harness: %v", err)},
		}
	}

	perfectH, err := NewMCPHarness(perfectEnv)
	if err != nil {
		return DimensionResult{
			Score:  0,
			Max:    100,
			Errors: []string{fmt.Sprintf("create perfect harness: %v", err)},
		}
	}

	const (
		lazyNS    = "bench-lazy"
		perfectNS = "bench-perfect"
	)

	observations := agentObservations()
	lazyIDs := make(map[string]string, len(observations))    // label → saved ID
	perfectIDs := make(map[string]string, len(observations)) // label → saved ID

	// ──────────────────────────────────────────────────────────────────────────
	// Simulation A — Lazy Agent
	// Saves 20 observations with ONLY title + content (no namespace, type, etc.)
	// ──────────────────────────────────────────────────────────────────────────
	for _, obs := range observations {
		resp, saveErr := lazyH.Save(map[string]any{
			"title":   obs.lazyTitle,
			"content": obs.lazyContent,
			// No namespace, no type, no kind, no tags, no files.
		})
		if saveErr != nil {
			errs = append(errs, fmt.Sprintf("lazy save %s: %v", obs.label, saveErr))
			continue
		}
		lazyIDs[obs.label] = resp.ID
	}

	// ──────────────────────────────────────────────────────────────────────────
	// Simulation B — Perfect Agent
	// Saves the same 20 observations with every relevant field populated.
	// ──────────────────────────────────────────────────────────────────────────
	for _, obs := range observations {
		args := map[string]any{
			"title":            obs.perfectTitle,
			"content":          obs.perfectContent,
			"observation_type": obs.obsType,
			"kind":             obs.kind,
			"tags":             obs.tags,
			"namespace":        perfectNS,
			"confidence":       obs.confidence,
		}
		if obs.files != "" {
			args["files"] = obs.files
		}
		resp, saveErr := perfectH.Save(args)
		if saveErr != nil {
			errs = append(errs, fmt.Sprintf("perfect save %s: %v", obs.label, saveErr))
			continue
		}
		perfectIDs[obs.label] = resp.ID
	}

	// ──────────────────────────────────────────────────────────────────────────
	// Checkpoint queries
	// ──────────────────────────────────────────────────────────────────────────

	cqs := checkpointQueries()

	// Per-query counters.
	lazyFound := 0       // queries where lazy found expected obs in top-10
	perfectFound := 0    // queries where perfect found expected obs in top-10
	perfectOutranks := 0 // queries where perfect ranks higher than lazy
	queryTotal := len(cqs)

	for _, cq := range cqs {
		// Recall from lazy (no namespace filter — lazy saves had no namespace).
		lazyOpts := RecallOpts{Limit: 10}
		if cq.obsType != "" {
			lazyOpts.ObservationType = cq.obsType
		}
		lazyResp, recallErr := lazyH.Recall(cq.query, lazyOpts)
		if recallErr != nil {
			errs = append(errs, fmt.Sprintf("lazy recall %s: %v", cq.label, recallErr))
		}

		// Recall from perfect (with namespace).
		perfectOpts := RecallOpts{Limit: 10, Namespace: perfectNS}
		if cq.obsType != "" {
			perfectOpts.ObservationType = cq.obsType
		}
		perfectResp, recallErr := perfectH.Recall(cq.query, perfectOpts)
		if recallErr != nil {
			errs = append(errs, fmt.Sprintf("perfect recall %s: %v", cq.label, recallErr))
		}

		lazyRank := rankInResults(lazyResp.Results, cq.expectedKey)
		perfectRank := rankInResults(perfectResp.Results, cq.expectedKey)

		lazyHit := lazyRank >= 0
		perfectHit := perfectRank >= 0

		if lazyHit {
			lazyFound++
		}
		if perfectHit {
			perfectFound++
		}

		// Perfect outranks lazy when:
		// - perfect found it and lazy did not, OR
		// - both found it but perfect rank is lower (better) index.
		if (perfectHit && !lazyHit) || (perfectHit && lazyHit && perfectRank < lazyRank) {
			perfectOutranks++
		}

		checks = append(checks,
			CheckResult{
				Name:   cq.label + ": lazy finds expected obs",
				Passed: lazyHit,
				Detail: fmt.Sprintf("query=%q expectedKey=%q lazy_rank=%d results=%d",
					cq.query, cq.expectedKey, lazyRank, len(lazyResp.Results)),
			},
			CheckResult{
				Name:   cq.label + ": perfect finds expected obs",
				Passed: perfectHit,
				Detail: fmt.Sprintf("query=%q expectedKey=%q perfect_rank=%d results=%d",
					cq.query, cq.expectedKey, perfectRank, len(perfectResp.Results)),
			},
			CheckResult{
				Name:   cq.label + ": perfect outranks lazy",
				Passed: (perfectHit && !lazyHit) || (perfectHit && lazyHit && perfectRank <= lazyRank),
				Detail: fmt.Sprintf("lazy_rank=%d perfect_rank=%d", lazyRank, perfectRank),
			},
		)
	}

	// ──────────────────────────────────────────────────────────────────────────
	// Q10: health_check comparison
	// ──────────────────────────────────────────────────────────────────────────
	lazyHealth, lazyHealthErr := lazyH.HealthCheck()
	perfectHealth, perfectHealthErr := perfectH.HealthCheck()

	lazyScore := extractHealthScore(lazyHealth, lazyHealthErr, &errs, "lazy")
	perfectScore := extractHealthScore(perfectHealth, perfectHealthErr, &errs, "perfect")

	healthCheckPassed := perfectScore > lazyScore
	checks = append(checks, CheckResult{
		Name:   "Q10: perfect health_check score > lazy health_check score",
		Passed: healthCheckPassed,
		Detail: fmt.Sprintf("perfect_score=%d lazy_score=%d", perfectScore, lazyScore),
	})
	queryTotal++ // count Q10 as a query

	// ──────────────────────────────────────────────────────────────────────────
	// Summary checks (aggregate assertions)
	// ──────────────────────────────────────────────────────────────────────────

	totalObs := len(observations)
	lazySavedCount := len(lazyIDs)
	perfectSavedCount := len(perfectIDs)

	lazyFindRate := 0.0
	perfectFindRate := 0.0
	perfectOutrankRate := 0.0
	if queryTotal > 0 {
		lazyFindRate = float64(lazyFound) / float64(queryTotal-1) * 100 // -1 to exclude Q10
		perfectFindRate = float64(perfectFound) / float64(queryTotal-1) * 100
		perfectOutrankRate = float64(perfectOutranks) / float64(queryTotal-1) * 100
	}

	checks = append(checks,
		CheckResult{
			Name:   "lazy agent finds ≥60% of expected observations",
			Passed: lazyFindRate >= 60,
			Detail: fmt.Sprintf("%.0f%% (%d/%d queries)", lazyFindRate, lazyFound, queryTotal-1),
		},
		CheckResult{
			Name:   "perfect agent finds ≥80% of expected observations",
			Passed: perfectFindRate >= 80,
			Detail: fmt.Sprintf("%.0f%% (%d/%d queries)", perfectFindRate, perfectFound, queryTotal-1),
		},
		CheckResult{
			Name:   "perfect outranks lazy on ≥70% of queries",
			Passed: perfectOutrankRate >= 70,
			Detail: fmt.Sprintf("%.0f%% (%d/%d queries)", perfectOutrankRate, perfectOutranks, queryTotal-1),
		},
		CheckResult{
			Name:   "all 20 lazy observations saved",
			Passed: lazySavedCount == totalObs,
			Detail: fmt.Sprintf("%d/%d saved", lazySavedCount, totalObs),
		},
		CheckResult{
			Name:   "all 20 perfect observations saved",
			Passed: perfectSavedCount == totalObs,
			Detail: fmt.Sprintf("%d/%d saved", perfectSavedCount, totalObs),
		},
	)

	// ──────────────────────────────────────────────────────────────────────────
	// Score
	// ──────────────────────────────────────────────────────────────────────────

	passed := 0
	for _, c := range checks {
		if c.Passed {
			passed++
		}
	}

	rawRate := float64(passed) / float64(len(checks)) * 100
	threshold := Threshold{Base: 50, Target: 70, Elite: 90}
	score, _ := EvaluateScore(rawRate, threshold)

	return DimensionResult{
		Score:  score,
		Max:    100,
		Checks: checks,
		Metrics: map[string]float64{
			"checks_passed":        float64(passed),
			"checks_total":         float64(len(checks)),
			"recall_rate":          rawRate,
			"lazy_find_rate":       lazyFindRate,
			"perfect_find_rate":    perfectFindRate,
			"perfect_outrank_rate": perfectOutrankRate,
			"lazy_health_score":    float64(lazyScore),
			"perfect_health_score": float64(perfectScore),
			"observations_saved":   float64(totalObs),
		},
		Errors: errs,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// helpers
// ─────────────────────────────────────────────────────────────────────────────

// rankInResults returns the 0-based index of the first result whose title
// contains expectedKey (case-insensitive substring match).
// Returns -1 if no result matches.
func rankInResults(results []RecallItem, expectedKey string) int {
	if expectedKey == "" {
		return -1
	}
	lower := toLower(expectedKey)
	for i, r := range results {
		if containsIgnoreCase(r.Title, lower) || containsIgnoreCase(r.Content, lower) {
			return i
		}
	}
	return -1
}

// toLower is a simple ASCII-only lowercase helper to avoid importing strings
// at package level just for this (strings is already imported by other files
// in the package, but we declare a local helper for clarity).
func toLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// containsIgnoreCase reports whether haystack contains needle (both already lowercase).
func containsIgnoreCase(haystack, needleLower string) bool {
	h := toLower(haystack)
	for i := 0; i <= len(h)-len(needleLower); i++ {
		if h[i:i+len(needleLower)] == needleLower {
			return true
		}
	}
	return false
}

// extractHealthScore extracts the integer "score" field from a health_check
// response map.  On error it appends to errs and returns 0.
func extractHealthScore(result map[string]any, err error, errs *[]string, label string) int {
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("%s health_check error: %v", label, err))
		return 0
	}
	if result == nil {
		*errs = append(*errs, fmt.Sprintf("%s health_check: nil result", label))
		return 0
	}
	switch v := result["score"].(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}

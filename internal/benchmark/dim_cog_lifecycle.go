package benchmark

import (
	"context"
	"fmt"
	"strings"

	"github.com/joeldevz/neurox/internal/facts"
	"github.com/joeldevz/neurox/internal/observation"
	"github.com/joeldevz/neurox/internal/recall"
)

// CogLifecycle benchmarks a complete 30-day brain simulation:
// bootstrap decisions → active development → knowledge evolution → maturity.
// It tests persistence of architectural decisions, invalidation of outdated
// knowledge, topic-key upsert, file-linked context, and signal/noise separation.
type CogLifecycle struct{}

func (d CogLifecycle) Name() string     { return "30-Day Brain Simulation" }
func (d CogLifecycle) Category() string { return "cognitive" }

func (d CogLifecycle) Run(ctx context.Context, env *BenchEnv) DimensionResult {
	var checks []CheckResult
	var errs []string

	// Track saved observation IDs keyed by a short label.
	ids := make(map[string]string)

	// ──────────────────────────────────────────────────────────────────────────
	// PHASE 1 — Bootstrap (Day 1-3): architecture decisions + facts
	// ──────────────────────────────────────────────────────────────────────────

	type obsSpec struct {
		label   string
		title   string
		content string
		obsType observation.ObservationType
		kind    observation.Kind
	}

	phase1Obs := []obsSpec{
		{
			label:   "react",
			title:   "Chose React 18 with TypeScript for frontend",
			content: "DECISION: Use React 18 with TypeScript strict mode for the frontend. Rationale: team familiarity, strong ecosystem, excellent typing support.",
			obsType: observation.ObservationTypeDecision,
			kind:    observation.KindSemantic,
		},
		{
			label:   "postgres",
			title:   "PostgreSQL 16 as primary database",
			content: "DECISION: PostgreSQL 16 is the primary database. Strong ACID guarantees, JSONB support for flexible schemas.",
			obsType: observation.ObservationTypeDecision,
			kind:    observation.KindSemantic,
		},
		{
			label:   "express",
			title:   "REST API with Express.js",
			content: "DECISION: REST API built with Express.js. GraphQL adds complexity not justified at current scale.",
			obsType: observation.ObservationTypeDecision,
			kind:    observation.KindSemantic,
		},
		{
			label:   "jwt",
			title:   "JWT authentication with RS256",
			content: "DECISION: JWT bearer tokens with RS256 signing algorithm. Tokens expire after 1h, refresh tokens in httpOnly cookies.",
			obsType: observation.ObservationTypeDecision,
			kind:    observation.KindSemantic,
		},
		{
			label:   "docker",
			title:   "Docker Compose for local development",
			content: "CONFIG: Docker Compose for local dev (Postgres, Redis, API, Frontend). Production uses Kubernetes.",
			obsType: observation.ObservationTypeConfig,
			kind:    observation.KindSemantic,
		},
		{
			label:   "redis",
			title:   "Redis for session caching",
			content: "DECISION: Redis 7 for session cache (TTL 24h) and API rate limiting. Also used as BullMQ backend.",
			obsType: observation.ObservationTypeDecision,
			kind:    observation.KindSemantic,
		},
		{
			label:   "monorepo",
			title:   "Monorepo with pnpm workspaces",
			content: "DECISION: pnpm workspaces monorepo with packages: api, frontend, shared, infra. Enables code sharing.",
			obsType: observation.ObservationTypeDecision,
			kind:    observation.KindSemantic,
		},
		{
			label:   "ci",
			title:   "GitHub Actions for CI/CD",
			content: "CONFIG: GitHub Actions for CI/CD pipeline. Docker multi-stage builds. Push to main triggers staging deploy.",
			obsType: observation.ObservationTypeConfig,
			kind:    observation.KindSemantic,
		},
		{
			label:   "pref-react",
			title:   "Prefer functional React components",
			content: "PREFERENCE: Always use functional components and hooks in React. Avoid class components.",
			obsType: observation.ObservationTypePreference,
			kind:    observation.KindProcedural,
		},
		{
			label:   "pref-commits",
			title:   "Use conventional commits",
			content: "PREFERENCE: All commits must follow conventional commits: feat, fix, chore, docs, refactor, test, perf, ci.",
			obsType: observation.ObservationTypePreference,
			kind:    observation.KindProcedural,
		},
	}

	for _, spec := range phase1Obs {
		saved, err := env.ObsStore.Save(ctx, observation.Observation{
			Title:           spec.title,
			Content:         spec.content,
			ObservationType: spec.obsType,
			Kind:            spec.kind,
			Namespace:       "bench-lifecycle",
			Retention:       observation.RetentionDurable,
			Confidence:      0.9,
		})
		if err != nil {
			errs = append(errs, fmt.Sprintf("phase1 save %s: %v", spec.label, err))
			continue
		}
		ids[spec.label] = saved.ID
	}

	// Save 5 facts
	factSpecs := []facts.Fact{
		{Subject: "project", Predicate: "uses_framework", Object: "React 18", Namespace: "bench-lifecycle"},
		{Subject: "project", Predicate: "uses_database", Object: "PostgreSQL 16", Namespace: "bench-lifecycle"},
		{Subject: "project", Predicate: "uses_api", Object: "REST with Express.js", Namespace: "bench-lifecycle"},
		{Subject: "project", Predicate: "uses_auth", Object: "JWT RS256", Namespace: "bench-lifecycle"},
		{Subject: "project", Predicate: "uses_cache", Object: "Redis", Namespace: "bench-lifecycle"},
	}
	for _, f := range factSpecs {
		if _, err := env.FactStore.Save(ctx, f); err != nil {
			errs = append(errs, fmt.Sprintf("phase1 fact %s/%s: %v", f.Subject, f.Predicate, err))
		}
	}

	// ──────────────────────────────────────────────────────────────────────────
	// PHASE 2 — Active Development (Day 5-15): bugs, gotchas, noise, patterns
	// ──────────────────────────────────────────────────────────────────────────

	// 5 bug discoveries
	bugObs := []obsSpec{
		{
			label:   "bug-race",
			title:   "Race condition in auth token rotation",
			content: "BUG: Concurrent requests with same refresh token cause duplicate session creation. Fix: PostgreSQL advisory lock on user ID during token rotation.",
			obsType: observation.ObservationTypeBugfix,
			kind:    observation.KindEpisodic,
		},
		{
			label:   "bug-memleak",
			title:   "Memory leak in websocket connections",
			content: "BUG: WebSocket event listeners not cleaned up on disconnect. Each reconnect adds listeners without removing old ones. Fix: call removeEventListener in onclose handler.",
			obsType: observation.ObservationTypeBugfix,
			kind:    observation.KindEpisodic,
		},
		{
			label:   "bug-cors",
			title:   "CORS preflight fails with Authorization header",
			content: "BUG: Browser preflight (OPTIONS) fails when Authorization header present. Fix: add Authorization to Access-Control-Allow-Headers.",
			obsType: observation.ObservationTypeBugfix,
			kind:    observation.KindEpisodic,
		},
		{
			label:   "bug-sqli",
			title:   "SQL injection in search endpoint",
			content: "BUG: Search endpoint interpolates user input directly into SQL string. Fix: use parameterized queries everywhere. Found in /api/search route.",
			obsType: observation.ObservationTypeBugfix,
			kind:    observation.KindEpisodic,
		},
		{
			label:   "bug-timezone",
			title:   "Timezone bug in cron jobs",
			content: "BUG: Cron jobs scheduled in server local time, not UTC. Jobs fire at wrong times in production (UTC server). Fix: always use UTC for all cron expressions.",
			obsType: observation.ObservationTypeBugfix,
			kind:    observation.KindEpisodic,
		},
	}

	for _, spec := range bugObs {
		saved, err := env.ObsStore.Save(ctx, observation.Observation{
			Title:           spec.title,
			Content:         spec.content,
			ObservationType: spec.obsType,
			Kind:            spec.kind,
			Namespace:       "bench-lifecycle",
			Retention:       observation.RetentionDurable,
			Confidence:      0.85,
		})
		if err != nil {
			errs = append(errs, fmt.Sprintf("phase2 bug %s: %v", spec.label, err))
			continue
		}
		ids[spec.label] = saved.ID
	}

	// 5 gotchas
	gotchaObs := []obsSpec{
		{
			label:   "gotcha-redis-scan",
			title:   "Redis SCAN is non-blocking but slow for pattern matching",
			content: "GOTCHA: Using KEYS pattern blocks Redis. Use SCAN with MATCH instead. For bulk invalidation, store key sets explicitly. Never use KEYS in production.",
			obsType: observation.ObservationTypeGotcha,
			kind:    observation.KindProcedural,
		},
		{
			label:   "gotcha-pg-pool",
			title:   "pg pool exhaustion under concurrent load",
			content: "GOTCHA: PostgreSQL connection pool exhausted under concurrent load. Pool defaulted to 10; each request opened 2 connections. Fix: set max to 50, use pgBouncer.",
			obsType: observation.ObservationTypeGotcha,
			kind:    observation.KindProcedural,
		},
		{
			label:   "gotcha-docker-dns",
			title:   "Docker DNS resolution fails between containers",
			content: "GOTCHA: Containers on different networks cannot resolve each other by name. Fix: put all services in same Docker network, use service name as hostname.",
			obsType: observation.ObservationTypeGotcha,
			kind:    observation.KindProcedural,
		},
		{
			label:   "gotcha-ts-null",
			title:   "TypeScript strict null checks break existing API clients",
			content: "GOTCHA: Enabling strict null checks causes 47 type errors. Optional fields typed as T instead of T | null | undefined. Fix: add explicit null types to all API response DTOs.",
			obsType: observation.ObservationTypeGotcha,
			kind:    observation.KindProcedural,
		},
		{
			label:   "gotcha-env-case",
			title:   "Env var case sensitivity on Linux",
			content: "GOTCHA: Environment variables are case-sensitive on Linux. DATABASE_URL != database_url. All env vars must be SCREAMING_SNAKE_CASE. Check .env.example for canonical names.",
			obsType: observation.ObservationTypeGotcha,
			kind:    observation.KindProcedural,
		},
	}

	for _, spec := range gotchaObs {
		saved, err := env.ObsStore.Save(ctx, observation.Observation{
			Title:           spec.title,
			Content:         spec.content,
			ObservationType: spec.obsType,
			Kind:            spec.kind,
			Namespace:       "bench-lifecycle",
			Retention:       observation.RetentionDurable,
			Confidence:      0.85,
		})
		if err != nil {
			errs = append(errs, fmt.Sprintf("phase2 gotcha %s: %v", spec.label, err))
			continue
		}
		ids[spec.label] = saved.ID
	}

	// 5 operational noise observations
	noiseObsPhase2 := []struct {
		title   string
		content string
	}{
		{"Step 1 completed", "Step 1: initial setup completed. Created repo structure, configured toolchain."},
		{"Build passed", "Build successful. 0 errors, 0 warnings. Output: dist/"},
		{"Tests green", "All 142 tests passed. Coverage: 74%. No regressions."},
		{"Deploy to staging", "Deployed to staging environment. Health check passing. Smoke tests OK."},
		{"PR merged", "PR #47 merged to main. Squash merged. CI passed. No conflicts."},
	}

	for i, n := range noiseObsPhase2 {
		if _, err := env.ObsStore.Save(ctx, observation.Observation{
			Title:           n.title,
			Content:         n.content,
			ObservationType: observation.ObservationTypeDiscovery,
			Kind:            observation.KindEpisodic,
			Namespace:       "bench-lifecycle",
			Retention:       observation.RetentionOperational,
			Confidence:      0.5,
		}); err != nil {
			errs = append(errs, fmt.Sprintf("phase2 noise %d: %v", i, err))
		}
	}

	// 5 file-linked observations
	fileLinkedObs := []struct {
		label   string
		title   string
		content string
		file    string
	}{
		{
			label:   "file-auth",
			title:   "Auth middleware validates JWT and extracts user claims",
			content: "DISCOVERY: Auth middleware validates JWT bearer tokens using RS256. Extracts user_id and role. Rejects expired tokens with 401.",
			file:    "src/auth.ts",
		},
		{
			label:   "file-db",
			title:   "Database pool configuration and connection management",
			content: "DISCOVERY: DB pool uses pg client, max 20 connections, idle timeout 30s, connection timeout 5s. Connection string from DATABASE_URL env var.",
			file:    "src/db.ts",
		},
		{
			label:   "file-middleware",
			title:   "Error handling middleware with structured logging",
			content: "DISCOVERY: Error middleware catches all unhandled errors. Logs structured JSON with correlation_id and stack trace. Returns RFC 7807 problem details.",
			file:    "src/middleware.ts",
		},
		{
			label:   "file-routes",
			title:   "API routes registration and versioning",
			content: "DISCOVERY: All routes registered under /api/v1 prefix. Versioning via URL path. Route files: auth.ts, users.ts, products.ts.",
			file:    "src/routes/api.ts",
		},
		{
			label:   "file-config",
			title:   "Configuration loading and validation",
			content: "DISCOVERY: Config loaded from env vars, validated with Zod. Fails fast at startup if required vars missing. Config object exported as singleton.",
			file:    "src/config.ts",
		},
	}

	for _, fl := range fileLinkedObs {
		saved, err := env.ObsStore.Save(ctx, observation.Observation{
			Title:           fl.title,
			Content:         fl.content,
			ObservationType: observation.ObservationTypeDiscovery,
			Kind:            observation.KindSemantic,
			Namespace:       "bench-lifecycle",
			Retention:       observation.RetentionDurable,
			Confidence:      0.85,
			Files:           []string{fl.file},
		})
		if err != nil {
			errs = append(errs, fmt.Sprintf("phase2 file-linked %s: %v", fl.label, err))
			continue
		}
		ids[fl.label] = saved.ID
	}

	// 5 preferences
	prefObs := []obsSpec{
		{
			label:   "pref-tab",
			title:   "Tab indentation in TypeScript files",
			content: "PREFERENCE: Use tabs for indentation in TypeScript. Configured in .editorconfig and prettier.",
			obsType: observation.ObservationTypePreference,
			kind:    observation.KindProcedural,
		},
		{
			label:   "pref-yaml",
			title:   "2-space indent in YAML files",
			content: "PREFERENCE: YAML files use 2-space indentation. Enforced by yamllint in CI.",
			obsType: observation.ObservationTypePreference,
			kind:    observation.KindProcedural,
		},
		{
			label:   "pref-linelen",
			title:   "Max line length 100 characters",
			content: "PREFERENCE: Maximum line length is 100 characters. Configured in prettier and ESLint.",
			obsType: observation.ObservationTypePreference,
			kind:    observation.KindProcedural,
		},
		{
			label:   "pref-console",
			title:   "No console.log in production code",
			content: "PREFERENCE: Never use console.log in production code. Use structured logger instead. ESLint rule: no-console enabled.",
			obsType: observation.ObservationTypePreference,
			kind:    observation.KindProcedural,
		},
		{
			label:   "pref-errors",
			title:   "Always handle errors explicitly",
			content: "PREFERENCE: All errors must be handled explicitly. Never silently swallow errors. Use Result types for expected error paths.",
			obsType: observation.ObservationTypePreference,
			kind:    observation.KindSemantic,
		},
	}

	for _, spec := range prefObs {
		saved, err := env.ObsStore.Save(ctx, observation.Observation{
			Title:           spec.title,
			Content:         spec.content,
			ObservationType: spec.obsType,
			Kind:            spec.kind,
			Namespace:       "bench-lifecycle",
			Retention:       observation.RetentionDurable,
			Confidence:      0.9,
		})
		if err != nil {
			errs = append(errs, fmt.Sprintf("phase2 pref %s: %v", spec.label, err))
			continue
		}
		ids[spec.label] = saved.ID
	}

	// 5 patterns
	patternObs := []obsSpec{
		{
			label:   "pat-repo",
			title:   "Repository pattern for database access",
			content: "PATTERN: All database access goes through repository classes. No raw SQL in controllers. Repositories return domain objects. Enables easy mocking in unit tests.",
			obsType: observation.ObservationTypePattern,
			kind:    observation.KindProcedural,
		},
		{
			label:   "pat-circuit",
			title:   "Circuit breaker for external HTTP calls",
			content: "PATTERN: Use circuit breaker (opossum library) for all external HTTP dependencies. Opens after 3 failures in 10s. Half-open probe after 30s.",
			obsType: observation.ObservationTypePattern,
			kind:    observation.KindProcedural,
		},
		{
			label:   "pat-retry",
			title:   "Retry with exponential backoff for transient failures",
			content: "PATTERN: Retry transient failures up to 3 times with exponential backoff: 100ms, 200ms, 400ms. Jitter ±10%. Do not retry 4xx errors.",
			obsType: observation.ObservationTypePattern,
			kind:    observation.KindProcedural,
		},
		{
			label:   "pat-logging",
			title:   "Structured logging with correlation IDs",
			content: "PATTERN: All log lines include correlation_id, service name, timestamp, log level. Use pino logger. Pass logger via dependency injection.",
			obsType: observation.ObservationTypePattern,
			kind:    observation.KindProcedural,
		},
		{
			label:   "pat-errbound",
			title:   "Error boundary in React component tree",
			content: "PATTERN: Wrap each major React feature section in an ErrorBoundary. Log errors to Sentry. Show user-friendly fallback UI. Never let errors propagate to root.",
			obsType: observation.ObservationTypePattern,
			kind:    observation.KindSemantic,
		},
	}

	for _, spec := range patternObs {
		saved, err := env.ObsStore.Save(ctx, observation.Observation{
			Title:           spec.title,
			Content:         spec.content,
			ObservationType: spec.obsType,
			Kind:            spec.kind,
			Namespace:       "bench-lifecycle",
			Retention:       observation.RetentionDurable,
			Confidence:      0.9,
		})
		if err != nil {
			errs = append(errs, fmt.Sprintf("phase2 pattern %s: %v", spec.label, err))
			continue
		}
		ids[spec.label] = saved.ID
	}

	// Run consolidation after Phase 2
	if err := env.Pipeline.Run(ctx); err != nil {
		errs = append(errs, fmt.Sprintf("phase2 pipeline: %v", err))
	}

	// ──────────────────────────────────────────────────────────────────────────
	// PHASE 3 — Knowledge Evolution (Day 16-25)
	// ──────────────────────────────────────────────────────────────────────────

	// Invalidate "REST API with Express.js" → replace with Fastify
	if expressID, ok := ids["express"]; ok {
		invResult, err := env.ObsStore.Invalidate(ctx, env.LinkStore, observation.InvalidateInput{
			ObservationID:      expressID,
			Reason:             "framework migration",
			ReplacementTitle:   "Migrated to Fastify for better performance",
			ReplacementContent: "DECISION: Migrated API from Express.js to Fastify. Fastify is 2x faster on benchmarks, has built-in schema validation, and better TypeScript support.",
		})
		if err != nil {
			errs = append(errs, fmt.Sprintf("phase3 invalidate express: %v", err))
		} else {
			ids["fastify"] = invResult.ReplacementID
		}
	} else {
		errs = append(errs, "phase3: express ID not found, skipping invalidation")
	}

	// Invalidate "JWT with RS256" → replace with ES256
	if jwtID, ok := ids["jwt"]; ok {
		invResult, err := env.ObsStore.Invalidate(ctx, env.LinkStore, observation.InvalidateInput{
			ObservationID:      jwtID,
			Reason:             "algorithm upgrade",
			ReplacementTitle:   "JWT now uses ES256 for smaller tokens",
			ReplacementContent: "DECISION: JWT signing algorithm changed from RS256 to ES256. ES256 produces smaller tokens (saves ~200 bytes per request) while maintaining equivalent security.",
		})
		if err != nil {
			errs = append(errs, fmt.Sprintf("phase3 invalidate jwt: %v", err))
		} else {
			ids["jwt-es256"] = invResult.ReplacementID
		}
	} else {
		errs = append(errs, "phase3: jwt ID not found, skipping invalidation")
	}

	// Topic key upsert: Node version V1 → V2
	nodeV1, err := env.ObsStore.Save(ctx, observation.Observation{
		Title:           "Node.js runtime version",
		Content:         "CONFIG: Runtime is Node.js 18 LTS. Recommended for all services.",
		ObservationType: observation.ObservationTypeConfig,
		Kind:            observation.KindSemantic,
		Namespace:       "bench-lifecycle",
		Retention:       observation.RetentionDurable,
		Confidence:      0.9,
		TopicKey:        "lifecycle-node-version",
	})
	if err != nil {
		errs = append(errs, fmt.Sprintf("phase3 node v1: %v", err))
	} else {
		ids["node-v1"] = nodeV1.ID
	}

	nodeV2, err := env.ObsStore.Save(ctx, observation.Observation{
		Title:           "Node.js runtime version",
		Content:         "CONFIG: Runtime upgraded to Node 20 LTS. All services updated. Node 18 EOL approaching.",
		ObservationType: observation.ObservationTypeConfig,
		Kind:            observation.KindSemantic,
		Namespace:       "bench-lifecycle",
		Retention:       observation.RetentionDurable,
		Confidence:      0.9,
		TopicKey:        "lifecycle-node-version",
	})
	if err != nil {
		errs = append(errs, fmt.Sprintf("phase3 node v2: %v", err))
	} else {
		ids["node-v2"] = nodeV2.ID
	}

	// Topic key upsert: TypeScript version V1 → V2
	tsV1, err := env.ObsStore.Save(ctx, observation.Observation{
		Title:           "TypeScript compiler version",
		Content:         "CONFIG: TypeScript 5.0 in use. Strict mode enabled.",
		ObservationType: observation.ObservationTypeConfig,
		Kind:            observation.KindSemantic,
		Namespace:       "bench-lifecycle",
		Retention:       observation.RetentionDurable,
		Confidence:      0.9,
		TopicKey:        "lifecycle-typescript-version",
	})
	if err != nil {
		errs = append(errs, fmt.Sprintf("phase3 ts v1: %v", err))
	} else {
		ids["ts-v1"] = tsV1.ID
	}

	tsV2, err := env.ObsStore.Save(ctx, observation.Observation{
		Title:           "TypeScript compiler version",
		Content:         "CONFIG: Upgraded to TypeScript 5.3. New features: import attributes, narrowing improvements.",
		ObservationType: observation.ObservationTypeConfig,
		Kind:            observation.KindSemantic,
		Namespace:       "bench-lifecycle",
		Retention:       observation.RetentionDurable,
		Confidence:      0.9,
		TopicKey:        "lifecycle-typescript-version",
	})
	if err != nil {
		errs = append(errs, fmt.Sprintf("phase3 ts v2: %v", err))
	} else {
		ids["ts-v2"] = tsV2.ID
	}

	// 5 more operational noise
	noiseObsPhase3 := []struct {
		title   string
		content string
	}{
		{"Phase 3 step A completed", "Refactored auth module. Tests passing. No regressions."},
		{"Phase 3 build green", "Build 2041 passed. 0 errors. Lint clean."},
		{"Phase 3 staging deploy", "Staging deploy successful. Fastify endpoint responding."},
		{"Phase 3 PR review", "PR #89 reviewed and approved. Merged to main."},
		{"Phase 3 monitoring check", "All services healthy. P99 latency < 50ms."},
	}

	for i, n := range noiseObsPhase3 {
		if _, err := env.ObsStore.Save(ctx, observation.Observation{
			Title:           n.title,
			Content:         n.content,
			ObservationType: observation.ObservationTypeDiscovery,
			Kind:            observation.KindEpisodic,
			Namespace:       "bench-lifecycle",
			Retention:       observation.RetentionOperational,
			Confidence:      0.5,
		}); err != nil {
			errs = append(errs, fmt.Sprintf("phase3 noise %d: %v", i, err))
		}
	}

	// 3 durable updates about new features
	featureObs := []obsSpec{
		{
			label:   "feature-realtime",
			title:   "Added real-time notifications via WebSockets",
			content: "DECISION: Real-time notifications implemented with WebSocket (ws library). Server-sent events considered but WebSocket chosen for bidirectional communication.",
			obsType: observation.ObservationTypeDecision,
			kind:    observation.KindSemantic,
		},
		{
			label:   "feature-search",
			title:   "Full-text search with PostgreSQL FTS",
			content: "DECISION: Full-text search implemented using PostgreSQL tsvector and tsquery. No external search engine needed at current scale.",
			obsType: observation.ObservationTypeDecision,
			kind:    observation.KindSemantic,
		},
		{
			label:   "feature-audit",
			title:   "Audit logging for all data mutations",
			content: "DECISION: All create/update/delete operations write to audit_log table. Includes actor_id, action, resource_type, resource_id, timestamp, and diff.",
			obsType: observation.ObservationTypeDecision,
			kind:    observation.KindSemantic,
		},
	}

	for _, spec := range featureObs {
		saved, err := env.ObsStore.Save(ctx, observation.Observation{
			Title:           spec.title,
			Content:         spec.content,
			ObservationType: spec.obsType,
			Kind:            spec.kind,
			Namespace:       "bench-lifecycle",
			Retention:       observation.RetentionDurable,
			Confidence:      0.9,
		})
		if err != nil {
			errs = append(errs, fmt.Sprintf("phase3 feature %s: %v", spec.label, err))
			continue
		}
		ids[spec.label] = saved.ID
	}

	// Run consolidation after Phase 3
	if err := env.Pipeline.Run(ctx); err != nil {
		errs = append(errs, fmt.Sprintf("phase3 pipeline: %v", err))
	}

	// ──────────────────────────────────────────────────────────────────────────
	// PHASE 4 — Maturity (Day 26-30)
	// ──────────────────────────────────────────────────────────────────────────

	// Run pipeline twice for final consolidation
	for i := 0; i < 2; i++ {
		if err := env.Pipeline.Run(ctx); err != nil {
			errs = append(errs, fmt.Sprintf("phase4 pipeline %d: %v", i+1, err))
		}
	}

	// Apply decay three times to simulate aging
	for i := 0; i < 3; i++ {
		if _, err := env.DecayEngine.ApplyDecay(ctx); err != nil {
			errs = append(errs, fmt.Sprintf("phase4 decay %d: %v", i+1, err))
		}
	}

	// ──────────────────────────────────────────────────────────────────────────
	// CHECKPOINT QUERIES (20 checks)
	// ──────────────────────────────────────────────────────────────────────────

	type checkpoint struct {
		name  string
		query string
		check func(results []recall.Result) bool
		stale bool     // use IncludeStale
		files []string // non-empty = use GetContext instead
	}

	checkpoints := []checkpoint{
		{
			name:  "CP01: framework is React",
			query: "What framework does the project use?",
			check: func(r []recall.Result) bool {
				return containsAnyLC(r, "react")
			},
		},
		{
			name:  "CP02: API framework is Fastify not Express",
			query: "What API framework do we use?",
			check: func(r []recall.Result) bool {
				return containsAnyLC(r, "fastify")
			},
		},
		{
			name:  "CP03: database is PostgreSQL",
			query: "What database does the project use?",
			check: func(r []recall.Result) bool {
				return containsAnyLC(r, "postgresql", "postgres")
			},
		},
		{
			name:  "CP04: auth method is ES256 not RS256",
			query: "What authentication method do we use?",
			check: func(r []recall.Result) bool {
				return containsAnyLC(r, "es256")
			},
		},
		{
			name:  "CP05: user preferences found",
			query: "User preferences",
			check: func(r []recall.Result) bool {
				count := 0
				for _, res := range r {
					if res.ObservationType == observation.ObservationTypePreference {
						count++
					}
				}
				return count >= 3
			},
		},
		{
			name:  "CP06: bugs discovered",
			query: "What bugs were found?",
			check: func(r []recall.Result) bool {
				count := 0
				for _, res := range r {
					if res.ObservationType == observation.ObservationTypeBugfix {
						count++
					}
				}
				return count >= 2
			},
		},
		{
			name:  "CP07: gotchas present",
			query: "What gotchas should I know?",
			check: func(r []recall.Result) bool {
				count := 0
				for _, res := range r {
					if res.ObservationType == observation.ObservationTypeGotcha {
						count++
					}
				}
				return count >= 2
			},
		},
		{
			name:  "CP08: patterns present",
			query: "Project patterns and conventions",
			check: func(r []recall.Result) bool {
				count := 0
				for _, res := range r {
					if res.ObservationType == observation.ObservationTypePattern {
						count++
					}
				}
				return count >= 2
			},
		},
		{
			name:  "CP09: Node version is 20",
			query: "Current Node version",
			check: func(r []recall.Result) bool {
				return containsAnyLC(r, "node 20", "node.js 20", "20 lts", "upgraded to node")
			},
		},
		{
			name:  "CP10: TypeScript version is 5.3",
			query: "Current TypeScript version",
			check: func(r []recall.Result) bool {
				return containsAnyLC(r, "5.3", "typescript 5.3")
			},
		},
		{
			name:  "CP11: file context for src/auth.ts",
			files: []string{"src/auth.ts"},
			check: func(r []recall.Result) bool {
				return len(r) >= 1
			},
		},
		{
			name:  "CP12: file context for src/db.ts",
			files: []string{"src/db.ts"},
			check: func(r []recall.Result) bool {
				return len(r) >= 1
			},
		},
		{
			name:  "CP13: Redis usage found",
			query: "Redis usage in project",
			check: func(r []recall.Result) bool {
				return containsAnyLC(r, "redis")
			},
		},
		{
			name:  "CP14: Docker configuration found",
			query: "Docker configuration",
			check: func(r []recall.Result) bool {
				return containsAnyLC(r, "docker")
			},
		},
		{
			name:  "CP15: CI/CD pipeline found",
			query: "CI/CD pipeline",
			check: func(r []recall.Result) bool {
				return containsAnyLC(r, "github actions", "ci/cd", "github", "actions")
			},
		},
		{
			name:  "CP16: architecture decisions found",
			query: "Architecture decisions",
			check: func(r []recall.Result) bool {
				count := 0
				for _, res := range r {
					if res.ObservationType == observation.ObservationTypeDecision {
						count++
					}
				}
				return count >= 3
			},
		},
		{
			name:  "CP17: API change history includes Fastify",
			query: "What changed in the API?",
			stale: true,
			check: func(r []recall.Result) bool {
				return containsAnyLC(r, "fastify", "migrat")
			},
		},
		{
			name:  "CP18: deployment infrastructure found",
			query: "Deployment and infrastructure",
			check: func(r []recall.Result) bool {
				return containsAnyLC(r, "deploy", "docker", "kubernetes", "staging", "github actions", "ci")
			},
		},
		{
			name:  "CP19: error handling approach found",
			query: "Error handling approach",
			check: func(r []recall.Result) bool {
				return containsAnyLC(r, "error")
			},
		},
		{
			name:  "CP20: caching strategy found",
			query: "Caching strategy",
			check: func(r []recall.Result) bool {
				return containsAnyLC(r, "cach", "redis")
			},
		},
	}

	for _, cp := range checkpoints {
		var passed bool
		var detail string

		if len(cp.files) > 0 {
			// File-context checkpoint: use GetContext
			ctxResult, ctxErr := env.ProactiveEngine.GetContext(ctx, "bench-lifecycle", cp.files, 10)
			if ctxErr != nil {
				errs = append(errs, fmt.Sprintf("%s: GetContext error: %v", cp.name, ctxErr))
				checks = append(checks, CheckResult{
					Name:   cp.name,
					Passed: false,
					Detail: fmt.Sprintf("GetContext error: %v", ctxErr),
				})
				continue
			}
			// Convert ContextItems to recall.Results for unified check function
			synth := make([]recall.Result, 0, len(ctxResult.Items))
			for _, item := range ctxResult.Items {
				synth = append(synth, recall.Result{
					ID:      item.ID,
					Title:   item.Title,
					Content: item.Content,
				})
			}
			passed = cp.check(synth)
			detail = fmt.Sprintf("files=%v items=%d", cp.files, len(ctxResult.Items))
		} else {
			// Regular search checkpoint
			results, searchErr := env.RecallEngine.Search(ctx, recall.SearchOptions{
				Query:        cp.query,
				Namespace:    "bench-lifecycle",
				Limit:        10,
				IncludeStale: cp.stale,
			})
			if searchErr != nil {
				errs = append(errs, fmt.Sprintf("%s: search error: %v", cp.name, searchErr))
				checks = append(checks, CheckResult{
					Name:   cp.name,
					Passed: false,
					Detail: fmt.Sprintf("search error: %v", searchErr),
				})
				continue
			}
			passed = cp.check(results)
			detail = fmt.Sprintf("query=%q results=%d", cp.query, len(results))
		}

		checks = append(checks, CheckResult{
			Name:   cp.name,
			Passed: passed,
			Detail: detail,
		})
	}

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
			"checks_passed": float64(passed),
			"checks_total":  float64(len(checks)),
			"recall_rate":   rawRate,
		},
		Errors: errs,
	}
}

// containsAnyLC reports whether any result's title+content contains at least
// one of the given keywords (case-insensitive).
func containsAnyLC(results []recall.Result, keywords ...string) bool {
	for _, r := range results {
		lower := strings.ToLower(r.Title + " " + r.Content)
		for _, kw := range keywords {
			if strings.Contains(lower, strings.ToLower(kw)) {
				return true
			}
		}
	}
	return false
}

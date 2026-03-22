package benchmark

import (
	"fmt"
	"time"

	"neurox/internal/observation"
)

// ProjectDay groups a batch of observations that were "written" on the same
// simulated project day.
type ProjectDay struct {
	Day          int
	Observations []observation.Observation
}

// ObsOpts is a convenience struct for building individual observations.
type ObsOpts struct {
	Title     string
	Content   string
	Type      observation.ObservationType
	Kind      observation.Kind
	Tags      []string
	Files     []string
	Namespace string
	Retention observation.Retention
}

// GenerateObservation returns a single observation with all fields populated
// from opts. Defaults follow the observation package conventions.
func GenerateObservation(opts ObsOpts) observation.Observation {
	ns := opts.Namespace
	if ns == "" {
		ns = "benchmark"
	}
	ret := opts.Retention
	if ret == "" {
		ret = observation.RetentionDurable
	}
	kind := opts.Kind
	if kind == "" {
		kind = observation.KindSemantic
	}
	obsType := opts.Type
	if obsType == "" {
		obsType = observation.ObservationTypeDiscovery
	}

	return observation.Observation{
		Title:           opts.Title,
		Content:         opts.Content,
		ObservationType: obsType,
		Kind:            kind,
		Tags:            opts.Tags,
		Files:           opts.Files,
		Namespace:       ns,
		Retention:       ret,
		Confidence:      0.8,
	}
}

// GenerateNoise returns n operational noise observations in the given namespace.
// These simulate ephemeral step logs, build output, etc.
func GenerateNoise(n int, namespace string) []observation.Observation {
	ns := namespace
	if ns == "" {
		ns = "benchmark"
	}

	out := make([]observation.Observation, 0, n)
	for i := range n {
		out = append(out, observation.Observation{
			Title: fmt.Sprintf("Build step %d completed", i+1),
			Content: fmt.Sprintf(
				"Step %d: compiled 42 packages in 1.2s. No errors. Output written to dist/.",
				i+1,
			),
			ObservationType: observation.ObservationTypeDiscovery,
			Kind:            observation.KindEpisodic,
			Tags:            []string{"build", "ci", "noise"},
			Namespace:       ns,
			Retention:       observation.RetentionOperational,
			Confidence:      0.5,
		})
	}
	return out
}

// GenerateProjectHistory creates a realistic sequence of observations spread
// across 30 simulated project days. The volume of observations is controlled
// by scale. This is the primary synthetic dataset used by cognitive dimensions.
//
// Distribution:
//   - Days 1-3:   Architecture decisions, tech-stack choices (type=decision, kind=semantic, durable)
//   - Days 5-10:  Bug discoveries, gotchas, configs (varied types/kinds)
//   - Days 12-20: Preferences, patterns, knowledge updates
//   - Days 22-30: Operational noise (step logs, builds — operational)
func GenerateProjectHistory(scale ScaleConfig) []ProjectDay {
	total := projectTotal(scale)

	// Allocate counts per phase
	durableCount := total * 60 / 100         // 60% durable
	operationalCount := total - durableCount // 40% operational

	var days []ProjectDay

	// --- Phase 1: Architecture decisions (Days 1-3) ---
	phase1Count := durableCount * 25 / 100
	days = append(days, generatePhase1(phase1Count)...)

	// --- Phase 2: Bugs, gotchas, configs (Days 5-10) ---
	phase2Count := durableCount * 35 / 100
	days = append(days, generatePhase2(phase2Count)...)

	// --- Phase 3: Preferences, patterns (Days 12-20) ---
	phase3Count := durableCount - phase1Count - phase2Count
	days = append(days, generatePhase3(phase3Count)...)

	// --- Phase 4: Operational noise (Days 22-30) ---
	days = append(days, generatePhase4(operationalCount)...)

	return days
}

// projectTotal returns total observation count for a scale.
func projectTotal(scale ScaleConfig) int {
	switch scale.Name {
	case "large":
		return 500
	case "medium":
		return 200
	default:
		return 50
	}
}

// --- phase generators ---

func generatePhase1(count int) []ProjectDay {
	templates := []struct {
		title   string
		content string
		tags    []string
		files   []string
	}{
		{
			"Chose React + TypeScript for frontend",
			"Decision: Use React 18 with TypeScript strict mode. Rationale: team familiarity, strong ecosystem, excellent typing support. Alternative (Vue 3) was rejected due to smaller talent pool.",
			[]string{"architecture", "react", "typescript", "frontend"},
			[]string{"frontend/package.json", "frontend/tsconfig.json"},
		},
		{
			"PostgreSQL selected as primary database",
			"Decision: PostgreSQL 16 as the primary datastore. Rationale: JSONB support for flexible schemas, strong ACID guarantees, and pgvector for future vector search. Rejected MongoDB due to lack of transactions.",
			[]string{"architecture", "database", "postgresql"},
			[]string{"docker-compose.yml", "migrations/001_init.sql"},
		},
		{
			"REST API with JWT authentication",
			"Decision: REST API (not GraphQL) with JWT bearer tokens. GraphQL adds complexity not justified at current scale. JWTs issued with 1h expiry, refresh tokens stored in httpOnly cookies.",
			[]string{"architecture", "api", "auth", "jwt"},
			[]string{"api/middleware/auth.ts", "api/routes/auth.ts"},
		},
		{
			"Monorepo structure with pnpm workspaces",
			"Decision: pnpm workspaces monorepo with packages: api, frontend, shared, infra. Enables code sharing between frontend and backend (types, validation schemas). pnpm chosen over Turborepo for simplicity.",
			[]string{"architecture", "monorepo", "pnpm"},
			[]string{"pnpm-workspace.yaml", "package.json"},
		},
		{
			"Redis for session caching and rate limiting",
			"Decision: Redis 7 for session cache (TTL 24h) and API rate limiting (token bucket per IP). Also used as BullMQ backend for async job processing.",
			[]string{"architecture", "redis", "caching"},
			[]string{"api/lib/redis.ts", "api/middleware/rateLimit.ts"},
		},
		{
			"Docker Compose for local development",
			"Decision: Docker Compose for local dev environment (Postgres, Redis, API, Frontend). Production uses Kubernetes. Dev compose mirrors prod topology to catch env-specific bugs early.",
			[]string{"architecture", "docker", "devops"},
			[]string{"docker-compose.yml", ".env.example"},
		},
	}

	return distribute(templates, count, 1, 3, observation.ObservationTypeDecision, observation.KindSemantic, observation.RetentionDurable)
}

func generatePhase2(count int) []ProjectDay {
	type entry struct {
		title   string
		content string
		obsType observation.ObservationType
		kind    observation.Kind
		tags    []string
		files   []string
	}

	templates := []entry{
		{
			"Gotcha: pg pool exhaustion under load",
			"GOTCHA: PostgreSQL connection pool exhausted under concurrent load. Root cause: pool size defaulted to 10, each API request opened 2 connections. Fix: set pool max to 50, use pgBouncer in transaction mode. Symptom: 'remaining connection slots reserved' error.",
			observation.ObservationTypeGotcha, observation.KindProcedural,
			[]string{"postgres", "performance", "gotcha"},
			[]string{"api/lib/db.ts"},
		},
		{
			"Bug: JWT refresh token rotation race condition",
			"BUG: Concurrent requests with same refresh token caused duplicate session creation. Root cause: non-atomic check-and-rotate. Fix: use PostgreSQL advisory lock on user ID during token rotation. See PR #47.",
			observation.ObservationTypeBugfix, observation.KindEpisodic,
			[]string{"auth", "jwt", "race-condition", "bugfix"},
			[]string{"api/routes/auth.ts", "api/services/session.ts"},
		},
		{
			"Config: environment variable naming convention",
			"CONVENTION: All env vars prefixed with APP_. Database vars: DATABASE_URL, DATABASE_POOL_MIN, DATABASE_POOL_MAX. Redis: REDIS_URL, REDIS_TTL_SESSION. Never commit .env files, only .env.example.",
			observation.ObservationTypeConfig, observation.KindProcedural,
			[]string{"config", "env-vars", "convention"},
			[]string{".env.example", "api/lib/config.ts"},
		},
		{
			"Gotcha: CORS preflight fails with Authorization header",
			"GOTCHA: Browser preflight (OPTIONS) requests fail when Authorization header is present unless server explicitly allows it. Fix: add 'Authorization' to Access-Control-Allow-Headers. Spent 2h debugging this.",
			observation.ObservationTypeGotcha, observation.KindProcedural,
			[]string{"cors", "api", "gotcha", "http"},
			[]string{"api/middleware/cors.ts"},
		},
		{
			"Bug: TypeScript strict null checks break existing API clients",
			"BUG: Enabling strict null checks in tsconfig caused 47 type errors in API client code. Root cause: optional fields were typed as T instead of T | null | undefined. Fix: added explicit null types to all API response DTOs.",
			observation.ObservationTypeBugfix, observation.KindEpisodic,
			[]string{"typescript", "types", "bugfix"},
			[]string{"shared/types/api.ts", "frontend/tsconfig.json"},
		},
		{
			"Config: pgvector extension setup",
			"CONFIG: pgvector requires CREATE EXTENSION vector; in a migration. Must be done as superuser. Docker image: ankane/pgvector. Default dimensions: 1536 (OpenAI ada-002). Index: CREATE INDEX ON items USING hnsw (embedding vector_cosine_ops).",
			observation.ObservationTypeConfig, observation.KindProcedural,
			[]string{"postgres", "pgvector", "config", "ai"},
			[]string{"migrations/003_pgvector.sql"},
		},
		{
			"Discovery: BullMQ job deduplication pattern",
			"DISCOVERY: BullMQ supports job deduplication via jobId. Set jobId to a content hash to prevent duplicate processing. Useful for webhook deduplication. Gotcha: jobs with same ID are silently dropped if already queued.",
			observation.ObservationTypeDiscovery, observation.KindSemantic,
			[]string{"bullmq", "queue", "patterns"},
			[]string{"api/queues/webhook.ts"},
		},
		{
			"Gotcha: Redis SCAN is non-blocking but slow for pattern matching",
			"GOTCHA: Using KEYS pattern blocks Redis. Use SCAN with MATCH instead. For bulk invalidation, store key sets explicitly (e.g. SET user:{id}:sessions). Never use KEYS in production.",
			observation.ObservationTypeGotcha, observation.KindProcedural,
			[]string{"redis", "performance", "gotcha"},
			[]string{"api/lib/cache.ts"},
		},
	}

	var days []ProjectDay
	baseDay := 5
	perDay := max(1, count/6)
	idx := 0
	for day := baseDay; day <= 10 && idx < count; day++ {
		var obs []observation.Observation
		for range perDay {
			t := templates[idx%len(templates)]
			obs = append(obs, observation.Observation{
				Title:           t.title,
				Content:         t.content,
				ObservationType: t.obsType,
				Kind:            t.kind,
				Tags:            t.tags,
				Files:           t.files,
				Namespace:       "benchmark",
				Retention:       observation.RetentionDurable,
				Confidence:      0.85,
				CreatedAt:       simulatedTime(day),
			})
			idx++
			if idx >= count {
				break
			}
		}
		days = append(days, ProjectDay{Day: day, Observations: obs})
	}
	return days
}

func generatePhase3(count int) []ProjectDay {
	type entry struct {
		title   string
		content string
		obsType observation.ObservationType
		kind    observation.Kind
		tags    []string
	}

	templates := []entry{
		{
			"Preference: always use named exports over default exports",
			"PREFERENCE: Team agreed to always use named exports in TypeScript. Default exports make refactoring harder (rename doesn't propagate). ESLint rule: import/no-default-export enabled.",
			observation.ObservationTypePreference, observation.KindProcedural,
			[]string{"typescript", "preference", "conventions"},
		},
		{
			"Pattern: repository pattern for database access",
			"PATTERN: All database access goes through repository classes. No raw SQL in controllers. Repositories return domain objects, not raw DB rows. Enables easy mocking in unit tests.",
			observation.ObservationTypePattern, observation.KindProcedural,
			[]string{"architecture", "pattern", "testing"},
		},
		{
			"Preference: error handling with Result type",
			"PREFERENCE: Use neverthrow Result<T, E> for expected errors instead of throwing. Reserve throws for truly unexpected errors (programming errors). This makes error paths explicit at the type level.",
			observation.ObservationTypePreference, observation.KindSemantic,
			[]string{"typescript", "error-handling", "preference"},
		},
		{
			"Pattern: optimistic UI updates in React",
			"PATTERN: For user-facing mutations, apply optimistic updates immediately then reconcile with server response. Use React Query's onMutate + onError (rollback) + onSettled pattern. Improves perceived performance significantly.",
			observation.ObservationTypePattern, observation.KindProcedural,
			[]string{"react", "ux", "pattern", "react-query"},
		},
		{
			"Discovery: Zod schemas shared between frontend and backend",
			"DISCOVERY: Zod validation schemas can live in the shared/ package and be imported by both API (validation) and frontend (form validation). Single source of truth for data shapes. Use .parse() on API input, .safeParse() on form submit.",
			observation.ObservationTypeDiscovery, observation.KindSemantic,
			[]string{"zod", "validation", "monorepo", "typescript"},
		},
		{
			"Preference: commit message format",
			"PREFERENCE: Conventional Commits format enforced via commitlint. Format: type(scope): description. Types: feat, fix, chore, docs, test, refactor, style, perf. Breaking changes: BREAKING CHANGE footer or ! after type.",
			observation.ObservationTypePreference, observation.KindProcedural,
			[]string{"git", "conventions", "preference"},
		},
	}

	var days []ProjectDay
	baseDay := 12
	perDay := max(1, count/9)
	idx := 0
	for day := baseDay; day <= 20 && idx < count; day++ {
		var obs []observation.Observation
		for range perDay {
			t := templates[idx%len(templates)]
			obs = append(obs, observation.Observation{
				Title:           t.title,
				Content:         t.content,
				ObservationType: t.obsType,
				Kind:            t.kind,
				Tags:            t.tags,
				Namespace:       "benchmark",
				Retention:       observation.RetentionDurable,
				Confidence:      0.9,
				CreatedAt:       simulatedTime(day),
			})
			idx++
			if idx >= count {
				break
			}
		}
		days = append(days, ProjectDay{Day: day, Observations: obs})
	}
	return days
}

func generatePhase4(count int) []ProjectDay {
	var days []ProjectDay
	baseDay := 22
	perDay := max(1, count/9)
	idx := 0
	for day := baseDay; day <= 30 && idx < count; day++ {
		var obs []observation.Observation
		for range perDay {
			obs = append(obs, observation.Observation{
				Title: fmt.Sprintf("CI pipeline run #%d", 1000+idx),
				Content: fmt.Sprintf(
					"Run #%d: 142 tests passed, 0 failed. Build time: 3m 22s. Coverage: 74%%.",
					1000+idx,
				),
				ObservationType: observation.ObservationTypeDiscovery,
				Kind:            observation.KindEpisodic,
				Tags:            []string{"ci", "build", "noise"},
				Namespace:       "benchmark",
				Retention:       observation.RetentionOperational,
				Confidence:      0.5,
				CreatedAt:       simulatedTime(day),
			})
			idx++
			if idx >= count {
				break
			}
		}
		days = append(days, ProjectDay{Day: day, Observations: obs})
	}
	return days
}

// distribute creates n observations from the templates list spread across a
// day range. All observations get the given type, kind, and retention.
func distribute(
	templates []struct {
		title   string
		content string
		tags    []string
		files   []string
	},
	count, firstDay, lastDay int,
	obsType observation.ObservationType,
	kind observation.Kind,
	retention observation.Retention,
) []ProjectDay {
	if count <= 0 || firstDay > lastDay {
		return nil
	}

	daySpan := lastDay - firstDay + 1
	perDay := max(1, count/daySpan)

	var days []ProjectDay
	idx := 0
	for day := firstDay; day <= lastDay && idx < count; day++ {
		var obs []observation.Observation
		for range perDay {
			t := templates[idx%len(templates)]
			obs = append(obs, observation.Observation{
				Title:           t.title,
				Content:         t.content,
				ObservationType: obsType,
				Kind:            kind,
				Tags:            t.tags,
				Files:           t.files,
				Namespace:       "benchmark",
				Retention:       retention,
				Confidence:      0.85,
				CreatedAt:       simulatedTime(day),
			})
			idx++
			if idx >= count {
				break
			}
		}
		if len(obs) > 0 {
			days = append(days, ProjectDay{Day: day, Observations: obs})
		}
	}
	return days
}

// simulatedTime returns a reference time offset by n project days.
// Day 1 = now - 29 days, Day 30 = now.
func simulatedTime(day int) time.Time {
	return time.Now().UTC().Add(-time.Duration(30-day) * 24 * time.Hour)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

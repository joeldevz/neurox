package benchmark

import (
	"context"
	"fmt"

	"neurox/internal/links"
	"neurox/internal/observation"
	"neurox/internal/recall"
)

// CogKnowledgeUpdate benchmarks the brain's ability to handle evolving knowledge:
// supersession via invalidation, topic-key upsert idempotency, contradiction
// resolution after consolidation, and recency-based ranking of partial updates.
type CogKnowledgeUpdate struct{}

func (d CogKnowledgeUpdate) Name() string     { return "Knowledge Evolution" }
func (d CogKnowledgeUpdate) Category() string { return "cognitive" }

func (d CogKnowledgeUpdate) Run(ctx context.Context, env *BenchEnv) DimensionResult {
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
	threshold := Threshold{Base: 70, Target: 85, Elite: 95}
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

// ────────────────────────────────────────────────────────────────────────────
// Scenario A: Simple supersession via Invalidate (10 cases)
// Save V1 → Invalidate with V2 → verify V2 appears, V1 does not.
// ────────────────────────────────────────────────────────────────────────────

type knowledgeCase struct {
	label   string
	v1Title string
	v1Body  string
	v2Title string
	v2Body  string
	query   string
	obsType observation.ObservationType
}

func knowledgeCasesA() []knowledgeCase {
	return []knowledgeCase{
		{
			label:   "A1",
			v1Title: "Database version",
			v1Body:  "Database is Postgres 14, deployed in primary cluster",
			v2Title: "Database version",
			v2Body:  "Database is Postgres 16, upgraded from 14",
			query:   "Postgres database version",
			obsType: observation.ObservationTypeConfig,
		},
		{
			label:   "A2",
			v1Title: "Backend framework",
			v1Body:  "Backend uses Express.js for HTTP routing",
			v2Title: "Backend framework",
			v2Body:  "Backend uses Fastify instead of Express for HTTP routing",
			query:   "HTTP backend framework",
			obsType: observation.ObservationTypeDecision,
		},
		{
			label:   "A3",
			v1Title: "Authentication method",
			v1Body:  "Authentication uses session cookies stored in Redis",
			v2Title: "Authentication method",
			v2Body:  "Authentication uses JWT tokens instead of session cookies",
			query:   "authentication session JWT",
			obsType: observation.ObservationTypeDecision,
		},
		{
			label:   "A4",
			v1Title: "Deployment target",
			v1Body:  "Application deployed on Heroku dynos",
			v2Title: "Deployment target",
			v2Body:  "Application deployed on AWS ECS, migrated from Heroku",
			query:   "deploy cloud infrastructure",
			obsType: observation.ObservationTypeDecision,
		},
		{
			label:   "A5",
			v1Title: "Node runtime version",
			v1Body:  "Runtime is Node.js 16 LTS",
			v2Title: "Node runtime version",
			v2Body:  "Runtime upgraded to Node.js 20 LTS",
			query:   "Node.js runtime version",
			obsType: observation.ObservationTypeConfig,
		},
		{
			label:   "A6",
			v1Title: "Package manager",
			v1Body:  "Project uses npm as package manager",
			v2Title: "Package manager",
			v2Body:  "Project switched to pnpm for faster installs",
			query:   "package manager npm pnpm",
			obsType: observation.ObservationTypeDecision,
		},
		{
			label:   "A7",
			v1Title: "CSS framework",
			v1Body:  "Frontend styled with Bootstrap 5",
			v2Title: "CSS framework",
			v2Body:  "Frontend switched to Tailwind CSS, Bootstrap removed",
			query:   "CSS styling framework",
			obsType: observation.ObservationTypeDecision,
		},
		{
			label:   "A8",
			v1Title: "API protocol",
			v1Body:  "Services communicate via REST over HTTP/1.1",
			v2Title: "API protocol",
			v2Body:  "Services communicate via gRPC, REST deprecated",
			query:   "API protocol communication gRPC REST",
			obsType: observation.ObservationTypeDecision,
		},
		{
			label:   "A9",
			v1Title: "Test framework",
			v1Body:  "Unit tests use Jest with ts-jest transformer",
			v2Title: "Test framework",
			v2Body:  "Unit tests migrated to Vitest, Jest removed",
			query:   "test framework unit testing",
			obsType: observation.ObservationTypeDecision,
		},
		{
			label:   "A10",
			v1Title: "CI system",
			v1Body:  "Continuous integration runs on Jenkins pipeline",
			v2Title: "CI system",
			v2Body:  "CI moved to GitHub Actions, Jenkins decommissioned",
			query:   "continuous integration CI pipeline",
			obsType: observation.ObservationTypeDecision,
		},
	}
}

func (d CogKnowledgeUpdate) scenarioA(ctx context.Context, env *BenchEnv, errs *[]string) []CheckResult {
	cases := knowledgeCasesA()
	checks := make([]CheckResult, 0, len(cases)*2)

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
			checks = append(checks,
				CheckResult{Name: tc.label + ": V2 in results", Passed: false, Detail: "save V1 failed"},
				CheckResult{Name: tc.label + ": V1 excluded", Passed: false, Detail: "save V1 failed"},
			)
			continue
		}

		// 2. Invalidate V1 with replacement V2
		invResult, err := env.ObsStore.Invalidate(ctx, env.LinkStore, observation.InvalidateInput{
			ObservationID:      v1.ID,
			Reason:             "knowledge updated",
			ReplacementTitle:   tc.v2Title,
			ReplacementContent: tc.v2Body,
		})
		if err != nil {
			*errs = append(*errs, fmt.Sprintf("%s: invalidate failed: %v", tc.label, err))
			checks = append(checks,
				CheckResult{Name: tc.label + ": V2 in results", Passed: false, Detail: "invalidate failed"},
				CheckResult{Name: tc.label + ": V1 excluded", Passed: false, Detail: "invalidate failed"},
			)
			continue
		}
		v2ID := invResult.ReplacementID

		// 3. Search
		results, err := env.RecallEngine.Search(ctx, recall.SearchOptions{
			Query:     tc.query,
			Namespace: "benchmark",
			Limit:     10,
		})
		if err != nil {
			*errs = append(*errs, fmt.Sprintf("%s: search failed: %v", tc.label, err))
			checks = append(checks,
				CheckResult{Name: tc.label + ": V2 in results", Passed: false, Detail: "search failed"},
				CheckResult{Name: tc.label + ": V1 excluded", Passed: false, Detail: "search failed"},
			)
			continue
		}

		v2Found := false
		v1Found := false
		for _, r := range results {
			if r.ID == v2ID {
				v2Found = true
			}
			if r.ID == v1.ID {
				v1Found = true
			}
		}

		checks = append(checks,
			CheckResult{
				Name:   tc.label + ": V2 in results",
				Passed: v2Found,
				Detail: fmt.Sprintf("v2ID=%s found=%v results=%d", v2ID, v2Found, len(results)),
			},
			CheckResult{
				Name:   tc.label + ": V1 excluded",
				Passed: !v1Found,
				Detail: fmt.Sprintf("v1ID=%s in_results=%v (should be absent, staleness=stale)", v1.ID, v1Found),
			},
		)
	}

	return checks
}

// ────────────────────────────────────────────────────────────────────────────
// Scenario B: TopicKey upsert — 3 sequential saves converge to 1 active row
// ────────────────────────────────────────────────────────────────────────────

type topicKeyCase struct {
	label    string
	topicKey string
	v1Title  string
	v1Body   string
	v2Title  string
	v2Body   string
	v3Title  string
	v3Body   string
	query    string
}

func topicKeyCasesB() []topicKeyCase {
	return []topicKeyCase{
		{
			label:    "B1",
			topicKey: "bench-db-engine",
			v1Title:  "DB engine decision",
			v1Body:   "Using SQLite for local development",
			v2Title:  "DB engine decision",
			v2Body:   "Switched to PostgreSQL for production workloads",
			v3Title:  "DB engine decision",
			v3Body:   "Settled on PostgreSQL with read replicas for all environments",
			query:    "database engine PostgreSQL SQLite",
		},
		{
			label:    "B2",
			topicKey: "bench-api-version",
			v1Title:  "API version",
			v1Body:   "API at v1 with no versioning strategy",
			v2Title:  "API version",
			v2Body:   "API at v2 with URL path versioning",
			v3Title:  "API version",
			v3Body:   "API at v3 with header-based versioning, v1 sunset",
			query:    "API versioning strategy",
		},
		{
			label:    "B3",
			topicKey: "bench-auth-provider",
			v1Title:  "Auth provider",
			v1Body:   "Authentication via custom JWT implementation",
			v2Title:  "Auth provider",
			v2Body:   "Switched to Auth0 for identity management",
			v3Title:  "Auth provider",
			v3Body:   "Using Clerk as identity provider after Auth0 cost review",
			query:    "authentication identity provider",
		},
		{
			label:    "B4",
			topicKey: "bench-cache-layer",
			v1Title:  "Cache layer",
			v1Body:   "In-memory LRU cache for frequently accessed data",
			v2Title:  "Cache layer",
			v2Body:   "Redis added as distributed cache",
			v3Title:  "Cache layer",
			v3Body:   "Redis with cluster mode, in-memory cache removed",
			query:    "cache Redis distributed",
		},
		{
			label:    "B5",
			topicKey: "bench-log-platform",
			v1Title:  "Logging platform",
			v1Body:   "Logging to stdout via console.log",
			v2Title:  "Logging platform",
			v2Body:   "Structured logging with Winston, shipped to Datadog",
			v3Title:  "Logging platform",
			v3Body:   "OpenTelemetry traces and logs, migrated from Datadog to Grafana",
			query:    "logging observability platform",
		},
		{
			label:    "B6",
			topicKey: "bench-deploy-cadence",
			v1Title:  "Deploy cadence",
			v1Body:   "Manual deploys via SSH on each release",
			v2Title:  "Deploy cadence",
			v2Body:   "Weekly automated deploys via CI/CD pipeline",
			v3Title:  "Deploy cadence",
			v3Body:   "Continuous deployment on every main branch merge",
			query:    "deploy release cadence automation",
		},
		{
			label:    "B7",
			topicKey: "bench-orm-choice",
			v1Title:  "ORM selection",
			v1Body:   "Raw SQL queries for all database access",
			v2Title:  "ORM selection",
			v2Body:   "Switched to TypeORM for entity management",
			v3Title:  "ORM selection",
			v3Body:   "Migrated to Prisma ORM, TypeORM removed",
			query:    "ORM database access layer",
		},
		{
			label:    "B8",
			topicKey: "bench-message-queue",
			v1Title:  "Message queue",
			v1Body:   "No message queue, synchronous processing only",
			v2Title:  "Message queue",
			v2Body:   "RabbitMQ added for async task processing",
			v3Title:  "Message queue",
			v3Body:   "Replaced RabbitMQ with Kafka for higher throughput",
			query:    "message queue async processing Kafka",
		},
		{
			label:    "B9",
			topicKey: "bench-frontend-framework",
			v1Title:  "Frontend framework",
			v1Body:   "Static HTML with vanilla JavaScript",
			v2Title:  "Frontend framework",
			v2Body:   "React SPA with CRA boilerplate",
			v3Title:  "Frontend framework",
			v3Body:   "Next.js with App Router, React SPA removed",
			query:    "frontend framework React Next.js",
		},
		{
			label:    "B10",
			topicKey: "bench-state-management",
			v1Title:  "State management",
			v1Body:   "Local component state with useState",
			v2Title:  "State management",
			v2Body:   "Redux Toolkit added for global state",
			v3Title:  "State management",
			v3Body:   "Zustand replaces Redux, simpler store pattern",
			query:    "state management Redux Zustand",
		},
	}
}

func (d CogKnowledgeUpdate) scenarioB(ctx context.Context, env *BenchEnv, errs *[]string) []CheckResult {
	cases := topicKeyCasesB()
	checks := make([]CheckResult, 0, len(cases)*2)

	for _, tc := range cases {
		// 1. Save V1
		_, err := env.ObsStore.Save(ctx, observation.Observation{
			Title:           tc.v1Title,
			Content:         tc.v1Body,
			ObservationType: observation.ObservationTypeDecision,
			Kind:            observation.KindSemantic,
			Namespace:       "benchmark",
			TopicKey:        tc.topicKey,
			Retention:       observation.RetentionDurable,
		})
		if err != nil {
			*errs = append(*errs, fmt.Sprintf("%s: save V1 failed: %v", tc.label, err))
			checks = append(checks,
				CheckResult{Name: tc.label + ": single active row", Passed: false, Detail: "save V1 failed"},
				CheckResult{Name: tc.label + ": content matches V3", Passed: false, Detail: "save V1 failed"},
			)
			continue
		}

		// 2. Save V2 (same TopicKey → upsert)
		_, err = env.ObsStore.Save(ctx, observation.Observation{
			Title:           tc.v2Title,
			Content:         tc.v2Body,
			ObservationType: observation.ObservationTypeDecision,
			Kind:            observation.KindSemantic,
			Namespace:       "benchmark",
			TopicKey:        tc.topicKey,
			Retention:       observation.RetentionDurable,
		})
		if err != nil {
			*errs = append(*errs, fmt.Sprintf("%s: save V2 failed: %v", tc.label, err))
			checks = append(checks,
				CheckResult{Name: tc.label + ": single active row", Passed: false, Detail: "save V2 failed"},
				CheckResult{Name: tc.label + ": content matches V3", Passed: false, Detail: "save V2 failed"},
			)
			continue
		}

		// 3. Save V3 (same TopicKey → upsert again)
		v3, err := env.ObsStore.Save(ctx, observation.Observation{
			Title:           tc.v3Title,
			Content:         tc.v3Body,
			ObservationType: observation.ObservationTypeDecision,
			Kind:            observation.KindSemantic,
			Namespace:       "benchmark",
			TopicKey:        tc.topicKey,
			Retention:       observation.RetentionDurable,
		})
		if err != nil {
			*errs = append(*errs, fmt.Sprintf("%s: save V3 failed: %v", tc.label, err))
			checks = append(checks,
				CheckResult{Name: tc.label + ": single active row", Passed: false, Detail: "save V3 failed"},
				CheckResult{Name: tc.label + ": content matches V3", Passed: false, Detail: "save V3 failed"},
			)
			continue
		}

		// 4. Verify via DB: exactly 1 active row for this topic_key + namespace
		var count int
		if err := env.DB.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM observations WHERE topic_key = ? AND namespace = ? AND deleted_at IS NULL`,
			tc.topicKey, "benchmark",
		).Scan(&count); err != nil {
			*errs = append(*errs, fmt.Sprintf("%s: count query failed: %v", tc.label, err))
			checks = append(checks,
				CheckResult{Name: tc.label + ": single active row", Passed: false, Detail: "count query failed"},
				CheckResult{Name: tc.label + ": content matches V3", Passed: false, Detail: "count query failed"},
			)
			continue
		}

		// 5. Read the surviving observation and verify its content matches V3
		survived, err := env.ObsStore.Get(ctx, v3.ID)
		contentOK := err == nil && survived.Content == tc.v3Body

		checks = append(checks,
			CheckResult{
				Name:   tc.label + ": single active row",
				Passed: count == 1,
				Detail: fmt.Sprintf("active_count=%d (expected 1), topicKey=%s", count, tc.topicKey),
			},
			CheckResult{
				Name:   tc.label + ": content matches V3",
				Passed: contentOK,
				Detail: fmt.Sprintf("content_match=%v, err=%v", contentOK, err),
			},
		)
	}

	return checks
}

// ────────────────────────────────────────────────────────────────────────────
// Scenario C: Contradiction after consolidation (10 cases)
// Save V1 (high importance) → run pipeline → Save V2 → Invalidate V1 →
// verify V2 ranks above V1, V1 is stale, supersedes link exists.
// ────────────────────────────────────────────────────────────────────────────

type consolidationCase struct {
	label   string
	v1Title string
	v1Body  string
	v2Title string
	v2Body  string
	query   string
}

func consolidationCasesC() []consolidationCase {
	return []consolidationCase{
		{
			label:   "C1",
			v1Title: "Search indexing strategy",
			v1Body:  "Full-text search implemented with Elasticsearch cluster",
			v2Title: "Search indexing strategy",
			v2Body:  "Replaced Elasticsearch with Typesense for simpler ops",
			query:   "search indexing full-text",
		},
		{
			label:   "C2",
			v1Title: "Secret management",
			v1Body:  "Secrets stored in environment variables in .env files",
			v2Title: "Secret management",
			v2Body:  "Secrets migrated to AWS Secrets Manager, .env removed",
			query:   "secret management credentials vault",
		},
		{
			label:   "C3",
			v1Title: "Service mesh",
			v1Body:  "No service mesh, direct service-to-service calls",
			v2Title: "Service mesh",
			v2Body:  "Istio service mesh deployed for traffic management",
			query:   "service mesh traffic routing",
		},
		{
			label:   "C4",
			v1Title: "Container registry",
			v1Body:  "Docker Hub used for container image storage",
			v2Title: "Container registry",
			v2Body:  "Migrated to ECR (Elastic Container Registry) for private images",
			query:   "container image registry Docker",
		},
		{
			label:   "C5",
			v1Title: "TLS termination",
			v1Body:  "TLS terminated at load balancer using ACM certificates",
			v2Title: "TLS termination",
			v2Body:  "TLS now handled by Nginx sidecar with Let's Encrypt",
			query:   "TLS certificate termination HTTPS",
		},
		{
			label:   "C6",
			v1Title: "Monitoring stack",
			v1Body:  "Monitoring via CloudWatch alarms and dashboards",
			v2Title: "Monitoring stack",
			v2Body:  "Replaced CloudWatch with Prometheus and Grafana stack",
			query:   "monitoring metrics Prometheus Grafana",
		},
		{
			label:   "C7",
			v1Title: "Feature flag system",
			v1Body:  "Feature flags via custom config file checked into git",
			v2Title: "Feature flag system",
			v2Body:  "Feature flags managed through LaunchDarkly service",
			query:   "feature flag toggle management",
		},
		{
			label:   "C8",
			v1Title: "Background job scheduler",
			v1Body:  "Cron jobs defined in crontab on application server",
			v2Title: "Background job scheduler",
			v2Body:  "Cron migrated to BullMQ queues with Redis, crontab removed",
			query:   "background job scheduler cron queue",
		},
		{
			label:   "C9",
			v1Title: "E2E test tool",
			v1Body:  "End-to-end tests written with Selenium WebDriver",
			v2Title: "E2E test tool",
			v2Body:  "Replaced Selenium with Playwright for E2E testing",
			query:   "end-to-end test browser automation",
		},
		{
			label:   "C10",
			v1Title: "GraphQL implementation",
			v1Body:  "GraphQL API built with Apollo Server v3",
			v2Title: "GraphQL implementation",
			v2Body:  "Migrated to Pothos GraphQL builder, Apollo Server removed",
			query:   "GraphQL API server schema builder",
		},
	}
}

func (d CogKnowledgeUpdate) scenarioC(ctx context.Context, env *BenchEnv, errs *[]string) []CheckResult {
	cases := consolidationCasesC()
	checks := make([]CheckResult, 0, len(cases)*3)

	for _, tc := range cases {
		// 1. Save V1 with high importance (confidence drives importance)
		v1, err := env.ObsStore.Save(ctx, observation.Observation{
			Title:           tc.v1Title,
			Content:         tc.v1Body,
			ObservationType: observation.ObservationTypeDecision,
			Kind:            observation.KindSemantic,
			Namespace:       "benchmark",
			Confidence:      0.8,
			Retention:       observation.RetentionDurable,
		})
		if err != nil {
			*errs = append(*errs, fmt.Sprintf("%s: save V1 failed: %v", tc.label, err))
			checks = append(checks,
				CheckResult{Name: tc.label + ": V2 ranks above V1", Passed: false, Detail: "save V1 failed"},
				CheckResult{Name: tc.label + ": V1 is stale", Passed: false, Detail: "save V1 failed"},
				CheckResult{Name: tc.label + ": supersedes link exists", Passed: false, Detail: "save V1 failed"},
			)
			continue
		}

		// 2. Run consolidation pipeline
		if err := env.Pipeline.Run(ctx); err != nil {
			// Non-fatal: log and continue — pipeline failures don't prevent the test from running
			*errs = append(*errs, fmt.Sprintf("%s: pipeline.Run warning: %v", tc.label, err))
		}

		// 3. Save V2 (fresh observation about same topic)
		v2, err := env.ObsStore.Save(ctx, observation.Observation{
			Title:           tc.v2Title,
			Content:         tc.v2Body,
			ObservationType: observation.ObservationTypeDecision,
			Kind:            observation.KindSemantic,
			Namespace:       "benchmark",
			Confidence:      0.9,
			Retention:       observation.RetentionDurable,
		})
		if err != nil {
			*errs = append(*errs, fmt.Sprintf("%s: save V2 failed: %v", tc.label, err))
			checks = append(checks,
				CheckResult{Name: tc.label + ": V2 ranks above V1", Passed: false, Detail: "save V2 failed"},
				CheckResult{Name: tc.label + ": V1 is stale", Passed: false, Detail: "save V2 failed"},
				CheckResult{Name: tc.label + ": supersedes link exists", Passed: false, Detail: "save V2 failed"},
			)
			continue
		}

		// 4. Invalidate V1 with V2's info as replacement
		invResult, err := env.ObsStore.Invalidate(ctx, env.LinkStore, observation.InvalidateInput{
			ObservationID:      v1.ID,
			Reason:             "superseded by newer decision",
			ReplacementTitle:   tc.v2Title,
			ReplacementContent: tc.v2Body,
		})
		if err != nil {
			*errs = append(*errs, fmt.Sprintf("%s: invalidate failed: %v", tc.label, err))
			checks = append(checks,
				CheckResult{Name: tc.label + ": V2 ranks above V1", Passed: false, Detail: "invalidate failed"},
				CheckResult{Name: tc.label + ": V1 is stale", Passed: false, Detail: "invalidate failed"},
				CheckResult{Name: tc.label + ": supersedes link exists", Passed: false, Detail: "invalidate failed"},
			)
			continue
		}
		replacementID := invResult.ReplacementID

		// 5. Search and collect results
		results, err := env.RecallEngine.Search(ctx, recall.SearchOptions{
			Query:     tc.query,
			Namespace: "benchmark",
			Limit:     20,
		})
		if err != nil {
			*errs = append(*errs, fmt.Sprintf("%s: search failed: %v", tc.label, err))
			checks = append(checks,
				CheckResult{Name: tc.label + ": V2 ranks above V1", Passed: false, Detail: "search failed"},
				CheckResult{Name: tc.label + ": V1 is stale", Passed: false, Detail: "search failed"},
				CheckResult{Name: tc.label + ": supersedes link exists", Passed: false, Detail: "search failed"},
			)
			continue
		}

		// V2 is the direct Save + the replacement created by Invalidate; either ID is acceptable.
		v2Ranks := -1
		v1Ranks := -1
		for i, r := range results {
			if r.ID == v2.ID || r.ID == replacementID {
				if v2Ranks < 0 {
					v2Ranks = i
				}
			}
			if r.ID == v1.ID {
				v1Ranks = i
			}
		}

		// V2 ranks above V1: either V1 absent (excluded as stale) OR V2 at lower index.
		v2AboveV1 := v2Ranks >= 0 && (v1Ranks < 0 || v2Ranks < v1Ranks)

		// 6. Verify V1 staleness via DB
		var staleness string
		_ = env.DB.QueryRowContext(ctx,
			`SELECT staleness FROM observations WHERE id = ?`, v1.ID,
		).Scan(&staleness)
		v1Stale := staleness == "stale"

		// 7. Verify supersedes link from replacement → V1
		linkFound := false
		if replacementID != "" {
			lks, linkErr := env.LinkStore.GetBySource(ctx, replacementID)
			if linkErr == nil {
				for _, lk := range lks {
					if lk.RelationType == links.RelationSupersedes && lk.TargetID == v1.ID {
						linkFound = true
						break
					}
				}
			}
		}

		checks = append(checks,
			CheckResult{
				Name:   tc.label + ": V2 ranks above V1",
				Passed: v2AboveV1,
				Detail: fmt.Sprintf("v2_rank=%d v1_rank=%d results=%d", v2Ranks, v1Ranks, len(results)),
			},
			CheckResult{
				Name:   tc.label + ": V1 is stale",
				Passed: v1Stale,
				Detail: fmt.Sprintf("v1_staleness=%q", staleness),
			},
			CheckResult{
				Name:   tc.label + ": supersedes link exists",
				Passed: linkFound,
				Detail: fmt.Sprintf("replacementID=%s v1ID=%s link_found=%v", replacementID, v1.ID, linkFound),
			},
		)
	}

	return checks
}

// ────────────────────────────────────────────────────────────────────────────
// Scenario D: Partial update ranking — no invalidation, newer ranks higher (5 cases)
// ────────────────────────────────────────────────────────────────────────────

type partialUpdateCase struct {
	label   string
	v1Title string
	v1Body  string
	v2Title string
	v2Body  string
	query   string
}

func partialUpdateCasesD() []partialUpdateCase {
	return []partialUpdateCase{
		{
			label:   "D1",
			v1Title: "JWT signing algorithm",
			v1Body:  "Auth uses JWT with RS256 signing algorithm",
			v2Title: "JWT signing algorithm update",
			v2Body:  "Auth now uses JWT with ES256, changed from RS256 for better performance",
			query:   "JWT signing algorithm",
		},
		{
			label:   "D2",
			v1Title: "Connection pool size",
			v1Body:  "Database connection pool configured with max 10 connections",
			v2Title: "Connection pool size update",
			v2Body:  "Database connection pool increased to max 50 connections after load testing",
			query:   "database connection pool size",
		},
		{
			label:   "D3",
			v1Title: "Image upload limit",
			v1Body:  "User uploads limited to 5 MB per image",
			v2Title: "Image upload limit update",
			v2Body:  "User upload limit raised to 20 MB after CDN migration, changed from 5 MB",
			query:   "image upload size limit",
		},
		{
			label:   "D4",
			v1Title: "Rate limiting policy",
			v1Body:  "API rate limited to 100 requests per minute per IP",
			v2Title: "Rate limiting policy update",
			v2Body:  "Rate limit adjusted to 500 requests per minute per user token, replaces IP-based limit",
			query:   "API rate limiting requests policy",
		},
		{
			label:   "D5",
			v1Title: "Session token expiry",
			v1Body:  "Session tokens expire after 24 hours",
			v2Title: "Session token expiry update",
			v2Body:  "Session tokens now expire after 7 days with rolling refresh, changed from 24 hours",
			query:   "session token expiry duration",
		},
	}
}

func (d CogKnowledgeUpdate) scenarioD(ctx context.Context, env *BenchEnv, errs *[]string) []CheckResult {
	cases := partialUpdateCasesD()
	checks := make([]CheckResult, 0, len(cases))

	for _, tc := range cases {
		// 1. Save V1
		v1, err := env.ObsStore.Save(ctx, observation.Observation{
			Title:           tc.v1Title,
			Content:         tc.v1Body,
			ObservationType: observation.ObservationTypeDecision,
			Kind:            observation.KindSemantic,
			Namespace:       "benchmark",
			Retention:       observation.RetentionDurable,
		})
		if err != nil {
			*errs = append(*errs, fmt.Sprintf("%s: save V1 failed: %v", tc.label, err))
			checks = append(checks, CheckResult{Name: tc.label + ": V2 ranks higher than V1", Passed: false, Detail: "save V1 failed"})
			continue
		}

		// 2. Save V2 (intentionally NO invalidation of V1)
		v2, err := env.ObsStore.Save(ctx, observation.Observation{
			Title:           tc.v2Title,
			Content:         tc.v2Body,
			ObservationType: observation.ObservationTypeDecision,
			Kind:            observation.KindSemantic,
			Namespace:       "benchmark",
			Retention:       observation.RetentionDurable,
		})
		if err != nil {
			*errs = append(*errs, fmt.Sprintf("%s: save V2 failed: %v", tc.label, err))
			checks = append(checks, CheckResult{Name: tc.label + ": V2 ranks higher than V1", Passed: false, Detail: "save V2 failed"})
			continue
		}

		// 3. Search — both observations exist; V2 should rank higher (recency scoring)
		results, err := env.RecallEngine.Search(ctx, recall.SearchOptions{
			Query:     tc.query,
			Namespace: "benchmark",
			Limit:     20,
		})
		if err != nil {
			*errs = append(*errs, fmt.Sprintf("%s: search failed: %v", tc.label, err))
			checks = append(checks, CheckResult{Name: tc.label + ": V2 ranks higher than V1", Passed: false, Detail: "search failed"})
			continue
		}

		v2Rank := -1
		v1Rank := -1
		for i, r := range results {
			if r.ID == v2.ID {
				v2Rank = i
			}
			if r.ID == v1.ID {
				v1Rank = i
			}
		}

		// V2 ranks above V1: both present and V2 at earlier (lower) index.
		// If V1 is absent but V2 is present, that also counts as V2 ranking higher.
		passed := v2Rank >= 0 && (v1Rank < 0 || v2Rank < v1Rank)

		checks = append(checks, CheckResult{
			Name:   tc.label + ": V2 ranks higher than V1",
			Passed: passed,
			Detail: fmt.Sprintf("v2_rank=%d v1_rank=%d results=%d v2ID=%s v1ID=%s", v2Rank, v1Rank, len(results), v2.ID, v1.ID),
		})
	}

	return checks
}

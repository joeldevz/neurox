package benchmark

import (
	"context"
	"fmt"

	"neurox/internal/classify"
	"neurox/internal/observation"
	"neurox/internal/recall"
)

// CogSignalNoise benchmarks the brain's ability to distinguish high-value
// durable observations from operational noise in four scenarios:
//
//	A. Needle in Haystack — 5 critical signals among 200 noise observations
//	B. Importance-Weighted Recall — decisions surface above low-confidence logs
//	C. Retention Classification Accuracy — InferRetention heuristic coverage
//	D. Consolidation Preserves Signal — durable observations survive epochs intact
type CogSignalNoise struct{}

func (d CogSignalNoise) Name() string     { return "Signal vs Noise" }
func (d CogSignalNoise) Category() string { return "cognitive" }

func (d CogSignalNoise) Run(ctx context.Context, env *BenchEnv) DimensionResult {
	var checks []CheckResult
	var errs []string

	signalsInTop10 := 0
	classificationAccuracy := 0.0

	scenarioAChecks, sigCount := d.scenarioA(ctx, env, &errs)
	checks = append(checks, scenarioAChecks...)
	signalsInTop10 = sigCount

	checks = append(checks, d.scenarioB(ctx, env, &errs)...)

	scenarioCChecks, classAcc := d.scenarioC(&errs)
	checks = append(checks, scenarioCChecks...)
	classificationAccuracy = classAcc

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
			"checks_passed":           float64(passed),
			"checks_total":            float64(len(checks)),
			"recall_rate":             rawRate,
			"signals_in_top10":        float64(signalsInTop10),
			"classification_accuracy": classificationAccuracy,
		},
		Errors: errs,
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Scenario A: Needle in Haystack
// Save 200 noise observations + 5 durable signals → consolidate →
// get proactive context → count how many signals appear in top-10.
// ────────────────────────────────────────────────────────────────────────────

func (d CogSignalNoise) scenarioA(ctx context.Context, env *BenchEnv, errs *[]string) ([]CheckResult, int) {
	const ns = "bench-signal"

	// 1. Save 200 operational noise observations.
	noise := GenerateNoise(200, ns)
	for _, obs := range noise {
		if _, err := env.ObsStore.Save(ctx, obs); err != nil {
			*errs = append(*errs, fmt.Sprintf("A: save noise failed: %v", err))
		}
	}

	// 2. Save 5 critical durable observations (signals).
	signals := []observation.Observation{
		{
			Title:           "Architecture: microservices with event sourcing",
			Content:         "Decision: adopt microservices architecture with event sourcing for order processing. Events stored in Postgres, projected to read models.",
			ObservationType: observation.ObservationTypeDecision,
			Kind:            observation.KindSemantic,
			Namespace:       ns,
			Retention:       observation.RetentionDurable,
			Confidence:      0.95,
		},
		{
			Title:           "Gotcha: Redis cluster failover loses 1-2 seconds of writes",
			Content:         "GOTCHA: During Redis Sentinel failover, writes are silently dropped for 1-2 seconds. Must implement write-behind queue for critical operations.",
			ObservationType: observation.ObservationTypeGotcha,
			Kind:            observation.KindProcedural,
			Namespace:       ns,
			Retention:       observation.RetentionDurable,
			Confidence:      0.9,
		},
		{
			Title:           "Preference: always use structured logging with correlation IDs",
			Content:         "PREFERENCE: All services must use structured JSON logging with correlation-id header propagated across service calls. Use pino for Node.js services.",
			ObservationType: observation.ObservationTypePreference,
			Kind:            observation.KindProcedural,
			Namespace:       ns,
			Retention:       observation.RetentionDurable,
			Confidence:      0.9,
		},
		{
			Title:           "Pattern: circuit breaker for external API calls",
			Content:         "PATTERN: All external HTTP calls wrapped in circuit breaker (opossum lib). Open after 5 failures in 30s. Half-open test every 10s. Prevents cascade failures.",
			ObservationType: observation.ObservationTypePattern,
			Kind:            observation.KindProcedural,
			Namespace:       ns,
			Retention:       observation.RetentionDurable,
			Confidence:      0.9,
		},
		{
			Title:           "Config: Kubernetes pod resource limits standard",
			Content:         "CONFIG: All pods must set resource requests AND limits. Default: requests 100m CPU / 128Mi RAM, limits 500m CPU / 512Mi RAM. OOMKilled pods without limits caused outages.",
			ObservationType: observation.ObservationTypeConfig,
			Kind:            observation.KindProcedural,
			Namespace:       ns,
			Retention:       observation.RetentionDurable,
			Confidence:      0.9,
		},
	}

	signalIDs := make([]string, 0, len(signals))
	allSaved := true
	for _, sig := range signals {
		saved, err := env.ObsStore.Save(ctx, sig)
		if err != nil {
			*errs = append(*errs, fmt.Sprintf("A: save signal failed: %v", err))
			allSaved = false
			continue
		}
		signalIDs = append(signalIDs, saved.ID)
	}

	if !allSaved {
		checks := []CheckResult{
			{Name: "A: ≥3 of 5 signals in top-10 context", Passed: false, Detail: "signal save failed"},
			{Name: "A: ≥4 of 5 signals in top-10 context", Passed: false, Detail: "signal save failed"},
			{Name: "A: all 5 signals in top-10 context", Passed: false, Detail: "signal save failed"},
		}
		return checks, 0
	}

	// 3. Run consolidation — promotes durable (importance >= 0.3) to Working.
	if err := env.Pipeline.Run(ctx); err != nil {
		*errs = append(*errs, fmt.Sprintf("A: pipeline.Run warning: %v", err))
	}

	// 4. Get proactive context (top 20 items).
	ctxResult, err := env.ProactiveEngine.GetContext(ctx, ns, nil, 20)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("A: GetContext failed: %v", err))
		checks := []CheckResult{
			{Name: "A: ≥3 of 5 signals in top-10 context", Passed: false, Detail: "GetContext failed"},
			{Name: "A: ≥4 of 5 signals in top-10 context", Passed: false, Detail: "GetContext failed"},
			{Name: "A: all 5 signals in top-10 context", Passed: false, Detail: "GetContext failed"},
		}
		return checks, 0
	}

	// 5. Count how many signal IDs appear in the top-10 context items.
	top10 := ctxResult.Items
	if len(top10) > 10 {
		top10 = top10[:10]
	}

	signalIDSet := make(map[string]bool, len(signalIDs))
	for _, id := range signalIDs {
		signalIDSet[id] = true
	}

	found := 0
	for _, item := range top10 {
		if signalIDSet[item.ID] {
			found++
		}
	}

	checks := []CheckResult{
		{
			Name:   "A: ≥3 of 5 signals in top-10 context",
			Passed: found >= 3,
			Detail: fmt.Sprintf("signals_found=%d/5 in top-%d context (total_items=%d)", found, len(top10), len(ctxResult.Items)),
		},
		{
			Name:   "A: ≥4 of 5 signals in top-10 context",
			Passed: found >= 4,
			Detail: fmt.Sprintf("signals_found=%d/5 in top-%d context", found, len(top10)),
		},
		{
			Name:   "A: all 5 signals in top-10 context",
			Passed: found == 5,
			Detail: fmt.Sprintf("signals_found=%d/5 in top-%d context", found, len(top10)),
		},
	}
	return checks, found
}

// ────────────────────────────────────────────────────────────────────────────
// Scenario B: Importance-Weighted Recall (5 topic domains)
// For each topic: save 20 low-importance ops + 2 high-importance decisions →
// search → check that at least 1 decision appears in top-3 results.
// ────────────────────────────────────────────────────────────────────────────

type signalTopic struct {
	label     string
	domain    string
	query     string
	decision1 struct{ title, content string }
	decision2 struct{ title, content string }
}

func signalTopics() []signalTopic {
	return []signalTopic{
		{
			label:  "B1",
			domain: "authentication",
			query:  "authentication JWT session",
			decision1: struct{ title, content string }{
				title:   "Decision: JWT with RS256 for authentication",
				content: "DECISION: All services use JWT bearer tokens signed with RS256. 1h expiry, refresh tokens in httpOnly cookies. Stateless, scalable across pods.",
			},
			decision2: struct{ title, content string }{
				title:   "Decision: session management with Redis store",
				content: "DECISION: Session data stored in Redis with 24h TTL. Session ID in httpOnly cookie. Invalidation on logout deletes Redis key immediately.",
			},
		},
		{
			label:  "B2",
			domain: "database",
			query:  "database PostgreSQL migration strategy",
			decision1: struct{ title, content string }{
				title:   "Decision: PostgreSQL with pgBouncer connection pooling",
				content: "DECISION: PostgreSQL 16 as primary DB. pgBouncer in transaction mode, pool size 50. Max idle connections per pod: 5. Prevents pool exhaustion under load.",
			},
			decision2: struct{ title, content string }{
				title:   "Decision: database migration strategy with golang-migrate",
				content: "DECISION: All schema changes via golang-migrate numbered migrations. Migrations run on app startup in staging, manual approval in production. No down migrations.",
			},
		},
		{
			label:  "B3",
			domain: "caching",
			query:  "cache Redis invalidation strategy",
			decision1: struct{ title, content string }{
				title:   "Decision: Redis for distributed caching with TTL strategy",
				content: "DECISION: Redis 7 cluster for all distributed caches. Cache keys prefixed by service. Default TTL: 5 minutes. User-specific caches invalidated on write. No cache stampede via Lua lock.",
			},
			decision2: struct{ title, content string }{
				title:   "Decision: cache invalidation via event-driven purge",
				content: "DECISION: Cache invalidation driven by domain events (EventBridge). On data mutation, downstream service publishes event. Cache consumers purge affected keys. Eventual consistency accepted.",
			},
		},
		{
			label:  "B4",
			domain: "deployment",
			query:  "Kubernetes deployment CI/CD pipeline",
			decision1: struct{ title, content string }{
				title:   "Decision: Kubernetes with Helm charts for deployment",
				content: "DECISION: All services deployed on Kubernetes via Helm charts. GitOps with ArgoCD. Namespace per environment (dev, staging, prod). Resource limits enforced via LimitRange.",
			},
			decision2: struct{ title, content string }{
				title:   "Decision: CI/CD with GitHub Actions and environment gates",
				content: "DECISION: GitHub Actions for CI/CD. Push to main triggers staging deploy automatically. Production deploys require manual approval via environment protection rules. Rollback: kubectl rollout undo.",
			},
		},
		{
			label:  "B5",
			domain: "monitoring",
			query:  "monitoring Datadog alerting observability",
			decision1: struct{ title, content string }{
				title:   "Decision: Datadog for APM and infrastructure monitoring",
				content: "DECISION: Datadog as primary observability platform. APM traces on all services. Infrastructure metrics via Datadog agent. Dashboards per service. SLO monitors for p99 latency and error rate.",
			},
			decision2: struct{ title, content string }{
				title:   "Decision: alerting strategy with PagerDuty escalation",
				content: "DECISION: Datadog alerts route to PagerDuty. P1 (service down) → immediate page. P2 (SLO breach) → 5min delay. P3 (warning) → Slack only. On-call rotation: 1 week per engineer.",
			},
		},
	}
}

func (d CogSignalNoise) scenarioB(ctx context.Context, env *BenchEnv, errs *[]string) []CheckResult {
	topics := signalTopics()
	checks := make([]CheckResult, 0, len(topics))

	for _, tc := range topics {
		ns := "bench-signal-b"

		// 1. Save 20 low-importance operational observations about the topic.
		for i := 0; i < 20; i++ {
			title := fmt.Sprintf("%s check log #%d", tc.domain, i+1)
			content := fmt.Sprintf("%s debug trace entry %d: operation completed, status ok, no anomalies detected.", tc.domain, i+1)
			if i%3 == 1 {
				title = fmt.Sprintf("%s debug trace #%d", tc.domain, i+1)
				content = fmt.Sprintf("Trace %d: %s subsystem processed request in 12ms, response 200 OK.", i+1, tc.domain)
			} else if i%3 == 2 {
				title = fmt.Sprintf("%s metric report #%d", tc.domain, i+1)
				content = fmt.Sprintf("Report %d: %s metrics within normal bounds, p50=8ms p95=42ms.", i+1, tc.domain)
			}
			_, err := env.ObsStore.Save(ctx, observation.Observation{
				Title:           title,
				Content:         content,
				ObservationType: observation.ObservationTypeDiscovery,
				Kind:            observation.KindEpisodic,
				Namespace:       ns,
				Retention:       observation.RetentionOperational,
				Confidence:      0.4,
			})
			if err != nil {
				*errs = append(*errs, fmt.Sprintf("%s: save noise #%d failed: %v", tc.label, i, err))
			}
		}

		// 2. Save 2 high-importance decision observations.
		dec1, err := env.ObsStore.Save(ctx, observation.Observation{
			Title:           tc.decision1.title,
			Content:         tc.decision1.content,
			ObservationType: observation.ObservationTypeDecision,
			Kind:            observation.KindSemantic,
			Namespace:       ns,
			Retention:       observation.RetentionDurable,
			Confidence:      0.95,
		})
		if err != nil {
			*errs = append(*errs, fmt.Sprintf("%s: save decision1 failed: %v", tc.label, err))
			checks = append(checks, CheckResult{
				Name:   tc.label + ": decision in top-3 recall",
				Passed: false,
				Detail: "decision1 save failed",
			})
			continue
		}

		dec2, err := env.ObsStore.Save(ctx, observation.Observation{
			Title:           tc.decision2.title,
			Content:         tc.decision2.content,
			ObservationType: observation.ObservationTypeDecision,
			Kind:            observation.KindSemantic,
			Namespace:       ns,
			Retention:       observation.RetentionDurable,
			Confidence:      0.95,
		})
		if err != nil {
			*errs = append(*errs, fmt.Sprintf("%s: save decision2 failed: %v", tc.label, err))
			checks = append(checks, CheckResult{
				Name:   tc.label + ": decision in top-3 recall",
				Passed: false,
				Detail: "decision2 save failed",
			})
			continue
		}

		// 3. Search for the topic.
		results, err := env.RecallEngine.Search(ctx, recall.SearchOptions{
			Query:     tc.query,
			Namespace: ns,
			Limit:     10,
		})
		if err != nil {
			*errs = append(*errs, fmt.Sprintf("%s: search failed: %v", tc.label, err))
			checks = append(checks, CheckResult{
				Name:   tc.label + ": decision in top-3 recall",
				Passed: false,
				Detail: "search failed",
			})
			continue
		}

		// 4. Check: at least 1 of the 2 decision IDs appears in top-3 results.
		decisionIDs := map[string]bool{dec1.ID: true, dec2.ID: true}
		foundInTop3 := false
		top3count := 3
		if len(results) < top3count {
			top3count = len(results)
		}
		for i := 0; i < top3count; i++ {
			if decisionIDs[results[i].ID] {
				foundInTop3 = true
				break
			}
		}

		checks = append(checks, CheckResult{
			Name:   tc.label + ": decision in top-3 recall",
			Passed: foundInTop3,
			Detail: fmt.Sprintf("domain=%s results=%d dec1=%s dec2=%s", tc.domain, len(results), dec1.ID, dec2.ID),
		})
	}

	return checks
}

// ────────────────────────────────────────────────────────────────────────────
// Scenario C: Retention Classification Accuracy (50 cases)
// Tests InferRetention() heuristics directly — no DB involved.
// ────────────────────────────────────────────────────────────────────────────

type classificationCase struct {
	title    string
	obsType  observation.ObservationType
	source   string
	expected observation.Retention
}

func classificationCases() []classificationCase {
	op := observation.RetentionOperational
	dur := observation.RetentionDurable

	return []classificationCase{
		// source="consolidator" → operational (5 cases)
		{"Implement Step 1: setup database", observation.ObservationTypeDiscovery, "consolidator", op},
		{"Step 2: wire up handlers", observation.ObservationTypeDecision, "consolidator", op},
		{"Plan completed successfully", observation.ObservationTypePattern, "consolidator", op},
		{"Build flags configured for FTS5", observation.ObservationTypeConfig, "consolidator", op},
		{"File observations indexed for recall", observation.ObservationTypeDiscovery, "consolidator", op},

		// source="reflection" → durable (5 cases)
		{"Reflection on architecture patterns", observation.ObservationTypePattern, "reflection", dur},
		{"Reflection: database access conventions", observation.ObservationTypeDiscovery, "reflection", dur},
		{"Reflection: error handling insights", observation.ObservationTypePreference, "reflection", dur},
		{"Reflection: testing strategy synthesis", observation.ObservationTypeDecision, "reflection", dur},
		{"Reflection: deployment gotchas", observation.ObservationTypeGotcha, "reflection", dur},

		// Title matches step patterns → operational (10 cases)
		{"Implement Step 3: add signal noise dimension", observation.ObservationTypeDiscovery, "", op},
		{"Step 4: wire consolidation pipeline", observation.ObservationTypeDiscovery, "", op},
		{"Plan completed: benchmark suite done", observation.ObservationTypeDecision, "", op},
		{"Build flags: enable CGO and FTS5 tags", observation.ObservationTypeConfig, "", op},
		{"File observations linked to source files", observation.ObservationTypeDiscovery, "", op},
		{"Embeddings queue imported and wired", observation.ObservationTypeDiscovery, "", op},
		{"Step 7 implemented temporal metadata", observation.ObservationTypeDiscovery, "", op},
		{"Implement Step 9: add benchmarks", observation.ObservationTypeDiscovery, "", op},
		{"Renamed local variables in handlers", observation.ObservationTypeDiscovery, "", op},
		{"Named return values fixed in MCP handlers", observation.ObservationTypeDiscovery, "", op},

		// type=decision → durable (5 cases)
		{"Authentication strategy chosen", observation.ObservationTypeDecision, "", dur},
		{"Database selected: PostgreSQL", observation.ObservationTypeDecision, "", dur},
		{"API versioning approach decided", observation.ObservationTypeDecision, "", dur},
		{"Monorepo structure approved by team", observation.ObservationTypeDecision, "", dur},
		{"Cache invalidation strategy agreed", observation.ObservationTypeDecision, "", dur},

		// type=gotcha → durable (5 cases)
		{"Gotcha: Redis KEYS blocks event loop", observation.ObservationTypeGotcha, "", dur},
		{"Gotcha: CORS preflight requires explicit headers", observation.ObservationTypeGotcha, "", dur},
		{"Gotcha: JWT rotation race condition possible", observation.ObservationTypeGotcha, "", dur},
		{"Gotcha: pg pool exhaustion under concurrent load", observation.ObservationTypeGotcha, "", dur},
		{"Gotcha: Docker build cache invalidated by ENV", observation.ObservationTypeGotcha, "", dur},

		// type=pattern → durable (5 cases)
		{"Pattern: repository pattern for DB access", observation.ObservationTypePattern, "", dur},
		{"Pattern: circuit breaker for external calls", observation.ObservationTypePattern, "", dur},
		{"Pattern: optimistic UI updates in React", observation.ObservationTypePattern, "", dur},
		{"Pattern: event sourcing for order domain", observation.ObservationTypePattern, "", dur},
		{"Pattern: saga for distributed transactions", observation.ObservationTypePattern, "", dur},

		// type=preference → durable (5 cases)
		{"Preference: named exports over default exports", observation.ObservationTypePreference, "", dur},
		{"Preference: Result type for error handling", observation.ObservationTypePreference, "", dur},
		{"Preference: structured logging with correlation ID", observation.ObservationTypePreference, "", dur},
		{"Preference: commit message conventional format", observation.ObservationTypePreference, "", dur},
		{"Preference: snake_case for database columns", observation.ObservationTypePreference, "", dur},

		// type=bugfix → durable (3 cases)
		{"Bug: null pointer in session handler fixed", observation.ObservationTypeBugfix, "", dur},
		{"Bug: race condition in token refresh resolved", observation.ObservationTypeBugfix, "", dur},
		{"Bug: missing index causes slow query fixed", observation.ObservationTypeBugfix, "", dur},

		// type=discovery → durable (3 cases)
		{"Discovery: BullMQ job deduplication via jobId", observation.ObservationTypeDiscovery, "", dur},
		{"Discovery: Zod schemas shareable across packages", observation.ObservationTypeDiscovery, "", dur},
		{"Discovery: pgvector requires superuser for extension", observation.ObservationTypeDiscovery, "", dur},

		// type=config → durable (2 cases)
		{"Config: environment variable naming convention", observation.ObservationTypeConfig, "", dur},
		{"Config: Kubernetes pod resource limits standard", observation.ObservationTypeConfig, "", dur},

		// type=question → durable (2 cases)
		{"Question: should we adopt GraphQL federation?", observation.ObservationTypeQuestion, "", dur},
		{"Question: what retry strategy for external APIs?", observation.ObservationTypeQuestion, "", dur},
	}
}

func (d CogSignalNoise) scenarioC(errs *[]string) ([]CheckResult, float64) {
	cases := classificationCases()
	checks := make([]CheckResult, 0, len(cases))

	passed := 0
	for i, tc := range cases {
		title := tc.title
		displayTitle := title
		if len(displayTitle) > 30 {
			displayTitle = displayTitle[:30]
		}

		got := classify.InferRetention(title, tc.obsType, tc.source)
		ok := got == tc.expected

		if ok {
			passed++
		}

		checks = append(checks, CheckResult{
			Name:   fmt.Sprintf("C%d: %s → %s", i+1, displayTitle, tc.expected),
			Passed: ok,
			Detail: fmt.Sprintf("expected %s, got %s (type=%s source=%q)", tc.expected, got, tc.obsType, tc.source),
		})
	}

	accuracy := 0.0
	if len(cases) > 0 {
		accuracy = float64(passed) / float64(len(cases)) * 100
	}

	return checks, accuracy
}

// ────────────────────────────────────────────────────────────────────────────
// Scenario D: Consolidation Preserves Signal
// Save 50 durable + 50 operational → run 5 consolidation epochs →
// verify durable avg importance > operational avg importance.
// ────────────────────────────────────────────────────────────────────────────

func (d CogSignalNoise) scenarioD(ctx context.Context, env *BenchEnv, errs *[]string) []CheckResult {
	const ns = "bench-signal-d"

	// 1. Save 50 high-importance durable observations.
	durableTypes := []observation.ObservationType{
		observation.ObservationTypeDecision,
		observation.ObservationTypePattern,
		observation.ObservationTypeGotcha,
	}
	for i := 0; i < 50; i++ {
		obsType := durableTypes[i%len(durableTypes)]
		_, err := env.ObsStore.Save(ctx, observation.Observation{
			Title:           fmt.Sprintf("Durable signal %d: architecture decision for subsystem %d", i+1, i%10),
			Content:         fmt.Sprintf("DECISION %d: important architectural choice for the system. This captures durable knowledge that should persist across consolidation cycles. Index: %d.", i+1, i),
			ObservationType: obsType,
			Kind:            observation.KindSemantic,
			Namespace:       ns,
			Retention:       observation.RetentionDurable,
			Confidence:      0.9,
		})
		if err != nil {
			*errs = append(*errs, fmt.Sprintf("D: save durable #%d failed: %v", i+1, err))
		}
	}

	// 2. Save 50 low-importance operational observations.
	for i := 0; i < 50; i++ {
		_, err := env.ObsStore.Save(ctx, observation.Observation{
			Title:           fmt.Sprintf("Build step %d completed", i+1),
			Content:         fmt.Sprintf("Step %d operational log: ephemeral trace entry, no long-term value.", i+1),
			ObservationType: observation.ObservationTypeDiscovery,
			Kind:            observation.KindEpisodic,
			Namespace:       ns,
			Retention:       observation.RetentionOperational,
			Confidence:      0.4,
		})
		if err != nil {
			*errs = append(*errs, fmt.Sprintf("D: save operational #%d failed: %v", i+1, err))
		}
	}

	// 3. Run 5 consolidation epochs.
	for i := 0; i < 5; i++ {
		if err := env.Pipeline.Run(ctx); err != nil {
			*errs = append(*errs, fmt.Sprintf("D: pipeline epoch %d warning: %v", i+1, err))
		}
	}

	// 4. Query average importance per retention type for surviving observations.
	type retentionStats struct {
		retention  string
		avgImp     float64
		hasResults bool
	}

	statsMap := make(map[string]*retentionStats)

	rows, err := env.DB.QueryContext(ctx, `
		SELECT retention, AVG(importance) as avg_imp
		FROM observations
		WHERE namespace = ? AND deleted_at IS NULL
		GROUP BY retention
	`, ns)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("D: stats query failed: %v", err))
		return []CheckResult{
			{Name: "D: durable avg importance > operational avg importance", Passed: false, Detail: "stats query failed"},
			{Name: "D: durable avg importance > 0.3", Passed: false, Detail: "stats query failed"},
			{Name: "D: operational avg importance < durable avg importance", Passed: false, Detail: "stats query failed"},
		}
	}
	defer rows.Close()

	for rows.Next() {
		var ret string
		var avgImp float64
		if err := rows.Scan(&ret, &avgImp); err != nil {
			*errs = append(*errs, fmt.Sprintf("D: stats scan failed: %v", err))
			continue
		}
		statsMap[ret] = &retentionStats{
			retention:  ret,
			avgImp:     avgImp,
			hasResults: true,
		}
	}
	if err := rows.Err(); err != nil {
		*errs = append(*errs, fmt.Sprintf("D: stats rows error: %v", err))
	}

	durableStats, hasDurable := statsMap[string(observation.RetentionDurable)]
	operationalStats, hasOperational := statsMap[string(observation.RetentionOperational)]

	if !hasDurable || !hasOperational {
		detail := fmt.Sprintf("hasDurable=%v hasOperational=%v", hasDurable, hasOperational)
		return []CheckResult{
			{Name: "D: durable avg importance > operational avg importance", Passed: false, Detail: detail},
			{Name: "D: durable avg importance > 0.3", Passed: false, Detail: detail},
			{Name: "D: operational avg importance < durable avg importance", Passed: false, Detail: detail},
		}
	}

	durableAvg := durableStats.avgImp
	operationalAvg := operationalStats.avgImp

	return []CheckResult{
		{
			Name:   "D: durable avg importance > operational avg importance",
			Passed: durableAvg > operationalAvg,
			Detail: fmt.Sprintf("durable_avg=%.4f operational_avg=%.4f", durableAvg, operationalAvg),
		},
		{
			Name:   "D: durable avg importance > 0.3",
			Passed: durableAvg > 0.3,
			Detail: fmt.Sprintf("durable_avg=%.4f threshold=0.3", durableAvg),
		},
		{
			Name:   "D: operational avg importance < durable avg importance",
			Passed: operationalAvg < durableAvg,
			Detail: fmt.Sprintf("operational_avg=%.4f durable_avg=%.4f", operationalAvg, durableAvg),
		},
	}
}

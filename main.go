package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	mcpserver "github.com/mark3labs/mcp-go/server"

	"neurox/internal/api"
	"neurox/internal/config"
	"neurox/internal/consolidate"
	"neurox/internal/db"
	"neurox/internal/decay"
	"neurox/internal/embed"
	"neurox/internal/facts"
	"neurox/internal/links"
	"neurox/internal/llm"
	neuroxmcp "neurox/internal/mcp"
	"neurox/internal/observation"
	"neurox/internal/proactive"
	"neurox/internal/recall"
	reflectpkg "neurox/internal/reflect"
	"neurox/internal/session"
	"neurox/internal/temporal"
)

const (
	version         = "0.1.0"
	defaultHTTPPort = 7438
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if len(os.Args) < 2 {
		printUsage()
		return
	}

	cmd := os.Args[1]

	// Commands that don't need DB
	switch cmd {
	case "install-hook":
		installHook()
		return
	case "version", "--version", "-v":
		fmt.Printf("neurox v%s\n", version)
		return
	case "help", "--help", "-h":
		printUsage()
		return
	}

	cfg, err := config.Load("")
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	database, err := db.Open(ctx, cfg.Database.Path)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			log.Printf("close database: %v", closeErr)
		}
	}()

	switch cmd {
	case "mcp":
		runMCP(ctx, database, cfg)
	case "serve":
		runHTTP(ctx, database, cfg)
	case "save":
		runSave(ctx, database, cfg)
	case "recall":
		runRecall(ctx, database, cfg)
	case "context":
		runContext(ctx, database, cfg)
	case "invalidate":
		runInvalidate(ctx, database, cfg)
	case "status":
		runStatus(ctx, database, cfg)
	case "consolidate":
		runConsolidate(ctx, database, cfg)
	case "config":
		runConfig(cfg)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Printf("neurox v%s — brain-inspired memory engine for AI agents\n\n", version)
	fmt.Println("Usage: neurox <command> [flags]")
	fmt.Println()
	fmt.Println("Server commands:")
	fmt.Println("  mcp              Start MCP server over stdio")
	fmt.Println("  serve            Start HTTP API server")
	fmt.Println()
	fmt.Println("Memory commands:")
	fmt.Println("  save             Save an observation to memory")
	fmt.Println("  recall           Search memories")
	fmt.Println("  context          Get relevant context for a namespace")
	fmt.Println("  invalidate       Mark an observation as incorrect")
	fmt.Println("  status           Show brain statistics")
	fmt.Println("  consolidate      Force immediate consolidation (promote, dedup, reflect)")
	fmt.Println()
	fmt.Println("Setup commands:")
	fmt.Println("  install-hook     Install git post-commit hook")
	fmt.Println("  config           Show current configuration")
	fmt.Println("  version          Show version")
	fmt.Println()
	fmt.Println("Run 'neurox <command> --help' for details on a command.")
}

// --- CLI subcommands ---

func runSave(ctx context.Context, database *sql.DB, cfg config.Config) {
	fs := flag.NewFlagSet("save", flag.ExitOnError)
	content := fs.String("content", "", "Structured content (What/Why/Where/Learned)")
	obsType := fs.String("type", "discovery", "Observation type (decision, bugfix, discovery, pattern, gotcha, config, preference)")
	kind := fs.String("kind", "semantic", "Memory kind (episodic, semantic, procedural)")
	confidence := fs.Float64("confidence", 0.7, "Confidence level (0.0-1.0)")
	topicKey := fs.String("topic-key", "", "Topic key for upsert")
	tags := fs.String("tags", "", "Comma-separated tags")
	files := fs.String("files", "", "Comma-separated file paths")
	namespace := fs.String("namespace", "default", "Namespace")
	fs.Parse(os.Args[2:])

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: neurox save \"title\" --content \"...\" [flags]")
		fs.PrintDefaults()
		os.Exit(1)
	}
	title := fs.Arg(0)

	obs := observation.Observation{
		Title:           title,
		Content:         *content,
		ObservationType: observation.ObservationType(*obsType),
		Kind:            observation.Kind(*kind),
		Confidence:      *confidence,
		TopicKey:        *topicKey,
		Namespace:       *namespace,
	}
	if *tags != "" {
		obs.Tags = splitCSV(*tags)
	}
	if *files != "" {
		obs.Files = splitCSV(*files)
	}

	store := observation.NewStore(database, nil)
	cliIDGen := observation.NewULIDGenerator()
	cliTemporalStore := temporal.NewStore(database, cliIDGen)
	cliTemporalExtractor := temporal.NewExtractor(temporal.NewParser(), cliTemporalStore)
	store.SetTemporalExtractor(cliTemporalExtractor)

	saved, err := store.Save(ctx, obs)
	if err != nil {
		log.Fatalf("save: %v", err)
	}

	// Trigger fact extraction if LLM available
	d := initDepsLight(ctx, database, cfg)
	if d.factExtractor != nil {
		go d.factExtractor.ExtractAndSave(context.Background(), saved.ID, saved.Title, saved.Content, saved.Namespace)
	}

	printJSON(map[string]any{
		"id":        saved.ID,
		"title":     saved.Title,
		"layer":     saved.Layer,
		"namespace": saved.Namespace,
		"topic_key": saved.TopicKey,
		"message":   "observation saved",
	})
}

func runRecall(ctx context.Context, database *sql.DB, cfg config.Config) {
	fs := flag.NewFlagSet("recall", flag.ExitOnError)
	obsType := fs.String("type", "", "Filter by observation type")
	kind := fs.String("kind", "", "Filter by memory kind")
	namespace := fs.String("namespace", "", "Filter by namespace")
	filesFlag := fs.String("files", "", "Filter by file paths (comma-separated)")
	includeStale := fs.Bool("include-stale", false, "Include stale/expired observations")
	limit := fs.Int("limit", 10, "Max results")
	fs.Parse(os.Args[2:])

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: neurox recall \"query\" [flags]")
		fs.PrintDefaults()
		os.Exit(1)
	}
	query := fs.Arg(0)

	embedder := embed.AutoDetect(ctx, embed.OllamaConfig{}, embed.RemoteConfig{
		URL: cfg.Embeddings.RemoteURL, APIKey: cfg.Embeddings.RemoteKey,
		Model: cfg.Embeddings.RemoteModel, Dimensions: cfg.Embeddings.Dimensions,
	})
	engine := recall.NewEngine(database, recall.WithEmbedder(embedder))

	opts := recall.SearchOptions{
		Query:           query,
		ObservationType: observation.ObservationType(*obsType),
		Kind:            observation.Kind(*kind),
		Namespace:       *namespace,
		IncludeStale:    *includeStale,
		Limit:           *limit,
	}
	if *filesFlag != "" {
		opts.Files = splitCSV(*filesFlag)
	}

	results, err := engine.Search(ctx, opts)
	if err != nil {
		log.Fatalf("recall: %v", err)
	}

	printJSON(map[string]any{
		"query":   query,
		"count":   len(results),
		"results": results,
	})
}

func runContext(ctx context.Context, database *sql.DB, cfg config.Config) {
	fs := flag.NewFlagSet("context", flag.ExitOnError)
	namespace := fs.String("namespace", "default", "Namespace")
	filesFlag := fs.String("files", "", "Comma-separated file paths")
	limit := fs.Int("limit", 20, "Max results")
	fs.Parse(os.Args[2:])

	embedder := embed.AutoDetect(ctx, embed.OllamaConfig{}, embed.RemoteConfig{
		URL: cfg.Embeddings.RemoteURL, APIKey: cfg.Embeddings.RemoteKey,
		Model: cfg.Embeddings.RemoteModel, Dimensions: cfg.Embeddings.Dimensions,
	})
	engine := proactive.NewEngine(database, embedder)

	var files []string
	if *filesFlag != "" {
		files = splitCSV(*filesFlag)
	}

	result, err := engine.GetContext(ctx, *namespace, files, *limit)
	if err != nil {
		log.Fatalf("context: %v", err)
	}

	printJSON(result)
}

func runInvalidate(ctx context.Context, database *sql.DB, cfg config.Config) {
	fs := flag.NewFlagSet("invalidate", flag.ExitOnError)
	reason := fs.String("reason", "", "Why the observation is incorrect (required)")
	replTitle := fs.String("replacement-title", "", "Title for replacement observation")
	replContent := fs.String("replacement-content", "", "Content for replacement observation")
	fs.Parse(os.Args[2:])

	if fs.NArg() < 1 || *reason == "" {
		fmt.Fprintln(os.Stderr, "Usage: neurox invalidate <id> --reason \"...\" [flags]")
		fs.PrintDefaults()
		os.Exit(1)
	}
	obsID := fs.Arg(0)

	idGen := observation.NewULIDGenerator()
	store := observation.NewStore(database, nil)
	linkStore := links.NewStore(database, idGen)

	input := observation.InvalidateInput{
		ObservationID:      obsID,
		Reason:             *reason,
		ReplacementTitle:   *replTitle,
		ReplacementContent: *replContent,
	}

	result, err := store.Invalidate(ctx, linkStore, input)
	if err != nil {
		log.Fatalf("invalidate: %v", err)
	}

	resp := map[string]string{
		"invalidated_id": result.InvalidatedID,
		"message":        "observation marked as stale",
	}
	if result.ReplacementID != "" {
		resp["replacement_id"] = result.ReplacementID
		resp["link_id"] = result.LinkID
		resp["message"] = "observation invalidated and replaced"
	}

	printJSON(resp)
}

func runStatus(ctx context.Context, database *sql.DB, cfg config.Config) {
	d := initDepsLight(ctx, database, cfg)

	var total, buffer, working, core int
	var staleCount, expiredCount int

	database.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL").Scan(&total)
	database.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL AND layer = 0").Scan(&buffer)
	database.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL AND layer = 1").Scan(&working)
	database.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL AND layer = 2").Scan(&core)
	database.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL AND staleness = 'stale'").Scan(&staleCount)
	database.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL AND staleness = 'expired'").Scan(&expiredCount)

	var linkCount, factCount, sessionCount, embeddingCount int
	database.QueryRowContext(ctx, "SELECT COUNT(*) FROM observation_links").Scan(&linkCount)
	database.QueryRowContext(ctx, "SELECT COUNT(*) FROM facts WHERE valid_until IS NULL").Scan(&factCount)
	database.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions WHERE status = 'active'").Scan(&sessionCount)
	database.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE embedding IS NOT NULL AND deleted_at IS NULL").Scan(&embeddingCount)

	var lastRun, lastRunStatus string
	database.QueryRowContext(ctx, "SELECT COALESCE(completed_at, started_at), status FROM consolidation_runs ORDER BY epoch DESC LIMIT 1").Scan(&lastRun, &lastRunStatus)

	printJSON(map[string]any{
		"total":              total,
		"buffer":             buffer,
		"working":            working,
		"core":               core,
		"stale":              staleCount,
		"expired":            expiredCount,
		"links":              linkCount,
		"facts":              factCount,
		"active_sessions":    sessionCount,
		"embeddings":         embeddingCount,
		"embedding_provider": d.embedder.Name(),
		"llm_provider":       d.llmProvider.Name(),
		"gate_mode":          string(d.llmGate.Mode()),
		"last_consolidation": lastRun,
		"last_run_status":    lastRunStatus,
		"database":           cfg.Database.Path,
	})
}

func runConsolidate(ctx context.Context, database *sql.DB, cfg config.Config) {
	d := initDeps(ctx, database, cfg)

	fmt.Println("Forcing full consolidation (all promotions, dedup, reflect)...")
	if err := d.pipeline.ForceRun(ctx); err != nil {
		log.Fatalf("consolidation failed: %v", err)
	}

	// Show results
	var buffer, working, core int
	database.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL AND layer = 0").Scan(&buffer)
	database.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL AND layer = 1").Scan(&working)
	database.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL AND layer = 2").Scan(&core)

	printJSON(map[string]any{
		"message": "consolidation completed",
		"buffer":  buffer,
		"working": working,
		"core":    core,
	})
}

func runConfig(cfg config.Config) {
	printJSON(map[string]any{
		"database_path": cfg.Database.Path,
		"config_path":   cfg.Meta.ConfigPath,
		"config_dir":    cfg.Meta.ConfigDir,
		"loaded_from":   cfg.Meta.LoadedFrom,
		"source":        cfg.Meta.Source,
		"llm": map[string]any{
			"provider":     cfg.LLM.Provider,
			"gate_mode":    cfg.LLM.GateMode,
			"ollama_url":   cfg.LLM.OllamaURL,
			"ollama_model": cfg.LLM.OllamaModel,
			"remote_url":   cfg.LLM.RemoteURL,
			"remote_model": cfg.LLM.RemoteModel,
		},
	})
}

// --- Server commands ---

func runMCP(ctx context.Context, database *sql.DB, cfg config.Config) {
	d := initDeps(ctx, database, cfg)

	d.pipeline.Start(ctx)
	defer d.pipeline.Stop()
	if d.embedQueue != nil {
		defer d.embedQueue.Stop()
	}

	mcpDeps := &neuroxmcp.Deps{
		ObservationStore: d.obsStore,
		RecallEngine:     d.recallEngine,
		LinkStore:        d.linkStore,
		FactStore:        d.factStore,
		FactExtractor:    d.factExtractor,
		ReflectEngine:    d.reflectEngine,
		SessionManager:   d.sessionManager,
		ProactiveEngine:  d.proactiveEngine,
		Pipeline:         d.pipeline,
		DB:               database,
		LLMProvider:      d.llmProvider,
		LLMGate:          d.llmGate,
	}

	srv := neuroxmcp.NewServer(mcpDeps)
	if err := mcpserver.ServeStdio(srv); err != nil {
		log.Fatalf("mcp server error: %v", err)
	}
}

func runHTTP(ctx context.Context, database *sql.DB, cfg config.Config) {
	d := initDeps(ctx, database, cfg)

	d.pipeline.Start(ctx)
	defer d.pipeline.Stop()
	if d.embedQueue != nil {
		defer d.embedQueue.Stop()
	}

	apiDeps := &api.Deps{
		ObservationStore: d.obsStore,
		RecallEngine:     d.recallEngine,
		LinkStore:        d.linkStore,
		DB:               database,
	}

	srv := api.NewServer(api.Config{Port: defaultHTTPPort}, apiDeps)

	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background())
	}()

	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("http server error: %v", err)
	}
}

// --- Dependencies ---

type deps struct {
	obsStore        *observation.Store
	recallEngine    *recall.Engine
	linkStore       *links.Store
	factStore       *facts.Store
	factExtractor   *facts.Extractor
	reflectEngine   *reflectpkg.Engine
	sessionManager  *session.Manager
	proactiveEngine *proactive.Engine
	embedder        embed.Provider
	embedQueue      *embed.Queue
	pipeline        *consolidate.Pipeline
	llmProvider     llm.Provider
	llmGate         *llm.Gate
}

func initDeps(ctx context.Context, database *sql.DB, cfg config.Config) *deps {
	embedder := embed.AutoDetect(ctx, embed.OllamaConfig{}, embed.RemoteConfig{
		URL: cfg.Embeddings.RemoteURL, APIKey: cfg.Embeddings.RemoteKey,
		Model: cfg.Embeddings.RemoteModel, Dimensions: cfg.Embeddings.Dimensions,
	})
	llmProvider := llm.AutoDetect(ctx,
		llm.OllamaConfig{URL: cfg.LLM.OllamaURL, Model: cfg.LLM.OllamaModel},
		llm.RemoteConfig{URL: cfg.LLM.RemoteURL, APIKey: cfg.LLM.RemoteAPIKey, Model: cfg.LLM.RemoteModel},
	)
	gate := llm.NewGate(llmProvider, llm.GateMode(cfg.LLM.GateMode))

	idGen := observation.NewULIDGenerator()
	obsStore := observation.NewStore(database, nil)
	recallEngine := recall.NewEngine(database, recall.WithEmbedder(embedder))
	linkStore := links.NewStore(database, idGen)
	factStore := facts.NewStore(database, idGen)
	factExtractor := facts.NewExtractor(llmProvider, factStore)
	reflectEngine := reflectpkg.NewEngine(database, llmProvider, linkStore, idGen)
	sessionMgr := session.NewManager(database, llmProvider, idGen)
	proactiveEng := proactive.NewEngine(database, embedder)

	// Wire temporal extraction into observation save and session extraction.
	temporalStore := temporal.NewStore(database, idGen)
	temporalExtractor := temporal.NewExtractor(temporal.NewParser(), temporalStore)
	obsStore.SetTemporalExtractor(temporalExtractor)
	sessionMgr.SetTemporalExtractor(temporalExtractor)

	var embedQueue *embed.Queue
	if embed.IsAvailable(embedder) {
		embedQueue = embed.NewQueue(embedder, database)
		embedQueue.Start(ctx)
	}

	decayEngine := decay.NewEngine(database)
	pipeline := consolidate.NewPipeline(database, decayEngine, embedder, gate, linkStore, llmProvider, idGen, consolidate.Config{})

	return &deps{
		obsStore:        obsStore,
		recallEngine:    recallEngine,
		linkStore:       linkStore,
		factStore:       factStore,
		factExtractor:   factExtractor,
		reflectEngine:   reflectEngine,
		sessionManager:  sessionMgr,
		proactiveEngine: proactiveEng,
		embedder:        embedder,
		embedQueue:      embedQueue,
		pipeline:        pipeline,
		llmProvider:     llmProvider,
		llmGate:         gate,
	}
}

// initDepsLight creates minimal deps for CLI commands that don't need background workers.
func initDepsLight(ctx context.Context, database *sql.DB, cfg config.Config) *deps {
	embedder := embed.AutoDetect(ctx, embed.OllamaConfig{}, embed.RemoteConfig{
		URL: cfg.Embeddings.RemoteURL, APIKey: cfg.Embeddings.RemoteKey,
		Model: cfg.Embeddings.RemoteModel, Dimensions: cfg.Embeddings.Dimensions,
	})
	llmProvider := llm.AutoDetect(ctx,
		llm.OllamaConfig{URL: cfg.LLM.OllamaURL, Model: cfg.LLM.OllamaModel},
		llm.RemoteConfig{URL: cfg.LLM.RemoteURL, APIKey: cfg.LLM.RemoteAPIKey, Model: cfg.LLM.RemoteModel},
	)
	gate := llm.NewGate(llmProvider, llm.GateMode(cfg.LLM.GateMode))

	idGen := observation.NewULIDGenerator()
	factStore := facts.NewStore(database, idGen)

	return &deps{
		factExtractor: facts.NewExtractor(llmProvider, factStore),
		embedder:      embedder,
		llmProvider:   llmProvider,
		llmGate:       gate,
	}
}

// --- Helpers ---

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		log.Fatalf("encode json: %v", err)
	}
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func installHook() {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		log.Fatalf("not a git repository: %v", err)
	}
	gitRoot := filepath.Clean(string(out[:len(out)-1]))
	hookDir := filepath.Join(gitRoot, ".git", "hooks")
	hookPath := filepath.Join(hookDir, "post-commit")

	exePath, err := os.Executable()
	if err != nil {
		log.Fatalf("find executable: %v", err)
	}
	scriptPath := filepath.Join(filepath.Dir(exePath), "..", "scripts", "post-commit")

	scriptContent, err := os.ReadFile(scriptPath)
	if err != nil {
		scriptContent = []byte(`#!/bin/sh
# neurox post-commit hook (auto-generated)
NEUROX_PORT="${NEUROX_PORT:-7437}"
CHANGED_FILES=$(git diff --name-only HEAD~1 HEAD 2>/dev/null)
[ -z "$CHANGED_FILES" ] && exit 0
COMMIT_SHA=$(git rev-parse HEAD)
BRANCH=$(git rev-parse --abbrev-ref HEAD)
FILES_JSON=$(echo "$CHANGED_FILES" | awk 'BEGIN{printf "["} NR>1{printf ","} {printf "\"%s\"", $0} END{printf "]"}')
curl -s -X POST "http://localhost:${NEUROX_PORT}/api/v1/hooks/git" \
  -H "Content-Type: application/json" \
  -d "{\"changed_files\":${FILES_JSON},\"commit_sha\":\"${COMMIT_SHA}\",\"branch\":\"${BRANCH}\"}" \
  --connect-timeout 2 --max-time 5 > /dev/null 2>&1 || true
`)
	}

	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		log.Fatalf("create hooks dir: %v", err)
	}

	if _, err := os.Stat(hookPath); err == nil {
		log.Fatalf("hook already exists at %s — remove it first if you want to replace it", hookPath)
	}

	if err := os.WriteFile(hookPath, scriptContent, 0o755); err != nil {
		log.Fatalf("write hook: %v", err)
	}

	fmt.Printf("installed post-commit hook at %s\n", hookPath)
}

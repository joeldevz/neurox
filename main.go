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

	"github.com/joeldevz/neurox/internal/api"
	benchpkg "github.com/joeldevz/neurox/internal/benchmark"
	"github.com/joeldevz/neurox/internal/config"
	"github.com/joeldevz/neurox/internal/consolidate"
	"github.com/joeldevz/neurox/internal/curate"
	"github.com/joeldevz/neurox/internal/db"
	"github.com/joeldevz/neurox/internal/decay"
	"github.com/joeldevz/neurox/internal/embed"
	exportpkg "github.com/joeldevz/neurox/internal/export"
	"github.com/joeldevz/neurox/internal/facts"
	"github.com/joeldevz/neurox/internal/graph"
	"github.com/joeldevz/neurox/internal/installer"
	"github.com/joeldevz/neurox/internal/links"
	"github.com/joeldevz/neurox/internal/llm"
	neuroxmcp "github.com/joeldevz/neurox/internal/mcp"
	"github.com/joeldevz/neurox/internal/observation"
	"github.com/joeldevz/neurox/internal/proactive"
	"github.com/joeldevz/neurox/internal/recall"
	reflectpkg "github.com/joeldevz/neurox/internal/reflect"
	"github.com/joeldevz/neurox/internal/session"
	"github.com/joeldevz/neurox/internal/telemetry"
	"github.com/joeldevz/neurox/internal/temporal"
)

const (
	version         = "0.1.10"
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
	case "install":
		if err := installer.Run(ctx, "."); err != nil {
			log.Fatalf("installer: %v", err)
		}
		return
	case "install-hook":
		installHook()
		return
	case "update":
		runUpdate()
		return
	case "version", "--version", "-v":
		fmt.Printf("neurox v%s\n", version)
		return
	case "help", "--help", "-h":
		printUsage()
		return
	case "benchmark":
		if err := benchpkg.RunCLI(os.Args[2:]); err != nil {
			log.Fatalf("benchmark: %v", err)
		}
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
	case "curate":
		runCurate(ctx, database, cfg)
	case "export":
		runExport(ctx, database)
	case "import":
		runImport(ctx, database)
	case "graph":
		runGraph(ctx, database)
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
	fmt.Println("  curate           Deep curation: clean noise, recalibrate importance (--namespace ns --dry-run)")
	fmt.Println()
	fmt.Println("Visualization:")
	fmt.Println("  graph            Generate interactive graph visualization")
	fmt.Println()
	fmt.Println("Benchmark:")
	fmt.Println("  benchmark        Run brain benchmark suite (--scale small|medium|large)")
	fmt.Println()
	fmt.Println("Export / Import:")
	fmt.Println("  export           Export observations as Markdown files (--format markdown --output dir --namespace ns)")
	fmt.Println("  import           Import .md observation files into the database (--source dir)")
	fmt.Println()
	fmt.Println("Setup commands:")
	fmt.Println("  install          Launch interactive installer")
	fmt.Println("  install-hook     Install git post-commit hook")
	fmt.Println("  update           Update neurox to the latest version")
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

func runCurate(ctx context.Context, database *sql.DB, cfg config.Config) {
	fs := flag.NewFlagSet("curate", flag.ExitOnError)
	namespace := fs.String("namespace", "", "Curate specific namespace (default: all)")
	fs.StringVar(namespace, "n", "", "Curate specific namespace (shorthand)")
	dryRun := fs.Bool("dry-run", false, "Preview changes without executing")
	fs.Parse(os.Args[2:])

	// Check curator provider is configured
	if cfg.Curator.Provider != "remote" || cfg.Curator.RemoteURL == "" || cfg.Curator.RemoteModel == "" {
		log.Fatalf("curator not configured. Set in config.yaml or via environment:\n" +
			"  NEUROX_CURATOR_PROVIDER=remote\n" +
			"  NEUROX_CURATOR_REMOTE_URL=https://generativelanguage.googleapis.com/v1beta/openai\n" +
			"  NEUROX_CURATOR_REMOTE_API_KEY=your-api-key\n" +
			"  NEUROX_CURATOR_REMOTE_MODEL=gemini-2.5-flash")
	}

	// Create curator provider
	curatorProvider := llm.NewRemote(llm.RemoteConfig{
		URL:    cfg.Curator.RemoteURL,
		APIKey: cfg.Curator.RemoteAPIKey,
		Model:  cfg.Curator.RemoteModel,
	})

	// Load priorities
	priorities, err := curate.LoadPriorities(cfg.Curator.PrioritiesFile)
	if err != nil {
		log.Fatalf("load priorities: %v", err)
	}

	// Create engine and run curation
	engine := curate.NewEngine(database, curatorProvider, priorities, cfg.Curator.RemoteModel)

	if *namespace != "" {
		fmt.Printf("Curating namespace %q%s...\n", *namespace, dryRunLabel(*dryRun))
		report, err := engine.CurateNamespace(ctx, *namespace, *dryRun)
		if err != nil {
			log.Fatalf("curate: %v", err)
		}
		printJSON(report)
	} else {
		fmt.Printf("Curating all namespaces%s...\n", dryRunLabel(*dryRun))
		report, err := engine.CurateAll(ctx, *dryRun)
		if err != nil {
			log.Fatalf("curate: %v", err)
		}
		printJSON(report)
	}
}

func dryRunLabel(dryRun bool) string {
	if dryRun {
		return " (dry-run)"
	}
	return ""
}

func runExport(ctx context.Context, database *sql.DB) {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	format := fs.String("format", "markdown", "Export format: markdown")
	output := fs.String("output", "./neurox-export", "Output directory")
	namespace := fs.String("namespace", "", "Namespace to export (empty = all)")
	fs.Parse(os.Args[2:])

	if *format != "markdown" {
		log.Fatalf("unsupported format: %s (only markdown supported)", *format)
	}

	count, err := exportpkg.ExportMarkdown(ctx, database, *namespace, *output)
	if err != nil {
		log.Fatalf("export: %v", err)
	}
	fmt.Printf("Exported %d observations to %s\n", count, *output)
}

func runImport(ctx context.Context, database *sql.DB) {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	source := fs.String("source", "", "Source directory with .md files (required)")
	fs.Parse(os.Args[2:])

	if *source == "" {
		log.Fatalf("--source is required")
	}

	count, err := exportpkg.ImportMarkdown(ctx, database, *source)
	if err != nil {
		log.Fatalf("import: %v", err)
	}
	fmt.Printf("Imported %d observations from %s\n", count, *source)
}

func runGraph(ctx context.Context, database *sql.DB) {
	fs := flag.NewFlagSet("graph", flag.ExitOnError)
	namespace := fs.String("namespace", "", "Filter by namespace")
	obsType := fs.String("type", "", "Filter by observation type")
	tags := fs.String("tags", "", "Filter by tags (comma-separated)")
	minImportance := fs.Float64("min-importance", 0, "Minimum importance (0.0-1.0)")
	limit := fs.Int("limit", 200, "Max nodes to display")
	linkedOnly := fs.Bool("linked-only", false, "Show only observations that have links")
	output := fs.String("output", "", "Output file path (default: neurox-graph.html)")
	noBrowser := fs.Bool("no-browser", false, "Don't open browser automatically")
	fs.Parse(os.Args[2:])

	if *output == "" {
		*output = "neurox-graph.html"
	}

	opts := graph.Options{
		Namespace:       *namespace,
		ObservationType: *obsType,
		Tags:            *tags,
		MinImportance:   *minImportance,
		Limit:           *limit,
		LinkedOnly:      *linkedOnly,
	}

	data, err := graph.Query(ctx, database, opts)
	if err != nil {
		log.Fatalf("query graph: %v", err)
	}

	if err := graph.RenderToFile(*output, data, !*noBrowser); err != nil {
		log.Fatalf("render graph: %v", err)
	}

	fmt.Printf("Graph generated: %s (%d nodes, %d edges)\n", *output, data.Stats.ShownNodes, data.Stats.ShownEdges)
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

	var curateEngine *curate.Engine
	if llm.IsAvailable(d.curatorProvider) {
		priorities, prErr := curate.LoadPriorities(cfg.Curator.PrioritiesFile)
		if prErr != nil {
			log.Printf("load priorities: %v (curation will work without priorities)", prErr)
		}
		curateEngine = curate.NewEngine(database, d.curatorProvider, priorities, cfg.Curator.RemoteModel)
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
		CurateEngine:     curateEngine,
		DB:               database,
		LLMProvider:      d.llmProvider,
		LLMGate:          d.llmGate,
		EmbedQueue:       d.embedQueue,
		Embedder:         d.embedder,
		Tracker:          d.tracker,
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
		LLMProvider:      d.llmProvider.Name(),
		EmbedProvider:    d.embedder.Name(),
		GateMode:         string(d.llmGate.Mode()),
		EmbedQueue:       d.embedQueue,
		Tracker:          d.tracker,
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
	curatorProvider llm.Provider
	llmGate         *llm.Gate
	tracker         *telemetry.Tracker
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

	// Build curator provider when configured; used for reflections and curation.
	var curatorProvider llm.Provider
	if cfg.Curator.Provider == "remote" && cfg.Curator.RemoteURL != "" && cfg.Curator.RemoteModel != "" {
		curatorProvider = llm.NewRemote(llm.RemoteConfig{
			URL:    cfg.Curator.RemoteURL,
			APIKey: cfg.Curator.RemoteAPIKey,
			Model:  cfg.Curator.RemoteModel,
		})
		log.Printf("using curator provider: %s", curatorProvider.Name())
	}

	idGen := observation.NewULIDGenerator()
	obsStore := observation.NewStore(database, nil)
	recallEngine := recall.NewEngine(database, recall.WithEmbedder(embedder))
	linkStore := links.NewStore(database, idGen)
	factStore := facts.NewStore(database, idGen)
	factExtractor := facts.NewExtractor(llmProvider, factStore)

	// Use curator provider for reflections when available; fall back to llmProvider.
	reflectLLM := llmProvider
	if llm.IsAvailable(curatorProvider) {
		reflectLLM = curatorProvider
	}
	reflectEngine := reflectpkg.NewEngine(database, reflectLLM, linkStore, idGen)

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
	pipeline := consolidate.NewPipeline(database, decayEngine, embedder, embedQueue, gate, linkStore, llmProvider, curatorProvider, idGen, consolidate.Config{})

	tracker := telemetry.NewTracker(database)

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
		curatorProvider: curatorProvider,
		llmGate:         gate,
		tracker:         tracker,
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
NEUROX_PORT="${NEUROX_PORT:-7438}"
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

func runUpdate() {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	yes := fs.Bool("yes", false, "skip confirmation prompt")
	fs.BoolVar(yes, "y", false, "skip confirmation prompt (shorthand)")
	_ = fs.Parse(os.Args[2:])
	// Step 2 will add internal/installer/updater.go; Step 3 will replace this with:
	// installer.RunUpdate(version, *yes)
	fmt.Fprintln(os.Stderr, "neurox update: not yet implemented (coming in next step)")
	os.Exit(1)
}

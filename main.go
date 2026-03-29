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
	"time"

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
	"github.com/joeldevz/neurox/internal/savepipeline"
	"github.com/joeldevz/neurox/internal/session"
	"github.com/joeldevz/neurox/internal/telemetry"
	"github.com/joeldevz/neurox/internal/temporal"
)

const (
	version         = "0.2.0"
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
	case "setup":
		runSetup()
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
	case "reembed":
		runReembed(ctx, database, cfg)
	case "export":
		runExport(ctx, database)
	case "import":
		runImport(ctx, database)
	case "graph":
		runGraph(ctx, database)
	case "backup":
		runBackup(ctx, database, cfg)
	case "session-start":
		runSessionStart(ctx, database, cfg)
	case "session-end":
		runSessionEnd(ctx, database, cfg)
	case "audit":
		runAudit(ctx, database)
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
	fmt.Println("  audit            Show full lifecycle of an observation (neurox audit <id>)")
	fmt.Println("  consolidate      Force immediate consolidation (promote, dedup, reflect)")
	fmt.Println("  curate           Deep curation: clean noise, recalibrate importance (--namespace ns --dry-run)")
	fmt.Println("  reembed          Force re-embedding of all observations (useful after model change)")
	fmt.Println()
	fmt.Println("Session commands:")
	fmt.Println("  session-start    Start a new session (--title, --directory, --branch, --namespace)")
	fmt.Println("  session-end      End a session (--session-id, --summary)")
	fmt.Println()
	fmt.Println("Visualization:")
	fmt.Println("  graph            Generate interactive graph visualization")
	fmt.Println()
	fmt.Println("Benchmark:")
	fmt.Println("  benchmark        Run brain benchmark suite (--scale small|medium|large)")
	fmt.Println()
	fmt.Println("Export / Import / Backup:")
	fmt.Println("  export           Export observations (--format md|json --output path --namespace ns)")
	fmt.Println("  import           Import observations (--format md|json --source path)")
	fmt.Println("  backup           Create a safe backup of the database (--output path)")
	fmt.Println()
	fmt.Println("Setup commands:")
	fmt.Println("  setup <agent>    Configure an AI agent (claude-code, opencode, cursor, vscode, antigravity)")
	fmt.Println("  install          Launch interactive installer")
	fmt.Println("  install-hook     Install git post-commit hook")
	fmt.Println("  update           Update neurox to the latest version")
	fmt.Println("  config           Show current configuration")
	fmt.Println("  version          Show version")
	fmt.Println()
	fmt.Println("Run 'neurox <command> --help' for details on a command.")
}

func runSetup() {
	// Handle no-arg and --list cases
	if len(os.Args) < 3 || os.Args[2] == "--list" || os.Args[2] == "-l" {
		printSetupUsage()
		return
	}

	agent := os.Args[2]
	if agent == "--help" || agent == "-h" {
		printSetupUsage()
		return
	}

	if err := installer.Setup(agent); err != nil {
		fmt.Fprintf(os.Stderr, "setup: %v\n", err)
		os.Exit(1)
	}
}

func printSetupUsage() {
	fmt.Println("Configure an AI agent to use Neurox as MCP server.")
	fmt.Println()
	fmt.Println("Usage: neurox setup <agent>")
	fmt.Println()
	fmt.Println("Supported agents:")
	for _, a := range installer.SupportedAgents() {
		fmt.Printf("  %-18s %s\n", a.Name, a.Description)
	}
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  neurox setup claude-code      # Configure Claude Code")
	fmt.Println("  neurox setup opencode          # Configure OpenCode")
	fmt.Println("  neurox setup cursor            # Configure Cursor")
}

// --- CLI subcommands ---

func runSave(ctx context.Context, database *sql.DB, cfg config.Config) {
	fs := flag.NewFlagSet("save", flag.ExitOnError)
	content := fs.String("content", "", "Structured content (What/Why/Where/Learned)")
	obsType := fs.String("type", "discovery", "Observation type (decision, bugfix, discovery, pattern, gotcha, config, preference)")
	kind := fs.String("kind", "semantic", "Memory kind (episodic, semantic, procedural)")
	confidence := fs.Float64("confidence", 0.7, "Confidence level (0.0-1.0)")
	topicKey := fs.String("topic-key", "", "Topic key for upsert")
	retention := fs.String("retention", "", "Retention policy: durable (default) or operational")
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
	if *retention != "" {
		obs.Retention = observation.Retention(*retention)
	}
	if *tags != "" {
		obs.Tags = splitCSV(*tags)
	}
	if *files != "" {
		obs.Files = splitCSV(*files)
	}

	// Build the observation store with temporal extraction wired in —
	// same as the full server path.
	idGen := observation.NewULIDGenerator()
	store := observation.NewStore(database, nil)
	temporalStore := temporal.NewStore(database, idGen)
	temporalExtractor := temporal.NewExtractor(temporal.NewParser(), temporalStore)
	store.SetTemporalExtractor(temporalExtractor)

	// Minimal deps: LLM gate, fact extractor, embedder — no background workers.
	d := initDepsLight(ctx, database, cfg)

	// Create a short-lived embed queue that we drain synchronously.
	// Unlike the server, the CLI process exits after the command, so we
	// cannot use a background worker — we stop the queue (which drains it)
	// before returning.
	var embedQueue *embed.Queue
	if embed.IsAvailable(d.embedder) {
		embedQueue = embed.NewQueue(d.embedder, database)
		embedQueue.Start(ctx)
	}

	// Delegate to the canonical shared save pipeline.
	pr, pErr := savepipeline.Run(ctx, savepipeline.Deps{
		Store:         store,
		SaveQueue:     nil, // CLI: always synchronous
		LLMGate:       d.llmGate,
		FactExtractor: d.factExtractor,
		EmbedQueue:    embedQueue,
		DB:            database,
	}, savepipeline.Input{
		Obs:     obs,
		Surface: "cli",
		Tool:    "save",
	})

	// Drain the embed queue synchronously before the process exits.
	if embedQueue != nil {
		embedQueue.Stop()
	}

	if pErr != nil {
		log.Fatalf("save: %v", pErr)
	}

	if pr.Rejected {
		printJSON(map[string]any{
			"message": pr.Message,
			"title":   pr.Title,
		})
		return
	}

	printJSON(map[string]any{
		"id":        pr.ID,
		"title":     pr.Title,
		"layer":     pr.Layer,
		"namespace": pr.Namespace,
		"topic_key": pr.TopicKey,
		"message":   pr.Message,
	})
}

func runRecall(ctx context.Context, database *sql.DB, cfg config.Config) {
	fs := flag.NewFlagSet("recall", flag.ExitOnError)
	obsType := fs.String("type", "", "Filter by observation type")
	kind := fs.String("kind", "", "Filter by memory kind")
	namespace := fs.String("namespace", "", "Filter by namespace")
	filesFlag := fs.String("files", "", "Filter by file paths (comma-separated)")
	includeStale := fs.Bool("include-stale", false, "Include stale/expired observations")
	debug := fs.Bool("debug", false, "Include score breakdown per result")
	limit := fs.Int("limit", 10, "Max results")
	fs.Parse(os.Args[2:])

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: neurox recall \"query\" [flags]")
		fs.PrintDefaults()
		os.Exit(1)
	}
	query := fs.Arg(0)

	embedder := embed.AutoDetect(ctx, embed.OllamaConfig{URL: cfg.Embeddings.OllamaURL, Model: cfg.Embeddings.OllamaModel}, embed.RemoteConfig{
		URL: cfg.Embeddings.RemoteURL, APIKey: cfg.Embeddings.RemoteKey,
		Model: cfg.Embeddings.RemoteModel, Dimensions: cfg.Embeddings.Dimensions,
	})
	idGen := observation.NewULIDGenerator()
	factStore := facts.NewStore(database, idGen)
	engine := recall.NewEngine(database, recall.WithEmbedder(embedder), recall.WithFactStore(factStore))

	opts := recall.SearchOptions{
		Query:           query,
		ObservationType: observation.ObservationType(*obsType),
		Kind:            observation.Kind(*kind),
		Namespace:       *namespace,
		IncludeStale:    *includeStale,
		Debug:           *debug,
		Limit:           *limit,
	}
	if *filesFlag != "" {
		opts.Files = splitCSV(*filesFlag)
	}

	results, err := engine.Search(ctx, opts)
	if err != nil {
		log.Fatalf("recall: %v", err)
	}

	resp := map[string]any{
		"query":   query,
		"count":   len(results),
		"results": results,
	}

	// Include detected temporal intent (same as MCP and HTTP surfaces).
	intent := recall.DetectTemporalIntent(query, time.Now().UTC())
	if intent.Kind != recall.IntentNone {
		resp["temporal_intent"] = string(intent.Kind)
	}

	printJSON(resp)
}

func runContext(ctx context.Context, database *sql.DB, cfg config.Config) {
	fs := flag.NewFlagSet("context", flag.ExitOnError)
	namespace := fs.String("namespace", "default", "Namespace")
	filesFlag := fs.String("files", "", "Comma-separated file paths")
	limit := fs.Int("limit", 20, "Max results")
	fs.Parse(os.Args[2:])

	embedder := embed.AutoDetect(ctx, embed.OllamaConfig{URL: cfg.Embeddings.OllamaURL, Model: cfg.Embeddings.OllamaModel}, embed.RemoteConfig{
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

func runAudit(ctx context.Context, database *sql.DB) {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "Usage: neurox audit <observation_id>")
		os.Exit(1)
	}
	obsID := os.Args[2]

	// 1. Fetch full observation row (all columns including ones not in Observation struct)
	var (
		id, title, content, obsType, kind, namespace                              string
		layer, accessCount, repetitionCount, modifiedEpoch                        int
		confidence, importance, activationLevel, consolidationStrength, decayRate float64
		tags, source, topicKey, staleness, consolidationStatus                    sql.NullString
		retention, sourceSurface, sourceSessionID, sourceTool                     sql.NullString
		validFrom, validUntil, lastAccessed                                       sql.NullString
		createdAt, updatedAt                                                      string
		deletedAt                                                                 sql.NullString
		rejectionEpoch                                                            sql.NullInt64
		hasEmbedding                                                              bool
	)

	err := database.QueryRowContext(ctx, `
		SELECT id, title, content, observation_type, kind, namespace,
		       layer, confidence, importance, activation_level, consolidation_strength,
		       access_count, last_accessed, repetition_count, decay_rate,
		       COALESCE(tags, ''), source, topic_key,
		       staleness, consolidation_status, rejection_epoch,
		       retention, source_surface, source_session_id, source_tool,
		       valid_from, valid_until,
		       created_at, updated_at, deleted_at, modified_epoch,
		       (embedding IS NOT NULL) AS has_embedding
		FROM observations WHERE id = ?
	`, obsID).Scan(
		&id, &title, &content, &obsType, &kind, &namespace,
		&layer, &confidence, &importance, &activationLevel, &consolidationStrength,
		&accessCount, &lastAccessed, &repetitionCount, &decayRate,
		&tags, &source, &topicKey,
		&staleness, &consolidationStatus, &rejectionEpoch,
		&retention, &sourceSurface, &sourceSessionID, &sourceTool,
		&validFrom, &validUntil,
		&createdAt, &updatedAt, &deletedAt, &modifiedEpoch,
		&hasEmbedding,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			fmt.Fprintf(os.Stderr, "observation not found: %s\n", obsID)
			os.Exit(1)
		}
		log.Fatalf("query observation: %v", err)
	}

	// Build creation section
	creation := map[string]any{
		"id":         id,
		"title":      title,
		"content":    content,
		"type":       obsType,
		"kind":       kind,
		"namespace":  namespace,
		"layer":      layer,
		"created_at": createdAt,
		"updated_at": updatedAt,
	}
	if deletedAt.Valid {
		creation["deleted_at"] = deletedAt.String
	}

	// Build provenance section
	provenance := map[string]any{}
	if sourceSurface.Valid {
		provenance["source_surface"] = sourceSurface.String
	}
	if sourceSessionID.Valid {
		provenance["source_session_id"] = sourceSessionID.String
	}
	if sourceTool.Valid {
		provenance["source_tool"] = sourceTool.String
	}
	if source.Valid {
		provenance["source"] = source.String
	}

	// Build current state section
	currentState := map[string]any{
		"importance":             importance,
		"confidence":             confidence,
		"activation_level":       activationLevel,
		"consolidation_strength": consolidationStrength,
		"retention":              nullStr(retention),
		"staleness":              nullStr(staleness),
		"consolidation_status":   nullStr(consolidationStatus),
		"layer":                  layer,
		"modified_epoch":         modifiedEpoch,
		"has_embedding":          hasEmbedding,
	}
	if tags.Valid && tags.String != "" {
		currentState["tags"] = splitCSV(tags.String)
	}
	if topicKey.Valid {
		currentState["topic_key"] = topicKey.String
	}
	if rejectionEpoch.Valid {
		currentState["rejection_epoch"] = rejectionEpoch.Int64
	}

	// Build decay section
	decayInfo := map[string]any{
		"access_count":     accessCount,
		"repetition_count": repetitionCount,
		"decay_rate":       decayRate,
	}
	if lastAccessed.Valid {
		decayInfo["last_accessed"] = lastAccessed.String
	}
	if validFrom.Valid {
		decayInfo["valid_from"] = validFrom.String
	}
	if validUntil.Valid {
		decayInfo["valid_until"] = validUntil.String
	}

	// 2. Query links (outgoing: this observation is source)
	outgoingLinks, err := queryAuditLinks(ctx, database, "source_id", obsID)
	if err != nil {
		log.Fatalf("query outgoing links: %v", err)
	}

	// 3. Query links (incoming: this observation is target)
	incomingLinks, err := queryAuditLinks(ctx, database, "target_id", obsID)
	if err != nil {
		log.Fatalf("query incoming links: %v", err)
	}

	linksSection := map[string]any{
		"outgoing": outgoingLinks,
		"incoming": incomingLinks,
	}

	// 4. Query file associations (all, including expired)
	fileAssocs, err := queryAuditFiles(ctx, database, obsID)
	if err != nil {
		log.Fatalf("query file associations: %v", err)
	}

	// 5. Query temporal mentions
	temporalMentions, err := queryAuditTemporal(ctx, database, obsID)
	if err != nil {
		log.Fatalf("query temporal mentions: %v", err)
	}

	// Assemble final output
	result := map[string]any{
		"creation":          creation,
		"provenance":        provenance,
		"current_state":     currentState,
		"decay":             decayInfo,
		"links":             linksSection,
		"files":             fileAssocs,
		"temporal_mentions": temporalMentions,
	}

	printJSON(result)
}

func queryAuditLinks(ctx context.Context, database *sql.DB, column, obsID string) ([]map[string]any, error) {
	rows, err := database.QueryContext(ctx, fmt.Sprintf(`
		SELECT id, source_id, target_id, relation_type, confidence, created_by, created_at
		FROM observation_links
		WHERE %s = ?
		ORDER BY created_at ASC
	`, column), obsID)
	if err != nil {
		return nil, fmt.Errorf("query links by %s: %w", column, err)
	}
	defer rows.Close()

	var result []map[string]any
	for rows.Next() {
		var linkID, sourceID, targetID, relationType, createdBy, createdAt string
		var linkConfidence float64
		if err := rows.Scan(&linkID, &sourceID, &targetID, &relationType, &linkConfidence, &createdBy, &createdAt); err != nil {
			return nil, fmt.Errorf("scan link: %w", err)
		}
		result = append(result, map[string]any{
			"id":            linkID,
			"source_id":     sourceID,
			"target_id":     targetID,
			"relation_type": relationType,
			"confidence":    linkConfidence,
			"created_by":    createdBy,
			"created_at":    createdAt,
		})
	}
	if result == nil {
		result = []map[string]any{}
	}
	return result, rows.Err()
}

func queryAuditFiles(ctx context.Context, database *sql.DB, obsID string) ([]map[string]any, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, file_path, created_at, valid_until
		FROM file_observations
		WHERE observation_id = ?
		ORDER BY created_at ASC
	`, obsID)
	if err != nil {
		return nil, fmt.Errorf("query file associations: %w", err)
	}
	defer rows.Close()

	var result []map[string]any
	for rows.Next() {
		var fileID, filePath, fileCreatedAt string
		var fileValidUntil sql.NullString
		if err := rows.Scan(&fileID, &filePath, &fileCreatedAt, &fileValidUntil); err != nil {
			return nil, fmt.Errorf("scan file association: %w", err)
		}
		entry := map[string]any{
			"id":         fileID,
			"file_path":  filePath,
			"created_at": fileCreatedAt,
			"active":     !fileValidUntil.Valid,
		}
		if fileValidUntil.Valid {
			entry["valid_until"] = fileValidUntil.String
		}
		result = append(result, entry)
	}
	if result == nil {
		result = []map[string]any{}
	}
	return result, rows.Err()
}

func queryAuditTemporal(ctx context.Context, database *sql.DB, obsID string) ([]map[string]any, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, raw_text, mention_kind, normalized_start, normalized_end, anchor_time, confidence, created_at
		FROM temporal_mentions
		WHERE observation_id = ?
		ORDER BY created_at ASC
	`, obsID)
	if err != nil {
		return nil, fmt.Errorf("query temporal mentions: %w", err)
	}
	defer rows.Close()

	var result []map[string]any
	for rows.Next() {
		var tmID, rawText, mentionKind, anchorTime, tmCreatedAt string
		var normalizedStart, normalizedEnd sql.NullString
		var tmConfidence float64
		if err := rows.Scan(&tmID, &rawText, &mentionKind, &normalizedStart, &normalizedEnd, &anchorTime, &tmConfidence, &tmCreatedAt); err != nil {
			return nil, fmt.Errorf("scan temporal mention: %w", err)
		}
		entry := map[string]any{
			"id":          tmID,
			"raw_text":    rawText,
			"kind":        mentionKind,
			"anchor_time": anchorTime,
			"confidence":  tmConfidence,
			"created_at":  tmCreatedAt,
		}
		if normalizedStart.Valid {
			entry["normalized_start"] = normalizedStart.String
		}
		if normalizedEnd.Valid {
			entry["normalized_end"] = normalizedEnd.String
		}
		result = append(result, entry)
	}
	if result == nil {
		result = []map[string]any{}
	}
	return result, rows.Err()
}

// nullStr returns the string value from a NullString, or empty string if null.
func nullStr(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

func runSessionStart(ctx context.Context, database *sql.DB, cfg config.Config) {
	fs := flag.NewFlagSet("session-start", flag.ExitOnError)
	title := fs.String("title", "", "Session title")
	directory := fs.String("directory", "", "Working directory (default: current directory)")
	branch := fs.String("branch", "", "Git branch")
	namespace := fs.String("namespace", "default", "Namespace")
	fs.Parse(os.Args[2:])

	// Default directory to cwd if not provided.
	if *directory == "" {
		cwd, err := os.Getwd()
		if err == nil {
			*directory = cwd
		}
	}

	d := initDepsSession(ctx, database, cfg)

	startResult, err := d.sessionManager.Start(ctx, *title, *directory, *branch, *namespace)
	if err != nil {
		log.Fatalf("session start: %v", err)
	}

	resp := map[string]any{
		"session_id": startResult.SessionID,
		"namespace":  startResult.Namespace,
		"abandoned":  startResult.Abandoned,
		"message":    "session started",
	}

	// Get proactive context for this session.
	if d.proactiveEngine != nil {
		cr, err := d.proactiveEngine.GetSessionContext(ctx, startResult.Namespace, *title, *directory, *branch, 15)
		if err == nil && cr.Count > 0 {
			resp["context"] = cr.Items
			resp["context_count"] = cr.Count
			if len(cr.Reflections) > 0 {
				resp["reflections"] = cr.Reflections
			}
		}
	}

	printJSON(resp)
}

func runSessionEnd(ctx context.Context, database *sql.DB, cfg config.Config) {
	fs := flag.NewFlagSet("session-end", flag.ExitOnError)
	sessionID := fs.String("session-id", "", "Session ID (required)")
	summary := fs.String("summary", "", "Session summary (required)")
	fs.Parse(os.Args[2:])

	if strings.TrimSpace(*sessionID) == "" {
		fmt.Fprintln(os.Stderr, "Usage: neurox session-end --session-id <id> --summary \"...\"")
		fs.PrintDefaults()
		os.Exit(1)
	}
	if strings.TrimSpace(*summary) == "" {
		fmt.Fprintln(os.Stderr, "Usage: neurox session-end --session-id <id> --summary \"...\"")
		fs.PrintDefaults()
		os.Exit(1)
	}

	d := initDepsSession(ctx, database, cfg)

	endResult, err := d.sessionManager.End(ctx, *sessionID, *summary, "cli")
	if err != nil {
		log.Fatalf("session end: %v", err)
	}

	resp := map[string]any{
		"session_id":             endResult.SessionID,
		"observations_extracted": endResult.ObservationsExtracted,
		"message":                "session completed",
	}
	if endResult.Warning != "" {
		resp["warning"] = endResult.Warning
	}

	printJSON(resp)
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

	// Create curator provider with extended timeout for large namespaces
	curatorProvider := llm.NewRemoteWithTimeout(llm.RemoteConfig{
		URL:    cfg.Curator.RemoteURL,
		APIKey: cfg.Curator.RemoteAPIKey,
		Model:  cfg.Curator.RemoteModel,
	}, 5*time.Minute)

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

func runReembed(ctx context.Context, database *sql.DB, cfg config.Config) {
	embedder := embed.AutoDetect(ctx, embed.OllamaConfig{URL: cfg.Embeddings.OllamaURL, Model: cfg.Embeddings.OllamaModel}, embed.RemoteConfig{
		URL: cfg.Embeddings.RemoteURL, APIKey: cfg.Embeddings.RemoteKey,
		Model: cfg.Embeddings.RemoteModel, Dimensions: cfg.Embeddings.Dimensions,
	})

	if !embed.IsAvailable(embedder) {
		log.Fatalf("no embedding provider available")
	}

	queue := embed.NewQueue(embedder, database)
	queue.Start(ctx)

	fmt.Println("Clearing all embeddings and re-embedding all observations...")
	if err := queue.ReembedAll(ctx); err != nil {
		queue.Stop()
		log.Fatalf("reembed failed: %v", err)
	}

	// Wait for queue to process
	fmt.Println("Waiting for embedding queue to process...")
	time.Sleep(2 * time.Second)
	queue.Stop()

	// Count embeddings
	var count int
	database.QueryRowContext(ctx, "SELECT COUNT(*) FROM observations WHERE embedding IS NOT NULL AND deleted_at IS NULL").Scan(&count)

	printJSON(map[string]any{
		"message":    "re-embed completed",
		"embeddings": count,
	})
}

func runExport(ctx context.Context, database *sql.DB) {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	format := fs.String("format", "md", "Export format: md, json")
	output := fs.String("output", "", "Output path (directory for md, file for json)")
	namespace := fs.String("namespace", "", "Namespace to export (empty = all)")
	fs.Parse(os.Args[2:])

	switch *format {
	case "md", "markdown":
		if *output == "" {
			*output = "./neurox-export"
		}
		count, err := exportpkg.ExportMarkdown(ctx, database, *namespace, *output)
		if err != nil {
			log.Fatalf("export: %v", err)
		}
		fmt.Printf("Exported %d observations to %s\n", count, *output)

	case "json":
		if *output == "" {
			*output = "neurox-export.json"
		}
		stats, err := exportpkg.ExportJSONWithStats(ctx, database, *namespace, *output)
		if err != nil {
			log.Fatalf("export: %v", err)
		}
		fmt.Printf("Exported to %s:\n", *output)
		fmt.Printf("  Observations:       %d\n", stats.Observations)
		fmt.Printf("  Links:              %d\n", stats.Links)
		fmt.Printf("  File observations:  %d\n", stats.FileObservations)
		fmt.Printf("  Facts:              %d\n", stats.Facts)
		fmt.Printf("  Temporal mentions:  %d\n", stats.TemporalMentions)
		fmt.Printf("  Sessions:           %d\n", stats.Sessions)
		fmt.Printf("  Reflections:        %d\n", stats.Reflections)
		fmt.Printf("  Consolidation runs: %d\n", stats.ConsolidationRuns)

	default:
		log.Fatalf("unsupported format: %s (supported: md, json)", *format)
	}
}

func runImport(ctx context.Context, database *sql.DB) {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	format := fs.String("format", "md", "Import format: md, json")
	source := fs.String("source", "", "Source path (directory for md, file for json) (required)")
	fs.Parse(os.Args[2:])

	if *source == "" {
		log.Fatalf("--source is required")
	}

	switch *format {
	case "md", "markdown":
		count, err := exportpkg.ImportMarkdown(ctx, database, *source)
		if err != nil {
			log.Fatalf("import: %v", err)
		}
		fmt.Printf("Imported %d observations from %s\n", count, *source)

	case "json":
		stats, err := exportpkg.ImportJSONWithStats(ctx, database, *source)
		if err != nil {
			log.Fatalf("import: %v", err)
		}
		fmt.Printf("Imported from %s:\n", *source)
		fmt.Printf("  Observations:       %d\n", stats.Observations)
		fmt.Printf("  Links:              %d\n", stats.Links)
		fmt.Printf("  File observations:  %d\n", stats.FileObservations)
		fmt.Printf("  Facts:              %d\n", stats.Facts)
		fmt.Printf("  Temporal mentions:  %d\n", stats.TemporalMentions)
		fmt.Printf("  Sessions:           %d\n", stats.Sessions)
		fmt.Printf("  Reflections:        %d\n", stats.Reflections)
		fmt.Printf("  Consolidation runs: %d\n", stats.ConsolidationRuns)

	default:
		log.Fatalf("unsupported format: %s (supported: md, json)", *format)
	}
}

func runBackup(ctx context.Context, database *sql.DB, cfg config.Config) {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	output := fs.String("output", "", "Destination path (default: <db_path>.backup)")
	fs.Parse(os.Args[2:])

	dest := *output
	if dest == "" {
		dest = db.DefaultBackupPath(cfg.Database.Path)
	}

	result, err := db.BackupWithResult(ctx, database, dest)
	if err != nil {
		log.Fatalf("backup: %v", err)
	}
	fmt.Printf("Backup completed: %s (%d bytes)\n", result.Path, result.SizeBytes)
}

func runGraph(ctx context.Context, database *sql.DB) {
	fs := flag.NewFlagSet("graph", flag.ExitOnError)
	namespace := fs.String("namespace", "", "Filter by namespace")
	obsType := fs.String("type", "", "Filter by observation type")
	tags := fs.String("tags", "", "Filter by tags (comma-separated)")
	minImportance := fs.Float64("min-importance", 0, "Minimum importance (0.0-1.0)")
	limit := fs.Int("limit", 2000, "Max nodes to display")
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

	// Async save queue: decouple MCP handler from SQLite writes.
	// All heavy work (LLM gate, SQLite write, facts, embeddings) runs in the
	// background worker so the MCP response is always instant.
	saveQueue := observation.NewSaveQueue(d.obsStore)
	if d.llmGate != nil {
		gate := d.llmGate
		saveQueue.OnPreSave(func(ctx context.Context, obs observation.Observation) bool {
			decision, _ := gate.SaveGateDecide(ctx, llm.SaveInput{
				Title:           obs.Title,
				Content:         obs.Content,
				ObservationType: string(obs.ObservationType),
			})
			return decision != llm.SaveReject
		})
	}
	if d.factExtractor != nil {
		fe := d.factExtractor
		saveQueue.OnPostSave(func(_ context.Context, saved observation.Observation) {
			go fe.ExtractAndSave(context.Background(), saved.ID, saved.Title, saved.Content, saved.Namespace)
		})
	}
	if d.embedQueue != nil {
		eq := d.embedQueue
		saveQueue.OnPostSave(func(_ context.Context, saved observation.Observation) {
			eq.Enqueue(saved.ID)
		})
	}
	saveQueue.Start(ctx)
	defer saveQueue.Stop()

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
		SaveQueue:        saveQueue,
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

	srv := neuroxmcp.NewServer(mcpDeps, version)
	if err := mcpserver.ServeStdio(srv); err != nil {
		log.Fatalf("mcp server error: %v", err)
	}
}

func runHTTP(ctx context.Context, database *sql.DB, cfg config.Config) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	host := fs.String("host", "127.0.0.1", "Bind address (e.g. 0.0.0.0 for all interfaces)")
	fs.Parse(os.Args[2:])

	// Allow env var override: NEUROX_HTTP_HOST
	if envHost := strings.TrimSpace(os.Getenv("NEUROX_HTTP_HOST")); envHost != "" {
		*host = envHost
	}

	d := initDeps(ctx, database, cfg)

	d.pipeline.Start(ctx)
	defer d.pipeline.Stop()
	if d.embedQueue != nil {
		defer d.embedQueue.Stop()
	}

	// Async save queue: decouple HTTP handler from SQLite writes,
	// mirroring the MCP save queue setup.
	saveQueue := observation.NewSaveQueue(d.obsStore)
	if d.llmGate != nil {
		gate := d.llmGate
		saveQueue.OnPreSave(func(ctx context.Context, obs observation.Observation) bool {
			decision, _ := gate.SaveGateDecide(ctx, llm.SaveInput{
				Title:           obs.Title,
				Content:         obs.Content,
				ObservationType: string(obs.ObservationType),
			})
			return decision != llm.SaveReject
		})
	}
	if d.factExtractor != nil {
		fe := d.factExtractor
		saveQueue.OnPostSave(func(_ context.Context, saved observation.Observation) {
			go fe.ExtractAndSave(context.Background(), saved.ID, saved.Title, saved.Content, saved.Namespace)
		})
	}
	if d.embedQueue != nil {
		eq := d.embedQueue
		saveQueue.OnPostSave(func(_ context.Context, saved observation.Observation) {
			eq.Enqueue(saved.ID)
		})
	}
	saveQueue.Start(ctx)
	defer saveQueue.Stop()

	var curateEngine *curate.Engine
	if llm.IsAvailable(d.curatorProvider) {
		priorities, prErr := curate.LoadPriorities(cfg.Curator.PrioritiesFile)
		if prErr != nil {
			log.Printf("load priorities: %v (curation will work without priorities)", prErr)
		}
		curateEngine = curate.NewEngine(database, d.curatorProvider, priorities, cfg.Curator.RemoteModel)
	}

	apiDeps := &api.Deps{
		ObservationStore:  d.obsStore,
		SaveQueue:         saveQueue,
		RecallEngine:      d.recallEngine,
		LinkStore:         d.linkStore,
		FactStore:         d.factStore,
		FactExtractor:     d.factExtractor,
		ReflectEngine:     d.reflectEngine,
		SessionManager:    d.sessionManager,
		ProactiveEngine:   d.proactiveEngine,
		Pipeline:          d.pipeline,
		CurateEngine:      curateEngine,
		DB:                database,
		LLMProvider:       d.llmProvider,
		LLMGate:           d.llmGate,
		EmbedQueue:        d.embedQueue,
		Embedder:          d.embedder,
		Tracker:           d.tracker,
		LLMProviderName:   d.llmProvider.Name(),
		EmbedProviderName: d.embedder.Name(),
		GateMode:          string(d.llmGate.Mode()),
		Version:           version,
	}

	srv := api.NewServer(api.Config{Host: *host, Port: defaultHTTPPort}, apiDeps)

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
	embedder := embed.AutoDetect(ctx, embed.OllamaConfig{URL: cfg.Embeddings.OllamaURL, Model: cfg.Embeddings.OllamaModel}, embed.RemoteConfig{
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
		curatorProvider = llm.NewRemoteWithTimeout(llm.RemoteConfig{
			URL:    cfg.Curator.RemoteURL,
			APIKey: cfg.Curator.RemoteAPIKey,
			Model:  cfg.Curator.RemoteModel,
		}, 5*time.Minute)
		log.Printf("using curator provider: %s", curatorProvider.Name())
	}

	idGen := observation.NewULIDGenerator()
	obsStore := observation.NewStore(database, nil)
	linkStore := links.NewStore(database, idGen)
	factStore := facts.NewStore(database, idGen)
	recallEngine := recall.NewEngine(database, recall.WithEmbedder(embedder), recall.WithFactStore(factStore))
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

	// Wire post-save hooks on session manager so observations extracted
	// from session summaries get fact extraction + embedding enqueue —
	// the same post-processing that the main save pipeline performs.
	if factExtractor != nil {
		fe := factExtractor
		sessionMgr.OnPostSave(func(_ context.Context, id, title, content, namespace string) {
			go fe.ExtractAndSave(context.Background(), id, title, content, namespace)
		})
	}
	if embedQueue != nil {
		eq := embedQueue
		sessionMgr.OnPostSave(func(_ context.Context, id, _, _, _ string) {
			eq.Enqueue(id)
		})
	}

	decayEngine := decay.NewEngine(database)
	pipelineCfg := consolidate.Config{
		Interval:         30 * time.Minute,
		DedupThreshold:   cfg.Consolidation.DedupThreshold,
		ContradictionMin: cfg.Consolidation.ContradictionMin,
		ContradictionMax: cfg.Consolidation.ContradictionMax,
		RelatedMin:       cfg.Consolidation.RelatedMin,
		RelatedMax:       cfg.Consolidation.RelatedMax,
	}
	pipeline := consolidate.NewPipeline(database, decayEngine, embedder, embedQueue, gate, linkStore, llmProvider, curatorProvider, idGen, pipelineCfg)

	// Wire ModelTracker for auto-reembedding on model change
	if embed.IsAvailable(embedder) && embedQueue != nil {
		modelTracker := embed.NewModelTracker(database, embedQueue)
		if err := modelTracker.CheckAndMigrate(ctx, embedder); err != nil {
			log.Printf("model tracker check: %v", err)
		}
	}

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

// initDepsSession creates deps for session CLI commands: SessionManager + ProactiveEngine.
// It does not start background workers (consolidation pipeline, embed queue).
func initDepsSession(ctx context.Context, database *sql.DB, cfg config.Config) *deps {
	embedder := embed.AutoDetect(ctx, embed.OllamaConfig{URL: cfg.Embeddings.OllamaURL, Model: cfg.Embeddings.OllamaModel}, embed.RemoteConfig{
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
	factExtractor := facts.NewExtractor(llmProvider, factStore)
	sessionMgr := session.NewManager(database, llmProvider, idGen)
	proactiveEng := proactive.NewEngine(database, embedder)

	// Wire temporal extraction for session-derived observations.
	temporalStore := temporal.NewStore(database, idGen)
	temporalExtractor := temporal.NewExtractor(temporal.NewParser(), temporalStore)
	sessionMgr.SetTemporalExtractor(temporalExtractor)

	// Wire post-save hooks for session-derived observations (fact extraction).
	// No embed queue in session CLI mode — embedding happens asynchronously
	// when a full MCP/HTTP server runs.
	if factExtractor != nil {
		fe := factExtractor
		sessionMgr.OnPostSave(func(_ context.Context, id, title, content, namespace string) {
			go fe.ExtractAndSave(context.Background(), id, title, content, namespace)
		})
	}

	return &deps{
		sessionManager:  sessionMgr,
		proactiveEngine: proactiveEng,
		embedder:        embedder,
		llmProvider:     llmProvider,
		llmGate:         gate,
	}
}

// initDepsLight creates minimal deps for CLI commands that don't need background workers.
func initDepsLight(ctx context.Context, database *sql.DB, cfg config.Config) *deps {
	embedder := embed.AutoDetect(ctx, embed.OllamaConfig{URL: cfg.Embeddings.OllamaURL, Model: cfg.Embeddings.OllamaModel}, embed.RemoteConfig{
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

	if err := installer.RunUpdate(version, *yes); err != nil {
		fmt.Fprintf(os.Stderr, "update failed: %v\n", err)
		os.Exit(1)
	}
}

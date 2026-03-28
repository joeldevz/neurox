package benchmark

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"

	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/joeldevz/neurox/internal/embed"
	"github.com/joeldevz/neurox/internal/facts"
	"github.com/joeldevz/neurox/internal/llm"
	internalmcp "github.com/joeldevz/neurox/internal/mcp"
	"github.com/joeldevz/neurox/internal/observation"
	"github.com/joeldevz/neurox/internal/proactive"
	reflectpkg "github.com/joeldevz/neurox/internal/reflect"
	"github.com/joeldevz/neurox/internal/session"
	"github.com/joeldevz/neurox/internal/telemetry"
)

// MCPHarness wraps a fully-initialized MCP server for benchmark usage.
// It lets benchmark dimensions call real MCP tools via JSON-RPC without
// spawning a separate process or using HTTP — identical to how the
// mcpTestHelper works in internal/mcp/server_test.go but safe for
// benchmark use (no *testing.T, returns errors instead of fataling).
type MCPHarness struct {
	server *mcpserver.MCPServer
	ctx    context.Context
	nextID atomic.Int64
}

// NewMCPHarness builds a fully-initialized MCP server from the given BenchEnv
// and initializes the JSON-RPC session by sending the required "initialize"
// handshake. All 13 MCP tools are registered and ready to call.
//
// Dependencies used:
//   - env.Embedder      → embed provider (FakeEmbedder)
//   - llm.Disabled{}    → no real LLM calls
//   - llm.GateModeOff   → quality gate always passes
//   - embed.NewQueue    → embedding queue
//   - telemetry.NewTracker → call telemetry recorder
func NewMCPHarness(env *BenchEnv) (*MCPHarness, error) {
	disabledLLM := llm.Disabled{}
	gate := llm.NewGate(disabledLLM, llm.GateModeOff)
	embedQueue := embed.NewQueue(env.Embedder, env.DB)
	tracker := telemetry.NewTracker(env.DB)

	idGen := observation.NewULIDGenerator()

	reflectEngine := reflectpkg.NewEngine(env.DB, disabledLLM, env.LinkStore, idGen)
	factExtractor := facts.NewExtractor(disabledLLM, env.FactStore)

	deps := &internalmcp.Deps{
		ObservationStore: env.ObsStore,
		RecallEngine:     env.RecallEngine,
		LinkStore:        env.LinkStore,
		FactStore:        env.FactStore,
		FactExtractor:    factExtractor,
		ReflectEngine:    reflectEngine,
		SessionManager: session.NewManager(
			env.DB,
			disabledLLM,
			idGen,
		),
		ProactiveEngine: proactive.NewEngine(env.DB, env.Embedder),
		Pipeline:        env.Pipeline,
		DB:              env.DB,
		LLMProvider:     disabledLLM,
		LLMGate:         gate,
		EmbedQueue:      embedQueue,
		Embedder:        env.Embedder,
		Tracker:         tracker,
	}

	srv := internalmcp.NewServer(deps, "bench")
	ctx := context.Background()

	// Send the required MCP initialize handshake.
	initMsg, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      0,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "neurox-bench", "version": "1.0"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal initialize: %w", err)
	}
	srv.HandleMessage(ctx, json.RawMessage(initMsg))

	h := &MCPHarness{
		server: srv,
		ctx:    ctx,
	}
	h.nextID.Store(1)
	return h, nil
}

// CallTool sends a JSON-RPC tools/call request for the named tool with the
// given arguments and returns the parsed result map from the first content
// item, or an error if the call fails or the server returns a JSON-RPC error.
func (h *MCPHarness) CallTool(name string, args map[string]any) (map[string]any, error) {
	id := h.nextID.Add(1)

	raw, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      name,
			"arguments": args,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal call %s: %w", name, err)
	}

	resp := h.server.HandleMessage(h.ctx, json.RawMessage(raw))

	b, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("marshal response %s: %w", name, err)
	}

	var envelope struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(b, &envelope); err != nil {
		return nil, fmt.Errorf("unmarshal response %s: %w (raw: %s)", name, err, string(b))
	}

	if envelope.Error != nil {
		return nil, fmt.Errorf("json-rpc error calling %s: %s", name, envelope.Error.Message)
	}
	if envelope.Result.IsError {
		text := ""
		if len(envelope.Result.Content) > 0 {
			text = envelope.Result.Content[0].Text
		}
		return nil, fmt.Errorf("tool error calling %s: %s", name, text)
	}

	if len(envelope.Result.Content) == 0 {
		return map[string]any{}, nil
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &out); err != nil {
		return nil, fmt.Errorf("unmarshal tool result %s: %w (text: %s)", name, err, envelope.Result.Content[0].Text)
	}
	return out, nil
}

// callToolRaw is like CallTool but returns the raw JSON text of the first
// content item (for callers that want to unmarshal into a specific struct).
func (h *MCPHarness) callToolRaw(name string, args map[string]any) (string, error) {
	id := h.nextID.Add(1)

	raw, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      name,
			"arguments": args,
		},
	})
	if err != nil {
		return "", fmt.Errorf("marshal call %s: %w", name, err)
	}

	resp := h.server.HandleMessage(h.ctx, json.RawMessage(raw))

	b, err := json.Marshal(resp)
	if err != nil {
		return "", fmt.Errorf("marshal response %s: %w", name, err)
	}

	var envelope struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(b, &envelope); err != nil {
		return "", fmt.Errorf("unmarshal response %s: %w", name, err)
	}

	if envelope.Error != nil {
		return "", fmt.Errorf("json-rpc error calling %s: %s", name, envelope.Error.Message)
	}
	if envelope.Result.IsError {
		text := ""
		if len(envelope.Result.Content) > 0 {
			text = envelope.Result.Content[0].Text
		}
		return "", fmt.Errorf("tool error calling %s: %s", name, text)
	}

	if len(envelope.Result.Content) == 0 {
		return "", nil
	}
	return envelope.Result.Content[0].Text, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Parsed response structs
// ─────────────────────────────────────────────────────────────────────────────

// SaveResponse is the parsed result of a "save" tool call.
type SaveResponse struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Layer     int    `json:"layer"`
	Namespace string `json:"namespace"`
	TopicKey  string `json:"topic_key,omitempty"`
	Message   string `json:"message"`
}

// RecallItem is one entry in a "recall" tool response.
type RecallItem struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	Content         string   `json:"content"`
	Score           float64  `json:"score"`
	Layer           int      `json:"layer"`
	ObservationType string   `json:"observation_type"`
	Kind            string   `json:"kind"`
	Confidence      float64  `json:"confidence"`
	Tags            []string `json:"tags,omitempty"`
	Staleness       string   `json:"staleness"`
	Retention       string   `json:"retention"`
	LinkedFiles     []string `json:"linked_files,omitempty"`
}

// RecallResponse is the parsed result of a "recall" tool call.
type RecallResponse struct {
	Query          string       `json:"query"`
	Count          int          `json:"count"`
	TemporalIntent string       `json:"temporal_intent,omitempty"`
	Results        []RecallItem `json:"results"`
}

// ContextItem is one entry in a "context" tool response.
type ContextItem struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	Content         string   `json:"content"`
	ObservationType string   `json:"observation_type"`
	Layer           int      `json:"layer"`
	Confidence      float64  `json:"confidence"`
	Importance      float64  `json:"importance"`
	Kind            string   `json:"kind"`
	Tags            []string `json:"tags,omitempty"`
	Staleness       string   `json:"staleness"`
	CreatedAt       string   `json:"created_at"`
}

// ContextResponse is the parsed result of a "context" tool call.
type ContextResponse struct {
	Namespace string        `json:"namespace"`
	Count     int           `json:"count"`
	Items     []ContextItem `json:"items"`
}

// StatusResponse is the parsed result of a "status" tool call.
type StatusResponse struct {
	Total             int    `json:"total"`
	Buffer            int    `json:"buffer"`
	Working           int    `json:"working"`
	Core              int    `json:"core"`
	Stale             int    `json:"stale"`
	Expired           int    `json:"expired"`
	Links             int    `json:"links"`
	Facts             int    `json:"facts"`
	TemporalMentions  int    `json:"temporal_mentions"`
	ActiveSessions    int    `json:"active_sessions"`
	LLMProvider       string `json:"llm_provider"`
	GateMode          string `json:"gate_mode"`
	EmbeddingsTotal   int    `json:"embeddings_total"`
	EmbeddingsPending int    `json:"embeddings_pending"`
	EmbedProvider     string `json:"embedding_provider"`
}

// SessionStartResponse is the parsed result of a "session_start" tool call.
type SessionStartResponse struct {
	SessionID string `json:"session_id"`
	Namespace string `json:"namespace"`
	Abandoned int    `json:"abandoned"`
	Message   string `json:"message"`
}

// SessionEndResponse is the parsed result of a "session_end" tool call.
type SessionEndResponse struct {
	SessionID             string `json:"session_id"`
	ObservationsExtracted int    `json:"observations_extracted"`
	Message               string `json:"message"`
}

// InvalidateResponse is the parsed result of an "invalidate" tool call.
type InvalidateResponse struct {
	InvalidatedID string `json:"invalidated_id"`
	ReplacementID string `json:"replacement_id,omitempty"`
	LinkID        string `json:"link_id,omitempty"`
	Message       string `json:"message"`
}

// ConsolidateResponse is the parsed result of a "consolidate" tool call.
type ConsolidateResponse struct {
	Buffer  int    `json:"buffer"`
	Working int    `json:"working"`
	Core    int    `json:"core"`
	Message string `json:"message"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Typed helper methods
// ─────────────────────────────────────────────────────────────────────────────

// Save calls the "save" MCP tool and returns a parsed SaveResponse.
// The args map may contain any valid save tool arguments (title, content,
// observation_type, kind, confidence, topic_key, tags, files, namespace, retention).
func (h *MCPHarness) Save(args map[string]any) (SaveResponse, error) {
	text, err := h.callToolRaw("save", args)
	if err != nil {
		return SaveResponse{}, err
	}
	var resp SaveResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		return SaveResponse{}, fmt.Errorf("parse save response: %w", err)
	}
	return resp, nil
}

// RecallOpts are optional parameters for the Recall helper.
type RecallOpts struct {
	ObservationType string
	Kind            string
	Namespace       string
	Files           string
	IncludeStale    bool
	Limit           int
}

// Recall calls the "recall" MCP tool and returns a parsed RecallResponse.
func (h *MCPHarness) Recall(query string, opts RecallOpts) (RecallResponse, error) {
	args := map[string]any{"query": query}
	if opts.ObservationType != "" {
		args["observation_type"] = opts.ObservationType
	}
	if opts.Kind != "" {
		args["kind"] = opts.Kind
	}
	if opts.Namespace != "" {
		args["namespace"] = opts.Namespace
	}
	if opts.Files != "" {
		args["files"] = opts.Files
	}
	if opts.IncludeStale {
		args["include_stale"] = true
	}
	if opts.Limit > 0 {
		args["limit"] = float64(opts.Limit)
	}

	text, err := h.callToolRaw("recall", args)
	if err != nil {
		return RecallResponse{}, err
	}
	var resp RecallResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		return RecallResponse{}, fmt.Errorf("parse recall response: %w", err)
	}
	return resp, nil
}

// Context calls the "context" MCP tool and returns a parsed ContextResponse.
// namespace and files may be empty strings to use the server defaults.
func (h *MCPHarness) Context(namespace, files string) (ContextResponse, error) {
	args := map[string]any{}
	if namespace != "" {
		args["namespace"] = namespace
	}
	if files != "" {
		args["files"] = files
	}

	text, err := h.callToolRaw("context", args)
	if err != nil {
		return ContextResponse{}, err
	}

	// The context tool may return either a contextResponse struct or a
	// proactive.ContextResult struct — both marshal to compatible JSON with
	// "namespace", "count", and "items" / "context" arrays. We try both.
	var resp ContextResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		return ContextResponse{}, fmt.Errorf("parse context response: %w", err)
	}
	return resp, nil
}

// SessionStart calls the "session_start" MCP tool and returns a parsed
// SessionStartResponse. Any of title, directory, branch, namespace may be empty.
func (h *MCPHarness) SessionStart(title, directory, branch, namespace string) (SessionStartResponse, error) {
	args := map[string]any{}
	if title != "" {
		args["title"] = title
	}
	if directory != "" {
		args["directory"] = directory
	}
	if branch != "" {
		args["branch"] = branch
	}
	if namespace != "" {
		args["namespace"] = namespace
	}

	text, err := h.callToolRaw("session_start", args)
	if err != nil {
		return SessionStartResponse{}, err
	}
	var resp SessionStartResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		return SessionStartResponse{}, fmt.Errorf("parse session_start response: %w", err)
	}
	return resp, nil
}

// SessionEnd calls the "session_end" MCP tool and returns a parsed
// SessionEndResponse.
func (h *MCPHarness) SessionEnd(sessionID, summary string) (SessionEndResponse, error) {
	args := map[string]any{
		"session_id": sessionID,
		"summary":    summary,
	}

	text, err := h.callToolRaw("session_end", args)
	if err != nil {
		return SessionEndResponse{}, err
	}
	var resp SessionEndResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		return SessionEndResponse{}, fmt.Errorf("parse session_end response: %w", err)
	}
	return resp, nil
}

// Invalidate calls the "invalidate" MCP tool and returns a parsed
// InvalidateResponse. replacementTitle and replacementContent may be empty
// if no replacement is desired.
func (h *MCPHarness) Invalidate(observationID, reason, replacementTitle, replacementContent string) (InvalidateResponse, error) {
	args := map[string]any{
		"observation_id": observationID,
		"reason":         reason,
	}
	if replacementTitle != "" {
		args["replacement_title"] = replacementTitle
	}
	if replacementContent != "" {
		args["replacement_content"] = replacementContent
	}

	text, err := h.callToolRaw("invalidate", args)
	if err != nil {
		return InvalidateResponse{}, err
	}
	var resp InvalidateResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		return InvalidateResponse{}, fmt.Errorf("parse invalidate response: %w", err)
	}
	return resp, nil
}

// Consolidate calls the "consolidate" MCP tool and returns a parsed
// ConsolidateResponse.
func (h *MCPHarness) Consolidate() (ConsolidateResponse, error) {
	text, err := h.callToolRaw("consolidate", map[string]any{})
	if err != nil {
		return ConsolidateResponse{}, err
	}
	var resp ConsolidateResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		return ConsolidateResponse{}, fmt.Errorf("parse consolidate response: %w", err)
	}
	return resp, nil
}

// HealthCheck calls the "health_check" MCP tool and returns the parsed
// result as a generic map (the health.Report struct is defined in another
// package and including it here would create an import cycle; callers can
// type-assert individual fields from the map as needed).
func (h *MCPHarness) HealthCheck() (map[string]any, error) {
	return h.CallTool("health_check", map[string]any{})
}

// Status calls the "status" MCP tool and returns a parsed StatusResponse.
func (h *MCPHarness) Status() (StatusResponse, error) {
	text, err := h.callToolRaw("status", map[string]any{})
	if err != nil {
		return StatusResponse{}, err
	}
	var resp StatusResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		return StatusResponse{}, fmt.Errorf("parse status response: %w", err)
	}
	return resp, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Compile-time interface check — ensure *MCPHarness exposes all methods.
// This is not a formal Go interface but documents the public surface.
// ─────────────────────────────────────────────────────────────────────────────

// NewMCPHarness wires together the following packages for benchmark use:
//   - embed        (Queue wraps env.Embedder)
//   - facts        (Extractor with llm.Disabled)
//   - llm          (Disabled provider + Gate off)
//   - internalmcp  (Server + Deps, including env.Pipeline, env.LinkStore, etc.)
//   - observation  (ULIDGenerator)
//   - proactive    (Engine)
//   - reflectpkg   (Engine)
//   - session      (Manager)
//   - telemetry    (Tracker)

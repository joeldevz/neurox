package api

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/joeldevz/neurox/internal/consolidate"
	curatepkg "github.com/joeldevz/neurox/internal/curate"
	"github.com/joeldevz/neurox/internal/embed"
	"github.com/joeldevz/neurox/internal/facts"
	"github.com/joeldevz/neurox/internal/links"
	"github.com/joeldevz/neurox/internal/llm"
	"github.com/joeldevz/neurox/internal/observation"
	"github.com/joeldevz/neurox/internal/proactive"
	"github.com/joeldevz/neurox/internal/recall"
	reflectpkg "github.com/joeldevz/neurox/internal/reflect"
	"github.com/joeldevz/neurox/internal/session"
	"github.com/joeldevz/neurox/internal/telemetry"
)

type Config struct {
	Host string
	Port int
}

type Server struct {
	httpServer *http.Server
	deps       *Deps
}

type Deps struct {
	ObservationStore *observation.Store
	SaveQueue        *observation.SaveQueue
	RecallEngine     *recall.Engine
	LinkStore        *links.Store
	FactStore        *facts.Store
	FactExtractor    *facts.Extractor
	ReflectEngine    *reflectpkg.Engine
	SessionManager   *session.Manager
	ProactiveEngine  *proactive.Engine
	Pipeline         *consolidate.Pipeline
	CurateEngine     *curatepkg.Engine
	DB               *sql.DB
	LLMProvider      llm.Provider
	LLMGate          *llm.Gate
	EmbedQueue       *embed.Queue
	Embedder         embed.Provider
	Tracker          *telemetry.Tracker
	// Display-only fields used by dashboard/status handlers.
	LLMProviderName   string
	EmbedProviderName string
	GateMode          string
	Version           string
}

func NewServer(cfg Config, deps *Deps) *Server {
	mux := http.NewServeMux()
	s := &Server{
		deps: deps,
		httpServer: &http.Server{
			Addr:         fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
			Handler:      corsMiddleware(mux),
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
			IdleTimeout:  60 * time.Second,
		},
	}

	s.registerRoutes(mux)
	return s
}

func (s *Server) ListenAndServe() error {
	ln, err := net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.httpServer.Addr, err)
	}
	log.Printf("neurox HTTP server listening on %s", ln.Addr())
	return s.httpServer.Serve(ln)
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /", s.handleDashboard)
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/status", s.handleStatus)
	mux.HandleFunc("GET /api/v1/observations/browse", s.handleBrowse)
	mux.HandleFunc("GET /api/v1/stats/breakdown", s.handleBreakdown)

	mux.HandleFunc("POST /api/v1/observations", s.handleSave)
	mux.HandleFunc("GET /api/v1/observations/search", s.handleRecall)
	mux.HandleFunc("GET /api/v1/observations/context", s.handleContext)
	mux.HandleFunc("GET /api/v1/observations/{id}", s.handleGet)
	mux.HandleFunc("PUT /api/v1/observations/{id}", s.handleUpdate)
	mux.HandleFunc("DELETE /api/v1/observations/{id}", s.handleForget)
	mux.HandleFunc("POST /api/v1/observations/{id}/invalidate", s.handleInvalidate)

	mux.HandleFunc("POST /api/v1/sessions", s.handleSessionStart)
	mux.HandleFunc("PUT /api/v1/sessions/{id}/end", s.handleSessionEnd)

	mux.HandleFunc("POST /api/v1/hooks/git", s.handleGitHook)
	mux.HandleFunc("POST /api/v1/reflect", s.handleReflect)
	mux.HandleFunc("POST /api/v1/consolidate", s.handleConsolidate)
	mux.HandleFunc("POST /api/v1/curate", s.handleCurate)
	mux.HandleFunc("GET /api/v1/graph", s.handleGraph)
	mux.HandleFunc("GET /api/v1/health-check", s.handleHealthCheck)
	mux.HandleFunc("POST /api/v1/backup", s.handleBackup)
	mux.HandleFunc("GET /api/v1/decay-timeline", s.handleDecayTimeline)
	mux.HandleFunc("GET /api/v1/stats/activity", s.handleActivity)

	// MCP registry discovery
	mux.HandleFunc("GET /.well-known/mcp/server-card.json", s.handleServerCard)
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

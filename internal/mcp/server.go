package mcp

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	ServerName    = "neurox"
	ServerVersion = "0.1.17"
)

func NewServer(deps *Deps) *server.MCPServer {
	s := server.NewMCPServer(
		ServerName,
		ServerVersion,
		server.WithToolCapabilities(false),
		server.WithRecovery(),
	)

	register(s, deps)
	return s
}

func register(s *server.MCPServer, deps *Deps) {
	add := func(tool mcp.Tool, handler server.ToolHandlerFunc) {
		s.AddTool(tool, handler)
	}

	add(saveTool(), deps.handleSave)
	add(recallTool(), deps.handleRecall)
	add(contextTool(), deps.handleContext)
	add(updateTool(), deps.handleUpdate)
	add(forgetTool(), deps.handleForget)
	add(invalidateTool(), deps.handleInvalidate)
	add(statusTool(), deps.handleStatus)
	add(sessionStartTool(), deps.handleSessionStart)
	add(sessionEndTool(), deps.handleSessionEnd)
	add(gitHookTool(), deps.handleGitHook)
	add(reflectTool(), deps.handleReflect)
	add(consolidateTool(), deps.handleConsolidate)
	add(healthCheckTool(), deps.handleHealthCheck)
	add(curateTool(), deps.handleCurate)
}

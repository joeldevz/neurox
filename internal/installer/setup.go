package installer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// AgentInfo describes a supported AI agent for the setup command.
type AgentInfo struct {
	Name        string // CLI-friendly name (e.g. "claude-code")
	Description string // Human-readable description
}

// SupportedAgents returns all agents that can be configured with `neurox setup`.
func SupportedAgents() []AgentInfo {
	return []AgentInfo{
		{Name: "claude-code", Description: "Claude Code (claude.ai/code)"},
		{Name: "claude-desktop", Description: "Claude Desktop app"},
		{Name: "opencode", Description: "OpenCode terminal IDE"},
		{Name: "cursor", Description: "Cursor editor"},
		{Name: "vscode", Description: "VS Code (workspace-level)"},
		{Name: "antigravity", Description: "Antigravity / Gemini CLI"},
	}
}

// Setup configures a specific AI agent to use Neurox as MCP server.
// It writes the minimum config needed — no questions asked.
// For agents that support instruction files (Claude Code, OpenCode,
// Antigravity), it also injects the Neurox behavioral protocol.
func Setup(agent string) error {
	agent = strings.ToLower(strings.TrimSpace(agent))

	configPath, err := agentConfigPath(agent)
	if err != nil {
		return err
	}

	neuroxCmd := findNeuroxBinary()

	// 1. Write MCP server config.
	switch agent {
	case "opencode":
		if err := setupOpenCode(configPath, neuroxCmd); err != nil {
			return err
		}
	case "vscode":
		if err := setupVSCode(configPath, neuroxCmd); err != nil {
			return err
		}
	default:
		// claude-code, claude-desktop, cursor, antigravity all use mcpServers
		if err := setupMCPServers(agent, configPath, neuroxCmd); err != nil {
			return err
		}
	}

	// 2. Install behavioral protocol into instruction files.
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home dir: %w", err)
	}

	switch agent {
	case "claude-code":
		// Also install Claude skill (SKILL.md)
		if err := installClaudeSkill(homeDir); err != nil {
			fmt.Printf("  warning: Claude Code skill: %v\n", err)
		} else {
			fmt.Printf("  + Installed skill → %s\n", filepath.Join(homeDir, ".claude", "skills", "neurox", "SKILL.md"))
		}
		if err := installClaudeProtocol(homeDir); err != nil {
			fmt.Printf("  warning: Claude Code protocol: %v\n", err)
		} else {
			fmt.Printf("  + Injected protocol → %s\n", filepath.Join(homeDir, ".claude", "CLAUDE.md"))
		}
	case "opencode":
		if err := installOpenCodeProtocol(homeDir); err != nil {
			fmt.Printf("  warning: OpenCode protocol: %v\n", err)
		} else {
			fmt.Printf("  + Injected protocol → %s\n", openCodeAgentsPath(homeDir))
		}
	case "antigravity":
		if err := installAntigravityProtocol(homeDir); err != nil {
			fmt.Printf("  warning: Antigravity protocol: %v\n", err)
		} else {
			fmt.Printf("  + Injected protocol → %s\n", filepath.Join(homeDir, ".gemini", "GEMINI.md"))
		}
	}

	return nil
}

// agentConfigPath returns the config file path for a given agent.
func agentConfigPath(agent string) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}

	xdgConfig := os.Getenv("XDG_CONFIG_HOME")
	if xdgConfig == "" {
		xdgConfig = filepath.Join(homeDir, ".config")
	}

	switch agent {
	case "claude-code":
		return filepath.Join(homeDir, ".claude.json"), nil
	case "claude-desktop":
		p := claudeDesktopConfigPath(homeDir)
		if p == "" {
			return "", fmt.Errorf("claude-desktop is not supported on %s", runtime.GOOS)
		}
		return p, nil
	case "opencode":
		return filepath.Join(xdgConfig, "opencode", "opencode.json"), nil
	case "cursor":
		return filepath.Join(homeDir, ".cursor", "mcp.json"), nil
	case "vscode":
		return filepath.Join(".vscode", "mcp.json"), nil
	case "antigravity":
		return filepath.Join(homeDir, ".gemini", "antigravity", "mcp_config.json"), nil
	default:
		return "", fmt.Errorf("unknown agent: %q\n\nSupported agents: %s", agent, agentList())
	}
}

// agentList returns a comma-separated list of supported agent names.
func agentList() string {
	agents := SupportedAgents()
	names := make([]string, len(agents))
	for i, a := range agents {
		names[i] = a.Name
	}
	return strings.Join(names, ", ")
}

// findNeuroxBinary returns the path to the neurox binary.
// If the current executable can be resolved, use its absolute path;
// otherwise fall back to just "neurox" (assumes it's in PATH).
func findNeuroxBinary() string {
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			return resolved
		}
		return exe
	}
	return "neurox"
}

// --- mcpServers format (claude-code, claude-desktop, cursor, antigravity) ---

func setupMCPServers(agent, configPath, neuroxCmd string) error {
	data, err := readOrCreateJSON(configPath)
	if err != nil {
		return err
	}

	servers, _ := data["mcpServers"].(map[string]interface{})
	if servers == nil {
		servers = make(map[string]interface{})
	}

	if _, exists := servers["neurox"]; exists {
		fmt.Printf("✓ neurox already configured in %s\n", configPath)
		return nil
	}

	servers["neurox"] = map[string]interface{}{
		"command": neuroxCmd,
		"args":    []interface{}{"mcp"},
	}
	data["mcpServers"] = servers

	if err := writeJSON(configPath, data); err != nil {
		return err
	}
	fmt.Printf("✓ Configured %s → %s\n", agent, configPath)
	return nil
}

// --- OpenCode format ---

func setupOpenCode(configPath, neuroxCmd string) error {
	data, err := readOrCreateJSON(configPath)
	if err != nil {
		return err
	}

	mcp, _ := data["mcp"].(map[string]interface{})
	if mcp == nil {
		mcp = make(map[string]interface{})
	}

	if _, exists := mcp["neurox"]; exists {
		fmt.Printf("✓ neurox already configured in %s\n", configPath)
		return nil
	}

	mcp["neurox"] = map[string]interface{}{
		"type":    "local",
		"command": []interface{}{neuroxCmd, "mcp"},
		"enabled": true,
	}
	data["mcp"] = mcp

	if err := writeJSON(configPath, data); err != nil {
		return err
	}
	fmt.Printf("✓ Configured opencode → %s\n", configPath)
	return nil
}

// --- VS Code format ---

func setupVSCode(configPath, neuroxCmd string) error {
	data, err := readOrCreateJSON(configPath)
	if err != nil {
		return err
	}

	servers, _ := data["servers"].(map[string]interface{})
	if servers == nil {
		servers = make(map[string]interface{})
	}

	if _, exists := servers["neurox"]; exists {
		fmt.Printf("✓ neurox already configured in %s\n", configPath)
		return nil
	}

	servers["neurox"] = map[string]interface{}{
		"command": neuroxCmd,
		"args":    []interface{}{"mcp"},
	}
	data["servers"] = servers

	if err := writeJSON(configPath, data); err != nil {
		return err
	}
	fmt.Printf("✓ Configured vscode → %s\n", configPath)
	return nil
}

// --- JSON helpers ---

// readOrCreateJSON reads an existing JSON file into a map, or returns an
// empty map if the file does not exist.
func readOrCreateJSON(path string) (map[string]interface{}, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]interface{}), nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	// Handle empty files
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return make(map[string]interface{}), nil
	}

	var data map[string]interface{}
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("parse %s: %w (file may be malformed)", path, err)
	}
	return data, nil
}

// writeJSON writes a map to a JSON file with 2-space indentation.
// It creates parent directories if needed.
func writeJSON(path string, data map[string]interface{}) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}

	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal JSON: %w", err)
	}
	out = append(out, '\n')

	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

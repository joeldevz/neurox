package installer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"gopkg.in/yaml.v3"

	"github.com/joeldevz/neurox/internal/config"
)

type Environment struct {
	SourceDir           string
	HomeDir             string
	DefaultConfigDir    string
	PreferredShellRC    string
	GitRoot             string
	ClaudeConfigPath    string
	ClaudeDesktopConfig string
	OpenCodeConfig      string
	CursorConfig        string
	AntigravityConfig   string
	OllamaAvailable     bool
	OllamaEmbedModel    string
	OllamaLLMModel      string
}

type state struct {
	ConfigDir              string
	EmbedProvider          string
	EmbedRemoteURL         string
	EmbedRemoteKey         string
	EmbedRemoteModel       string
	LLMProvider            string
	LLMRemoteURL           string
	LLMRemoteKey           string
	LLMRemoteModel         string
	GateMode               string
	ConfigureClaude        bool
	ConfigureClaudeDesktop bool
	ConfigureOpenCode      bool
	ConfigureCursor        bool
	ConfigureAntigravity   bool
	InstallHook            bool
}

type stepKind int

const (
	stepWelcome stepKind = iota
	stepProviders
	stepIntegrations
	stepReview
	stepInstalling
	stepDone
)

type fieldType int

const (
	fieldText fieldType = iota
	fieldSelect
	fieldToggle
	fieldAction
)

type field struct {
	Type        fieldType
	Key         string
	Label       string
	Description string
	Options     []string
	Masked      bool
	Danger      bool
}

type model struct {
	env         Environment
	state       state
	step        stepKind
	cursor      int
	editing     bool
	inputBuffer string
	status      string
	width       int
	height      int
	done        bool
	result      installResult
	spinner     int

	titleStyle         lipgloss.Style
	subtleStyle        lipgloss.Style
	mutedStyle         lipgloss.Style
	headingStyle       lipgloss.Style
	selectedStyle      lipgloss.Style
	cardStyle          lipgloss.Style
	sidebarStyle       lipgloss.Style
	tagStyle           lipgloss.Style
	okStyle            lipgloss.Style
	warnStyle          lipgloss.Style
	errorStyle         lipgloss.Style
	accentStyle        lipgloss.Style
	buttonStyle        lipgloss.Style
	buttonGhostStyle   lipgloss.Style
	progressOnStyle    lipgloss.Style
	progressOffStyle   lipgloss.Style
	selectedBoxStyle   lipgloss.Style
	unselectedBoxStyle lipgloss.Style
}

type installFinishedMsg struct {
	result installResult
}

type spinnerTickMsg struct{}

type installResult struct {
	BinaryPath string
	ConfigFile string
	Database   string
	Updated    []string
	Warnings   []string
	Err        error
}

func Run(ctx context.Context, sourceDir string) error {
	env, err := detectEnvironment(sourceDir)
	if err != nil {
		return err
	}
	p := tea.NewProgram(newModel(env), tea.WithAltScreen())
	_, err = p.Run()
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func detectEnvironment(sourceDir string) (Environment, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return Environment{}, fmt.Errorf("resolve home dir: %w", err)
	}
	// Always use ~/.config for neurox — it's a CLI tool, not a GUI app.
	// os.UserConfigDir() returns ~/Library/Application Support on macOS
	// which is wrong for terminal tools. Use XDG_CONFIG_HOME if set,
	// otherwise fall back to ~/.config.
	xdgConfig := os.Getenv("XDG_CONFIG_HOME")
	if xdgConfig == "" {
		xdgConfig = filepath.Join(homeDir, ".config")
	}
	env := Environment{
		SourceDir:           sourceDir,
		HomeDir:             homeDir,
		DefaultConfigDir:    filepath.Join(xdgConfig, "neurox"),
		PreferredShellRC:    preferredShellRC(homeDir),
		ClaudeConfigPath:    filepath.Join(homeDir, ".claude.json"),
		ClaudeDesktopConfig: claudeDesktopConfigPath(homeDir),
		OpenCodeConfig:      filepath.Join(xdgConfig, "opencode", "opencode.json"),
		CursorConfig:        filepath.Join(homeDir, ".cursor", "mcp.json"),
		AntigravityConfig:   filepath.Join(homeDir, ".gemini", "antigravity", "mcp_config.json"),
	}
	if out, gitErr := exec.Command("git", "rev-parse", "--show-toplevel").Output(); gitErr == nil {
		env.GitRoot = strings.TrimSpace(string(out))
	}
	ollama := detectOllamaModels()
	env.OllamaAvailable = ollama.available
	env.OllamaEmbedModel = ollama.embedModel
	env.OllamaLLMModel = ollama.llmModel
	return env, nil
}

type ollamaInfo struct {
	available  bool
	embedModel string
	llmModel   string
}

func detectOllamaModels() ollamaInfo {
	client := http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Get("http://localhost:11434/api/tags")
	if err != nil {
		return ollamaInfo{}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ollamaInfo{}
	}
	var payload struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return ollamaInfo{}
	}
	info := ollamaInfo{available: true}
	for _, model := range payload.Models {
		if info.embedModel == "" && looksLikeEmbeddingModel(model.Name) {
			info.embedModel = model.Name
			continue
		}
		if info.llmModel == "" && !looksLikeEmbeddingModel(model.Name) {
			info.llmModel = model.Name
		}
	}
	return info
}

func looksLikeEmbeddingModel(name string) bool {
	patterns := []string{"qwen3-embedding", "nomic-embed-text", "mxbai-embed", "all-minilm", "bge-", "snowflake-arctic-embed"}
	for _, pattern := range patterns {
		if strings.Contains(name, pattern) {
			return true
		}
	}
	return false
}

func preferredShellRC(homeDir string) string {
	for _, candidate := range []string{".zshrc", ".bashrc"} {
		path := filepath.Join(homeDir, candidate)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func claudeDesktopConfigPath(homeDir string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(homeDir, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			return ""
		}
		return filepath.Join(appData, "Claude", "claude_desktop_config.json")
	default:
		// Linux and others: Claude Desktop does not officially exist
		return ""
	}
}

func newModel(env Environment) model {
	state := state{
		ConfigDir:              env.DefaultConfigDir,
		EmbedProvider:          defaultProvider(env.OllamaEmbedModel),
		LLMProvider:            defaultProvider(env.OllamaLLMModel),
		GateMode:               "auto",
		ConfigureClaude:        false,
		ConfigureClaudeDesktop: false,
		ConfigureOpenCode:      true,
		ConfigureCursor:        false,
		ConfigureAntigravity:   false,
		InstallHook:            env.GitRoot != "",
	}
	return model{
		env:         env,
		state:       state,
		step:        stepWelcome,
		status:      "Welcome to Neurox",
		titleStyle:  lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("252")),
		subtleStyle: lipgloss.NewStyle().Foreground(lipgloss.Color("250")),
		mutedStyle:  lipgloss.NewStyle().Foreground(lipgloss.Color("244")),
		headingStyle: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("225")),
		selectedStyle: lipgloss.NewStyle().Foreground(lipgloss.Color("45")).Bold(true),
		cardStyle: lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("239")).
			Background(lipgloss.Color("234")).
			Padding(1, 2),
		sidebarStyle: lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("239")).
			Background(lipgloss.Color("233")).
			Padding(1, 2),
		tagStyle:    lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true),
		okStyle:     lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true),
		warnStyle:   lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true),
		errorStyle:  lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true),
		accentStyle: lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true),
		buttonStyle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("16")).
			Background(lipgloss.Color("255")).
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("255")).
			Padding(0, 2).
			Bold(true),
		buttonGhostStyle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")).
			Background(lipgloss.Color("236")).
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("241")).
			Padding(0, 2).
			Bold(true),
		progressOnStyle:  lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Bold(true),
		progressOffStyle: lipgloss.NewStyle().Foreground(lipgloss.Color("239")),
		selectedBoxStyle: lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true),
		unselectedBoxStyle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("244")),
	}
}

func defaultProvider(model string) string {
	if model != "" {
		return "ollama"
	}
	return "disabled"
}

func (m model) Init() tea.Cmd {
	return tickSpinner()
}

func tickSpinner() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return spinnerTickMsg{} })
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case spinnerTickMsg:
		if m.step == stepInstalling {
			m.spinner = (m.spinner + 1) % len(spinnerFrames)
		}
		return m, tickSpinner()
	case installFinishedMsg:
		m.result = msg.result
		m.step = stepDone
		m.done = true
		if msg.result.Err != nil {
			m.status = msg.result.Err.Error()
		} else {
			m.status = "Installation complete"
		}
		return m, nil
	case tea.KeyMsg:
		if m.step == stepInstalling {
			if msg.String() == "q" || msg.String() == "ctrl+c" {
				return m, tea.Quit
			}
			return m, nil
		}
		if m.done {
			switch msg.String() {
			case "enter", "q", "ctrl+c":
				return m, tea.Quit
			}
			return m, nil
		}
		if m.editing {
			return m.handleEditing(msg)
		}
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			m.moveCursor(-1)
		case "down", "j", "tab":
			m.moveCursor(1)
		case "left", "h":
			m.adjustCurrent(-1)
		case "right", "l":
			m.adjustCurrent(1)
		case " ":
			m.toggleCurrent()
		case "enter":
			return m.activateCurrent()
		}
	}
	return m, nil
}

func (m model) handleEditing(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	fields := m.currentFields()
	current := fields[m.cursor]
	switch msg.Type {
	case tea.KeyEsc:
		m.editing = false
		m.status = fmt.Sprintf("Cancelled editing %s", current.Label)
		return m, nil
	case tea.KeyEnter:
		m.setTextFieldValue(current.Key, strings.TrimSpace(m.inputBuffer))
		m.editing = false
		m.status = fmt.Sprintf("Updated %s", current.Label)
		return m, nil
	case tea.KeyBackspace, tea.KeyCtrlH:
		if len(m.inputBuffer) > 0 {
			m.inputBuffer = m.inputBuffer[:len(m.inputBuffer)-1]
		}
		return m, nil
	case tea.KeyCtrlU:
		m.inputBuffer = ""
		return m, nil
	case tea.KeyRunes:
		m.inputBuffer += msg.String()
		return m, nil
	}
	return m, nil
}

func (m *model) activateCurrent() (tea.Model, tea.Cmd) {
	fields := m.currentFields()
	current := fields[m.cursor]
	switch current.Type {
	case fieldText:
		m.editing = true
		m.inputBuffer = m.textFieldValue(current.Key)
		m.status = fmt.Sprintf("Editing %s", current.Label)
		return *m, nil
	case fieldToggle:
		m.toggleCurrent()
		return *m, nil
	case fieldSelect:
		m.adjustCurrent(1)
		return *m, nil
	case fieldAction:
		return m.handleAction(current.Key)
	default:
		return *m, nil
	}
}

func (m *model) handleAction(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "start":
		m.step = stepProviders
		m.cursor = 0
		m.status = "Pick your providers"
	case "welcome_quit", "cancel":
		return *m, tea.Quit
	case "providers_back":
		m.step = stepWelcome
		m.cursor = 0
	case "providers_next":
		if err := m.validateProviders(); err != nil {
			m.status = err.Error()
			return *m, nil
		}
		m.step = stepIntegrations
		m.cursor = 0
		m.status = "Choose integrations"
	case "integrations_back":
		m.step = stepProviders
		m.cursor = 0
	case "integrations_next":
		m.step = stepReview
		m.cursor = 0
		m.status = "Review installation plan"
	case "review_back":
		m.step = stepIntegrations
		m.cursor = 0
	case "install":
		if err := m.validate(); err != nil {
			m.status = err.Error()
			return *m, nil
		}
		m.step = stepInstalling
		m.cursor = 0
		m.status = "Installing Neurox"
		return *m, performInstallCmd(m.state, m.env)
	}
	return *m, nil
}

func (m *model) moveCursor(delta int) {
	fields := m.currentFields()
	if len(fields) == 0 {
		return
	}
	m.cursor = (m.cursor + delta + len(fields)) % len(fields)
}

func (m *model) adjustCurrent(delta int) {
	fields := m.currentFields()
	if len(fields) == 0 {
		return
	}
	current := fields[m.cursor]
	switch current.Key {
	case "embed_provider":
		m.state.EmbedProvider = cycleValue(m.providerOptions(true), m.state.EmbedProvider, delta)
	case "llm_provider":
		m.state.LLMProvider = cycleValue(m.providerOptions(false), m.state.LLMProvider, delta)
	case "gate_mode":
		m.state.GateMode = cycleValue([]string{"auto", "full", "off"}, m.state.GateMode, delta)
	default:
		return
	}
	m.status = fmt.Sprintf("Updated %s", current.Label)
}

func (m *model) toggleCurrent() {
	fields := m.currentFields()
	if len(fields) == 0 {
		return
	}
	current := fields[m.cursor]
	switch current.Key {
	case "claude":
		m.state.ConfigureClaude = !m.state.ConfigureClaude
	case "claude_desktop":
		m.state.ConfigureClaudeDesktop = !m.state.ConfigureClaudeDesktop
	case "opencode":
		m.state.ConfigureOpenCode = !m.state.ConfigureOpenCode
	case "cursor":
		m.state.ConfigureCursor = !m.state.ConfigureCursor
	case "antigravity":
		m.state.ConfigureAntigravity = !m.state.ConfigureAntigravity
	case "install_hook":
		if m.env.GitRoot != "" {
			m.state.InstallHook = !m.state.InstallHook
		}
	}
}

func (m model) currentFields() []field {
	switch m.step {
	case stepWelcome:
		return []field{
			{Type: fieldAction, Key: "start", Label: "Start setup"},
			{Type: fieldAction, Key: "welcome_quit", Label: "Quit", Danger: true},
		}
	case stepProviders:
		fields := []field{{Type: fieldSelect, Key: "embed_provider", Label: "Embeddings provider", Description: "FTS5 only, local Ollama, or remote API"}}
		if m.state.EmbedProvider == "remote" {
			fields = append(fields,
				field{Type: fieldText, Key: "embed_url", Label: "Embeddings API URL"},
				field{Type: fieldText, Key: "embed_key", Label: "Embeddings API key", Masked: true},
				field{Type: fieldText, Key: "embed_model", Label: "Embeddings model"},
			)
		}
		fields = append(fields, field{Type: fieldSelect, Key: "llm_provider", Label: "LLM provider", Description: "Reflection, fact extraction, and quality gate"})
		if m.state.LLMProvider == "remote" {
			fields = append(fields,
				field{Type: fieldText, Key: "llm_url", Label: "LLM API URL"},
				field{Type: fieldText, Key: "llm_key", Label: "LLM API key", Masked: true},
				field{Type: fieldText, Key: "llm_model", Label: "LLM model"},
			)
		}
		if m.state.LLMProvider != "disabled" {
			fields = append(fields, field{Type: fieldSelect, Key: "gate_mode", Label: "Quality gate mode", Description: "How aggressively the LLM filters observations"})
		}
		fields = append(fields,
			field{Type: fieldAction, Key: "providers_back", Label: "Back"},
			field{Type: fieldAction, Key: "providers_next", Label: "Continue"},
		)
		return fields
	case stepIntegrations:
		fields := []field{
			{Type: fieldToggle, Key: "claude", Label: "Claude Code"},
		}
		if m.env.ClaudeDesktopConfig != "" {
			fields = append(fields, field{Type: fieldToggle, Key: "claude_desktop", Label: "Claude Desktop", Description: m.env.ClaudeDesktopConfig})
		}
		fields = append(fields,
			field{Type: fieldToggle, Key: "opencode", Label: "OpenCode"},
			field{Type: fieldToggle, Key: "cursor", Label: "Cursor"},
			field{Type: fieldToggle, Key: "antigravity", Label: "Antigravity", Description: m.env.AntigravityConfig},
		)
		if m.env.GitRoot != "" {
			fields = append(fields, field{Type: fieldToggle, Key: "install_hook", Label: "Git hook"})
		}
		fields = append(fields,
			field{Type: fieldAction, Key: "integrations_back", Label: "Back"},
			field{Type: fieldAction, Key: "integrations_next", Label: "Continue"},
		)
		return fields
	case stepReview:
		return []field{
			{Type: fieldAction, Key: "review_back", Label: "Back"},
			{Type: fieldAction, Key: "install", Label: "Install Neurox"},
		}
	default:
		return nil
	}
}

func (m model) View() string {
	if m.step == stepDone {
		return m.renderDone()
	}
	if m.step == stepWelcome {
		return m.renderWelcome()
	}
	main := m.renderWizardCard()
	footer := m.mutedStyle.Render("Arrows move • Enter edits/selects • Space toggles • q exits")
	return lipgloss.JoinVertical(lipgloss.Left,
		m.renderHeader(),
		"",
		main,
		"",
		m.renderStatus(),
		footer,
	)
}

func (m model) renderHeader() string {
	steps := []string{"Welcome", "Providers", "Integrations", "Review"}
	index := 0
	if m.step > stepWelcome && m.step <= stepReview {
		index = int(m.step)
	}
	segments := make([]string, 0, len(steps))
	for i, s := range steps {
		if i <= index {
			segments = append(segments, m.accentStyle.Render("◆ "+s))
		} else {
			segments = append(segments, m.mutedStyle.Render("○ "+s))
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, segments...)
}

func (m model) renderWelcome() string {
	brand := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("252")).Render(strings.Join([]string{
		"███    ██ ███████ ██    ██ ██████   ██████  ██   ██",
		"████   ██ ██      ██    ██ ██   ██ ██    ██  ██ ██ ",
		"██ ██  ██ █████   ██    ██ ██████  ██    ██   ███  ",
		"██  ██ ██ ██      ██    ██ ██   ██ ██    ██  ██ ██ ",
		"██   ████ ███████  ██████  ██   ██  ██████  ██   ██",
	}, "\n"))
	badge := lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("45")).Padding(0, 1).Bold(true).Render(" setup ")
	hero := lipgloss.JoinVertical(lipgloss.Left,
		badge,
		"",
		brand,
		"",
		m.subtleStyle.Render("Configure your AI clients and providers."),
		m.subtleStyle.Render("Neurox is already installed — this wizard writes your MCP configs"),
		m.subtleStyle.Render("and installs the skill so your agent uses memory automatically."),
		"",
		m.okStyle.Render("◇ Providers"),
		m.okStyle.Render("◇ Integrations"),
		m.accentStyle.Render("● Review before writing files"),
	)
	fields := m.currentFields()
	content := lipgloss.JoinVertical(lipgloss.Left,
		hero,
		"",
		m.renderField(fields[0], 0 == m.cursor),
		m.renderField(fields[1], 1 == m.cursor),
	)
	return lipgloss.JoinVertical(lipgloss.Left, content)
}

func (m model) renderWizardCard() string {
	fields := m.currentFields()
	lines := []string{m.headingStyle.Render(m.stepTitle()), m.mutedStyle.Render(m.stepDescription()), ""}
	if summary := m.renderInlineSummary(); summary != "" {
		lines = append(lines, summary, "")
	}
	for i, f := range fields {
		lines = append(lines, m.renderField(f, i == m.cursor))
		if f.Description != "" && f.Type != fieldToggle {
			lines = append(lines, "  "+m.mutedStyle.Render(f.Description))
		}
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func (m model) renderInlineSummary() string {
	var lines []string
	switch m.step {
	case stepProviders:
		lines = append(lines,
			m.okStyle.Render("◇ Embeddings")+" "+m.shortProviderLabel(m.state.EmbedProvider, true),
			m.okStyle.Render("◇ LLM")+" "+m.shortProviderLabel(m.state.LLMProvider, false),
		)
	case stepIntegrations:
		items := m.shortIntegrations()
		if len(items) == 0 {
			lines = append(lines, m.okStyle.Render("◇ Integrations")+" none")
		} else {
			lines = append(lines, m.okStyle.Render("◇ Integrations")+" "+strings.Join(items, ", "))
		}
	case stepReview:
		lines = append(lines,
			m.headingStyle.Render("Summary"),
			"  Config: "+filepath.Join(m.state.ConfigDir, "config.yaml"),
			"  Embeddings: "+m.shortProviderLabel(m.state.EmbedProvider, true),
			"  LLM: "+m.shortProviderLabel(m.state.LLMProvider, false),
		)
		if m.state.LLMProvider != "disabled" {
			lines = append(lines, "  Gate: "+m.state.GateMode)
		}
		if items := m.shortIntegrations(); len(items) > 0 {
			lines = append(lines, "  Integrations: "+strings.Join(items, ", "))
		}
	}
	return strings.Join(lines, "\n")
}

func (m model) renderField(f field, selected bool) string {
	if f.Type == fieldAction {
		return m.renderActionButton(f, selected)
	}
	prefix := "  "
	if selected {
		prefix = m.selectedStyle.Render("›")
	}
	label := f.Label + ": " + m.renderValue(f, selected)
	if selected {
		return prefix + " " + m.accentStyle.Render(label)
	}
	return prefix + " " + label
}

func (m model) renderActionButton(f field, selected bool) string {
	label := f.Label
	if selected {
		label = m.accentStyle.Render("› " + label)
	} else if f.Danger {
		label = m.mutedStyle.Render("  " + label)
	} else {
		label = "  " + label
	}
	return label
}

func (m model) renderValue(f field, selected bool) string {
	switch f.Type {
	case fieldText:
		value := m.textFieldValue(f.Key)
		if m.editing && selected {
			masked := m.inputBuffer
			if f.Masked && masked != "" {
				masked = strings.Repeat("*", min(len(masked), 12))
			}
			return masked + "_"
		}
		if f.Masked && value != "" {
			return strings.Repeat("*", min(len(value), 12))
		}
		if strings.TrimSpace(value) == "" {
			return m.mutedStyle.Render("<empty>")
		}
		return value
	case fieldSelect:
		switch f.Key {
		case "embed_provider":
			return m.providerLabel(m.state.EmbedProvider, true)
		case "llm_provider":
			return m.providerLabel(m.state.LLMProvider, false)
		case "gate_mode":
			return m.state.GateMode
		}
	case fieldToggle:
		if m.toggleValue(f.Key) {
			return m.selectedBoxStyle.Render("[x]")
		}
		return m.unselectedBoxStyle.Render("[ ]")
	}
	return ""
}

func (m model) stepTitle() string {
	switch m.step {
	case stepProviders:
		return "Step 1 — Configure providers"
	case stepIntegrations:
		return "Step 2 — Wire integrations"
	case stepReview:
		return "Step 3 — Review before applying"
	default:
		return ""
	}
}

func (m model) stepDescription() string {
	switch m.step {
	case stepProviders:
		return "Choose whether you want local Ollama, remote APIs, or a lightweight FTS5-only setup."
	case stepIntegrations:
		return "Pick the editors and repo integrations the wizard should configure for you."
	case stepReview:
		return "Final check before files are written. You can still go back and tweak anything."
	default:
		return ""
	}
}

func (m model) renderStatus() string {
	if strings.TrimSpace(m.status) == "" {
		return ""
	}
	if m.step == stepInstalling {
		return m.accentStyle.Render("Installing" + spinnerFrames[m.spinner])
	}
	lower := strings.ToLower(m.status)
	if strings.Contains(lower, "required") || strings.Contains(lower, "error") {
		return m.errorStyle.Render(m.status)
	}
	return m.mutedStyle.Render(m.status)
}

var spinnerFrames = []string{".", "..", "...", "...."}

func (m model) renderDone() string {
	if m.result.Err != nil {
		content := m.cardStyle.Width(96).Render(lipgloss.JoinVertical(lipgloss.Left,
			m.errorStyle.Render("Installation failed"),
			"",
			m.result.Err.Error(),
			"",
			m.mutedStyle.Render("Press Enter or q to exit."),
		))
		return lipgloss.Place(max(100, m.width), max(30, m.height), lipgloss.Center, lipgloss.Center, content)
	}
	lines := []string{
		m.okStyle.Render("Neurox is installed"),
		"",
		fmt.Sprintf("Binary   %s", m.result.BinaryPath),
		fmt.Sprintf("Config   %s", m.result.ConfigFile),
		fmt.Sprintf("Database %s", m.result.Database),
	}
	if len(m.result.Updated) > 0 {
		lines = append(lines, "", m.headingStyle.Render("Updated files"))
		lines = append(lines, m.result.Updated...)
	}
	if len(m.result.Warnings) > 0 {
		lines = append(lines, "", m.warnStyle.Render("Warnings"))
		lines = append(lines, m.result.Warnings...)
	}
	lines = append(lines, "", m.mutedStyle.Render("Press Enter or q to exit."))
	content := m.cardStyle.Width(100).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(max(104, m.width), max(34, m.height), lipgloss.Center, lipgloss.Center, content)
}

func (m model) providerOptions(embeddings bool) []string {
	options := []string{"disabled"}
	if embeddings {
		if m.env.OllamaEmbedModel != "" {
			options = append(options, "ollama")
		}
		return append(options, "remote")
	}
	if m.env.OllamaLLMModel != "" {
		options = append(options, "ollama")
	}
	return append(options, "remote")
}

func cycleValue(options []string, current string, delta int) string {
	if len(options) == 0 {
		return current
	}
	idx := 0
	for i, option := range options {
		if option == current {
			idx = i
			break
		}
	}
	idx = (idx + delta + len(options)) % len(options)
	return options[idx]
}

func (m model) providerLabel(value string, embeddings bool) string {
	switch value {
	case "ollama":
		if embeddings {
			return fmt.Sprintf("Ollama (%s)", fallback(m.env.OllamaEmbedModel, "detected"))
		}
		return fmt.Sprintf("Ollama (%s)", fallback(m.env.OllamaLLMModel, "detected"))
	case "remote":
		return "Remote OpenAI-compatible API"
	default:
		if embeddings {
			return "Disabled (FTS5 only)"
		}
		return "Disabled (heuristic only)"
	}
}

func (m model) shortProviderLabel(value string, embeddings bool) string {
	switch value {
	case "ollama":
		return "Ollama"
	case "remote":
		if embeddings {
			return "Remote API"
		}
		return "Remote API"
	default:
		if embeddings {
			return "Disabled"
		}
		return "Disabled"
	}
}

func (m model) shortIntegrations() []string {
	var items []string
	if m.state.ConfigureClaude {
		items = append(items, "Claude Code (+ skill)")
	}
	if m.state.ConfigureClaudeDesktop {
		items = append(items, "Claude Desktop")
	}
	if m.state.ConfigureOpenCode {
		items = append(items, "OpenCode")
	}
	if m.state.ConfigureCursor {
		items = append(items, "Cursor")
	}
	if m.state.ConfigureAntigravity {
		items = append(items, "Antigravity")
	}
	if m.state.InstallHook && m.env.GitRoot != "" {
		items = append(items, "Git hook")
	}
	return items
}

func (m model) textFieldValue(key string) string {
	switch key {
	case "config_dir":
		return m.state.ConfigDir
	case "embed_url":
		return m.state.EmbedRemoteURL
	case "embed_key":
		return m.state.EmbedRemoteKey
	case "embed_model":
		return m.state.EmbedRemoteModel
	case "llm_url":
		return m.state.LLMRemoteURL
	case "llm_key":
		return m.state.LLMRemoteKey
	case "llm_model":
		return m.state.LLMRemoteModel
	default:
		return ""
	}
}

func (m *model) setTextFieldValue(key string, value string) {
	switch key {
	case "config_dir":
		m.state.ConfigDir = value
	case "embed_url":
		m.state.EmbedRemoteURL = value
	case "embed_key":
		m.state.EmbedRemoteKey = value
	case "embed_model":
		m.state.EmbedRemoteModel = value
	case "llm_url":
		m.state.LLMRemoteURL = value
	case "llm_key":
		m.state.LLMRemoteKey = value
	case "llm_model":
		m.state.LLMRemoteModel = value
	}
}

func (m model) toggleValue(key string) bool {
	switch key {
	case "claude":
		return m.state.ConfigureClaude
	case "claude_desktop":
		return m.state.ConfigureClaudeDesktop
	case "opencode":
		return m.state.ConfigureOpenCode
	case "cursor":
		return m.state.ConfigureCursor
	case "antigravity":
		return m.state.ConfigureAntigravity
	case "install_hook":
		return m.state.InstallHook
	default:
		return false
	}
}

func (m model) integrationTargets() []string {
	var files []string
	if m.state.ConfigureClaude {
		files = append(files, m.env.ClaudeConfigPath)
	}
	if m.state.ConfigureOpenCode {
		files = append(files, m.env.OpenCodeConfig)
	}
	if m.state.ConfigureCursor {
		files = append(files, m.env.CursorConfig)
	}
	if m.state.ConfigureAntigravity {
		files = append(files, m.env.AntigravityConfig)
	}
	if m.state.InstallHook && m.env.GitRoot != "" {
		files = append(files, filepath.Join(m.env.GitRoot, ".git", "hooks", "post-commit"))
	}
	return files
}

func (m model) configHints() []string {
	var lines []string
	if m.state.EmbedProvider == "disabled" && m.state.LLMProvider == "disabled" {
		return []string{"No external providers required.", "Neurox will run with FTS5-only search."}
	}
	if m.state.EmbedProvider == "ollama" {
		lines = append(lines, fmt.Sprintf("Embeddings use local Ollama model %s", fallback(m.env.OllamaEmbedModel, "detected")))
	}
	if m.state.EmbedProvider == "remote" {
		lines = append(lines, "Need embeddings URL, API key, and model")
	}
	if m.state.LLMProvider == "ollama" {
		lines = append(lines, fmt.Sprintf("LLM uses local Ollama model %s", fallback(m.env.OllamaLLMModel, "detected")))
	}
	if m.state.LLMProvider == "remote" {
		lines = append(lines, "Need LLM URL, API key, and model")
	}
	if m.state.LLMProvider != "disabled" {
		lines = append(lines, fmt.Sprintf("Gate mode will be %s", m.state.GateMode))
	}
	return lines
}

func (m model) validateProviders() error {
	if m.state.EmbedProvider == "remote" {
		if strings.TrimSpace(m.state.EmbedRemoteURL) == "" || strings.TrimSpace(m.state.EmbedRemoteKey) == "" || strings.TrimSpace(m.state.EmbedRemoteModel) == "" {
			return errors.New("remote embeddings need URL, API key, and model")
		}
	}
	if m.state.LLMProvider == "remote" {
		if strings.TrimSpace(m.state.LLMRemoteURL) == "" || strings.TrimSpace(m.state.LLMRemoteKey) == "" || strings.TrimSpace(m.state.LLMRemoteModel) == "" {
			return errors.New("remote LLM needs URL, API key, and model")
		}
	}
	return nil
}

func (m model) validate() error {
	if strings.TrimSpace(m.state.ConfigDir) == "" {
		return errors.New("config directory is required")
	}
	return m.validateProviders()
}

func performInstallCmd(s state, env Environment) tea.Cmd {
	return func() tea.Msg { return installFinishedMsg{result: executeInstall(s, env)} }
}

func executeInstall(s state, env Environment) installResult {
	result := installResult{}

	// Resolve the running executable path for MCP configs.
	// EvalSymlinks gives the real path even when run via a symlink.
	// Falls back to "neurox" (relies on PATH) if resolution fails.
	self, err := os.Executable()
	if err != nil {
		self = "neurox"
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	binaryPath := self
	result.BinaryPath = binaryPath

	configFile := filepath.Join(s.ConfigDir, "config.yaml")
	databasePath := filepath.Join(s.ConfigDir, "neurox.db")
	result.ConfigFile = configFile
	result.Database = databasePath

	if err := os.MkdirAll(s.ConfigDir, 0o755); err != nil {
		result.Err = fmt.Errorf("create config dir: %w", err)
		return result
	}
	if err := writeConfigFile(configFile, databasePath, s, env); err != nil {
		result.Err = fmt.Errorf("write config: %w", err)
		return result
	}
	result.Updated = append(result.Updated, configFile)

	if s.ConfigureClaude {
		if err := upsertClaudeConfig(env.ClaudeConfigPath, binaryPath); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("Claude Code config: %v", err))
		} else {
			result.Updated = append(result.Updated, env.ClaudeConfigPath)
		}
		if err := installClaudeSkill(env.HomeDir); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("Claude Code skill: %v", err))
		} else {
			result.Updated = append(result.Updated, filepath.Join(env.HomeDir, ".claude", "skills", "neurox", "SKILL.md"))
		}
		if err := installClaudeProtocol(env.HomeDir); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("Claude Code protocol: %v", err))
		} else {
			result.Updated = append(result.Updated, filepath.Join(env.HomeDir, ".claude", "CLAUDE.md"))
		}
	}
	if s.ConfigureClaudeDesktop {
		if err := upsertClaudeDesktopConfig(env.ClaudeDesktopConfig, binaryPath); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("Claude Desktop config: %v", err))
		} else {
			result.Updated = append(result.Updated, env.ClaudeDesktopConfig)
		}
	}
	if s.ConfigureOpenCode {
		if err := upsertOpenCodeConfig(env.OpenCodeConfig, binaryPath); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("OpenCode config: %v", err))
		} else {
			result.Updated = append(result.Updated, env.OpenCodeConfig)
		}
		if err := installOpenCodeProtocol(env.HomeDir); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("OpenCode protocol: %v", err))
		} else {
			result.Updated = append(result.Updated, openCodeAgentsPath(env.HomeDir))
		}
	}
	if s.ConfigureCursor {
		if err := upsertCursorConfig(env.CursorConfig, binaryPath); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("Cursor config: %v", err))
		} else {
			result.Updated = append(result.Updated, env.CursorConfig)
		}
	}
	if s.ConfigureAntigravity {
		if err := upsertAntigravityConfig(env.AntigravityConfig, binaryPath); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("Antigravity config: %v", err))
		} else {
			result.Updated = append(result.Updated, env.AntigravityConfig)
		}
		if err := installAntigravityProtocol(env.HomeDir); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("Antigravity protocol: %v", err))
		} else {
			result.Updated = append(result.Updated, filepath.Join(env.HomeDir, ".gemini", "GEMINI.md"))
		}
	}
	if s.InstallHook && env.GitRoot != "" {
		hookCmd := exec.Command(binaryPath, "install-hook")
		hookCmd.Dir = env.GitRoot
		if out, err := hookCmd.CombinedOutput(); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("Git hook not installed: %s", strings.TrimSpace(string(out))))
		} else {
			result.Updated = append(result.Updated, filepath.Join(env.GitRoot, ".git", "hooks", "post-commit"))
		}
	}
	return result
}

func writeConfigFile(path string, databasePath string, s state, env Environment) error {
	cfg := config.Config{
		Database: config.DatabaseConfig{Path: databasePath},
		LLM: config.LLMConfig{
			Provider: llmProviderValue(s.LLMProvider),
			GateMode: s.GateMode,
		},
		Embeddings: config.EmbeddingsConfig{
			Provider: embedProviderValue(s.EmbedProvider),
		},
	}
	if s.LLMProvider == "ollama" {
		cfg.LLM.OllamaModel = env.OllamaLLMModel
	}
	if s.LLMProvider == "remote" {
		cfg.LLM.RemoteURL = strings.TrimSpace(s.LLMRemoteURL)
		cfg.LLM.RemoteAPIKey = strings.TrimSpace(s.LLMRemoteKey)
		cfg.LLM.RemoteModel = strings.TrimSpace(s.LLMRemoteModel)
	}
	if s.EmbedProvider == "remote" {
		cfg.Embeddings.RemoteURL = strings.TrimSpace(s.EmbedRemoteURL)
		cfg.Embeddings.RemoteKey = strings.TrimSpace(s.EmbedRemoteKey)
		cfg.Embeddings.RemoteModel = strings.TrimSpace(s.EmbedRemoteModel)
	}
	data, err := yaml.Marshal(&cfg)
	if err != nil {
		return err
	}
	content := "# Neurox configuration\n# Generated by the interactive installer\n\n" + string(data)
	return os.WriteFile(path, []byte(content), 0o600)
}

func llmProviderValue(value string) string {
	if value == "disabled" {
		return ""
	}
	return value
}

func embedProviderValue(value string) string {
	if value == "disabled" {
		return ""
	}
	return value
}

// installClaudeSkill writes the embedded SKILL.md to
// ~/.claude/skills/neurox/SKILL.md so Claude Code loads it automatically.
// The skill content is embedded at build time — no source directory needed.
func installClaudeSkill(homeDir string) error {
	destDir := filepath.Join(homeDir, ".claude", "skills", "neurox")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("create skills dir: %w", err)
	}
	dest := filepath.Join(destDir, "SKILL.md")
	return os.WriteFile(dest, neuroxSkillContent, 0o644)
}

func upsertClaudeConfig(path string, binaryPath string) error {
	var cfg map[string]any
	if err := readJSONFile(path, &cfg); err != nil {
		return err
	}
	if cfg == nil {
		cfg = map[string]any{}
	}
	servers := ensureObject(cfg, "mcpServers")
	// Use the binary name only (not absolute path) so the config works
	// regardless of where neurox is installed, as long as it is on PATH.
	servers["neurox"] = map[string]any{"type": "stdio", "command": "neurox", "args": []string{"mcp"}}
	return writeJSONFile(path, cfg)
}

func upsertClaudeDesktopConfig(path string, binaryPath string) error {
	var cfg map[string]any
	if err := readJSONFile(path, &cfg); err != nil {
		return err
	}
	if cfg == nil {
		cfg = map[string]any{}
	}
	servers := ensureObject(cfg, "mcpServers")
	// Claude Desktop does NOT use "type": "stdio" — only command + args
	servers["neurox"] = map[string]any{
		"command": "neurox",
		"args":    []string{"mcp"},
	}
	return writeJSONFile(path, cfg)
}

func upsertOpenCodeConfig(path string, binaryPath string) error {
	var cfg map[string]any
	if err := readJSONFile(path, &cfg); err != nil {
		return err
	}
	if cfg == nil {
		cfg = map[string]any{}
	}
	mcp := ensureObject(cfg, "mcp")
	mcp["neurox"] = map[string]any{"type": "local", "command": []string{"neurox", "mcp"}, "enabled": true}
	return writeJSONFile(path, cfg)
}

func upsertCursorConfig(path string, binaryPath string) error {
	var cfg map[string]any
	if err := readJSONFile(path, &cfg); err != nil {
		return err
	}
	if cfg == nil {
		cfg = map[string]any{}
	}
	servers := ensureObject(cfg, "mcpServers")
	servers["neurox"] = map[string]any{"command": "neurox", "args": []string{"mcp"}}
	return writeJSONFile(path, cfg)
}

func upsertAntigravityConfig(path string, binaryPath string) error {
	var cfg map[string]any
	if err := readJSONFile(path, &cfg); err != nil {
		return err
	}
	if cfg == nil {
		cfg = map[string]any{}
	}
	servers := ensureObject(cfg, "mcpServers")
	servers["neurox"] = map[string]any{"command": "neurox", "args": []string{"mcp"}}
	return writeJSONFile(path, cfg)
}

func readJSONFile(path string, target *map[string]any) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		*target = map[string]any{}
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		*target = map[string]any{}
		return nil
	}
	return json.Unmarshal(data, target)
}

func writeJSONFile(path string, cfg map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func ensureObject(parent map[string]any, key string) map[string]any {
	if existing, ok := parent[key].(map[string]any); ok {
		return existing
	}
	child := map[string]any{}
	parent[key] = child
	return child
}

// Protocol injection markers — used to idempotently inject/update the Neurox
// behavioral protocol into agent instruction files (CLAUDE.md, AGENTS.md,
// GEMINI.md). Re-running the installer updates the section; user content
// outside the markers is never touched.
const (
	protocolMarkerBegin = "<!-- neurox:protocol -->"
	protocolMarkerEnd   = "<!-- /neurox:protocol -->"
)

// upsertProtocol injects the Neurox protocol into a Markdown instruction file.
// If the file does not exist, it is created with just the protocol section.
// If the file exists but has no markers, the protocol is appended.
// If the file already has markers, the section between them is replaced.
// User content outside the markers is never modified.
func upsertProtocol(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create parent dir: %w", err)
	}

	section := protocolMarkerBegin + "\n" + strings.TrimRight(string(neuroxProtocolContent), "\n") + "\n" + protocolMarkerEnd

	existing, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read %s: %w", path, err)
		}
		// File does not exist — create with just the protocol section.
		return os.WriteFile(path, []byte(section+"\n"), 0o644)
	}

	content := string(existing)
	beginIdx := strings.Index(content, protocolMarkerBegin)
	endIdx := strings.Index(content, protocolMarkerEnd)

	if beginIdx >= 0 && endIdx >= 0 && endIdx > beginIdx {
		// Replace existing section (begin marker through end marker inclusive).
		updated := content[:beginIdx] + section + content[endIdx+len(protocolMarkerEnd):]
		return os.WriteFile(path, []byte(updated), 0o644)
	}

	// No markers found — append with a blank line separator.
	separator := "\n\n"
	trimmed := strings.TrimRight(content, "\n\r\t ")
	if trimmed == "" {
		separator = ""
	}
	return os.WriteFile(path, []byte(trimmed+separator+section+"\n"), 0o644)
}

// installClaudeProtocol injects the Neurox protocol into ~/.claude/CLAUDE.md.
func installClaudeProtocol(homeDir string) error {
	path := filepath.Join(homeDir, ".claude", "CLAUDE.md")
	return upsertProtocol(path)
}

// openCodeAgentsPath returns the path to the OpenCode global instructions file.
func openCodeAgentsPath(homeDir string) string {
	xdgConfig := os.Getenv("XDG_CONFIG_HOME")
	if xdgConfig == "" {
		xdgConfig = filepath.Join(homeDir, ".config")
	}
	return filepath.Join(xdgConfig, "opencode", "AGENTS.md")
}

// installOpenCodeProtocol injects the Neurox protocol into ~/.config/opencode/AGENTS.md.
func installOpenCodeProtocol(homeDir string) error {
	return upsertProtocol(openCodeAgentsPath(homeDir))
}

// installAntigravityProtocol injects the Neurox protocol into ~/.gemini/GEMINI.md.
func installAntigravityProtocol(homeDir string) error {
	path := filepath.Join(homeDir, ".gemini", "GEMINI.md")
	return upsertProtocol(path)
}

func fallback(value string, alt string) string {
	if strings.TrimSpace(value) == "" {
		return alt
	}
	return value
}

package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/cost"
	"github.com/topcheer/ggcode/internal/diff"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/image"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/subagent"
)

// logoMsg is sent on startup to display the ASCII art logo.
type logoMsg struct {
	Provider string
	Model    string
}

// ApprovalMsg is sent to TUI when agent requests permission.
type ApprovalMsg struct {
	ToolName string
	Input    string
	Response chan permission.Decision
}

// approvalResponseMsg is the user's response to an approval request.
type approvalResponseMsg struct {
	decision permission.Decision
}

// Model is the main Bubble Tea model for the REPL.
type Model struct {
	input              textinput.Model
	output             *bytes.Buffer
	loading            bool
	quitting           bool
	width              int
	height             int
	styles             styles
	agent              *agent.Agent
	program            *tea.Program
	cancelFunc         func()
	policy             permission.PermissionPolicy
	spinner            *ToolSpinner
	history            []string
	historyIdx         int
	pendingApproval    *ApprovalMsg
	session            *session.Session
	sessionStore       session.Store
	costMgr            *cost.Manager
	costProvider       string
	costModel          string
	lastCost           string
	mcpServers         []MCPInfo
	config             *config.Config
	customCmds         map[string]*commands.Command
	autoMem            *memory.AutoMemory
	projMemFiles       []string
	autoMemFiles       []string
	pluginMgr          *plugin.Manager
	subAgentMgr        *subagent.Manager
	mode               permission.PermissionMode
	pendingDiffConfirm *DiffConfirmMsg
	fullscreen         bool

	// Approval selection list
	approvalOptions []approvalOption
	approvalCursor  int

	// Diff confirm selection list
	diffOptions []approvalOption
	diffCursor  int
	pendingImage       *imageAttachedMsg

	// Viewport for scrollable output
	viewport ViewportModel

	// Markdown rendering
	mdRenderer       *glamour.TermRenderer
	streamBuffer     *bytes.Buffer
	streamStartPos   int

	// Status bar state
	statusActivity  string // "Thinking...", "Writing...", "Executing: tool_name"
	statusToolName  string // current executing tool name
	statusToolArg   string // current tool argument summary (truncated)
	statusTokens    int64  // current session cumulative tokens (in + out)
	statusCost      float64 // current session cumulative cost
	statusToolCount int    // tool calls executed this iteration

	// Slash command autocomplete
	autoCompleteItems    []string
	autoCompleteIndex    int
	autoCompleteActive   bool
	autoCompleteKind     string // "slash" or "mention"
	autoCompleteWorkDir  string // working directory for mention completion
}

// MCPInfo holds display info about a connected MCP server.
type MCPInfo struct {
	Name      string
	ToolNames []string
	Connected bool
}

type styles struct {
	user            lipgloss.Style
	assistant       lipgloss.Style
	tool            lipgloss.Style
	error           lipgloss.Style
	prompt          lipgloss.Style
	title           lipgloss.Style
	approval        lipgloss.Style
	warn            lipgloss.Style
	approvalCursor  lipgloss.Style
	approvalDim     lipgloss.Style
	statusBar       lipgloss.Style
	markdown        lipgloss.Style
}

// DiffConfirmMsg is sent to TUI when agent wants user to confirm a file edit diff.
type DiffConfirmMsg struct {
	FilePath string
	DiffText string
	Response chan bool
}

// approvalOption represents a selectable option in the approval list.
type approvalOption struct {
	label    string
	shortcut string
	decision permission.Decision
}

// defaultApprovalOptions returns the standard approval options.
func defaultApprovalOptions() []approvalOption {
	return []approvalOption{
		{label: "Allow", shortcut: "y", decision: permission.Allow},
		{label: "Allow Always", shortcut: "a", decision: permission.Allow},
		{label: "Deny", shortcut: "n", decision: permission.Deny},
	}
}

// diffConfirmOptions returns the options for diff confirmation.
func diffConfirmOptions() []approvalOption {
	return []approvalOption{
		{label: "Accept", shortcut: "y", decision: permission.Allow},
		{label: "Reject", shortcut: "n", decision: permission.Deny},
	}
}

// streamMsg wraps a string from the agent goroutine.
type streamMsg string

// doneMsg signals generation is complete.
type doneMsg struct{}

// errMsg signals an error.
type errMsg struct{ err error }

// toolStatusMsg wraps a tool status update.
type toolStatusMsg ToolStatusMsg

// statusMsg updates the status bar display.
type statusMsg struct {
	Activity  string // current activity description
	ToolName  string
	ToolArg   string
	ToolCount int
}

// statusCostMsg updates cost/token info in the status bar.
type statusCostMsg struct {
	Tokens int64
	Cost   float64
}

// costUpdateMsg carries token usage info from the agent goroutine.
type costUpdateMsg struct {
	InputTokens  int
	OutputTokens int
}

// NewModel creates a new TUI model.
func NewModel(a *agent.Agent, policy permission.PermissionPolicy) Model {
	ti := textinput.New()
	ti.Prompt = "> "
	ti.Placeholder = "Type a message..."
	ti.Focus()

	s := styles{
		user:      lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true),
		assistant: lipgloss.NewStyle().Foreground(lipgloss.Color("15")),
		tool:      lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		error:     lipgloss.NewStyle().Foreground(lipgloss.Color("9")),
		prompt:    lipgloss.NewStyle().Foreground(lipgloss.Color("6")),
		title: lipgloss.NewStyle().
			Foreground(lipgloss.Color("14")).
			Bold(true).
			MarginBottom(1),
		approval: lipgloss.NewStyle().
			Foreground(lipgloss.Color("11")).
			Bold(true),
		warn: lipgloss.NewStyle().
		Foreground(lipgloss.Color("9")).
		Bold(true),
		approvalCursor: lipgloss.NewStyle().
			Foreground(lipgloss.Color("226")).
			Background(lipgloss.Color("236")),
		approvalDim: lipgloss.NewStyle().
		Foreground(lipgloss.Color("15")),
	statusBar: lipgloss.NewStyle().
		Foreground(lipgloss.Color("6")),
	}

	mdRenderer, _ := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(80),
	)

	return Model{
		input:      ti,
		output:     &bytes.Buffer{},
		styles:     s,
		agent:      a,
		policy:     policy,
		spinner:    NewToolSpinner(),
		history:    make([]string, 0, 100),
		mdRenderer: mdRenderer,
		viewport:   NewViewportModel(80, 20),
	}
}

// Init initializes the model.
func (m Model) Init() tea.Cmd {
	return textinput.Blink
}

// setProgramMsg is sent via program.Send so the model copy inside Bubble Tea's
// event loop gets the real *tea.Program reference (NewProgram copies the model).
type setProgramMsg struct {
	Program *tea.Program
}

// SetProgram sets the tea.Program reference for async sends.
func (m *Model) SetProgram(p *tea.Program) {
	m.program = p
}

// SetSession sets the active session and store.
func (m *Model) SetSession(ses *session.Session, store session.Store) {
	m.session = ses
	m.sessionStore = store
}

// Session returns the current session.
func (m *Model) Session() *session.Session {
	return m.session
}

// SetMCPServers stores MCP server info for the /mcp command.
func (m *Model) SetMCPServers(servers []MCPInfo) {
	m.mcpServers = servers
}

func (m *Model) SetPluginManager(mgr *plugin.Manager) {
	m.pluginMgr = mgr
}

func (m *Model) SetCustomCommands(cmds map[string]*commands.Command) {
	m.customCmds = cmds
}

func (m *Model) SetAutoMemory(am *memory.AutoMemory) {
	m.autoMem = am
}

func (m *Model) SetProjectMemoryFiles(files []string) {
	m.projMemFiles = files
}

func (m *Model) SetAutoMemoryFiles(files []string) {
	m.autoMemFiles = files
}

func (m *Model) SetConfig(cfg *config.Config) {
	m.config = cfg
}

func asciiLogo() string {
	return "   _      \n __| | ___ \n/ _` |/ _ \\ \n| (_| | (_) | \n \\__,_|\\___/ \n"
}

func (m *Model) SetSubAgentManager(mgr *subagent.Manager) {
	m.subAgentMgr = mgr
}

func (m *Model) providerNames() string {
	names := make([]string, 0, len(m.config.Providers))
	for name := range m.config.Providers {
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

// Update handles messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Handle spinner ticks first
	if m.spinner.IsActive() {
		if cmd := m.spinner.Update(msg); cmd != nil {
			_ = cmd
		}
	}

	switch msg := msg.(type) {
	case logoMsg:
		m.output.WriteString(asciiLogo())
		info := fmt.Sprintf("  Provider: %s  |  Model: %s\n", msg.Provider, msg.Model)
		m.output.WriteString(m.styles.title.Render(info))
		m.output.WriteString("\n")
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// viewport height calculation — total content lines minus visible area
		// We track total content lines via renderOutput
		viewportHeight := msg.Height - 5
		if viewportHeight < 3 {
			viewportHeight = 3
		}
		m.viewport.SetSize(msg.Width, viewportHeight)
		m.input.Width = msg.Width
		// Set content to update viewport's internal total line count
		m.viewport.SetContent(m.renderOutput())
		if wrap := m.width - 4; wrap > 20 {
			if r, err := glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(wrap)); err == nil {
				m.mdRenderer = r
			}
		}
		return m, nil

	case tea.MouseMsg:
		// Option/Alt+mouse: release mouse to terminal for native text selection
		if msg.Alt {
			return m, nil
		}
		switch msg.Type {
		case tea.MouseWheelUp:
			m.viewport.ScrollUp(3)
			return m, nil
		case tea.MouseWheelDown:
			m.viewport.ScrollDown(3)
			return m, nil
		}

	case tea.KeyMsg:
		// Handle approval mode (selection list)
		if m.pendingApproval != nil {
			switch msg.String() {
			case "up", "k":
				m.approvalCursor = (m.approvalCursor - 1 + len(m.approvalOptions)) % len(m.approvalOptions)
				return m, nil
			case "down", "j":
				m.approvalCursor = (m.approvalCursor + 1) % len(m.approvalOptions)
				return m, nil
			case "enter", "right":
				opt := m.approvalOptions[m.approvalCursor]
				if opt.shortcut == "a" {
					return m, m.handleApprovalAllowAlways()
				}
				return m, m.handleApproval(opt.decision)
			case "y", "Y":
				return m, m.handleApproval(permission.Allow)
			case "n", "N":
				return m, m.handleApproval(permission.Deny)
			case "a", "A":
				return m, m.handleApprovalAllowAlways()
			case "esc", "ctrl+c":
				return m, m.handleApproval(permission.Deny)
			}
			return m, nil
		}

		// Handle diff confirmation mode (selection list)
		if m.pendingDiffConfirm != nil {
			switch msg.String() {
			case "up", "k":
				m.diffCursor = (m.diffCursor - 1 + len(m.diffOptions)) % len(m.diffOptions)
				return m, nil
			case "down", "j":
				m.diffCursor = (m.diffCursor + 1) % len(m.diffOptions)
				return m, nil
			case "enter", "right":
				opt := m.diffOptions[m.diffCursor]
				return m, m.handleDiffConfirm(opt.decision == permission.Allow)
			case "y", "Y":
				return m, m.handleDiffConfirm(true)
			case "n", "N":
				return m, m.handleDiffConfirm(false)
			case "esc", "ctrl+c":
				return m, m.handleDiffConfirm(false)
			}
			return m, nil
		}

		if m.loading {
			if msg.String() == "ctrl+c" {
				if m.cancelFunc != nil {
					m.cancelFunc()
				}
				m.loading = false
				m.spinner.Stop()
				debug.Log("tui", "loading set to false (interrupted)")
				m.output.WriteString("\n[interrupted]\n\n")
				return m, nil
			}
			return m, nil
		}

		switch msg.String() {
		case "ctrl+c", "ctrl+d":
			m.quitting = true
			return m, tea.Quit
		case "shift+tab":
			return m.handleModeSwitch()
		case "pgup":
			m.viewport.ScrollUp(m.viewport.VisibleLineCount() / 2)
			return m, nil
		case "pgdown":
			m.viewport.ScrollDown(m.viewport.VisibleLineCount() / 2)
			return m, nil
		case "up":
			if m.autoCompleteActive && len(m.autoCompleteItems) > 0 {
				m.autoCompleteIndex = (m.autoCompleteIndex - 1 + len(m.autoCompleteItems)) % len(m.autoCompleteItems)
				var cmd tea.Cmd
				m.input, cmd = m.input.Update(msg)
				return m, cmd
			}
			return m.handleHistoryUp()
		case "down":
			if m.autoCompleteActive && len(m.autoCompleteItems) > 0 {
				m.autoCompleteIndex = (m.autoCompleteIndex + 1) % len(m.autoCompleteItems)
				var cmd tea.Cmd
				m.input, cmd = m.input.Update(msg)
				return m, cmd
			}
			return m.handleHistoryDown()
		case "tab":
			if m.autoCompleteActive && len(m.autoCompleteItems) > 0 {
				m.applyAutoComplete()
				return m, nil
			}
		case "esc":
			if m.autoCompleteActive {
				m.autoCompleteActive = false
				m.autoCompleteItems = nil
				return m, nil
			}
		case "enter":
			if m.autoCompleteActive && len(m.autoCompleteItems) > 0 {
				m.applyAutoComplete()
				return m, nil
			}
			text := strings.TrimSpace(m.input.Value())
			m.input.SetValue("")
			if text == "" {
				return m, nil
			}
			// Add to history
			m.history = append(m.history, text)
			m.historyIdx = len(m.history)
			debug.Log("tui", "handleCommand: %s", text)
			return m, m.handleCommand(text)
		}

	case streamMsg:
		if m.streamBuffer != nil {
			m.streamBuffer.WriteString(string(msg))
		}
		m.output.WriteString(string(msg))
		m.viewport.GotoBottom()
		return m, nil

	case doneMsg:
		m.loading = false
		m.spinner.Stop()
		m.cancelFunc = nil
		// Render accumulated stream buffer as markdown
		if m.streamBuffer != nil && m.streamBuffer.Len() > 0 {
			rendered, err := m.mdRenderer.Render(m.streamBuffer.String())
			if err != nil {
				rendered = m.streamBuffer.String()
			}
			m.output.Truncate(m.streamStartPos)
			m.output.WriteString(rendered)
			m.streamBuffer = nil
		}
		if m.lastCost != "" {
			m.output.WriteString(m.styles.prompt.Render(m.lastCost + "\n"))
		}
		m.output.WriteString("\n")
		m.viewport.GotoBottom()
		return m, nil

	case costUpdateMsg:
		m.lastCost = fmt.Sprintf("tokens: %d in / %d out", msg.InputTokens, msg.OutputTokens)
		if m.costMgr != nil {
			if sc, ok := m.costMgr.SessionCost("current"); ok {
				m.lastCost += fmt.Sprintf(" | session cost: %s", cost.FormatCost(sc.TotalCostUSD))
				// Also update status bar cost/tokens
				m.statusTokens = sc.InputTokens + sc.OutputTokens
				m.statusCost = sc.TotalCostUSD
			}
		}
		return m, nil

	case errMsg:
		m.loading = false
		m.spinner.Stop()
		m.cancelFunc = nil
		m.output.WriteString(m.styles.error.Render(fmt.Sprintf("Error: %v\n\n", msg.err)))
		return m, nil

	case ApprovalMsg:
		// Agent is requesting approval
		m.pendingApproval = &msg
		m.approvalOptions = defaultApprovalOptions()
		m.approvalCursor = 0
		isWarn := m.mode == permission.BypassMode
		titleStyle := m.styles.approval
		if isWarn {
			titleStyle = m.styles.warn
		}
		m.output.WriteString(titleStyle.Render(
			fmt.Sprintf("\n⚠ Permission required: %s\n", msg.ToolName),
		))
		m.output.WriteString(fmt.Sprintf("  Input: %s\n", truncateString(msg.Input, 200)))
		m.output.WriteString(m.renderApprovalOptions(m.approvalOptions, m.approvalCursor))
		return m, nil

	case DiffConfirmMsg:
		m.pendingDiffConfirm = &msg
		m.diffOptions = diffConfirmOptions()
		m.diffCursor = 0
		m.output.WriteString(m.styles.approval.Render(
			fmt.Sprintf("\n\u270f File edit: %s\n", msg.FilePath),
		))
		m.output.WriteString(FormatDiff(msg.DiffText))
		m.output.WriteString(m.renderApprovalOptions(m.diffOptions, m.diffCursor))
		return m, nil

	case toolStatusMsg:
		ts := ToolStatusMsg(msg)
		if ts.Running {
			m.spinner.Start(ts.ToolName)
		} else {
			m.spinner.Stop()
			m.output.WriteString(FormatToolStatus(ts))
		}
		return m, nil

	case setProgramMsg:
		debug.Log("tui", "setProgramMsg received, program was nil=%v", m.program == nil)
		m.program = msg.Program
		return m, nil

	case imageAttachedMsg:
		m.pendingImage = &msg
		m.output.WriteString(m.styles.assistant.Render("Image attached: " + msg.placeholder + "\n"))
		m.output.WriteString(m.styles.prompt.Render("Send a message to include the image, or /image to attach another.\n\n"))
		return m, nil

	case statusMsg:
		m.statusActivity = msg.Activity
		m.statusToolName = msg.ToolName
		m.statusToolArg = msg.ToolArg
		if msg.ToolCount > 0 {
			m.statusToolCount = msg.ToolCount
		}
		return m, nil

	case statusCostMsg:
		m.statusTokens = msg.Tokens
		m.statusCost = msg.Cost
		return m, nil
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)

	// Update autocomplete state based on current input
	m.updateAutoComplete()

	return m, cmd
}

// updateAutoComplete checks if the current input should trigger autocomplete.
func (m *Model) updateAutoComplete() {
	// Check for slash command
	if active, prefix := DetectSlashCommand(m.input); active {
		matches := CompleteSlashCommand("/" + prefix)
		if len(matches) > 0 {
			m.autoCompleteActive = true
			m.autoCompleteKind = "slash"
			m.autoCompleteItems = matches
			// Reset index if the filtered list changed
			if m.autoCompleteIndex >= len(matches) {
				m.autoCompleteIndex = 0
			}
			return
		}
	}

	// Check for @mention
	if active, prefix := DetectMention(m.input); active {
		workDir, _ := os.Getwd()
		matches := CompleteMention(prefix, workDir)
		if len(matches) > 0 {
			m.autoCompleteActive = true
			m.autoCompleteKind = "mention"
			m.autoCompleteWorkDir = workDir
			m.autoCompleteItems = matches
			if m.autoCompleteIndex >= len(matches) {
				m.autoCompleteIndex = 0
			}
			return
		}
	}

	// No autocomplete active
	m.autoCompleteActive = false
	m.autoCompleteItems = nil
}

// applyAutoComplete replaces the current prefix with the selected completion.
func (m *Model) applyAutoComplete() {
	if m.autoCompleteIndex >= len(m.autoCompleteItems) {
		return
	}
	selected := m.autoCompleteItems[m.autoCompleteIndex]

	value := m.input.Value()
	cursor := m.input.Position()

	var replacement string
	if m.autoCompleteKind == "slash" {
		// Replace from the "/" to cursor with the selected command
		wordStart := cursor
		for wordStart > 0 && value[wordStart-1] != ' ' && value[wordStart-1] != '\t' {
			wordStart--
		}
		replacement = selected + " "
		value = value[:wordStart] + replacement + value[cursor:]
	} else if m.autoCompleteKind == "mention" {
		// Replace from the "@" to cursor with the selected path
		atPos := cursor - 1
		for atPos >= 0 && value[atPos] != '@' {
			atPos--
		}
		replacement = "@" + selected + " "
		value = value[:atPos] + replacement + value[cursor:]
	}

	m.input.SetValue(value)
	m.autoCompleteActive = false
	m.autoCompleteItems = nil
}

// View renders the UI.
func (m Model) View() string {
	if m.quitting {
		return ""
	}

	title := m.styles.title.Render("ggcode \u2014 AI Coding Assistant")
	input := m.input.View()

	// Set content into viewport
	m.viewport.SetContent(m.renderOutput())

	// Pre-calculate status bar
	statusBar := m.renderStatusBar()

	var sb strings.Builder
	sb.WriteString(title)
	sb.WriteString("\n")

	// Render viewport content — only use viewport for scroll offset, not padding
	content := m.renderOutput()
	lines := strings.Split(content, "\n")
	totalLines := len(lines)
	if totalLines == 0 {
		totalLines = 1
	}
	// Calculate viewport height dynamically
	headerLines := 1 // title
	footerLines := 2 // input + help
	if statusBar != "" {
		footerLines += 2 // status bar lines
	}
	if m.autoCompleteActive && len(m.autoCompleteItems) > 0 {
		footerLines += len(m.autoCompleteItems) + 1
	}
	visibleLines := m.height - headerLines - footerLines
	if visibleLines < 1 {
		visibleLines = 1
	}
	// Apply scroll offset
	offset := m.viewport.YOffset()
	maxOffset := totalLines - visibleLines
	if maxOffset < 0 {
		maxOffset = 0
	}
	if offset > maxOffset {
		offset = maxOffset
	}
	if offset < 0 {
		offset = 0
	}
	// Render visible lines only
	start := offset
	end := offset + visibleLines
	if end > totalLines {
		end = totalLines
	}
	for i := start; i < end; i++ {
		sb.WriteString(lines[i])
		sb.WriteString("\n")
	}
	// Pad remaining lines with newlines to keep input at bottom
	for i := end; i < start+visibleLines; i++ {
		sb.WriteString("\n")
	}

	// Render autocomplete overlay above input
	if m.autoCompleteActive && len(m.autoCompleteItems) > 0 {
		sb.WriteString(m.renderAutoComplete())
	}

	// Render status bar during loading
	if statusBar != "" {
		sb.WriteString("\n")
		sb.WriteString(statusBar)
		sb.WriteString("\n")
	}

	sb.WriteString(input)

	if !m.loading && m.pendingApproval == nil && m.pendingDiffConfirm == nil {
		modeStr := fmt.Sprintf("[mode: %s]", m.mode)
		agentStr := ""
		if m.subAgentMgr != nil {
			n := m.subAgentMgr.RunningCount()
			if n > 0 {
				agentStr = fmt.Sprintf(" [agents: %d running]", n)
			}
		}
		sb.WriteString(m.styles.prompt.Render("\n  " + modeStr + agentStr + " /help /sessions /resume /export /model /provider /mode /clear /exit | Shift+Tab toggle mode | Ctrl+C interrupt | Ctrl+D quit | PgUp/PgDn scroll"))
	}

	return sb.String()
}

// renderOutput renders the conversation output (used by both normal and fullscreen modes).
func (m Model) renderOutput() string {
	var sb strings.Builder
	output := m.output.String()
	if output != "" {
		output = strings.TrimRight(output, "\n")
		sb.WriteString(output)
		if m.loading && m.spinner.IsActive() {
			sb.WriteString("\n")
			sb.WriteString(m.spinner.String())
		} else if m.loading {
			sb.WriteString("\u258c")
		}
		sb.WriteString("\n\n")
	}
	return sb.String()
}

// handleCommand processes user input commands.
func (m *Model) handleCommand(text string) tea.Cmd {
	// Slash commands
	if strings.HasPrefix(text, "/") {
		parts := strings.Fields(text)
		cmd := strings.ToLower(parts[0])
		switch cmd {
		case "/exit", "/quit":
			m.quitting = true
			return tea.Quit
		case "/clear":
			m.output.Reset()
			return nil
		case "/help":
			m.output.WriteString(m.styles.assistant.Render(helpText()))
			m.output.WriteString("\n\n")
			return nil
		case "/model":
			if len(parts) > 1 {
				m.config.Model = parts[1]
				m.costModel = parts[1]
				// Recreate provider with new model
				if prov, err := provider.NewProvider(m.config); err == nil {
					m.agent.SetProvider(prov)
					m.output.WriteString(fmt.Sprintf("Switched model to: %s (provider: %s)\n\n", parts[1], m.config.Provider))
				} else {
					m.output.WriteString(m.styles.error.Render(fmt.Sprintf("Failed to switch model: %v\n\n", err)))
				}
			} else {
				m.output.WriteString(fmt.Sprintf("Current model: %s (provider: %s)\nUsage: /model <model-name>\n\n", m.config.Model, m.config.Provider))
			}
			return nil
		case "/provider":
			if len(parts) > 1 {
				newProvider := parts[1]
				if _, ok := m.config.Providers[newProvider]; !ok {
					m.output.WriteString(m.styles.error.Render(fmt.Sprintf("Unknown provider: %s (available: %v)\n\n", newProvider, m.providerNames())))
					return nil
				}
				m.config.Provider = newProvider
				m.costProvider = newProvider
				if prov, err := provider.NewProvider(m.config); err == nil {
					m.agent.SetProvider(prov)
					m.output.WriteString(fmt.Sprintf("Switched provider to: %s (model: %s)\n\n", newProvider, m.config.Model))
				} else {
					m.output.WriteString(m.styles.error.Render(fmt.Sprintf("Failed to switch provider: %v\n\n", err)))
				}
			} else {
				m.output.WriteString(fmt.Sprintf("Current provider: %s (model: %s)\nAvailable: %s\nUsage: /provider <name>\n\n", m.config.Provider, m.config.Model, m.providerNames()))
			}
			return nil
		case "/allow":
			if len(parts) > 1 {
				if m.policy != nil {
					m.policy.SetOverride(parts[1], permission.Allow)
					m.output.WriteString(fmt.Sprintf("\u2713 %s is now always allowed\n\n", parts[1]))
				}
			} else {
				m.output.WriteString("Usage: /allow <tool-name>\n\n")
			}
			return nil
		case "/cost":
			return m.handleCostCommand(parts)
		case "/sessions":
			return m.listSessions()
		case "/resume":
			if len(parts) > 1 {
				return m.resumeSession(parts[1])
			}
			m.output.WriteString("Usage: /resume <session-id>\n\n")
			return nil
		case "/export":
			if len(parts) > 1 {
				return m.exportSession(parts[1])
			}
			m.output.WriteString("Usage: /export <session-id>\n\n")
			return nil
		case "/plugins":
			return m.handlePluginsCommand()
		case "/image":
			return m.handleImageCommand(parts)
		case "/fullscreen":
			return m.handleFullscreenCommand()
		case "/mcp":
			return m.handleMCPCommand()
		case "/mode":
			return m.handleModeCommand(parts)
		case "/memory":
			return m.handleMemoryCommand(parts)
		case "/undo":
			return m.handleUndoCommand()
		case "/checkpoints":
			return m.handleCheckpointsCommand()
		case "/agents":
			return m.handleAgentsCommand(parts)
		case "/agent":
			return m.handleAgentDetailCommand(parts)
		case "/compact":
			return m.handleCompactCommand()
		case "/todo":
			return m.handleTodoCommand(parts)
		case "/bug":
			return m.handleBugCommand()
		case "/config":
			return m.handleConfigCommand(parts)
		case "/status":
			return m.handleStatusCommand()
		default:
			// Check custom commands
			if cmdName := strings.TrimPrefix(cmd, "/"); cmdName != "" {
				if custom, ok := m.customCmds[cmdName]; ok {
					vars := map[string]string{
						"DIR": workingDirFromModel(m),
					}
					expanded := custom.Expand(vars)
					m.output.WriteString(m.styles.user.Render(fmt.Sprintf("Custom command /%s:\n", cmdName)))
					m.output.WriteString(expanded)
					m.output.WriteString("\n\n")
					m.loading = true
					// Reset status bar state
					m.statusActivity = "Thinking..."
					m.statusToolName = ""
					m.statusToolArg = ""
					m.statusToolCount = 0
					return m.startAgent(expanded)
				}
			}
			m.output.WriteString(m.styles.error.Render(fmt.Sprintf("Unknown command: %s\n", text)))
			m.output.WriteString(m.styles.prompt.Render("Type /help for available commands\n\n"))
			return nil
		}
	}

	// Regular message → start agent
	// Expand @mentions
	workDir, _ := os.Getwd()
	expandedMsg, expandErr := ExpandMentions(text, workDir)
	if expandErr != nil {
		m.output.WriteString(m.styles.error.Render(fmt.Sprintf("Mention expansion error: %v", expandErr)))
		m.output.WriteString("\n\n")
	}

	m.output.WriteString(text)
	m.output.WriteString("\n\n")

	// Save original user message to session
	m.appendUserMessage(text)

	m.streamBuffer = &bytes.Buffer{}
	m.streamStartPos = m.output.Len()
	m.loading = true
	// Reset status bar state
	m.statusActivity = "Thinking..."
	m.statusToolName = ""
	m.statusToolArg = ""
	m.statusToolCount = 0
	return m.startAgent(expandedMsg)
}

// appendUserMessage saves a user message to the current session.
func (m *Model) appendUserMessage(text string) {
	if m.session == nil || m.sessionStore == nil {
		return
	}
	msg := provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: text}},
	}
	// Auto-generate title from first user message
	if m.session.Title == "" || m.session.Title == "New session" {
		if len(text) > 60 {
			m.session.Title = text[:57] + "..."
		} else {
			m.session.Title = text
		}
	}
	if store, ok := m.sessionStore.(*session.JSONLStore); ok {
		_ = store.AppendMessage(m.session, msg)
	} else {
		m.session.Messages = append(m.session.Messages, msg)
		_ = m.sessionStore.Save(m.session)
	}
}

// listSessions returns a command that lists all sessions.
func (m *Model) listSessions() tea.Cmd {
	return func() tea.Msg {
		if m.sessionStore == nil {
			return streamMsg("Session store not configured.\n\n")
		}
		sessions, err := m.sessionStore.List()
		if err != nil {
			return streamMsg(fmt.Sprintf("Error listing sessions: %v\n\n", err))
		}
		if len(sessions) == 0 {
			return streamMsg("No sessions found.\n\n")
		}
		var b strings.Builder
		b.WriteString("Sessions:\n\n")
		for i, s := range sessions {
			title := s.Title
			if title == "" {
				title = "untitled"
			}
			updated := s.UpdatedAt.Format(time.RFC3339)
			b.WriteString(fmt.Sprintf("  %d. %s  %s  (%s)\n", i+1, s.ID, title, updated))
		}
		b.WriteString("\nUse /resume <id> to continue a session\n\n")
		return streamMsg(b.String())
	}
}

// resumeSession returns a command that loads a session.
func (m *Model) resumeSession(id string) tea.Cmd {
	return func() tea.Msg {
		if m.sessionStore == nil {
			return streamMsg("Session store not configured.\n\n")
		}
		ses, err := m.sessionStore.Load(id)
		if err != nil {
			return streamMsg(fmt.Sprintf("Failed to resume session %s: %v\n\n", id, err))
		}
		// Restore messages into agent
		for _, msg := range ses.Messages {
			m.agent.AddMessage(msg)
		}
		m.session = ses
		title := ses.Title
		if title == "" {
			title = "untitled"
		}
		return streamMsg(fmt.Sprintf("Resumed session: %s \u2014 %s (%d messages)\n\n", ses.ID, title, len(ses.Messages)))
	}
}

// exportSession returns a command that exports a session to markdown.
func (m *Model) exportSession(id string) tea.Cmd {
	return func() tea.Msg {
		if m.sessionStore == nil {
			return streamMsg("Session store not configured.\n\n")
		}
		md, err := m.sessionStore.ExportMarkdown(id)
		if err != nil {
			return streamMsg(fmt.Sprintf("Error exporting session: %v\n\n", err))
		}
		filename := fmt.Sprintf("session-%s.md", id)
		if err := os.WriteFile(filename, []byte(md), 0644); err != nil {
			return streamMsg(fmt.Sprintf("Error writing file: %v\n\n", err))
		}
		absPath, _ := filepath.Abs(filename)
		return streamMsg(fmt.Sprintf("Exported session %s to %s\n\n", id, absPath))
	}
}

// handleApproval sends the user's decision back via the channel.
func (m *Model) handleApproval(d permission.Decision) tea.Cmd {
	pa := m.pendingApproval
	m.pendingApproval = nil
	if pa == nil || pa.Response == nil {
		return nil
	}
	go func() {
		pa.Response <- d
	}()
	return nil
}

// handleApprovalAllowAlways approves and adds to policy.
func (m *Model) handleApprovalAllowAlways() tea.Cmd {
	pa := m.pendingApproval
	m.pendingApproval = nil
	if pa != nil && m.policy != nil {
		m.policy.SetOverride(pa.ToolName, permission.Allow)
		m.output.WriteString(fmt.Sprintf("\u2713 %s is now always allowed\n\n", pa.ToolName))
	}
	if pa != nil && pa.Response != nil {
		go func() {
			pa.Response <- permission.Allow
		}()
	}
	return nil
}

// handleDiffConfirm sends the user's diff decision back via the channel.
func (m *Model) handleDiffConfirm(approved bool) tea.Cmd {
	pd := m.pendingDiffConfirm
	m.pendingDiffConfirm = nil
	if pd == nil || pd.Response == nil {
		return nil
	}
	go func() {
		pd.Response <- approved
	}()
	if !approved {
		m.output.WriteString(m.styles.error.Render("  Rejected.\n"))
	}
	return nil
}

// handleHistoryUp navigates up in command history.
func (m Model) handleHistoryUp() (tea.Model, tea.Cmd) {
	if m.historyIdx > 0 {
		m.historyIdx--
		m.input.SetValue(m.history[m.historyIdx])
	}
	return m, nil
}

// handleHistoryDown navigates down in command history.
func (m Model) handleHistoryDown() (tea.Model, tea.Cmd) {
	if m.historyIdx < len(m.history)-1 {
		m.historyIdx++
		m.input.SetValue(m.history[m.historyIdx])
	} else {
		m.historyIdx = len(m.history)
		m.input.SetValue("")
	}
	return m, nil
}

// handleModeSwitch cycles through permission modes (Shift+Tab).
func (m Model) handleModeSwitch() (tea.Model, tea.Cmd) {
	m.mode = m.mode.Next()
	// Update policy mode
	if cp, ok := m.policy.(*permission.ConfigPolicy); ok {
		cp.SetMode(m.mode)
	}
	m.output.WriteString(fmt.Sprintf("Mode: %s\n", m.mode))
	return m, nil
}

// handleModeCommand handles the /mode slash command.
func (m *Model) handleModeCommand(parts []string) tea.Cmd {
	if len(parts) > 1 {
		newMode := permission.ParsePermissionMode(parts[1])
		m.mode = newMode
		if cp, ok := m.policy.(*permission.ConfigPolicy); ok {
			cp.SetMode(newMode)
		}
		m.output.WriteString(fmt.Sprintf("Mode set to: %s\n\n", newMode))
	} else {
		m.output.WriteString(fmt.Sprintf("Current mode: %s\nUsage: /mode <supervised|plan|auto|bypass>\n\n", m.mode))
	}
	return nil
}

// startAgent returns a tea.Cmd that runs the agent in a goroutine.
func (m *Model) startAgent(text string) tea.Cmd {
	debug.Log("tui", "startAgent called: text=%s", truncateStr(text, 200))
	// Capture and clear pending image
	img := m.pendingImage
	m.pendingImage = nil

	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		m.cancelFunc = cancel

		go func() {
			defer func() {
				if m.program != nil {
					m.program.Send(doneMsg{})
				}
				cancel()
			}()

			if img != nil {
				content := []provider.ContentBlock{
					provider.TextBlock(text),
					provider.ImageBlock(img.img.MIME, image.EncodeBase64(img.img)),
				}
				_ = m.agent.RunStreamWithContent(ctx, content, func(event provider.StreamEvent) {
					if m.program == nil {
						return
					}
					switch event.Type {
					case provider.StreamEventText:
						m.program.Send(streamMsg(event.Text))
						m.program.Send(statusMsg{
							Activity: "Writing...",
						})
					case provider.StreamEventToolCallDone:
						m.program.Send(statusMsg{
							Activity:  "Thinking...",
							ToolName:  event.Tool.Name,
							ToolCount: m.statusToolCount + 1,
						})
						m.program.Send(toolStatusMsg{
							ToolName: event.Tool.Name,
							Running:  true,
						})
					case provider.StreamEventError:
						m.program.Send(errMsg{err: event.Error})
					}
				})
			} else {
				_ = m.agent.RunStream(ctx, text, func(event provider.StreamEvent) {
					if m.program == nil {
						return
					}
					switch event.Type {
					case provider.StreamEventText:
						m.program.Send(streamMsg(event.Text))
						m.program.Send(statusMsg{
							Activity: "Writing...",
						})
					case provider.StreamEventToolCallDone:
						m.program.Send(statusMsg{
							Activity:  "Thinking...",
							ToolName:  event.Tool.Name,
							ToolCount: m.statusToolCount + 1,
						})
						m.program.Send(toolStatusMsg{
							ToolName: event.Tool.Name,
							Running:  true,
						})
					case provider.StreamEventError:
						m.program.Send(errMsg{err: event.Error})
					}
				})
			}
		}()

		return nil
	}
}

// handleUndoCommand rolls back the most recent checkpoint.
func (m *Model) handleUndoCommand() tea.Cmd {
	return func() tea.Msg {
		cpMgr := m.agent.CheckpointManager()
		if cpMgr == nil {
			return streamMsg("Checkpointing not enabled.\n\n")
		}
		cp, err := cpMgr.Undo()
		if err != nil {
			return streamMsg(fmt.Sprintf("Undo failed: %v\n\n", err))
		}
		// Show diff (new -> old)
		diffText := diff.UnifiedDiff(cp.NewContent, cp.OldContent, 3)
		var b strings.Builder
		b.WriteString(fmt.Sprintf("Undid %s on %s (checkpoint %s)\n", cp.ToolCall, cp.FilePath, cp.ID))
		b.WriteString(FormatDiff(diffText))
		b.WriteString("\n")
		return streamMsg(b.String())
	}
}

// handleCheckpointsCommand lists all checkpoints.
func (m *Model) handleCheckpointsCommand() tea.Cmd {
	return func() tea.Msg {
		cpMgr := m.agent.CheckpointManager()
		if cpMgr == nil {
			return streamMsg("Checkpointing not enabled.\n\n")
		}
		ps := cpMgr.List()
		if len(ps) == 0 {
			return streamMsg("No checkpoints.\n\n")
		}
		var b strings.Builder
		b.WriteString(fmt.Sprintf("Checkpoints (%d):\n\n", len(ps)))
		for i, cp := range ps {
			b.WriteString(fmt.Sprintf("  %d. %s  %s  %s  %s\n", i+1, cp.ID, cp.FilePath, cp.ToolCall, cp.Timestamp.Format("15:04:05")))
		}
		b.WriteString("\nUse /undo to revert the most recent.\n\n")
		return streamMsg(b.String())
	}
}

// helpText returns the help message.
func workingDirFromModel(m *Model) string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	return dir
}

func (m *Model) handleMemoryCommand(parts []string) tea.Cmd {
	sub := ""
	if len(parts) > 1 {
		sub = strings.ToLower(parts[1])
	}
	switch sub {
	case "list":
		if m.autoMem == nil {
			m.output.WriteString(m.styles.prompt.Render("Auto memory not initialized.\n\n"))
			return nil
		}
		keys, err := m.autoMem.List()
		if err != nil {
			m.output.WriteString(m.styles.error.Render(fmt.Sprintf("Error listing memories: %v\n\n", err)))
			return nil
		}
		if len(keys) == 0 {
			m.output.WriteString(m.styles.prompt.Render("No auto memories saved.\n\n"))
			return nil
		}
		m.output.WriteString(m.styles.title.Render("Auto Memories:\n"))
		for _, k := range keys {
			m.output.WriteString(fmt.Sprintf("  - %s\n", k))
		}
		m.output.WriteString("\n")
	case "clear":
		if m.autoMem == nil {
			m.output.WriteString(m.styles.prompt.Render("Auto memory not initialized.\n\n"))
			return nil
		}
		if err := m.autoMem.Clear(); err != nil {
			m.output.WriteString(m.styles.error.Render(fmt.Sprintf("Error clearing memories: %v\n\n", err)))
			return nil
		}
		m.output.WriteString(m.styles.assistant.Render("All auto memories cleared.\n\n"))
	default:
		m.output.WriteString(m.styles.title.Render("Memory:\n"))
		if len(m.projMemFiles) > 0 {
			m.output.WriteString(m.styles.assistant.Render("Project Memory (GGCODE.md):\n"))
			for _, f := range m.projMemFiles {
				m.output.WriteString(fmt.Sprintf("  %s\n", f))
			}
			m.output.WriteString("\n")
		} else {
			m.output.WriteString(m.styles.prompt.Render("  No GGCODE.md files loaded.\n"))
		}
		if len(m.autoMemFiles) > 0 {
			m.output.WriteString(m.styles.assistant.Render("Auto Memory:\n"))
			for _, f := range m.autoMemFiles {
				m.output.WriteString(fmt.Sprintf("  %s\n", f))
			}
			m.output.WriteString("\n")
		} else {
			m.output.WriteString(m.styles.prompt.Render("  No auto memories loaded.\n"))
		}
		m.output.WriteString(m.styles.prompt.Render("\nUsage: /memory [list|clear]\n\n"))
	}
	return nil
}

// renderApprovalOptions renders a selection list for approval/diff confirm.
func (m Model) renderApprovalOptions(options []approvalOption, cursor int) string {
	var sb strings.Builder
	maxLabel := 0
	for _, opt := range options {
		label := fmt.Sprintf("%s (%s)", opt.label, opt.shortcut)
		if len(label) > maxLabel {
			maxLabel = len(label)
		}
	}
	sb.WriteString("\n")
	for i, opt := range options {
		label := fmt.Sprintf("%s (%s)", opt.label, opt.shortcut)
		if i == cursor {
			sb.WriteString(m.styles.approvalCursor.Render(fmt.Sprintf("  \u276f %-*s", maxLabel, label)))
			sb.WriteString("\n")
		} else {
			sb.WriteString(m.styles.approvalDim.Render(fmt.Sprintf("    %-*s", maxLabel, label)))
			sb.WriteString("\n")
		}
	}
	sb.WriteString(m.styles.prompt.Render("  \u2191/\u2193 navigate, Enter confirm, shortcut keys still work\n"))
	return sb.String()
}

// renderAutoComplete renders the autocomplete dropdown.
func (m Model) renderAutoComplete() string {
	if len(m.autoCompleteItems) == 0 {
		return ""
	}

	// Limit visible items
	maxVisible := 8
	start := 0
	if len(m.autoCompleteItems) > maxVisible {
		start = m.autoCompleteIndex
		// Keep selection visible
		if start >= len(m.autoCompleteItems)-maxVisible/2 {
			start = len(m.autoCompleteItems) - maxVisible
		}
		if start < 0 {
			start = 0
		}
	}
	end := start + maxVisible
	if end > len(m.autoCompleteItems) {
		end = len(m.autoCompleteItems)
	}

	items := m.autoCompleteItems[start:end]

	// Find max width for alignment
	maxWidth := 0
	for _, item := range items {
		if len(item) > maxWidth {
			maxWidth = len(item)
		}
	}

	var sb strings.Builder
	sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("\n  Commands:\n"))

	for i, item := range items {
		realIdx := start + i
		selected := realIdx == m.autoCompleteIndex

		if selected {
			sb.WriteString(lipgloss.NewStyle().
				Foreground(lipgloss.Color("226")).
				Background(lipgloss.Color("236")).
				Render(fmt.Sprintf("  \u25b6 %-*s", maxWidth, item)))
			sb.WriteString(" ")
			if desc, ok := SlashCommandDescriptions[item]; ok {
				sb.WriteString(lipgloss.NewStyle().
					Foreground(lipgloss.Color("8")).
					Background(lipgloss.Color("236")).
					Render(desc))
			}
			sb.WriteString("\n")
		} else {
			sb.WriteString(lipgloss.NewStyle().
				Foreground(lipgloss.Color("15")).
				Render(fmt.Sprintf("    %-*s", maxWidth, item)))
			sb.WriteString(" ")
			if desc, ok := SlashCommandDescriptions[item]; ok {
				sb.WriteString(lipgloss.NewStyle().
					Foreground(lipgloss.Color("8")).
					Render(desc))
			}
			sb.WriteString("\n")
		}
	}

	sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("  Tab/Enter to select, Esc to cancel\n"))
	return sb.String()
}

// renderStatusBar renders the status bar above input during loading.
func (m Model) renderStatusBar() string {
	if !m.loading {
		return ""
	}

	var sb strings.Builder

	// Main status line
	activity := m.statusActivity
	if activity == "" {
		activity = "Thinking..."
	}

	spinnerChar := ""
	if m.spinner.IsActive() {
		frame := m.spinner.CurrentFrame()
		spinnerChar = string(spinnerChars[frame%len(spinnerChars)])
	} else {
		spinnerChar = "⏳"
	}

	// Format tokens with commas
	tokens := fmt.Sprintf("%d", m.statusTokens)
	if len(tokens) > 3 {
		for i := len(tokens) - 3; i > 0; i -= 3 {
			tokens = tokens[:i] + "," + tokens[i:]
		}
	}

	// Format cost
	cost := fmt.Sprintf("%.4f", m.statusCost)
	if cost == "0.0000" {
		cost = "0.00"
	}

	line1 := fmt.Sprintf(" %s %s │ 📊 %s tokens │ 💰 $%s",
		spinnerChar, activity, tokens, cost)
	sb.WriteString(m.styles.statusBar.Render(line1))

	// Tool info line
	if m.statusToolCount > 0 || m.statusToolName != "" {
		sb.WriteString("\n ")
		if m.statusToolCount > 0 {
			sb.WriteString(fmt.Sprintf("🔧 %d tools used", m.statusToolCount))
			if m.statusToolName != "" {
				sb.WriteString(" │ ")
			}
		}
		if m.statusToolName != "" {
			sb.WriteString(fmt.Sprintf("%s", m.statusToolName))
			if m.statusToolArg != "" {
				arg := m.statusToolArg
				if len(arg) > 50 {
					arg = arg[:50] + "..."
				}
				sb.WriteString(fmt.Sprintf(": %s", arg))
			}
		}
	}

	// Subagent info line
	if m.subAgentMgr != nil && m.subAgentMgr.RunningCount() > 0 {
		agents := m.subAgentMgr.List()
		sb.WriteString("\n 🤖 ")
		first := true
		for _, a := range agents {
			if !first {
				sb.WriteString(" │ ")
			}
			first = false
			icon := "✅"
			if a.Status == subagent.StatusRunning {
				icon = "⏳"
			}
			sb.WriteString(fmt.Sprintf("%s %s (%d tools)", icon, a.ID, a.ToolCallCount))
		}
	}

	return sb.String()
}

// handleCompactCommand compresses the conversation history.
func (m *Model) handleCompactCommand() tea.Cmd {
	return func() tea.Msg {
		cm := m.agent.ContextManager()
		if cm == nil {
			return streamMsg("Context manager not available.\n\n")
		}
		if err := cm.Summarize(context.Background(), m.agent.Provider()); err != nil {
			return streamMsg(fmt.Sprintf("Compact failed: %v\n\n", err))
		}
		return streamMsg("Conversation history compacted.\n\n")
	}
}

// handleTodoCommand views/manages the todo list.
func (m *Model) handleTodoCommand(parts []string) tea.Cmd {
	if len(parts) > 1 && strings.ToLower(parts[1]) == "clear" {
		// Clear todos
		todopath := func() string { d, _ := os.UserHomeDir(); return filepath.Join(d, ".ggcode", "todos.json") }()
		if err := os.WriteFile(todopath, []byte("[]\n"), 0644); err != nil {
			return func() tea.Msg {
				return streamMsg(fmt.Sprintf("Error clearing todos: %v\n\n", err))
			}
		}
		m.output.WriteString(m.styles.assistant.Render("Todo list cleared.\n\n"))
		return nil
	}
	return func() tea.Msg {
		todopath := func() string { d, _ := os.UserHomeDir(); return filepath.Join(d, ".ggcode", "todos.json") }()
		data, err := os.ReadFile(todopath)
		if err != nil {
			if os.IsNotExist(err) {
				return streamMsg("No todo list found. Use the todo_write tool to create one.\n\n")
			}
			return streamMsg(fmt.Sprintf("Error reading todos: %v\n\n", err))
		}
		// Pretty print JSON
		var raw interface{}
		if err := json.Unmarshal(data, &raw); err != nil {
			return streamMsg(fmt.Sprintf("Error parsing todos: %v\n\n", err))
		}
		pretty, _ := json.MarshalIndent(raw, "", "  ")
		return streamMsg(fmt.Sprintf("Todo list:\n%s\n\n", string(pretty)))
	}
}

// handleBugCommand generates diagnostic info for bug reporting.
func (m *Model) handleBugCommand() tea.Cmd {
	return func() tea.Msg {
		var b strings.Builder
		b.WriteString("=== Bug Report Diagnostics ===\n\n")

		// Version info
		b.WriteString("Version: ggcode (dev)\n")
		b.WriteString(fmt.Sprintf("OS: %s %s\n", runtime.GOOS, runtime.GOARCH))
		b.WriteString(fmt.Sprintf("Go: %s\n", runtime.Version()))

		// Config info
		if m.config != nil {
			b.WriteString(fmt.Sprintf("Provider: %s\n", m.config.Provider))
			b.WriteString(fmt.Sprintf("Model: %s\n", m.config.Model))
		}

		// Session info
		if m.session != nil {
			b.WriteString(fmt.Sprintf("Session: %s (%d messages)\n", m.session.ID, len(m.session.Messages)))
		}

		// MCP info
		if len(m.mcpServers) > 0 {
			b.WriteString(fmt.Sprintf("MCP servers: %d\n", len(m.mcpServers)))
		}

		// Recent errors from output
		output := m.output.String()
		if idx := strings.LastIndex(output, "Error:"); idx >= 0 {
			end := idx + 500
			if end > len(output) {
				end = len(output)
			}
			b.WriteString(fmt.Sprintf("Last error: %s\n", output[idx:end]))
		}

		b.WriteString("\nPlease include this information when reporting a bug.\n\n")
		return streamMsg(b.String())
	}
}

// handleConfigCommand shows or modifies configuration.
func (m *Model) handleConfigCommand(parts []string) tea.Cmd {
	if len(parts) > 1 && strings.ToLower(parts[1]) == "set" {
		if len(parts) < 4 {
			m.output.WriteString(m.styles.error.Render("Usage: /config set <key> <value>\n\n"))
			return nil
		}
		key := parts[2]
		value := parts[3]
		if m.config == nil {
			m.output.WriteString(m.styles.error.Render("Config not loaded.\n\n"))
			return nil
		}
		switch key {
		case "model":
			m.config.Model = value
			m.output.WriteString(fmt.Sprintf("Config: model = %s\n\n", value))
		case "provider":
			m.config.Provider = value
			m.output.WriteString(fmt.Sprintf("Config: provider = %s\n\n", value))
		default:
			m.output.WriteString(m.styles.error.Render(fmt.Sprintf("Unknown config key: %s\nSupported: model, provider\n\n", key)))
		}
		return nil
	}
	// Show current config
	var b strings.Builder
	b.WriteString(m.styles.title.Render("Current Configuration:\n"))
	if m.config != nil {
		b.WriteString(fmt.Sprintf("  Provider:    %s\n", m.config.Provider))
		b.WriteString(fmt.Sprintf("  Model:       %s\n", m.config.Model))
		if pc, ok := m.config.Providers[m.config.Provider]; ok && pc.MaxTokens > 0 {
			b.WriteString(fmt.Sprintf("  MaxTokens:   %d\n", pc.MaxTokens))
		}
		if len(m.config.Providers) > 0 {
			b.WriteString(fmt.Sprintf("  Providers:    %v\n", m.providerNames()))
		}
		b.WriteString(fmt.Sprintf("  MCP Servers: %d\n", len(m.config.MCPServers)))
	}
	b.WriteString(m.styles.prompt.Render("\nUsage: /config set <key> <value>\n\n"))
	m.output.WriteString(b.String())
	return nil
}

// handleStatusCommand shows current status.
func (m *Model) handleStatusCommand() tea.Cmd {
	var b strings.Builder
	b.WriteString(m.styles.title.Render("Status:\n"))
	b.WriteString(fmt.Sprintf("  Provider:    %s\n", m.config.Provider))
	b.WriteString(fmt.Sprintf("  Model:       %s\n", m.config.Model))
	b.WriteString(fmt.Sprintf("  Mode:        %s\n", m.mode))
	b.WriteString(fmt.Sprintf("  Fullscreen:  %v\n", m.fullscreen))

	if m.session != nil {
		b.WriteString(fmt.Sprintf("  Session:     %s\n", m.session.ID))
		b.WriteString(fmt.Sprintf("  Messages:    %d\n", len(m.session.Messages)))
	}

	if m.lastCost != "" {
		b.WriteString(fmt.Sprintf("  %s\n", m.lastCost))
	}

	if m.subAgentMgr != nil {
		n := m.subAgentMgr.RunningCount()
		b.WriteString(fmt.Sprintf("  Agents:      %d running\n", n))
	}

	b.WriteString(fmt.Sprintf("  MCP Servers: %d connected\n", len(m.mcpServers)))
	b.WriteString("\n")
	m.output.WriteString(b.String())
	return nil
}

func helpText() string {
	return `Available commands:
  /help              Show this help message
  /cost              Show current session cost stats
  /cost all          Show all session cost summary
  /sessions          List all saved sessions
  /resume <id>       Resume a previous session
  /export <id>       Export session to markdown file
  /model <name>      Switch model
  /provider <name>    Switch provider
  /clear             Clear conversation history
  /mcp               Show connected MCP servers and tools
  /memory            Show loaded memory files
  /memory list       List auto memory entries
  /memory clear      Clear all auto memories
  /undo              Undo the last file edit (checkpoint rollback)
  /checkpoints       List all file edit checkpoints

  /allow <tool>      Always allow a specific tool
  /plugins           List loaded plugins and their tools
  /image <path>       Attach an image file
  /fullscreen         Toggle fullscreen mode
  /mode <mode>       Set permission mode (supervised|plan|auto|bypass)
  /agents            List sub-agents
  /agent <id>        Show sub-agent details
  /agent cancel <id> Cancel a sub-agent

  /compact           Compress conversation history
  /todo              View todo list
  /todo clear        Clear todo list
  /bug               Report a bug with diagnostics
  /config            Show current configuration
  /config set <k> <v> Set a config value
  /status            Show current status
  /exit, /quit       Exit ggcode

Keyboard shortcuts:
  Tab               Autocomplete slash commands
  Esc               Cancel autocomplete
  \u2191/\u2193                Browse command history (or autocomplete)
  Shift+Tab         Toggle permission mode
  Ctrl+C             Interrupt current generation
  Ctrl+D             Exit

Mouse:
  Option+drag / Shift+drag  Select text to copy (bypasses app mouse capture)
  Mouse wheel              Scroll conversation output`
}

// handleCostCommand displays cost statistics.
// handleMCPCommand shows connected MCP servers and their tools.
func (m *Model) handlePluginsCommand() tea.Cmd {
	if m.pluginMgr == nil {
		m.output.WriteString(m.styles.prompt.Render("Plugin manager not available.\n\n"))
		return nil
	}
	results := m.pluginMgr.Results()
	if len(results) == 0 {
		m.output.WriteString(m.styles.prompt.Render("No plugins loaded.\n\n"))
		return nil
	}
	m.output.WriteString(m.styles.title.Render("Plugins:\n"))
	for _, r := range results {
		status := "\u2713"
		style := m.styles.assistant
		if !r.Success {
			status = "\u2717"
			style = m.styles.error
		}
		m.output.WriteString(style.Render(fmt.Sprintf("  %s %s", status, r.Name)))
		if r.Error != nil {
			m.output.WriteString(style.Render(fmt.Sprintf(" - %v", r.Error)))
		}
		m.output.WriteString("\n")
		for _, tn := range r.Tools {
			m.output.WriteString(fmt.Sprintf("    - %s\n", tn))
		}
	}
	m.output.WriteString("\n")
	return nil
}

func (m *Model) handleMCPCommand() tea.Cmd {
	if len(m.mcpServers) == 0 {
		m.output.WriteString(m.styles.prompt.Render("No MCP servers configured.\n\n"))
		return nil
	}
	m.output.WriteString(m.styles.title.Render("MCP Servers:\n"))
	for _, srv := range m.mcpServers {
		status := "\u2713"
		if !srv.Connected {
			status = "\u2717"
		}
		m.output.WriteString(fmt.Sprintf("  %s %s (%d tools)\n", status, srv.Name, len(srv.ToolNames)))
		for _, tn := range srv.ToolNames {
			m.output.WriteString(fmt.Sprintf("    - %s\n", tn))
		}
	}
	m.output.WriteString("\n")
	return nil
}

func (m *Model) handleCostCommand(parts []string) tea.Cmd {
	if m.costMgr == nil {
		m.output.WriteString(m.styles.error.Render("Cost tracking not enabled.\n\n"))
		return nil
	}

	showAll := len(parts) > 1 && strings.ToLower(parts[1]) == "all"

	if showAll {
		all := m.costMgr.AllCosts()
		if len(all) == 0 {
			m.output.WriteString("No cost data yet.\n\n")
			return nil
		}
		m.output.WriteString(m.styles.title.Render("Cost Summary (all sessions)\n"))
		for _, sc := range all {
			m.output.WriteString(cost.FormatSessionCost(sc, time.Time{}) + "\n")
		}
		total := m.costMgr.TotalCost()
		m.output.WriteString(fmt.Sprintf("\n  Total: %s\n\n", cost.FormatCost(total)))
		return nil
	}

	// Current session
	if sc, ok := m.costMgr.SessionCost("current"); ok {
		m.output.WriteString(m.styles.title.Render("Current Session Cost\n"))
		m.output.WriteString(fmt.Sprintf("  Provider: %s\n", sc.Provider))
		m.output.WriteString(fmt.Sprintf("  Model:    %s\n", sc.Model))
		m.output.WriteString(fmt.Sprintf("  Input:    %s tokens\n", cost.FormatTokens(sc.InputTokens)))
		m.output.WriteString(fmt.Sprintf("  Output:   %s tokens\n", cost.FormatTokens(sc.OutputTokens)))
		m.output.WriteString(fmt.Sprintf("  Cost:     %s USD\n\n", cost.FormatCost(sc.TotalCostUSD)))
	} else {
		m.output.WriteString("No cost data for current session yet.\n\n")
	}
	return nil
}

// truncateString truncates a string to maxLen.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// handleImageCommand handles the /image slash command to attach an image file.
func (m *Model) handleImageCommand(parts []string) tea.Cmd {
	if len(parts) < 2 {
		m.output.WriteString(m.styles.error.Render("Usage: /image <path/to/file.png>\n"))
		m.output.WriteString(m.styles.prompt.Render("Supported formats: PNG, JPEG, GIF, WebP (max 20MB)\n\n"))
		return nil
	}
	path := parts[1]
	return func() tea.Msg {
		img, err := image.ReadFile(path)
		if err != nil {
			return errMsg{err: fmt.Errorf("reading image: %w", err)}
		}
		placeholder := image.Placeholder(path, img)
		return imageAttachedMsg{
			placeholder: placeholder,
			img:         img,
			filename:    path,
		}
	}
}

// imageAttachedMsg is sent when an image is successfully loaded.
type imageAttachedMsg struct {
	placeholder string
	img         image.Image
	filename    string
}

// handleFullscreenCommand toggles fullscreen mode.
func (m *Model) handleFullscreenCommand() tea.Cmd {
	m.fullscreen = !m.fullscreen
	state := "off"
	if m.fullscreen {
		state = "on"
	}
	m.output.WriteString(fmt.Sprintf("Fullscreen: %s\n\n", state))
	return nil
}

// handleAgentsCommand lists all sub-agents.
func (m *Model) handleAgentsCommand(parts []string) tea.Cmd {
	if m.subAgentMgr == nil {
		m.output.WriteString(m.styles.error.Render("Sub-agent manager not configured.\n\n"))
		return nil
	}
	agents := m.subAgentMgr.List()
	if len(agents) == 0 {
		m.output.WriteString("No sub-agents spawned yet.\nUsage: LLM can use spawn_agent tool to create sub-agents.\n\n")
		return nil
	}
	m.output.WriteString(fmt.Sprintf("%d sub-agent(s):\n", len(agents)))
	for _, sa := range agents {
		duration := ""
		if !sa.EndedAt.IsZero() && !sa.StartedAt.IsZero() {
			duration = fmt.Sprintf(" (%v)", sa.EndedAt.Sub(sa.StartedAt).Round(1e9))
		}
		m.output.WriteString(fmt.Sprintf("  %s [%s]%s - %s\n", sa.ID, sa.Status, duration, truncateStr(sa.Task, 60)))
	}
	m.output.WriteString("\nUse /agent <id> for details, /agent cancel <id> to cancel.\n\n")
	return nil
}

// handleAgentDetailCommand shows details for a specific sub-agent or cancels it.
func (m *Model) handleAgentDetailCommand(parts []string) tea.Cmd {
	if m.subAgentMgr == nil {
		m.output.WriteString(m.styles.error.Render("Sub-agent manager not configured.\n\n"))
		return nil
	}
	if len(parts) < 2 {
		m.output.WriteString("Usage: /agent <id> or /agent cancel <id>\n\n")
		return nil
	}
	if parts[1] == "cancel" && len(parts) >= 3 {
		if m.subAgentMgr.Cancel(parts[2]) {
			m.output.WriteString(fmt.Sprintf("Cancelled sub-agent %s\n\n", parts[2]))
		} else {
			m.output.WriteString(m.styles.error.Render(fmt.Sprintf("Could not cancel %s (not found or not running)\n\n", parts[2])))
		}
		return nil
	}
	sa, ok := m.subAgentMgr.Get(parts[1])
	if !ok {
		m.output.WriteString(m.styles.error.Render(fmt.Sprintf("Sub-agent %s not found\n\n", parts[1])))
		return nil
	}
	m.output.WriteString(fmt.Sprintf("Agent: %s\nStatus: %s\nTask: %s\n", sa.ID, sa.Status, sa.Task))
	if sa.Result != "" {
		m.output.WriteString(fmt.Sprintf("Result: %s\n", sa.Result))
	}
	if sa.Error != nil {
		m.output.WriteString(fmt.Sprintf("Error: %v\n", sa.Error))
	}
	m.output.WriteString("\n")
	return nil
}

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

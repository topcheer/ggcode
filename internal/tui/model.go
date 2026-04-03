package tui

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/cost"
	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/image"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/util"
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

const maxOutputLines = 50000

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
	streamPrefixWritten bool

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

// setProgramMsg is sent via program.Send so the model copy inside Bubble Tea's
// event loop gets the real *tea.Program reference (NewProgram copies the model).
type setProgramMsg struct {
	Program *tea.Program
}


func NewModel(a *agent.Agent, policy permission.PermissionPolicy) Model {
	ti := textinput.New()
	ti.Prompt = "❯ "
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

func (m Model) Init() tea.Cmd {
	return textinput.Blink
}

func (m *Model) SetProgram(p *tea.Program) {
	m.program = p
}

func (m *Model) SetSession(ses *session.Session, store session.Store) {
	m.session = ses
	m.sessionStore = store
}

func (m *Model) Session() *session.Session {
	return m.session
}

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

func truncateString(s string, maxLen int) string {
	return util.Truncate(s, maxLen)
}

func truncateStr(s string, max int) string {
	return util.Truncate(s, max)
}

func (m *Model) trimOutput() {
	data := m.output.Bytes()
	lines := bytes.Count(data, []byte("\n"))
	if lines > maxOutputLines {
		target := lines * 20 / 100
		cut := 0
		count := 0
		for i, b := range data {
			if b == '\n' {
				count++
				if count == target {
					cut = i + 1
					break
				}
			}
		}
		if cut > 0 {
			m.output.Next(cut)
		}
	}
}

// imageAttachedMsg is sent when an image is successfully loaded.
type imageAttachedMsg struct {
	placeholder string
	img         image.Image
	filename    string
}

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
		m.handleResize(msg.Width, msg.Height)
		m.rebuildMarkdownRenderer()
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
		if !m.streamPrefixWritten {
			m.output.WriteString(bulletStyle.Render("● "))
			m.streamPrefixWritten = true
		}
		if m.streamBuffer != nil {
			m.streamBuffer.WriteString(string(msg))
		}
		m.output.WriteString(string(msg))
		m.trimOutput()
		m.viewport.GotoBottom()
		return m, nil

	case doneMsg:
		m.loading = false
		m.spinner.Stop()
		m.cancelFunc = nil
		m.streamPrefixWritten = false
		// Render accumulated stream buffer as markdown
		if m.streamBuffer != nil && m.streamBuffer.Len() > 0 {
			rendered, err := m.mdRenderer.Render(m.streamBuffer.String())
			if err != nil {
				rendered = m.streamBuffer.String()
			}
			m.output.Truncate(m.streamStartPos)
			m.output.WriteString(bulletStyle.Render("● "))
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
			// Flush current stream buffer (render markdown) before tool output
			if m.streamBuffer != nil && m.streamBuffer.Len() > 0 {
				rendered, err := m.mdRenderer.Render(m.streamBuffer.String())
				if err != nil {
					rendered = m.streamBuffer.String()
				}
				m.output.Truncate(m.streamStartPos)
				m.output.WriteString(bulletStyle.Render("● "))
				m.output.WriteString(rendered)
				m.streamBuffer.Reset()
				m.streamStartPos = m.output.Len()
			}
			m.spinner.Start(ts.ToolName)
			// Write tree-style header
			m.output.WriteString(FormatToolStart(ts.ToolName, ts.Args))
		} else {
			m.spinner.Stop()
			m.output.WriteString(FormatToolResult(ts))
			// Reset stream prefix so next text block gets ●
			m.streamPrefixWritten = false
			// Reset stream buffer position for next text chunk
			m.streamStartPos = m.output.Len()
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

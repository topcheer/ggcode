package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/topcheer/ggcode/internal/agent"
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
	Vendor   string
	Endpoint string
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

type policyModeGetter interface {
	Mode() permission.PermissionMode
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
	mcpServers         []MCPInfo
	config             *config.Config
	language           Language
	startupVendor      string
	startupEndpoint    string
	startupModel       string
	customCmds         map[string]*commands.Command
	autoMem            *memory.AutoMemory
	projMemFiles       []string
	autoMemFiles       []string
	pluginMgr          *plugin.Manager
	subAgentMgr        *subagent.Manager
	mode               permission.PermissionMode
	pendingDiffConfirm *DiffConfirmMsg
	fullscreen         bool
	providerPanel      *providerPanelState

	// Approval selection list
	approvalOptions []approvalOption
	approvalCursor  int

	// Diff confirm selection list
	diffOptions  []approvalOption
	diffCursor   int
	pendingImage *imageAttachedMsg

	// Viewport for scrollable output
	viewport ViewportModel

	// Markdown rendering
	mdRenderer          *glamour.TermRenderer
	streamBuffer        *bytes.Buffer
	streamStartPos      int
	streamPrefixWritten bool

	// Status bar state
	statusActivity  string // "Thinking...", "Writing...", "Executing: tool_name"
	statusToolName  string // current executing tool name
	statusToolArg   string // current tool argument summary (truncated)
	statusToolCount int    // tool calls executed this iteration
	activityGroups  []toolActivityGroup
	todoSnapshot    map[string]todoStateItem
	activeTodo      *todoStateItem

	// Slash command autocomplete
	autoCompleteItems   []string
	autoCompleteIndex   int
	autoCompleteActive  bool
	autoCompleteKind    string // "slash" or "mention"
	autoCompleteWorkDir string // working directory for mention completion
	lastResizeAt        time.Time
	exitConfirmPending  bool
	pendingSubmissions  []string
	runCanceled         bool
	runFailed           bool
}

type toolActivityGroup struct {
	Title       string
	Categories  []string
	Items       []toolActivityItem
	Active      bool
	TodoID      string
	TodoContent string
}

type toolActivityItem struct {
	Summary string
	Running bool
}

// MCPInfo holds display info about a connected MCP server.
type MCPInfo struct {
	Name      string
	ToolNames []string
	Connected bool
}

type styles struct {
	user           lipgloss.Style
	assistant      lipgloss.Style
	tool           lipgloss.Style
	error          lipgloss.Style
	prompt         lipgloss.Style
	title          lipgloss.Style
	approval       lipgloss.Style
	warn           lipgloss.Style
	approvalCursor lipgloss.Style
	approvalDim    lipgloss.Style
	statusBar      lipgloss.Style
	markdown       lipgloss.Style
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
	return defaultApprovalOptionsFor(LangEnglish)
}

func defaultApprovalOptionsFor(lang Language) []approvalOption {
	return []approvalOption{
		{label: tr(lang, "approval.allow"), shortcut: "y", decision: permission.Allow},
		{label: tr(lang, "approval.allow_always"), shortcut: "a", decision: permission.Allow},
		{label: tr(lang, "approval.deny"), shortcut: "n", decision: permission.Deny},
	}
}

// diffConfirmOptions returns the options for diff confirmation.
func diffConfirmOptions() []approvalOption {
	return diffConfirmOptionsFor(LangEnglish)
}

func diffConfirmOptionsFor(lang Language) []approvalOption {
	return []approvalOption{
		{label: tr(lang, "approval.accept"), shortcut: "y", decision: permission.Allow},
		{label: tr(lang, "approval.reject"), shortcut: "n", decision: permission.Deny},
	}
}

// streamMsg wraps a string from the agent goroutine.
type streamMsg string

// doneMsg signals generation is complete.
type doneMsg struct{}

// errMsg signals an error.
type errMsg struct{ err error }

type subAgentUpdateMsg struct{}

var ansiKeyFragmentPattern = regexp.MustCompile(`^(?:\[[0-9;?<>=]*[A-Za-z~]|\[<\d+(?:;\d+){0,2}[A-Za-zmM])$`)

// toolStatusMsg wraps a tool status update.
type toolStatusMsg ToolStatusMsg

// statusMsg updates the status bar display.
type statusMsg struct {
	Activity  string // current activity description
	ToolName  string
	ToolArg   string
	ToolCount int
}

// setProgramMsg is sent via program.Send so the model copy inside Bubble Tea's
// event loop gets the real *tea.Program reference (NewProgram copies the model).
type setProgramMsg struct {
	Program *tea.Program
}

func (m *Model) resetExitConfirm() {
	m.exitConfirmPending = false
}

func (m *Model) promptExitConfirm() {
	m.input.SetValue("")
	m.exitConfirmPending = true
	m.output.WriteString(m.styles.prompt.Render(m.t("exit.confirm")))
	m.syncConversationViewport()
	if m.viewport.AutoFollow() {
		m.viewport.GotoBottom()
	}
}

func (m *Model) queuePendingSubmission(text string) {
	m.pendingSubmissions = append(m.pendingSubmissions, text)
	m.output.WriteString(m.styles.prompt.Render(m.t("queued.output", len(m.pendingSubmissions))))
	m.syncConversationViewport()
	if m.viewport.AutoFollow() {
		m.viewport.GotoBottom()
	}
}

func (m *Model) consumePendingSubmission() string {
	joined := strings.TrimSpace(strings.Join(m.pendingSubmissions, "\n\n"))
	m.pendingSubmissions = nil
	return joined
}

func (m *Model) restorePendingInput() {
	pending := strings.TrimSpace(strings.Join(m.pendingSubmissions, "\n\n"))
	draft := strings.TrimSpace(m.input.Value())
	switch {
	case pending == "":
		return
	case draft == "":
		m.input.SetValue(pending)
	case draft == pending:
		m.input.SetValue(draft)
	default:
		m.input.SetValue(pending + "\n\n" + draft)
	}
	m.input.CursorEnd()
	m.pendingSubmissions = nil
}

func NewModel(a *agent.Agent, policy permission.PermissionPolicy) Model {
	ti := textinput.New()
	ti.Prompt = "❯ "
	ti.Placeholder = tr(LangEnglish, "input.placeholder")
	ti.Focus()
	ti.Width = 74

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
		language:   LangEnglish,
		policy:     policy,
		spinner:    NewToolSpinner(),
		history:    make([]string, 0, 100),
		mdRenderer: mdRenderer,
		viewport:   NewViewportModel(80, 20),
		mode:       policyMode(policy),
	}
}

func policyMode(policy permission.PermissionPolicy) permission.PermissionMode {
	if getter, ok := policy.(policyModeGetter); ok {
		return getter.Mode()
	}
	return permission.SupervisedMode
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
	if cfg != nil {
		m.setLanguage(cfg.Language)
	}
}

func asciiLogo() string {
	return "   ____ ____ ____ ___  ____  ______\n  / ___/ ___/ ___/ _ \\/ __ \\/ ____/\n / (_ / (_ / /__/ // / /_/ / /__  \n \\___/\\___/\\___/____/\\____/\\___/  \n"
}

func (m *Model) SetSubAgentManager(mgr *subagent.Manager) {
	m.subAgentMgr = mgr
}

func (m *Model) vendorNames() string {
	if m.config == nil {
		return ""
	}
	return strings.Join(m.config.VendorNames(), ", ")
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
		m.startupVendor = msg.Vendor
		m.startupEndpoint = msg.Endpoint
		m.startupModel = msg.Model
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
			m.syncConversationViewport()
			m.viewport.ScrollUp(3)
			return m, nil
		case tea.MouseWheelDown:
			m.syncConversationViewport()
			m.viewport.ScrollDown(3)
			return m, nil
		}
		return m, nil

	case tea.KeyMsg:
		if msg.String() != "ctrl+c" {
			m.resetExitConfirm()
		}

		// Handle approval mode (selection list)
		if m.providerPanel != nil {
			return m.handleProviderPanelKey(msg)
		}

		// Handle approval mode (selection list)
		if m.pendingApproval != nil {
			switch msg.String() {
			case "up", "k":
				m.approvalCursor = (m.approvalCursor - 1 + len(m.approvalOptions)) % len(m.approvalOptions)
				return m, nil
			case "down", "j":
				m.approvalCursor = (m.approvalCursor + 1) % len(m.approvalOptions)
				return m, nil
			case "tab":
				m.approvalCursor = (m.approvalCursor + 1) % len(m.approvalOptions)
				return m, nil
			case "shift+tab":
				m.approvalCursor = (m.approvalCursor - 1 + len(m.approvalOptions)) % len(m.approvalOptions)
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
			case "tab":
				m.diffCursor = (m.diffCursor + 1) % len(m.diffOptions)
				return m, nil
			case "shift+tab":
				m.diffCursor = (m.diffCursor - 1 + len(m.diffOptions)) % len(m.diffOptions)
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

		if m.loading && msg.String() == "ctrl+c" {
			m.resetExitConfirm()
			m.runCanceled = true
			if m.cancelFunc != nil {
				m.cancelFunc()
			}
			m.spinner.Stop()
			m.statusActivity = m.t("status.cancelling")
			if len(m.pendingSubmissions) > 0 {
				m.restorePendingInput()
			}
			debug.Log("tui", "cancelling active loop")
			m.output.WriteString("\n" + m.t("interrupted"))
			m.syncConversationViewport()
			if m.viewport.AutoFollow() {
				m.viewport.GotoBottom()
			}
			return m, nil
		}

		switch msg.String() {
		case "ctrl+c":
			if m.autoCompleteActive {
				m.autoCompleteActive = false
				m.autoCompleteItems = nil
				m.resetExitConfirm()
				return m, nil
			}
			if m.exitConfirmPending {
				m.quitting = true
				return m, tea.Quit
			}
			m.promptExitConfirm()
			return m, nil
		case "ctrl+d":
			m.quitting = true
			return m, tea.Quit
		case "shift+tab":
			if m.autoCompleteActive && len(m.autoCompleteItems) > 0 {
				m.autoCompleteIndex = (m.autoCompleteIndex - 1 + len(m.autoCompleteItems)) % len(m.autoCompleteItems)
				return m, nil
			}
			return m.handleModeSwitch()
		case "pgup":
			m.syncConversationViewport()
			m.viewport.ScrollUp(m.viewport.VisibleLineCount() / 2)
			return m, nil
		case "pgdown":
			m.syncConversationViewport()
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
				if len(m.autoCompleteItems) == 1 {
					return m, m.applyAutoComplete()
				}
				m.autoCompleteIndex = (m.autoCompleteIndex + 1) % len(m.autoCompleteItems)
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
				return m, m.applyAutoComplete()
			}
			m.resetExitConfirm()
			text := strings.TrimSpace(m.input.Value())
			m.input.SetValue("")
			if text == "" {
				return m, nil
			}
			if m.loading {
				m.history = append(m.history, text)
				m.historyIdx = len(m.history)
				m.queuePendingSubmission(text)
				return m, nil
			}
			return m, m.submitText(text, true)
		}

	case streamMsg:
		m.closeToolActivityGroup()
		m.flushGroupedActivitiesToOutput()
		if !m.streamPrefixWritten {
			m.streamStartPos = m.output.Len()
			m.output.WriteString(bulletStyle.Render("● "))
			m.streamPrefixWritten = true
		}
		if m.streamBuffer != nil {
			m.streamBuffer.WriteString(string(msg))
		}
		m.output.WriteString(string(msg))
		m.trimOutput()
		m.syncConversationViewport()
		m.viewport.GotoBottom()
		return m, nil

	case doneMsg:
		m.loading = false
		m.spinner.Stop()
		m.closeToolActivityGroup()
		m.flushGroupedActivitiesToOutput()
		m.cancelFunc = nil
		m.streamPrefixWritten = false
		wasCanceled := m.runCanceled
		wasFailed := m.runFailed
		m.runCanceled = false
		m.runFailed = false
		m.statusActivity = ""
		m.statusToolName = ""
		m.statusToolArg = ""
		m.statusToolCount = 0
		// Render accumulated stream buffer as markdown
		if m.streamBuffer != nil && m.streamBuffer.Len() > 0 {
			rendered, err := m.mdRenderer.Render(m.streamBuffer.String())
			if err != nil {
				rendered = m.streamBuffer.String()
			}
			rendered = trimLeadingRenderedSpacing(rendered)
			m.output.Truncate(m.streamStartPos)
			m.output.WriteString(bulletStyle.Render("● "))
			m.output.WriteString(rendered)
			m.streamBuffer = nil
		}
		m.output.WriteString("\n")
		m.syncConversationViewport()
		m.viewport.GotoBottom()
		if !wasCanceled && !wasFailed && len(m.pendingSubmissions) > 0 {
			return m, m.submitText(m.consumePendingSubmission(), false)
		}
		return m, nil

	case errMsg:
		if errors.Is(msg.err, context.Canceled) {
			return m, nil
		}
		m.runFailed = true
		m.loading = false
		m.spinner.Stop()
		m.closeToolActivityGroup()
		m.flushGroupedActivitiesToOutput()
		m.cancelFunc = nil
		if len(m.pendingSubmissions) > 0 {
			m.restorePendingInput()
		}
		m.output.WriteString(m.styles.error.Render(fmt.Sprintf("Error: %v\n\n", msg.err)))
		m.syncConversationViewport()
		m.viewport.GotoBottom()
		return m, nil

	case ApprovalMsg:
		if m.mode == permission.AutopilotMode {
			m.pendingApproval = &msg
			return m, m.handleApproval(permission.Allow)
		}
		// Agent is requesting approval
		m.pendingApproval = &msg
		m.approvalOptions = defaultApprovalOptions()
		m.approvalCursor = 0
		return m, nil

	case DiffConfirmMsg:
		if m.mode == permission.AutopilotMode {
			m.pendingDiffConfirm = &msg
			return m, m.handleDiffConfirm(true)
		}
		m.pendingDiffConfirm = &msg
		m.diffOptions = diffConfirmOptions()
		m.diffCursor = 0
		return m, nil

	case subAgentUpdateMsg:
		m.syncConversationViewport()
		if m.viewport.AutoFollow() {
			m.viewport.GotoBottom()
		}
		return m, nil

	case toolStatusMsg:
		ts := ToolStatusMsg(msg)
		if ts.Running {
			m.startToolActivity(ts)
			// Flush current stream buffer (render markdown) before tool output
			if m.streamBuffer != nil && m.streamBuffer.Len() > 0 {
				rendered, err := m.mdRenderer.Render(m.streamBuffer.String())
				if err != nil {
					rendered = m.streamBuffer.String()
				}
				rendered = trimLeadingRenderedSpacing(rendered)
				m.output.Truncate(m.streamStartPos)
				m.output.WriteString(bulletStyle.Render("● "))
				m.output.WriteString(rendered)
				m.streamBuffer.Reset()
				m.streamStartPos = m.output.Len()
			}
			m.spinner.Start(firstNonEmpty(ts.Activity, formatToolInline(toolDisplayName(ts), toolDetail(ts))))
		} else {
			m.finishToolActivity(ts)
			ts.Elapsed = m.spinner.Elapsed()
			m.spinner.Stop()
			// Reset stream prefix so next text block gets ●
			m.streamPrefixWritten = false
			// Reset stream buffer position for next text chunk
			m.streamStartPos = m.output.Len()
		}
		m.syncConversationViewport()
		if m.viewport.AutoFollow() {
			m.viewport.GotoBottom()
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
		m.syncConversationViewport()
		if m.viewport.AutoFollow() {
			m.viewport.GotoBottom()
		}
		return m, nil

	case statusMsg:
		m.statusActivity = msg.Activity
		m.statusToolName = msg.ToolName
		m.statusToolArg = msg.ToolArg
		if msg.ToolCount > 0 {
			m.statusToolCount = msg.ToolCount
		}
		return m, nil

	}

	var cmd tea.Cmd
	if shouldIgnoreInputUpdate(msg, m.lastResizeAt) {
		return m, nil
	}
	m.input, cmd = m.input.Update(msg)

	// Update autocomplete state based on current input
	m.updateAutoComplete()

	return m, cmd
}

func shouldIgnoreInputUpdate(msg tea.Msg, lastResizeAt time.Time) bool {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok || keyMsg.Type != tea.KeyRunes || keyMsg.Paste || len(keyMsg.Runes) == 0 {
		return false
	}

	raw := string(keyMsg.Runes)
	if strings.ContainsRune(raw, '\x1b') {
		return true
	}
	for _, r := range keyMsg.Runes {
		if unicode.IsControl(r) {
			return true
		}
	}
	if lastResizeAt.IsZero() || time.Since(lastResizeAt) > 250*time.Millisecond || len(keyMsg.Runes) < 2 {
		return false
	}
	return ansiKeyFragmentPattern.MatchString(raw)
}

package tui

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
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
	commandMgr         *commands.Manager
	autoMem            *memory.AutoMemory
	projMemFiles       []string
	autoMemFiles       []string
	pluginMgr          *plugin.Manager
	subAgentMgr        *subagent.Manager
	mcpManager         mcpManager
	mode               permission.PermissionMode
	pendingDiffConfirm *DiffConfirmMsg
	fullscreen         bool
	providerPanel      *providerPanelState
	mcpPanel           *mcpPanelState
	skillsPanel        *skillsPanelState

	// Approval selection list
	approvalOptions []approvalOption
	approvalCursor  int

	// Diff confirm selection list
	diffOptions            []approvalOption
	diffCursor             int
	pendingImage           *imageAttachedMsg
	langOptions            []languageOption
	langCursor             int
	languagePromptRequired bool

	// Viewport for scrollable output
	viewport ViewportModel

	// Markdown rendering
	mdRenderer          *glamour.TermRenderer
	markdownWrapWidth   int
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
	activeMCPTools  map[string]ToolStatusMsg

	// Slash command autocomplete
	autoCompleteItems    []string
	autoCompleteIndex    int
	autoCompleteActive   bool
	autoCompleteKind     string // "slash" or "mention"
	autoCompleteWorkDir  string // working directory for mention completion
	startedAt            time.Time
	startupBannerVisible bool
	lastResizeAt         time.Time
	sidebarVisible       bool
	exitConfirmPending   bool
	pendingSubmissions   []string
	runCanceled          bool
	runFailed            bool
	clipboardLoader      func() (imageAttachedMsg, error)
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
	Name          string
	ToolNames     []string
	PromptNames   []string
	ResourceNames []string
	Connected     bool
	Pending       bool
	Error         string
	Transport     string
	Migrated      bool
}

type mcpManager interface {
	Retry(name string) bool
	Install(ctx context.Context, server config.MCPServerConfig) error
	Uninstall(name string) bool
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

type languageOption struct {
	label    string
	shortcut string
	lang     Language
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

type startupReadyMsg struct{}

type subAgentUpdateMsg struct{}

type skillsChangedMsg struct{}

var ansiKeyFragmentPattern = regexp.MustCompile(`^(?:\[[0-9;?<>=]*[A-Za-z~]|\[<\d+(?:;\d+){0,2}[A-Za-zmM])$`)
var terminalResponseFragmentPattern = regexp.MustCompile(`^(?:\]?(?:10|11);rgb:[0-9a-fA-F]{4}/[0-9a-fA-F]{4}/[0-9a-fA-F]{4}\\?|[() \]]*\d{1,2};rgb:[0-9a-fA-F]{4}/[0-9a-fA-F]{4}/[0-9a-fA-F]{4}\\?)$`)
var ansiChunkPattern = regexp.MustCompile(`\[[0-9;?<>=]*[A-Za-z~]|\[<\d+(?:;\d+){0,2}[A-Za-zmM]`)
var ansiMouseChunkPattern = regexp.MustCompile(`\[<\d+(?:;\d+){0,2}[A-Za-zmM]`)
var terminalResponseChunkPattern = regexp.MustCompile(`(?:\\?\]?|[() \]]*)?\d{1,2};rgb:[0-9a-fA-F]{4}/[0-9a-fA-F]{4}/[0-9a-fA-F]{4}\\?`)
var terminalOrphanFragmentPattern = regexp.MustCompile(`^[\[\]()\\]+$`)
var bareMouseFragmentPattern = regexp.MustCompile(`^(?:<\d+(?:;\d+){2}[mM])+$`)
var bareMouseChunkPattern = regexp.MustCompile(`<\d+(?:;\d+){2}[mM]`)

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

type mcpServersMsg struct {
	Servers []plugin.MCPServerInfo
}

func (m *Model) resetExitConfirm() {
	m.exitConfirmPending = false
}

func (m *Model) promptExitConfirm() {
	m.input.SetValue("")
	m.exitConfirmPending = true
	m.ensureOutputHasBlankLine()
	m.output.WriteString(m.styles.prompt.Render(m.t("exit.confirm")))
	m.syncConversationViewport()
	if m.viewport.AutoFollow() {
		m.viewport.GotoBottom()
	}
}

func (m *Model) queuePendingSubmission(text string) {
	m.pendingSubmissions = append(m.pendingSubmissions, text)
	m.ensureOutputHasBlankLine()
	m.output.WriteString(m.styles.prompt.Render(m.t("queued.output", len(m.pendingSubmissions))))
	m.syncConversationViewport()
	if m.viewport.AutoFollow() {
		m.viewport.GotoBottom()
	}
}

func (m *Model) cancelActiveRun() {
	if m.runCanceled {
		return
	}
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

func stripImagePlaceholder(value, placeholder string) string {
	trimmed := strings.TrimSpace(value)
	placeholder = strings.TrimSpace(placeholder)
	if trimmed == "" || placeholder == "" {
		return trimmed
	}
	if trimmed == placeholder {
		return ""
	}
	if strings.HasPrefix(trimmed, placeholder) {
		return strings.TrimSpace(strings.TrimPrefix(trimmed, placeholder))
	}
	return trimmed
}

func (m *Model) stripPendingImagePlaceholder(value string) string {
	if m.pendingImage == nil {
		return strings.TrimSpace(value)
	}
	return stripImagePlaceholder(value, m.pendingImage.placeholder)
}

func (m *Model) setComposerImagePlaceholder(msg imageAttachedMsg) {
	draft := m.input.Value()
	if m.pendingImage != nil {
		draft = stripImagePlaceholder(draft, m.pendingImage.placeholder)
	}
	draft = strings.TrimSpace(draft)
	if draft == "" {
		m.input.SetValue(msg.placeholder + " ")
	} else {
		m.input.SetValue(msg.placeholder + " " + draft)
	}
	m.input.CursorEnd()
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
		input:                ti,
		output:               &bytes.Buffer{},
		styles:               s,
		agent:                a,
		language:             LangEnglish,
		policy:               policy,
		spinner:              NewToolSpinner(),
		history:              make([]string, 0, 100),
		mdRenderer:           mdRenderer,
		markdownWrapWidth:    80,
		viewport:             NewViewportModel(80, 20),
		mode:                 policyMode(policy),
		startedAt:            time.Now(),
		startupBannerVisible: true,
		sidebarVisible:       true,
		activeMCPTools:       make(map[string]ToolStatusMsg),
		clipboardLoader:      loadClipboardImage,
	}
}

func loadClipboardImage() (imageAttachedMsg, error) {
	img, err := image.ReadClipboard()
	if err != nil {
		return imageAttachedMsg{}, err
	}
	filename, err := newClipboardImageFilename()
	if err != nil {
		return imageAttachedMsg{}, err
	}
	return imageAttachedMsg{
		placeholder: image.Placeholder(filename, img),
		img:         img,
		filename:    filename,
	}, nil
}

func newClipboardImageFilename() (string, error) {
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", fmt.Errorf("generating clipboard image filename: %w", err)
	}
	return "ggcode-image-" + hex.EncodeToString(suffix[:]) + ".png", nil
}

func policyMode(policy permission.PermissionPolicy) permission.PermissionMode {
	if getter, ok := policy.(policyModeGetter); ok {
		return getter.Mode()
	}
	return permission.SupervisedMode
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		tea.WindowSize(),
		tea.Tick(3*time.Second, func(time.Time) tea.Msg { return startupReadyMsg{} }),
	)
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

func (m *Model) SetMCPManager(mgr mcpManager) {
	m.mcpManager = mgr
}

func (m *Model) SetPluginManager(mgr *plugin.Manager) {
	m.pluginMgr = mgr
}

func (m *Model) SetCustomCommands(cmds map[string]*commands.Command) {
	m.customCmds = cmds
}

func (m *Model) SetCommandsManager(mgr *commands.Manager) {
	m.commandMgr = mgr
	if mgr != nil {
		m.customCmds = mgr.UserSlashCommands()
	}
}

func (m *Model) refreshCommands() {
	if m.commandMgr == nil {
		return
	}
	m.customCmds = m.commandMgr.UserSlashCommands()
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
		if cfg.FirstRun {
			m.openLanguageSelector(true)
		}
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
	var spinnerCmd tea.Cmd
	if m.spinner.IsActive() {
		spinnerCmd = m.spinner.Update(msg)
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
		if m.startupBannerVisible && !shouldIgnoreInputUpdate(msg, m.lastResizeAt, m.startedAt) {
			m.startupBannerVisible = false
		}
		if msg.String() != "ctrl+c" {
			m.resetExitConfirm()
		}
		if msg.String() == "ctrl+r" {
			m.sidebarVisible = !m.sidebarVisible
			return m, nil
		}

		if msg.String() == "ctrl+c" && !m.loading && (m.providerPanel != nil || m.mcpPanel != nil || len(m.langOptions) > 0) {
			if m.exitConfirmPending {
				m.quitting = true
				return m, tea.Quit
			}
			m.promptExitConfirm()
			return m, nil
		}

		// Handle approval mode (selection list)
		if m.providerPanel != nil {
			return m.handleProviderPanelKey(msg)
		}

		if m.mcpPanel != nil {
			return m.handleMCPPanelKey(msg)
		}

		if m.skillsPanel != nil {
			return m.handleSkillsPanelKey(msg)
		}

		if len(m.langOptions) > 0 {
			switch msg.String() {
			case "up", "k":
				m.langCursor = (m.langCursor - 1 + len(m.langOptions)) % len(m.langOptions)
				return m, nil
			case "down", "j", "tab":
				m.langCursor = (m.langCursor + 1) % len(m.langOptions)
				return m, nil
			case "shift+tab":
				m.langCursor = (m.langCursor - 1 + len(m.langOptions)) % len(m.langOptions)
				return m, nil
			case "enter", "right":
				return m, m.applyLanguageSelection(m.langOptions[m.langCursor].lang)
			case "e", "E":
				return m, m.applyLanguageSelection(LangEnglish)
			case "z", "Z":
				return m, m.applyLanguageSelection(LangZhCN)
			case "esc":
				if m.languagePromptRequired {
					return m, nil
				}
				m.langOptions = nil
				return m, nil
			}
			return m, nil
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

		if m.loading && (msg.String() == "ctrl+c" || msg.String() == "esc") {
			m.resetExitConfirm()
			m.cancelActiveRun()
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
		case "ctrl+v":
			if !m.loading {
				return m, m.handleClipboardPaste()
			}
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
			m.ensureOutputHasBlankLine()
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

	case startupReadyMsg:
		m.startupBannerVisible = false
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

	case skillsChangedMsg:
		m.refreshCommands()
		return m, nil

	case toolStatusMsg:
		ts := ToolStatusMsg(msg)
		m.updateActiveMCPTools(ts)
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
			startCmd := m.spinner.Start(firstNonEmpty(ts.Activity, formatToolInline(toolDisplayName(ts), toolDetail(ts))))
			spinnerCmd = combineCmds(spinnerCmd, startCmd)
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
		return m, spinnerCmd

	case mcpServersMsg:
		m.mcpServers = toMCPInfos(msg.Servers)
		return m, nil

	case mcpInstallResultMsg:
		if m.mcpPanel != nil {
			if msg.err != nil {
				m.mcpPanel.message = fmt.Sprintf("Install failed: %v", msg.err)
			} else if msg.replaced {
				m.mcpPanel.message = fmt.Sprintf("Updated MCP server %s.", msg.name)
			} else {
				m.mcpPanel.message = fmt.Sprintf("Installed MCP server %s.", msg.name)
			}
		}
		return m, nil

	case mcpUninstallResultMsg:
		if m.mcpPanel != nil {
			if msg.err != nil {
				m.mcpPanel.message = fmt.Sprintf("Uninstall failed: %v", msg.err)
			} else {
				m.mcpPanel.message = fmt.Sprintf("Uninstalled MCP server %s.", msg.name)
				if m.mcpPanel.selected >= len(m.mcpServers) && len(m.mcpServers) > 0 {
					m.mcpPanel.selected = len(m.mcpServers) - 1
				}
			}
		}
		return m, nil

	case setProgramMsg:
		debug.Log("tui", "setProgramMsg received, program was nil=%v", m.program == nil)
		m.program = msg.Program
		return m, nil

	case imageAttachedMsg:
		m.setComposerImagePlaceholder(msg)
		m.pendingImage = &msg
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
	if shouldIgnoreInputUpdate(msg, m.lastResizeAt, m.startedAt) {
		return m, nil
	}
	m.input, cmd = m.input.Update(msg)
	m.sanitizeTerminalResponseInput()

	// Update autocomplete state based on current input
	m.updateAutoComplete()

	return m, combineCmds(spinnerCmd, cmd)
}

func combineCmds(cmds ...tea.Cmd) tea.Cmd {
	filtered := make([]tea.Cmd, 0, len(cmds))
	for _, cmd := range cmds {
		if cmd != nil {
			filtered = append(filtered, cmd)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	if len(filtered) == 1 {
		return filtered[0]
	}
	return tea.Batch(filtered...)
}

func shouldIgnoreInputUpdate(msg tea.Msg, lastResizeAt, startedAt time.Time) bool {
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
	if bareMouseFragmentPattern.MatchString(raw) {
		return true
	}
	if looksLikeExactTerminalNoise(raw) {
		return true
	}
	if !terminalResponseSuppressionActive(lastResizeAt, startedAt) {
		return false
	}
	return ansiKeyFragmentPattern.MatchString(raw) || terminalResponseFragmentPattern.MatchString(raw) || terminalOrphanFragmentPattern.MatchString(raw)
}

func (m *Model) sanitizeTerminalResponseInput() {
	value := m.input.Value()
	if value == "" {
		return
	}
	cleaned := stripExactTerminalNoise(value)
	if terminalResponseSuppressionActive(m.lastResizeAt, m.startedAt) {
		cleaned = ansiChunkPattern.ReplaceAllString(cleaned, "")
	}
	if terminalOrphanFragmentPattern.MatchString(strings.TrimSpace(cleaned)) {
		cleaned = ""
	}
	if cleaned == value {
		return
	}
	m.input.SetValue(cleaned)
	m.input.CursorEnd()
}

func terminalResponseSuppressionActive(lastResizeAt, startedAt time.Time) bool {
	if !lastResizeAt.IsZero() && time.Since(lastResizeAt) <= 1500*time.Millisecond {
		return true
	}
	return !startedAt.IsZero() && time.Since(startedAt) <= 3*time.Second
}

func looksLikeExactTerminalNoise(value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	return strings.TrimSpace(stripExactTerminalNoise(value)) == ""
}

func stripExactTerminalNoise(value string) string {
	cleaned := terminalResponseChunkPattern.ReplaceAllString(value, "")
	cleaned = ansiMouseChunkPattern.ReplaceAllString(cleaned, "")
	cleaned = bareMouseChunkPattern.ReplaceAllString(cleaned, "")
	return cleaned
}

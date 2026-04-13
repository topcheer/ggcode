package tui

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/auth"
	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/harness"
	"github.com/topcheer/ggcode/internal/image"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/subagent"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/update"
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
	input                           textinput.Model
	output                          *bytes.Buffer
	shellMode                       bool
	loading                         bool
	quitting                        bool
	width                           int
	height                          int
	styles                          styles
	agent                           *agent.Agent
	program                         *tea.Program
	cancelFunc                      func()
	policy                          permission.PermissionPolicy
	spinner                         *ToolSpinner
	history                         []string
	historyIdx                      int
	pendingApproval                 *ApprovalMsg
	session                         *session.Session
	sessionStore                    session.Store
	mcpServers                      []MCPInfo
	config                          *config.Config
	language                        Language
	startupVendor                   string
	startupEndpoint                 string
	startupModel                    string
	activeVendor                    string
	activeEndpoint                  string
	activeModel                     string
	customCmds                      map[string]*commands.Command
	commandMgr                      *commands.Manager
	autoMem                         *memory.AutoMemory
	projMemFiles                    []string
	autoMemFiles                    []string
	pluginMgr                       *plugin.Manager
	subAgentMgr                     *subagent.Manager
	mcpManager                      mcpManager
	mode                            permission.PermissionMode
	pendingDiffConfirm              *DiffConfirmMsg
	pendingQuestionnaire            *questionnaireState
	pendingHarnessCheckpointConfirm *HarnessCheckpointConfirmMsg
	fullscreen                      bool
	modelPanel                      *modelPanelState
	providerPanel                   *providerPanelState
	mcpPanel                        *mcpPanelState
	skillsPanel                     *skillsPanelState
	inspectorPanel                  *inspectorPanelState
	previewPanel                    *previewPanelState
	fileBrowser                     *fileBrowserState
	harnessPanel                    *harnessPanelState
	harnessContextPrompt            *harnessContextPromptState

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

	streamBuffer        *bytes.Buffer
	shellBuffer         *bytes.Buffer
	streamStartPos      int
	streamPrefixWritten bool
	harnessRunRemainder string
	harnessRunLiveTail  string

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
	autoCompleteItems     []string
	autoCompleteIndex     int
	autoCompleteActive    bool
	autoCompleteKind      string // "slash" or "mention"
	autoCompleteWorkDir   string // working directory for mention completion
	startedAt             time.Time
	startupBannerVisible  bool
	lastResizeAt          time.Time
	sidebarVisible        bool
	exitConfirmPending    bool
	pendingSubmissions    []string
	pendingMu             *sync.Mutex
	sessionMu             *sync.Mutex
	projectMemoryLoading  bool
	runCanceled           bool
	runFailed             bool
	activeAgentRunID      int
	activeShellRunID      int
	shellCommandSubmitter func(command string, addToHistory bool) tea.Cmd
	harnessRunProject     *harness.Project
	harnessRunGoal        string
	harnessRunTaskID      string
	harnessRunLogPath     string
	harnessRunLogOffset   int64
	harnessRunLastDetail  string
	clipboardLoader       func() (imageAttachedMsg, error)
	clipboardWriter       func(string) error
	urlOpener             func(string) error
	updateSvc             *update.Service
	updateInfo            update.CheckResult
	updateError           string
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
	CallID                 string
	Summary                string
	Running                bool
	CommandTitle           string
	CommandLines           []string
	CommandHiddenLineCount int
	OutputLines            []string
	OutputHiddenLineCount  int
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

type HarnessCheckpointConfirmMsg struct {
	Checkpoint harness.DirtyWorkspaceCheckpoint
	Response   chan bool
}

type AskUserMsg struct {
	Request  toolpkg.AskUserRequest
	Response chan toolpkg.AskUserResponse
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

type harnessRunResultMsg struct {
	Summary *harness.RunSummary
	Err     error
}

type harnessRunProgressMsg struct {
	TaskID    string
	Activity  string
	Detail    string
	LogPath   string
	LogChunk  string
	LogOffset int64
}

type harnessPanelAutoRefreshMsg struct{}

type harnessContextSuggestionsMsg struct {
	Contexts []harness.ContextConfig
	Err      error
}

type harnessInitResultMsg struct {
	Result *harness.InitResult
	Err    error
}

type projectMemoryLoadedMsg struct {
	Content string
	Files   []string
	Err     error
}

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

type agentStreamMsg struct {
	RunID int
	Text  string
}

type agentDoneMsg struct {
	RunID int
}

type agentErrMsg struct {
	RunID int
	Err   error
}

type agentToolStatusMsg struct {
	RunID int
	ToolStatusMsg
}

type agentStatusMsg struct {
	RunID int
	statusMsg
}

type agentInterruptMsg struct {
	RunID int
	Text  string
}

// setProgramMsg is sent via program.Send so the model copy inside Bubble Tea's
// event loop gets the real *tea.Program reference (NewProgram copies the model).
type setProgramMsg struct {
	Program *tea.Program
}

type mcpServersMsg struct {
	Servers []plugin.MCPServerInfo
}

type updateCheckResultMsg struct {
	Result update.CheckResult
	Err    error
}

type updatePrepareResultMsg struct {
	Prepared update.PreparedUpdate
	Err      error
}

type updateCheckTickMsg struct{}

func (m *Model) resetExitConfirm() {
	m.exitConfirmPending = false
}

func (m *Model) promptExitConfirm() {
	m.input.SetValue("")
	m.exitConfirmPending = true
	m.ensureOutputHasBlankLine()
	m.output.WriteString(m.styles.prompt.Render(m.t("exit.confirm")))
	m.output.WriteString("\n")
	m.syncConversationViewport()
	if m.viewport.AutoFollow() {
		m.viewport.GotoBottom()
	}
}

func (m *Model) queuePendingSubmission(text string) {
	count := m.enqueuePendingSubmission(text)
	if count == 0 {
		return
	}
	m.ensureOutputHasBlankLine()
	m.output.WriteString(m.styles.prompt.Render(m.t("queued.output", count)))
	m.syncConversationViewport()
	if m.viewport.AutoFollow() {
		m.viewport.GotoBottom()
	}
}

func (m *Model) enqueuePendingSubmission(text string) int {
	m.pendingMutex().Lock()
	defer m.pendingMutex().Unlock()
	m.pendingSubmissions = append(m.pendingSubmissions, text)
	return len(m.pendingSubmissions)
}

func (m *Model) pendingSubmissionCount() int {
	m.pendingMutex().Lock()
	defer m.pendingMutex().Unlock()
	return len(m.pendingSubmissions)
}

func (m *Model) clearPendingSubmissions() {
	m.pendingMutex().Lock()
	defer m.pendingMutex().Unlock()
	m.pendingSubmissions = nil
}

func (m *Model) pendingSubmissionSnapshot() []string {
	m.pendingMutex().Lock()
	defer m.pendingMutex().Unlock()
	if len(m.pendingSubmissions) == 0 {
		return nil
	}
	out := make([]string, len(m.pendingSubmissions))
	copy(out, m.pendingSubmissions)
	return out
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
	if m.harnessRunProject != nil {
		m.statusActivity = m.t("status.cancelling")
	} else {
		m.loading = false
		m.cancelFunc = nil
		m.statusActivity = ""
	}
	m.statusToolName = ""
	m.statusToolArg = ""
	m.statusToolCount = 0
	m.resetActivityGroups()
	if m.pendingSubmissionCount() > 0 {
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
	m.pendingMutex().Lock()
	defer m.pendingMutex().Unlock()
	joined := strings.TrimSpace(strings.Join(m.pendingSubmissions, "\n\n"))
	m.pendingSubmissions = nil
	return joined
}

func (m *Model) restorePendingInput() {
	m.pendingMutex().Lock()
	pending := strings.TrimSpace(strings.Join(m.pendingSubmissions, "\n\n"))
	draft := strings.TrimSpace(m.input.Value())
	switch {
	case pending == "":
		m.pendingMutex().Unlock()
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
	m.pendingMutex().Unlock()
}

func (m *Model) drainPendingInterrupt(runID int) string {
	text := m.consumePendingSubmission()
	if text == "" {
		return ""
	}
	m.appendUserMessage(text)
	if m.program != nil {
		m.program.Send(agentInterruptMsg{RunID: runID, Text: text})
	}
	return text
}

func (m *Model) pendingMutex() *sync.Mutex {
	if m.pendingMu == nil {
		m.pendingMu = &sync.Mutex{}
	}
	return m.pendingMu
}

func (m *Model) sessionMutex() *sync.Mutex {
	if m.sessionMu == nil {
		m.sessionMu = &sync.Mutex{}
	}
	return m.sessionMu
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

	return Model{
		input:                ti,
		output:               &bytes.Buffer{},
		styles:               s,
		agent:                a,
		language:             LangEnglish,
		policy:               policy,
		spinner:              NewToolSpinner(),
		history:              make([]string, 0, 100),
		viewport:             NewViewportModel(80, 20),
		mode:                 policyMode(policy),
		startedAt:            time.Now(),
		startupBannerVisible: false,
		sidebarVisible:       true,
		activeMCPTools:       make(map[string]ToolStatusMsg),
		clipboardLoader:      loadClipboardImage,
		clipboardWriter:      copyTextToClipboard,
		urlOpener:            openSystemURL,
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
	sourcePath, err := persistAttachedImage(filename, img)
	if err != nil {
		return imageAttachedMsg{}, err
	}
	return imageAttachedMsg{
		placeholder: image.Placeholder(filename, img),
		img:         img,
		filename:    filename,
		sourcePath:  sourcePath,
	}, nil
}

func newClipboardImageFilename() (string, error) {
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", fmt.Errorf("generating clipboard image filename: %w", err)
	}
	return "ggcode-image-" + hex.EncodeToString(suffix[:]) + ".png", nil
}

func persistAttachedImage(filename string, img image.Image) (string, error) {
	cacheDir := filepath.Join(os.TempDir(), "ggcode-images")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return "", fmt.Errorf("creating image cache dir: %w", err)
	}
	path := filepath.Join(cacheDir, filepath.Base(filename))
	if err := os.WriteFile(path, img.Data, 0o600); err != nil {
		return "", fmt.Errorf("writing attached image: %w", err)
	}
	return path, nil
}

func policyMode(policy permission.PermissionPolicy) permission.PermissionMode {
	if getter, ok := policy.(policyModeGetter); ok {
		return getter.Mode()
	}
	return permission.SupervisedMode
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		textinput.Blink,
		tea.WindowSize(),
	}
	if m.updateSvc != nil {
		cmds = append(cmds, m.checkForUpdateCmd())
		cmds = append(cmds, m.scheduleUpdateCheckCmd())
	}
	return tea.Batch(cmds...)
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

func (m *Model) SetUpdateService(svc *update.Service) {
	m.updateSvc = svc
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

func (m *Model) SetProjectMemoryLoading(loading bool) {
	m.projectMemoryLoading = loading
}

func (m *Model) SetAutoMemoryFiles(files []string) {
	m.autoMemFiles = files
}

func (m *Model) SetConfig(cfg *config.Config) {
	m.config = cfg
	if cfg != nil {
		m.setLanguage(cfg.Language)
		m.sidebarVisible = cfg.SidebarVisible()
		if resolved, err := cfg.ResolveActiveEndpoint(); err == nil && m.activeVendor == "" && m.activeEndpoint == "" && m.activeModel == "" {
			m.setActiveRuntimeSelection(resolved.VendorName, resolved.EndpointName, resolved.Model)
		}
		if cfg.FirstRun {
			m.openLanguageSelector(true)
		}
	}
}

func (m *Model) setActiveRuntimeSelection(vendor, endpoint, model string) {
	m.activeVendor = strings.TrimSpace(vendor)
	m.activeEndpoint = strings.TrimSpace(endpoint)
	m.activeModel = strings.TrimSpace(model)
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
	sourcePath  string
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
		m.setActiveRuntimeSelection(msg.Vendor, msg.Endpoint, msg.Model)
		return m, nil

	case tea.WindowSizeMsg:
		m.handleResize(msg.Width, msg.Height)
		return m, nil

	case tea.MouseMsg:
		if m.fileBrowser != nil {
			return m.handleFileBrowserMouse(msg)
		}
		if m.previewPanel != nil {
			return m.handlePreviewMouse(msg)
		}
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
		if m.startupBannerVisible && !shouldIgnoreInputUpdate(msg, m.lastResizeAt) {
			m.startupBannerVisible = false
		}
		if msg.String() != "ctrl+c" {
			m.resetExitConfirm()
		}
		if msg.String() == "ctrl+r" {
			m.sidebarVisible = !m.sidebarVisible
			if m.config != nil {
				_ = m.config.SaveSidebarPreference(m.sidebarVisible)
			}
			return m, nil
		}
		if msg.String() == "ctrl+f" {
			m.toggleFileBrowser()
			return m, nil
		}
		if m.fileBrowser != nil {
			return m.handleFileBrowserKey(msg)
		}
		if m.previewPanel != nil {
			return m.handlePreviewKey(msg)
		}

		if msg.String() == "ctrl+c" && !m.loading && (m.modelPanel != nil || m.providerPanel != nil || m.mcpPanel != nil || m.skillsPanel != nil || m.inspectorPanel != nil || m.harnessPanel != nil || m.harnessContextPrompt != nil || len(m.langOptions) > 0) {
			if m.exitConfirmPending {
				m.quitting = true
				return m, tea.Quit
			}
			m.promptExitConfirm()
			return m, nil
		}

		// Handle approval mode (selection list)
		if m.modelPanel != nil {
			return m.handleModelPanelKey(msg)
		}

		if m.providerPanel != nil {
			return m.handleProviderPanelKey(msg)
		}

		if m.mcpPanel != nil {
			return m.handleMCPPanelKey(msg)
		}

		if m.skillsPanel != nil {
			return m.handleSkillsPanelKey(msg)
		}

		if m.inspectorPanel != nil {
			return m.handleInspectorPanelKey(msg)
		}

		if m.harnessContextPrompt != nil {
			return m.handleHarnessContextPromptKey(msg)
		}

		if m.harnessPanel != nil {
			return m.handleHarnessPanelKey(msg)
		}

		if m.pendingQuestionnaire != nil {
			return m.handleQuestionnaireKey(msg)
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
		if m.pendingHarnessCheckpointConfirm != nil {
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
				return m, m.handleHarnessCheckpointConfirm(opt.decision == permission.Allow)
			case "y", "Y":
				return m, m.handleHarnessCheckpointConfirm(true)
			case "n", "N":
				return m, m.handleHarnessCheckpointConfirm(false)
			case "esc", "ctrl+c":
				return m, m.handleHarnessCheckpointConfirm(false)
			}
			return m, nil
		}

		if msg.String() == "esc" && m.previewPanel != nil {
			m.closePreviewPanel()
			return m, nil
		}

		if m.loading && (msg.String() == "ctrl+c" || msg.String() == "esc") {
			m.resetExitConfirm()
			m.cancelActiveRun()
			return m, nil
		}

		switch msg.String() {
		case "$", "!":
			if !m.shellMode && !m.loading && !m.projectMemoryLoading && strings.TrimSpace(m.input.Value()) == "" {
				m.setShellMode(true)
				return m, nil
			}
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
			if m.shellMode && !m.loading {
				m.setShellMode(false)
				m.input.SetValue("")
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
			if m.shellMode {
				if m.loading || m.projectMemoryLoading {
					m.history = append(m.history, "$ "+text)
					m.historyIdx = len(m.history)
					m.queuePendingSubmission(text)
					return m, nil
				}
				return m, m.submitShellCommand(text, true)
			}
			if m.loading || m.projectMemoryLoading {
				if shouldAllowBusyHarnessPanel(text) {
					return m, m.submitText(text, true)
				}
				m.history = append(m.history, text)
				m.historyIdx = len(m.history)
				m.queuePendingSubmission(text)
				return m, nil
			}
			return m, m.submitText(text, true)
		}

	case streamMsg:
		if m.runCanceled {
			return m, nil
		}
		m.appendStreamChunk(string(msg))
		return m, combineCmds(spinnerCmd, m.ensureLoadingSpinner(m.statusActivity))

	case agentStreamMsg:
		if msg.RunID != m.activeAgentRunID || m.runCanceled || !m.loading {
			return m, nil
		}
		m.appendStreamChunk(msg.Text)
		return m, combineCmds(spinnerCmd, m.ensureLoadingSpinner(m.statusActivity))

	case agentInterruptMsg:
		if msg.RunID != m.activeAgentRunID {
			return m, nil
		}
		m.ensureOutputEndsWithNewline()
		m.output.WriteString(m.renderConversationUserEntry("❯ ", msg.Text))
		m.output.WriteString("\n")
		m.output.WriteString(m.styles.prompt.Render(m.t("interrupt.delivered")))
		m.output.WriteString("\n")
		m.syncConversationViewport()
		m.viewport.GotoBottom()
		return m, nil

	case shellCommandStreamMsg:
		if msg.RunID != m.activeShellRunID || m.runCanceled || !m.loading {
			return m, nil
		}
		m.appendShellChunk(msg.Text)
		return m, combineCmds(spinnerCmd, m.ensureLoadingSpinner(m.statusActivity))

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
		if m.streamBuffer != nil && m.streamBuffer.Len() > 0 {
			m.renderStreamBuffer(true)
			m.streamBuffer = nil
		}
		m.output.WriteString("\n")
		m.syncConversationViewport()
		m.viewport.GotoBottom()
		if !wasCanceled && !wasFailed && m.pendingSubmissionCount() > 0 {
			return m, m.submitText(m.consumePendingSubmission(), false)
		}
		return m, nil

	case agentDoneMsg:
		if msg.RunID != m.activeAgentRunID {
			return m, nil
		}
		if m.agent != nil {
			m.projMemFiles = m.agent.ProjectMemoryFiles()
		}
		m.loading = false
		m.spinner.Stop()
		m.closeToolActivityGroup()
		m.flushGroupedActivitiesToOutput()
		m.cancelFunc = nil
		wasCanceled := m.runCanceled
		wasFailed := m.runFailed
		m.runCanceled = false
		m.runFailed = false
		m.statusActivity = ""
		m.statusToolName = ""
		m.statusToolArg = ""
		m.statusToolCount = 0
		if m.streamBuffer != nil && m.streamBuffer.Len() > 0 {
			m.renderStreamBuffer(true)
			m.streamBuffer = nil
		}
		m.output.WriteString("\n")
		m.syncConversationViewport()
		m.viewport.GotoBottom()
		if !wasCanceled && !wasFailed && m.pendingSubmissionCount() > 0 {
			return m, m.submitText(m.consumePendingSubmission(), false)
		}
		return m, nil

	case shellCommandDoneMsg:
		if msg.RunID != m.activeShellRunID {
			return m, nil
		}
		hadShellOutput := m.shellBuffer != nil && m.shellBuffer.Len() > 0
		m.loading = false
		m.spinner.Stop()
		m.cancelFunc = nil
		wasCanceled := m.runCanceled
		wasFailed := m.runFailed
		m.runCanceled = false
		m.runFailed = false
		m.statusActivity = ""
		m.statusToolName = ""
		m.statusToolArg = ""
		m.statusToolCount = 0
		if msg.Status == toolpkg.CommandJobFailed || msg.Status == toolpkg.CommandJobTimedOut {
			m.runFailed = true
			if m.pendingSubmissionCount() > 0 {
				m.restorePendingInput()
			}
			if text := strings.TrimSpace(msg.ErrText); text != "" {
				m.ensureOutputEndsWithNewline()
				m.output.WriteString(m.styles.error.Render(text))
				m.output.WriteString("\n")
			}
		}
		if !wasCanceled && (hadShellOutput || strings.TrimSpace(msg.ErrText) != "") {
			m.ensureOutputHasBlankLine()
		}
		if msg.Status == toolpkg.CommandJobCompleted && m.pendingSubmissionCount() > 0 && !wasCanceled && !wasFailed {
			return m, m.submitShellCommand(m.consumePendingSubmission(), false)
		}
		m.syncConversationViewport()
		m.viewport.GotoBottom()
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
		if m.pendingSubmissionCount() > 0 {
			m.restorePendingInput()
		}
		m.output.WriteString(m.styles.error.Render(formatUserFacingError(m.currentLanguage(), msg.err) + "\n\n"))
		m.syncConversationViewport()
		m.viewport.GotoBottom()
		return m, nil

	case agentErrMsg:
		if msg.RunID != m.activeAgentRunID {
			return m, nil
		}
		if errors.Is(msg.Err, context.Canceled) {
			return m, nil
		}
		m.runFailed = true
		m.loading = false
		m.spinner.Stop()
		m.closeToolActivityGroup()
		m.flushGroupedActivitiesToOutput()
		m.cancelFunc = nil
		if m.pendingSubmissionCount() > 0 {
			m.restorePendingInput()
		}
		m.output.WriteString(m.styles.error.Render(formatUserFacingError(m.currentLanguage(), msg.Err) + "\n\n"))
		m.syncConversationViewport()
		m.viewport.GotoBottom()
		return m, nil

	case harnessRunResultMsg:
		if path := strings.TrimSpace(m.harnessRunLogPath); path != "" {
			chunk, nextOffset := readHarnessRunLogChunk(path, m.harnessRunLogOffset)
			m.harnessRunLogOffset = nextOffset
			m.appendHarnessLogChunk(chunk)
		} else if msg.Summary != nil && msg.Summary.Task != nil {
			path := strings.TrimSpace(msg.Summary.Task.LogPath)
			chunk, nextOffset := readHarnessRunLogChunk(path, m.harnessRunLogOffset)
			m.harnessRunLogPath = path
			m.harnessRunLogOffset = nextOffset
			m.appendHarnessLogChunk(chunk)
		}
		m.flushHarnessLogRemainder()
		m.loading = false
		m.spinner.Stop()
		m.closeToolActivityGroup()
		m.flushGroupedActivitiesToOutput()
		m.cancelFunc = nil
		wasCanceled := m.runCanceled
		wasFailed := m.runFailed
		m.runCanceled = false
		m.runFailed = false
		m.statusActivity = ""
		m.statusToolName = ""
		m.statusToolArg = ""
		m.statusToolCount = 0
		m.harnessRunProject = nil
		m.harnessRunGoal = ""
		m.harnessRunTaskID = ""
		m.harnessRunLastDetail = ""
		m.harnessRunRemainder = ""
		m.harnessRunLiveTail = ""
		streamedHarnessOutput := m.harnessRunLogOffset > 0 || strings.TrimSpace(m.harnessRunLastDetail) != ""
		m.harnessRunLogPath = ""
		m.harnessRunLogOffset = 0
		if errors.Is(msg.Err, context.Canceled) {
			return m, nil
		}
		if msg.Err != nil {
			m.runFailed = true
			if m.pendingSubmissionCount() > 0 {
				m.restorePendingInput()
			}
			m.output.WriteString(m.styles.error.Render(msg.Err.Error()))
			m.output.WriteString("\n")
			m.syncConversationViewport()
			m.viewport.GotoBottom()
			return m, nil
		}
		rendered := harness.FormatRunSummary(msg.Summary)
		if streamedHarnessOutput {
			rendered = trimHarnessRunOutputSection(rendered)
		}
		m.renderStreamBuffer(true)
		m.ensureOutputHasBlankLine()
		if msg.Summary != nil && msg.Summary.Task != nil && msg.Summary.Task.Status == harness.TaskFailed {
			m.output.WriteString(m.styles.error.Render(rendered))
		} else {
			m.output.WriteString(m.styles.assistant.Render(rendered))
		}
		m.output.WriteString("\n")
		m.syncConversationViewport()
		m.viewport.GotoBottom()
		if !wasCanceled && !wasFailed && m.pendingSubmissionCount() > 0 {
			return m, m.submitText(m.consumePendingSubmission(), false)
		}
		return m, nil

	case harnessContextSuggestionsMsg:
		state := m.harnessContextPrompt
		if state == nil || state.mode != harnessContextPromptInit {
			return m, nil
		}
		state.step = harnessContextPromptStepSelect
		state.suggestions = harness.NormalizeContexts(msg.Contexts)
		state.selected = map[int]bool{}
		state.cursor = 0
		state.input.Placeholder = "Optional custom contexts: payments, checkout=apps/checkout"
		state.input.SetValue("")
		state.inputFocus = len(state.suggestions) == 0
		if state.inputFocus {
			state.input.Focus()
		} else {
			state.input.Blur()
		}
		if msg.Err != nil {
			state.message = msg.Err.Error()
		} else if len(state.suggestions) == 0 {
			state.message = "No suggestions found. Add custom contexts below."
		} else {
			state.message = ""
		}
		return m, nil

	case harnessInitResultMsg:
		state := m.harnessContextPrompt
		if msg.Err != nil {
			if state != nil {
				state.message = msg.Err.Error()
				if state.existingProject {
					state.step = harnessContextPromptStepUpgrade
				} else {
					state.step = harnessContextPromptStepSelect
				}
			}
			return m, nil
		}
		m.closeHarnessContextPrompt("")
		m.refreshHarnessPanel()
		if msg.Result != nil {
			commandText := "/harness init"
			if state != nil && strings.TrimSpace(state.commandText) != "" {
				commandText = strings.TrimSpace(state.commandText)
			}
			m.output.WriteString(m.renderConversationUserEntry("❯ ", commandText))
			m.output.WriteString("\n")
			m.appendUserMessage(commandText)
			m.output.WriteString(m.styles.assistant.Render(formatHarnessInitResult(msg.Result)))
			m.output.WriteString("\n")
			m.syncConversationViewport()
			m.viewport.GotoBottom()
			if panel := m.harnessPanel; panel != nil {
				panel.message = fmt.Sprintf("Initialized harness in %s", msg.Result.Project.RootDir)
			}
		}
		return m, nil

	case harnessRunProgressMsg:
		if !m.loading || m.harnessRunProject == nil {
			return m, nil
		}
		if msg.TaskID != "" {
			m.harnessRunTaskID = msg.TaskID
		}
		if msg.LogPath != "" {
			m.harnessRunLogPath = msg.LogPath
		}
		if msg.LogChunk != "" {
			m.appendHarnessLogChunk(msg.LogChunk)
		}
		if msg.LogOffset > 0 {
			m.harnessRunLogOffset = msg.LogOffset
		}
		if detail := strings.TrimSpace(msg.Detail); detail != "" && detail != m.harnessRunLastDetail {
			m.harnessRunLastDetail = detail
			if !harnessLogChunkContainsDetail(m.currentLanguage(), m.harnessRunProject, msg.LogChunk, detail) {
				m.appendHarnessProgressDetail(detail)
			}
		}
		if strings.TrimSpace(msg.Activity) != "" {
			m.statusActivity = msg.Activity
		}
		return m, m.pollHarnessRunProgress()

	case harnessPanelAutoRefreshMsg:
		if !m.shouldAutoRefreshHarnessTask() {
			return m, nil
		}
		m.refreshHarnessPanel()
		if !m.shouldAutoRefreshHarnessTask() {
			return m, nil
		}
		return m, m.pollHarnessPanelAutoRefresh()

	case startupReadyMsg:
		m.startupBannerVisible = false
		return m, nil

	case projectMemoryLoadedMsg:
		m.projectMemoryLoading = false
		if msg.Err != nil {
			debug.Log("tui", "project memory load failed: %v", msg.Err)
			if m.pendingSubmissionCount() > 0 && !m.loading {
				return m, m.submitText(m.consumePendingSubmission(), false)
			}
			return m, nil
		}
		m.projMemFiles = append([]string(nil), msg.Files...)
		if m.agent != nil && strings.TrimSpace(msg.Content) != "" {
			m.agent.SetProjectMemoryFiles(msg.Files)
			m.agent.AddMessage(provider.Message{
				Role:    "system",
				Content: []provider.ContentBlock{{Type: "text", Text: "## Project Memory\n" + msg.Content}},
			})
		}
		if m.pendingSubmissionCount() > 0 && !m.loading {
			if m.shellMode {
				return m, m.submitShellCommand(m.consumePendingSubmission(), false)
			}
			return m, m.submitText(m.consumePendingSubmission(), false)
		}
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

	case AskUserMsg:
		if m.pendingQuestionnaire != nil {
			if msg.Response != nil {
				go func() {
					msg.Response <- toolpkg.AskUserResponse{
						Status:        toolpkg.AskUserStatusCancelled,
						Title:         msg.Request.Title,
						QuestionCount: len(msg.Request.Questions),
					}
				}()
			}
			return m, nil
		}
		m.pendingQuestionnaire = newQuestionnaireState(msg.Request, msg.Response, m.currentLanguage())
		m.syncQuestionnaireInputWidth()
		return m, nil

	case HarnessCheckpointConfirmMsg:
		if m.mode == permission.AutopilotMode {
			m.pendingHarnessCheckpointConfirm = &msg
			return m, m.handleHarnessCheckpointConfirm(true)
		}
		m.pendingHarnessCheckpointConfirm = &msg
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

	case updateCheckResultMsg:
		m.applyUpdateCheckResult(msg)
		return m, nil

	case updateCheckTickMsg:
		return m, tea.Batch(m.checkForUpdateCmd(), m.scheduleUpdateCheckCmd())

	case updatePrepareResultMsg:
		return m.handlePreparedUpdate(msg)

	case toolStatusMsg:
		if m.runCanceled || !m.loading {
			return m, nil
		}
		ts := ToolStatusMsg(msg)
		m.updateActiveMCPTools(ts)
		if ts.Running {
			if !isSubAgentLifecycleTool(ts.ToolName) {
				m.statusToolCount++
			}
			m.startToolActivity(ts)
			if m.streamBuffer != nil && m.streamBuffer.Len() > 0 {
				m.renderStreamBuffer(true)
				m.streamStartPos = m.output.Len()
			}
			startCmd := m.spinner.Start(firstNonEmpty(ts.Activity, formatToolInline(toolDisplayName(ts), toolDetail(ts))))
			spinnerCmd = combineCmds(spinnerCmd, startCmd)
		} else {
			m.finishToolActivity(ts)
			ts.Elapsed = m.spinner.Elapsed()
			m.spinner.Stop()
			spinnerCmd = combineCmds(spinnerCmd, m.ensureLoadingSpinner(m.statusActivity))
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

	case agentToolStatusMsg:
		if msg.RunID != m.activeAgentRunID || m.runCanceled || !m.loading {
			return m, nil
		}
		ts := msg.ToolStatusMsg
		m.updateActiveMCPTools(ts)
		if ts.Running {
			if !isSubAgentLifecycleTool(ts.ToolName) {
				m.statusToolCount++
			}
			m.startToolActivity(ts)
			if m.streamBuffer != nil && m.streamBuffer.Len() > 0 {
				m.renderStreamBuffer(true)
				m.streamStartPos = m.output.Len()
			}
			startCmd := m.spinner.Start(firstNonEmpty(ts.Activity, formatToolInline(toolDisplayName(ts), toolDetail(ts))))
			spinnerCmd = combineCmds(spinnerCmd, startCmd)
		} else {
			m.finishToolActivity(ts)
			ts.Elapsed = m.spinner.Elapsed()
			m.spinner.Stop()
			spinnerCmd = combineCmds(spinnerCmd, m.ensureLoadingSpinner(m.statusActivity))
			m.streamPrefixWritten = false
			m.streamStartPos = m.output.Len()
		}
		m.syncConversationViewport()
		if m.viewport.AutoFollow() {
			m.viewport.GotoBottom()
		}
		return m, spinnerCmd

	case mcpServersMsg:
		m.mcpServers = toMCPInfos(msg.Servers)
		m.refreshCommands()
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

	case providerModelsRefreshResultMsg:
		if m.providerPanel != nil && m.providerPanel.refreshVendor == msg.vendor {
			m.providerPanel.refreshing = false
			m.providerPanel.refreshVendor = ""
			currentEndpoint := m.providerPanel.selectedEndpoint()
			currentModel := m.providerPanel.selectedModel()
			m.providerPanel.selectEndpoint(currentEndpoint, currentModel, m.configView())
			switch {
			case msg.saveErr != nil:
				m.providerPanel.message = m.t("panel.provider.refresh.save_failed", msg.saveErr.Error())
			case msg.updated > 0 && msg.discoverErr != nil:
				m.providerPanel.message = m.t("panel.provider.refresh.partial", msg.updated, msg.discovered, msg.discoverErr)
			case msg.updated > 0:
				m.providerPanel.message = m.t("panel.provider.refresh.success", msg.updated, msg.discovered)
			case msg.discoverErr != nil:
				m.providerPanel.message = m.t("panel.provider.refresh.failed", msg.discoverErr.Error())
			default:
				m.providerPanel.message = m.t("panel.provider.refresh.none")
			}
		}
		return m, nil

	case providerAuthStartMsg:
		if m.providerPanel != nil && msg.vendor == auth.ProviderGitHubCopilot {
			if msg.err != nil {
				m.providerPanel.authBusy = false
				m.providerPanel.message = msg.err.Error()
				return m, nil
			}
			if msg.flow != nil {
				m.providerPanel.enterpriseURL = msg.flow.EnterpriseURL
				notes := []string{m.t("panel.provider.login.instructions", msg.flow.VerificationURI, msg.flow.UserCode)}
				switch {
				case msg.copyErr == nil:
					notes = append(notes, m.t("panel.provider.login.copied"))
				default:
					notes = append(notes, m.t("panel.provider.login.copy_failed", msg.copyErr.Error()))
				}
				switch {
				case msg.openErr == nil:
					notes = append(notes, m.t("panel.provider.login.browser_opened"))
				default:
					notes = append(notes, m.t("panel.provider.login.browser_failed", msg.openErr.Error()))
				}
				m.providerPanel.message = strings.Join(notes, "\n")
				return m, m.pollCopilotLogin(msg.flow)
			}
		}
		return m, nil

	case providerAuthResultMsg:
		if m.providerPanel != nil && msg.vendor == auth.ProviderGitHubCopilot {
			m.providerPanel.authBusy = false
			if msg.err != nil {
				m.providerPanel.message = m.t("panel.provider.login.failed", msg.err.Error())
				return m, nil
			}
			if msg.info != nil {
				m.providerPanel.enterpriseURL = msg.info.EnterpriseURL
			}
			m.providerPanel.message = m.t("panel.provider.login.success")
			return m, m.refreshProviderModelsForVendor(auth.ProviderGitHubCopilot)
		}
		return m, nil

	case modelPanelRefreshResultMsg:
		if m.modelPanel != nil {
			m.modelPanel.refreshing = false
			m.modelPanel.remote = msg.remote
			m.modelPanel.models = uniqueStrings(msg.models)
			if len(m.modelPanel.models) == 0 && m.config != nil && strings.TrimSpace(m.config.Model) != "" {
				m.modelPanel.models = []string{m.config.Model}
			}
			if current := m.config.Model; strings.TrimSpace(current) != "" {
				m.modelPanel.selected = indexOf(m.modelPanel.models, current)
			}
			if m.modelPanel.selected < 0 {
				m.modelPanel.selected = 0
			}
			switch {
			case msg.saveErr != nil:
				m.modelPanel.message = m.t("panel.model.refresh.save_failed", msg.saveErr.Error())
			case msg.discoverErr != nil:
				m.modelPanel.message = m.t("panel.model.refresh.builtin_reason", msg.discoverErr.Error())
			case msg.remote:
				m.modelPanel.message = m.t("panel.model.refresh.remote_loaded", len(m.modelPanel.models))
			default:
				m.modelPanel.message = m.t("panel.model.refresh.builtin_loaded")
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
		if m.runCanceled || !m.loading {
			return m, nil
		}
		m.statusActivity = msg.Activity
		m.statusToolName = msg.ToolName
		m.statusToolArg = msg.ToolArg
		if msg.ToolCount > 0 {
			m.statusToolCount = msg.ToolCount
		}
		return m, combineCmds(spinnerCmd, m.ensureLoadingSpinner(m.statusActivity))

	case agentStatusMsg:
		if msg.RunID != m.activeAgentRunID || m.runCanceled || !m.loading {
			return m, nil
		}
		m.statusActivity = msg.Activity
		m.statusToolName = msg.ToolName
		m.statusToolArg = msg.ToolArg
		if msg.ToolCount > 0 {
			m.statusToolCount = msg.ToolCount
		}
		return m, combineCmds(spinnerCmd, m.ensureLoadingSpinner(m.statusActivity))

	}

	var cmd tea.Cmd
	if shouldIgnoreInputUpdate(msg, m.lastResizeAt) {
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
	if bareMouseFragmentPattern.MatchString(raw) {
		return true
	}
	if looksLikeExactTerminalNoise(raw) {
		return true
	}
	if !terminalResponseSuppressionActive(lastResizeAt) {
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
	if terminalResponseSuppressionActive(m.lastResizeAt) {
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

func terminalResponseSuppressionActive(lastResizeAt time.Time) bool {
	if !lastResizeAt.IsZero() && time.Since(lastResizeAt) <= 1500*time.Millisecond {
		return true
	}
	return false
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

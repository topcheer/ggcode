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

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/auth"
	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/harness"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/image"
	"github.com/topcheer/ggcode/internal/mcp"
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

// startupInputGateWindow suppresses terminal noise (CSI/OSC responses) that
// arrive shortly after program start. During this window, only single
// printable characters with no modifiers are passed to textinput; everything
// else (multi-char text, modified keys) is dropped as likely terminal garbage.
const startupInputGateWindow = 500 * time.Millisecond

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
	imManager                       *im.Manager
	imEmitter                       *im.IMEmitter
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
	qqPanel                         *qqPanelState
	tgPanel                         *tgPanelState
	pcPanel                         *pcPanelState
	discordPanel                    *discordPanelState
	feishuPanel                     *feishuPanelState
	slackPanel                      *slackPanelState
	dingtalkPanel                   *dingtalkPanelState
	mcpPanel                        *mcpPanelState
	pendingDeviceCodes              []deviceCodeInfo
	skillsPanel                     *skillsPanelState
	inspectorPanel                  *inspectorPanelState
	previewPanel                    *previewPanelState
	fileBrowser                     *fileBrowserState
	harnessPanel                    *harnessPanelState
	harnessContextPrompt            *harnessContextPromptState
	impersonatePanel                *impersonatePanelState

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
	inputDrainUntil       time.Time // suppress all KeyPressMsg until this time (after setProgramMsg)
	inputReady            bool      // true after setProgramMsg + drain completes; before that, all KeyPress is discarded
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
	systemPromptRebuilder func() string // rebuilds and returns the full system prompt
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
	Disabled      bool
}

type mcpManager interface {
	Retry(name string) bool
	Install(ctx context.Context, server config.MCPServerConfig) error
	Uninstall(name string) bool
	Disconnect(name string) bool
	Reconnect(name string) bool
	PendingOAuth() *plugin.MCPOAuthRequiredError
	ClearPendingOAuth()
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

var ansiChunkPattern = regexp.MustCompile(`\[[0-9;?<>=]*[A-Za-z~]|\[<\d+(?:;\d+){0,2}[A-Za-zmM]`)

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

type agentRoundProgressMsg struct {
	RunID int
	Text  string
}

type agentRoundSummaryMsg struct {
	RunID         int
	Text          string
	ToolCalls     int
	ToolSuccesses int
	ToolFailures  int
}

type agentAskUserMsg struct {
	RunID int
	Text  string
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

// inputDrainEndMsg signals the end of the startup input drain window.
type inputDrainEndMsg struct{}

type mcpServersMsg struct {
	Servers []plugin.MCPServerInfo
}

type deviceCodeInfo struct {
	serverName string
	userCode   string
	verifyURL  string
}

type mcpOAuthStartMsg struct {
	serverName     string
	authorizeURL   string
	handler        *mcp.OAuthHandler
	openErr        error
	err            error
	deviceUserCode string // set when using device flow
}

type mcpOAuthResultMsg struct {
	serverName string
	err        error
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
	ti.SetWidth(74)

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
		startedAt:            time.Time{}, // set on first WindowSizeMsg
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
		func() tea.Msg { return textinput.Blink() },
		func() tea.Msg { return tea.RequestWindowSize() },
	}
	if m.updateSvc != nil {
		cmds = append(cmds, m.checkForUpdateCmd())
		cmds = append(cmds, m.scheduleUpdateCheckCmd())
	}
	return tea.Batch(cmds...)
}

func (m *Model) SetProgram(p *tea.Program) {
	m.program = p
	m.refreshIMRuntimeHooks()
}

func (m *Model) SetSession(ses *session.Session, store session.Store) {
	m.session = ses
	m.sessionStore = store
	m.bindIMSession()
}

func (m *Model) Session() *session.Session {
	return m.session
}

func (m *Model) SetIMManager(mgr *im.Manager) {
	m.imManager = mgr
	if mgr != nil {
		lang := "en"
		if m.language == LangZhCN {
			lang = "zh-CN"
		}
		m.imEmitter = im.NewIMEmitter(mgr, lang, m.autoCompleteWorkDir)
	}
	m.refreshIMRuntimeHooks()
	m.bindIMSession()
}

func (m *Model) refreshIMRuntimeHooks() {
	if m.imManager == nil {
		return
	}
	m.imManager.SetOnUpdate(func(im.StatusSnapshot) {
		if m.program != nil {
			m.program.Send(imRuntimeUpdatedMsg{})
		}
	})
}

func (m *Model) bindIMSession() {
	if m.imManager == nil {
		return
	}
	if m.session == nil {
		m.imManager.UnbindSession()
		return
	}
	m.imManager.BindSession(im.SessionBinding{
		SessionID: m.session.ID,
		Workspace: m.session.Workspace,
	})
}

func (m Model) pendingPairingChallenge() *im.PairingChallenge {
	if m.imManager == nil {
		return nil
	}
	return m.imManager.Snapshot().PendingPairing
}

func (m *Model) rejectPendingPairing() tea.Cmd {
	if m.imManager == nil {
		return nil
	}
	challenge, blacklisted, err := m.imManager.RejectPendingPairing()
	if err != nil {
		return nil
	}
	mgr := m.imManager
	reply := "当前配对请求已被拒绝，如需继续请重新发起。"
	if blacklisted {
		reply = "该 QQ 渠道因多次被拒绝，已被加入黑名单。"
	}
	binding := challenge.ReplyBinding()
	return func() tea.Msg {
		_ = mgr.SendDirect(context.Background(), binding, im.OutboundEvent{
			Kind: im.OutboundEventText,
			Text: reply,
		})
		return imRuntimeUpdatedMsg{}
	}
}

func (m *Model) closeActivePanel() bool {
	switch {
	case m.modelPanel != nil:
		m.closeModelPanel()
	case m.providerPanel != nil:
		m.closeProviderPanel()
	case m.tgPanel != nil:
		m.closeTGPanel()
	case m.qqPanel != nil:
		m.closeQQPanel()
	case m.pcPanel != nil:
		m.closePCPanel()
	case m.discordPanel != nil:
		m.closeDiscordPanel()
	case m.feishuPanel != nil:
		m.closeFeishuPanel()
	case m.slackPanel != nil:
		m.closeSlackPanel()
	case m.dingtalkPanel != nil:
		m.closeDingtalkPanel()
	case m.mcpPanel != nil:
		m.closeMCPPanel()
	case m.skillsPanel != nil:
		m.closeSkillsPanel()
	case m.inspectorPanel != nil:
		m.closeInspectorPanel()
	case m.harnessContextPrompt != nil:
		m.harnessContextPrompt = nil
	case m.harnessPanel != nil:
		m.closeHarnessPanel()
	case m.impersonatePanel != nil:
		m.closeImpersonatePanel()
	case len(m.langOptions) > 0:
		m.langOptions = nil
	default:
		return false
	}
	m.resetExitConfirm()
	return true
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

// SetSystemPromptRebuilder sets a callback that rebuilds the full system prompt.
func (m *Model) SetSystemPromptRebuilder(fn func() string) {
	m.systemPromptRebuilder = fn
}

// rebuildSystemPrompt rebuilds the system prompt and updates the agent context.
func (m *Model) rebuildSystemPrompt() {
	if m.systemPromptRebuilder == nil || m.agent == nil {
		return
	}
	newPrompt := m.systemPromptRebuilder()
	m.agent.UpdateSystemPrompt(newPrompt)
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
		// Reset the startup clock when Bubble Tea sends the first WindowSizeMsg.
		// This ensures the startup input gate window is measured from the moment
		// the TUI event loop actually starts, not from model creation time (which
		// can be hundreds of milliseconds earlier due to config loading, IM setup, etc.).
		if m.startedAt.IsZero() || time.Since(m.startedAt) > startupInputGateWindow {
			m.startedAt = time.Now()
		}
		return m, nil

	case imRuntimeUpdatedMsg:
		return m, nil

	case tea.MouseWheelMsg:
		if startupInputSuppressionActive(m.startedAt) {
			return m, nil
		}
		// Route mouse wheel to the active panel's viewport if one is open.
		// MouseWheelMsg implements the MouseMsg interface, so it must appear
		// BEFORE case tea.MouseMsg in this type switch to be matched here.
		if m.fileBrowser != nil && m.fileBrowser.preview != nil {
			if msg.Button == tea.MouseWheelUp {
				m.fileBrowser.preview.viewport.ScrollUp(3)
			} else {
				m.fileBrowser.preview.viewport.ScrollDown(3)
			}
			return m, nil
		}
		if m.previewPanel != nil {
			if msg.Button == tea.MouseWheelUp {
				m.previewPanel.viewport.ScrollUp(3)
			} else {
				m.previewPanel.viewport.ScrollDown(3)
			}
			return m, nil
		}
		// Default: scroll the main conversation viewport.
		m.syncConversationViewport()
		if msg.Button == tea.MouseWheelUp {
			m.viewport.ScrollUp(3)
		} else {
			m.viewport.ScrollDown(3)
		}
		return m, nil

	case tea.MouseMsg:
		if startupInputSuppressionActive(m.startedAt) {
			debug.Log("tui", "startup gate dropping mouse event age=%s", time.Since(m.startedAt))
			return m, nil
		}
		if m.fileBrowser != nil {
			return m.handleFileBrowserMouse(msg)
		}
		if m.previewPanel != nil {
			return m.handlePreviewMouse(msg)
		}
		// Option/Alt+mouse: release mouse to terminal for native text selection
		return m, nil

	case tea.KeyPressMsg:
		// During startup input drain, suppress all keyboard input.
		// This prevents terminal responses (OSC 11, CPR, Kitty mode report)
		// from appearing as garbage in the text input field.
		if !m.inputDrainUntil.IsZero() && time.Now().Before(m.inputDrainUntil) {
			debug.Log("tui", "KEYPRESS dropped (input drain) key=%q text=%q", msg.String(), msg.Text)
			return m, nil
		}
		if shouldIgnoreTerminalProbeKey(msg) {
			debug.Log("tui", "ignoring terminal probe key=%q text=%q mod=%v", msg.String(), msg.Text, msg.Mod)
			return m, nil
		}
		if m.startupBannerVisible && !shouldIgnoreInputUpdate(msg, m.startedAt, m.lastResizeAt) {
			m.startupBannerVisible = false
		}
		if msg.String() != "ctrl+c" {
			m.resetExitConfirm()
		}
		debug.Log("tui", "KEYPRESS str=%q text=%q mod=%v code=%v input_before=%q", msg.String(), msg.Text, msg.Mod, msg.Code, truncateStr(m.input.Value(), 80))
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

		if msg.String() == "ctrl+c" && !m.loading && len(m.langOptions) == 0 && m.closeActivePanel() {
			return m, nil
		}

		// Handle approval mode (selection list)
		if m.modelPanel != nil {
			return m.handleModelPanelKey(msg)
		}

		if m.providerPanel != nil {
			return m.handleProviderPanelKey(msg)
		}

		if m.qqPanel != nil {
			return m.handleQQPanelKey(msg)
		}

		if m.tgPanel != nil {
			return m.handleTGPanelKey(msg)
		}

		if m.pcPanel != nil {
			return m.handlePCPanelKey(msg)
		}

		if m.discordPanel != nil {
			return m.handleDiscordPanelKey(msg)
		}

		if m.feishuPanel != nil {
			return m.handleFeishuPanelKey(msg)
		}

		if m.slackPanel != nil {
			return m.handleSlackPanelKey(msg)
		}

		if m.dingtalkPanel != nil {
			return m.handleDingtalkPanelKey(msg)
		}

		if m.mcpPanel != nil {
			return m.handleMCPPanelKey(msg)
		}

		if m.impersonatePanel != nil {
			return m.handleImpersonatePanelKey(msg)
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

		if m.pendingPairingChallenge() != nil {
			switch msg.String() {
			case "esc":
				return m, m.rejectPendingPairing()
			case "ctrl+c":
				if m.loading {
					m.resetExitConfirm()
					m.cancelActiveRun()
					return m, nil
				}
				if m.exitConfirmPending {
					m.quitting = true
					return m, tea.Quit
				}
				m.promptExitConfirm()
				return m, nil
			default:
				return m, nil
			}
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
			case "ctrl+c":
				m.promptExitConfirm()
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
				m.emitIMLocalUserText("$ " + text)
				if m.loading || m.projectMemoryLoading {
					m.history = append(m.history, "$ "+text)
					m.historyIdx = len(m.history)
					m.queuePendingSubmission(text)
					return m, nil
				}
				return m, m.submitShellCommand(text, true)
			}
			m.emitIMLocalUserText(text)
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

	case remoteInboundMsg:
		prompt := buildRemoteInboundPrompt(msg.Message)
		if m.pendingQuestionnaire != nil {
			if strings.TrimSpace(prompt) == "" {
				if msg.Response != nil {
					msg.Response <- fmt.Errorf("empty remote message")
				}
				return m, nil
			}
			completed, err := m.pendingQuestionnaire.applyRemoteAnswer(prompt, m.currentLanguage())
			if msg.Response != nil {
				msg.Response <- nil
			}
			if err != nil {
				switch m.currentLanguage() {
				case LangZhCN:
					m.emitIMText("没有识别出有效的问卷答案，请直接回复选项编号或文本答案。")
				default:
					m.emitIMText("I couldn't parse that questionnaire answer. Reply with choice numbers or plain text.")
				}
				return m, nil
			}
			if completed {
				return m, m.handleQuestionnaireResult(toolpkg.AskUserStatusSubmitted)
			}
			if nextIdx := m.pendingQuestionnaire.firstUnansweredQuestionIndex(); nextIdx >= 0 {
				m.emitIMAskUser(m.formatIMAskUserQuestion(m.pendingQuestionnaire.request.Title, m.pendingQuestionnaire.request.Questions[nextIdx]))
			}
			return m, nil
		}
		if response, handled := m.ExecuteRemoteSlashCommand(prompt); handled {
			if strings.TrimSpace(response) != "" {
				m.emitIMText(response)
			}
			if msg.Response != nil {
				msg.Response <- nil
			}
			return m, nil
		}
		if strings.TrimSpace(prompt) == "" {
			if msg.Response != nil {
				msg.Response <- fmt.Errorf("empty remote message")
			}
			return m, nil
		}
		if msg.Response != nil {
			msg.Response <- nil
		}
		if m.loading {
			m.queuePendingSubmission(prompt)
			return m, nil
		}
		return m, m.submitText(prompt, false)

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
		finalIMText := m.pendingIMStreamText()
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
		if finalIMText != "" {
			m.emitIMText(finalIMText)
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
		m.emitIMText(formatUserFacingError(m.currentLanguage(), msg.Err))
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
		if m.mcpManager != nil {
			if pending := m.mcpManager.PendingOAuth(); pending != nil {
				m.mcpManager.ClearPendingOAuth()
				return m, m.startMCPOAuth(pending)
			}
		}
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

	case qqBindResultMsg:
		if m.qqPanel != nil {
			if msg.err != nil {
				m.qqPanel.shareAdapter = ""
				m.qqPanel.shareLink = ""
				m.qqPanel.shareQRCode = ""
				m.qqPanel.message = msg.err.Error()
			} else {
				m.qqPanel.shareAdapter = msg.shareAdapter
				m.qqPanel.shareLink = msg.shareLink
				m.qqPanel.shareQRCode = msg.shareQRCode
				m.qqPanel.message = msg.message
			}
		}
		return m, nil

	case feishuBindResultMsg:
		if m.feishuPanel != nil {
			if msg.err != nil {
				m.feishuPanel.message = msg.err.Error()
			} else {
				m.feishuPanel.message = msg.message
			}
		}
		return m, nil

	case slackBindResultMsg:
		if m.slackPanel != nil {
			if msg.err != nil {
				m.slackPanel.message = msg.err.Error()
			} else {
				m.slackPanel.message = msg.message
			}
		}
		return m, nil

	case discordBindResultMsg:
		if m.discordPanel != nil {
			if msg.err != nil {
				m.discordPanel.message = msg.err.Error()
			} else {
				m.discordPanel.message = msg.message
			}
		}
		return m, nil

	case dingtalkBindResultMsg:
		if m.dingtalkPanel != nil {
			if msg.err != nil {
				m.dingtalkPanel.message = msg.err.Error()
			} else {
				m.dingtalkPanel.message = msg.message
			}
		}
		return m, nil

	case tgBindResultMsg:
		if m.tgPanel != nil {
			if msg.err != nil {
				m.tgPanel.message = msg.err.Error()
			} else {
				m.tgPanel.message = msg.message
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
		if m.providerPanel != nil && msg.vendor == auth.ProviderAnthropic {
			if msg.err != nil {
				m.providerPanel.authBusy = false
				m.providerPanel.message = m.t("panel.provider.login.claude_failed", msg.err.Error())
				return m, nil
			}
			if msg.claudeFlow != nil {
				notes := []string{m.t("panel.provider.login.claude_instructions")}
				switch {
				case msg.openErr == nil:
					notes = append(notes, m.t("panel.provider.login.browser_opened"))
				default:
					notes = append(notes, m.t("panel.provider.login.browser_failed", msg.openErr.Error()))
					notes = append(notes, m.t("panel.provider.login.claude_manual", msg.claudeFlow.ManualURL))
				}
				m.providerPanel.message = strings.Join(notes, "\n")
				return m, m.waitForClaudeAuthCode(msg.claudeFlow)
			}
		}
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
		if m.providerPanel != nil && msg.vendor == auth.ProviderAnthropic {
			m.providerPanel.authBusy = false
			if msg.err != nil {
				m.providerPanel.message = m.t("panel.provider.login.claude_failed", msg.err.Error())
				return m, nil
			}
			m.providerPanel.message = m.t("panel.provider.login.claude_success")
			return m, m.refreshProviderModelsForVendor(auth.ProviderAnthropic)
		}
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

	case mcpOAuthStartMsg:
		if msg.err != nil {
			if m.mcpPanel != nil {
				m.mcpPanel.message = fmt.Sprintf("MCP OAuth failed for %s: %v", msg.serverName, msg.err)
			}
			return m, nil
		}
		if msg.deviceUserCode != "" {
			// Device flow: store code info for banner display, poll in background
			m.addDeviceCode(msg.serverName, msg.deviceUserCode, msg.authorizeURL)
			if m.mcpPanel != nil {
				m.mcpPanel.message = fmt.Sprintf("Waiting for %s device authorization...", msg.serverName)
			}
			return m, m.waitForMCPOAuthDevice(msg.handler)
		}
		// Browser flow
		// Auto-open MCP panel so user can see the auth instructions
		if m.mcpPanel == nil {
			m.openMCPPanel()
		}
		notes := []string{fmt.Sprintf("Opening browser for MCP server %s authentication...", msg.serverName)}
		if msg.openErr != nil {
			notes = append(notes, fmt.Sprintf("Browser failed: %v", msg.openErr))
			notes = append(notes, fmt.Sprintf("Visit: %s", msg.authorizeURL))
		}
		m.mcpPanel.message = strings.Join(notes, "\n")
		return m, m.waitForMCPOAuthCallback(msg.handler)

	case mcpOAuthResultMsg:
		if msg.err != nil {
			m.removeDeviceCode(msg.serverName)
			if m.mcpPanel != nil {
				m.mcpPanel.message = fmt.Sprintf("MCP OAuth failed for %s: %v", msg.serverName, msg.err)
			}
			return m, nil
		}
		m.removeDeviceCode(msg.serverName)
		if m.mcpPanel != nil {
			m.mcpPanel.message = fmt.Sprintf("MCP server %s authenticated successfully", msg.serverName)
		}
		if m.mcpManager != nil {
			m.mcpManager.Retry(msg.serverName)
		}
		return m, nil

	case setProgramMsg:
		debug.Log("tui", "setProgramMsg received, program was nil=%v", m.program == nil)
		m.program = msg.Program
		// Set startedAt for startup gate if not already set.
		if m.startedAt.IsZero() {
			m.startedAt = time.Now()
		}
		// Clear any terminal response garbage that leaked into the input
		// field before we had a chance to set up the drain guard.
		// Only clear when the content looks like terminal response fragments
		// (contains ;, :, /, digits etc.) to avoid wiping legitimate input
		// set programmatically by callers (e.g. IM tests).
		if val := m.input.Value(); val != "" && looksLikeStartupGarbage(val) {
			debug.Log("tui", "clearing pre-drain input garbage: %q", truncateStr(val, 80))
			m.input.SetValue("")
		}
		// Start the input drain window. Terminal responses (OSC 11 color
		// query, CPR, Kitty mode report) arrive as individual KeyPressMsg
		// events that are indistinguishable from real typing. We suppress
		// all keyboard input until inputDrainEndMsg arrives (50ms from now).
		m.inputDrainUntil = time.Now().Add(50 * time.Millisecond)
		return m, tea.Tick(50*time.Millisecond, func(_ time.Time) tea.Msg {
			return inputDrainEndMsg{}
		})

	case inputDrainEndMsg:
		m.inputDrainUntil = time.Time{} // zero = drain ended
		m.inputReady = true
		debug.Log("tui", "input drain ended, input ready")
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

	case agentRoundProgressMsg:
		return m, nil

	case agentRoundSummaryMsg:
		if msg.RunID != m.activeAgentRunID {
			return m, nil
		}
		m.emitIMRoundSummary(msg.Text, msg.ToolCalls, msg.ToolSuccesses, msg.ToolFailures)
		return m, nil

	case agentAskUserMsg:
		if msg.RunID != m.activeAgentRunID || m.runCanceled || !m.loading {
			return m, nil
		}
		m.emitIMAskUser(msg.Text)
		return m, nil

	}

	// Skip spinnerMsg — it fires every tick and would flood the log.
	if _, isSpinner := msg.(spinnerMsg); !isSpinner {
		debug.Log("tui", "CATCHALL msg=%T value=%q", msg, fmt.Sprintf("%+v", msg))
	}
	keyMsg, isKeyPress := msg.(tea.KeyPressMsg)
	if !isKeyPress {
		// Only KeyPressMsg should reach the textinput. Other message types
		// (BackgroundColorMsg, ModeReportMsg, spinnerMsg, etc.) are not keyboard input.
		return m, spinnerCmd
	}
	var cmd tea.Cmd
	// During startup input drain, suppress all keyboard input.
	if !m.inputDrainUntil.IsZero() && time.Now().Before(m.inputDrainUntil) {
		debug.Log("tui", "CATCHALL dropped (input drain) key=%q text=%q", keyMsg.String(), keyMsg.Text)
		return m, spinnerCmd
	}
	// Before inputReady, discard all keyboard input (same reason as KeyPressMsg handler).
	if !m.inputReady {
		debug.Log("tui", "CATCHALL dropped (not ready) key=%q text=%q", keyMsg.String(), keyMsg.Text)
		return m, spinnerCmd
	}
	if shouldIgnoreInputUpdate(msg, m.startedAt, m.lastResizeAt) {
		debug.Log("tui", "CATCHALL ignored key text=%q", keyMsg.Text)
		return m, spinnerCmd
	}

	// During the startup window, terminal CSI/OSC responses arrive as
	// misparsed KeyPressMsg events. These are always multi-character or
	// carry modifiers. Real human typing is always a single printable
	// character with no modifiers (IME uses PasteMsg instead).
	// Filter aggressively during startup to prevent garbage in textinput.
	if startupInputSuppressionActive(m.startedAt) {
		if len(keyMsg.Text) != 1 || keyMsg.Mod != 0 || !unicode.IsPrint(rune(keyMsg.Text[0])) {
			debug.Log("tui", "CATCHALL startup gate dropped key=%q text=%q mod=%v", keyMsg.String(), keyMsg.Text, keyMsg.Mod)
			return m, spinnerCmd
		}
	}

	if len(keyMsg.Text) > 1 && looksLikeTerminalResponse(keyMsg.Text) {
		// Human keyboard input produces at most 1 character per KeyPressMsg
		// (IME compositions use PasteMsg instead). Multi-character Text
		// containing terminal response patterns is a misparse from
		// EscTimeout-truncated terminal responses.
		debug.Log("tui", "CATCHALL ignored terminal fragment text=%q", truncateStr(keyMsg.Text, 60))
		return m, spinnerCmd
	}
	oldValue := m.input.Value()
	m.input, cmd = m.input.Update(msg)
	newValue := m.input.Value()
	if oldValue != newValue {
		debug.Log("tui", "CATCHALL input changed old=%q new=%q", truncateStr(oldValue, 80), truncateStr(newValue, 80))
	}

	// Post-update terminal response cleanup: after textinput processes the
	// key, check if the accumulated value looks like a terminal response that
	// leaked through as individual single-character KeyPressMsg events.
	// Terminal responses (OSC 11, CSI CPR, DECRPM, etc.) arrive character by
	// character, each indistinguishable from normal typing. We detect them by
	// inspecting the accumulated value for distinctive patterns.
	if oldValue != newValue && looksLikeTerminalResponseInput(newValue) {
		debug.Log("tui", "CATCHALL terminal response detected in input, clearing: %q", truncateStr(newValue, 80))
		m.input.SetValue("")
	}

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

// looksLikeStartupGarbage checks whether the input value accumulated before
// setProgramMsg looks like terminal response garbage rather than legitimate
// user input. Terminal responses contain ASCII control characters ([, ], digits,
// and punctuation like ;, :, /) that don't appear in normal typing.
// Normal pre-set values like "ping" contain only alphabetic characters.
func looksLikeStartupGarbage(val string) bool {
	for _, r := range val {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == ' ' || r == '-' || r == '_':
			// Common in normal text, keep going
		default:
			// Contains characters not found in normal typing:
			// ; : / [ ] $ ? = etc. — terminal response artifacts
			return true
		}
	}
	return false
}

// looksLikeTerminalResponse checks whether text appears to be a fragment of a
// terminal response that leaked through due to EscTimeout truncation.
// These fragments always contain ASCII punctuation like ;, :, /, $ combined
// with digits, which never appears in normal human keyboard input (IME input
// uses PasteMsg instead of multi-char KeyPressMsg).
func looksLikeTerminalResponse(text string) bool {
	hasDigit := false
	hasPunct := false
	for _, r := range text {
		switch {
		case r >= '0' && r <= '9':
			hasDigit = true
		case r == ';' || r == ':' || r == '/' || r == '$' || r == '?' || r == '=':
			hasPunct = true
		case r == 'r' || r == 'g' || r == 'b' || r == 'y' || r == 'R':
			// Common in "rgb:", "R" (CPR), "y" (DECRPM)
		default:
			// Contains non-terminal-response characters (e.g. CJK text)
			return false
		}
	}
	return hasDigit && hasPunct
}

func shouldIgnoreInputUpdate(msg tea.Msg, startedAt, lastResizeAt time.Time) bool {
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok || len(keyMsg.Text) == 0 {
		return false
	}

	raw := keyMsg.Text
	// Human keyboard input never contains ESC or control characters.
	// Any KeyPressMsg carrying these is a misparse of a terminal response.
	if strings.ContainsRune(raw, '\x1b') {
		return true
	}
	for _, r := range raw {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func shouldIgnoreTerminalProbeKey(msg tea.KeyPressMsg) bool {
	// Fast path: normal single-character printable input is never a probe.
	if len(msg.Text) == 1 && msg.Mod == 0 {
		return false
	}

	raw := msg.Text

	// Human keyboard input never contains ESC or other control characters.
	// Terminal CSI/OSC responses often carry these when misparsed as key events.
	for _, r := range raw {
		if r == '\x1b' || unicode.IsControl(r) {
			return true
		}
	}

	// Terminal responses masquerading as Alt+key sequences:
	// - Alt+] with text like "11;rgb:0000/0000/0000" (OSC 11 color query response)
	// - Alt+\ with various CSI fragments
	// - Any Alt+key with long text (human Alt shortcuts are at most 2-3 chars)
	if msg.Mod.Contains(tea.ModAlt) {
		switch raw {
		case "]", "\\":
			return true
		default:
			// Any Alt-modified text longer than 2 chars is almost certainly
			// a misparsed terminal response (CSI/OSC fragments).
			if len(raw) > 2 {
				return true
			}
		}
	}

	// Catch CSI-like fragments even without Alt modifier.
	// Patterns: "1;1R", "?1u", "11;rgb:...", "<0;93;43m", etc.
	// These contain semicolons followed by letters or contain "rgb:".
	if strings.Contains(raw, ";") && (strings.ContainsAny(raw, "RrumM") || strings.Contains(raw, "rgb:")) {
		return true
	}

	// Mouse-like fragments without proper SGR encoding: "<0;93;43m"
	if strings.HasPrefix(raw, "<") && strings.Contains(raw, ";") {
		return true
	}

	return false
}

func (m *Model) sanitizeTerminalResponseInput() {
	value := m.input.Value()
	if value == "" {
		return
	}
	// Strip any ESC sequences that leaked into input value.
	cleaned := ansiChunkPattern.ReplaceAllString(value, "")
	// Strip remaining fragments containing control characters.
	var buf strings.Builder
	for _, r := range cleaned {
		if r == '\x1b' || unicode.IsControl(r) {
			continue
		}
		buf.WriteRune(r)
	}
	cleaned = buf.String()
	if cleaned == value {
		return
	}
	debug.Log("tui", "sanitize input changed value=%q cleaned=%q", truncateStr(value, 120), truncateStr(cleaned, 120))
	m.input.SetValue(cleaned)
	m.input.CursorEnd()
}

// terminalResponsePattern matches terminal response fragments that leak into
// the text input field as individual KeyPressMsg characters. These arrive from:
//   - OSC 11 background color query: ]11;rgb:XXXX/XXXX/XXXX
//   - CSI CPR (cursor position report): [1;1R
//   - CSI DECRPM (mode report): [?2026;2$y, [?1u
//   - CSI SGR mouse: [<0;93;43m
//   - Partial/truncated fragments: 11;rgb:..., ;1R, 35;1
//
// The pattern detects these by looking for distinctive subsequences that never
// appear in normal human typing:
//   - "rgb:" — always from OSC 11 color responses
//   - "]11;" or "11;rgb" — OSC 11 response or fragment
//   - "[<digits;" — SGR mouse encoding
//   - "[?digits" followed by letter/$ — DECRPM responses
//   - ";digitsR" — CPR response tail (e.g. ";1R", ";16R")
//   - "$y" — XTVERSION/DECRPM response tail
var terminalResponsePattern = regexp.MustCompile(
	`rgb:` +
		`|\]11;` +
		`|11;rgb` +
		`|\[<\d+;` +
		`|\[\?\d+[a-zA-Z\$]` +
		`|;\d+R` +
		`|\$\d+y` +
		`|\[\d+;\d+R` +
		`|\[\d+;\d+;\d+`)

// looksLikeTerminalResponseInput checks whether the textinput value appears to
// have accumulated terminal response garbage. Unlike the per-keystroke checks
// (shouldIgnoreTerminalProbeKey, shouldIgnoreInputUpdate) which inspect
// individual KeyPressMsg events, this function inspects the accumulated input
// value after it has been updated. This catches the case where terminal
// responses arrive as a rapid burst of individual single-character
// KeyPressMsg events that each look like legitimate typing.
//
// Returns true if the entire value looks like a terminal response (should be
// cleared), false otherwise.
func looksLikeTerminalResponseInput(val string) bool {
	if val == "" {
		return false
	}

	// Fast path: if the full terminalResponsePattern matches, it's definitely garbage.
	if terminalResponsePattern.MatchString(val) {
		return true
	}

	// Check for partial OSC/CSI patterns that build up character-by-character.
	// These are fragments that start with ] or [ followed by digits/semicolons,
	// which is how terminal responses begin before the distinctive tail arrives.
	//
	// We require the value to be ENTIRELY composed of terminal-response-like
	// characters (no letters except r/g/b/y/R/u, no CJK, no spaces between words)
	// to avoid false positives on normal input.
	if looksLikePartialTerminalSequence(val) {
		return true
	}

	return false
}

// looksLikePartialTerminalSequence checks if the entire string looks like the
// beginning of a terminal response sequence. Terminal responses consist of:
//   - ] or [ prefix characters
//   - digits and semicolons as separators
//   - lowercase letters r, g, b (from "rgb:"), y (from "$y"), u (from "?1u")
//   - uppercase R (from CPR responses), m/M (from mouse/SGR responses)
//   - / and : (from OSC 11 color specifiers like "rgb:0000/0000/0000")
//   - ? (from CSI ? sequences)
//   - $ (from DECRPM responses like "?2026;2$y")
//   - < (from SGR mouse encoding like "<0;93")
//
// Normal human typing in the input box would contain a mix of CJK characters,
// spaces between words, or diverse letters — not this narrow character set.
func looksLikePartialTerminalSequence(val string) bool {
	// Too short to be meaningful; avoid false positives on single characters.
	if len(val) < 3 {
		return false
	}

	hasStructuralPunct := false // ; : / ? $ < — terminal sequence punctuation
	allTerminalChars := true

	for _, r := range val {
		switch {
		case r >= '0' && r <= '9':
			// digits appear in both terminal responses and normal input
		case r == ']' || r == '[':
			hasStructuralPunct = true
		case r == ';' || r == ':' || r == '/' || r == '?' || r == '$' || r == '<':
			hasStructuralPunct = true
		case r == 'r' || r == 'g' || r == 'b' || r == 'y' || r == 'u':
			// Letters commonly appearing in terminal responses (rgb, y, u)
		case r == 'R' || r == 'm' || r == 'M':
			// Uppercase terminators (CPR "R", mouse "m/M")
		default:
			// Any other character (CJK, most Latin letters, spaces, etc.)
			// means this is likely normal human input.
			allTerminalChars = false
		}
	}

	return allTerminalChars && hasStructuralPunct
}

func startupInputSuppressionActive(startedAt time.Time) bool {
	if startedAt.IsZero() {
		return false
	}
	return time.Since(startedAt) <= startupInputGateWindow
}

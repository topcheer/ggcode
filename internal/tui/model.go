package tui

import (
	"bytes"
	"context"
	"os"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/chat"
	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/harness"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/image"
	"github.com/topcheer/ggcode/internal/knight"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/swarm"
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
	input                           textarea.Model
	chatList                        *chat.List // virtual-scrolling conversation list
	chatStyles                      chat.Styles
	shellMode                       bool
	loading                         bool
	quitting                        bool
	restartRequested                bool
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
	instanceDetect                  *im.InstanceDetect
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
	knight                          *knight.Knight
	mcpManager                      mcpManager
	mode                            permission.PermissionMode
	pendingDiffConfirm              *DiffConfirmMsg
	pendingQuestionnaire            *questionnaireState
	pendingHarnessCheckpointConfirm *HarnessCheckpointConfirmMsg
	modelPanel                      *modelPanelState
	providerPanel                   *providerPanelState
	qqPanel                         *qqPanelState
	tgPanel                         *tgPanelState
	pcPanel                         *pcPanelState
	discordPanel                    *discordPanelState
	feishuPanel                     *feishuPanelState
	slackPanel                      *slackPanelState
	dingtalkPanel                   *dingtalkPanelState
	imPanel                         *imPanelState
	mcpPanel                        *mcpPanelState
	pendingDeviceCodes              []deviceCodeInfo
	skillsPanel                     *skillsPanelState
	inspectorPanel                  *inspectorPanelState
	swarmMgr                        *swarm.Manager
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
	streamPrefixWritten bool
	harnessRunRemainder string
	harnessRunLiveTail  string

	// Status bar state
	statusActivity  string // "Thinking...", "Writing...", "Executing: tool_name"
	statusToolName  string // current executing tool name
	statusToolArg   string // current tool argument summary (truncated)
	statusToolCount int    // tool calls executed this iteration
	todoSnapshot    map[string]todoStateItem
	todoOrder       []string // preserves original insertion order from todo_write
	activeTodo      *todoStateItem
	activeMCPTools  map[string]ToolStatusMsg

	// Slash command autocomplete
	autoCompleteItems    []string
	autoCompleteIndex    int
	autoCompleteActive   bool
	autoCompleteKind     string // "slash" or "mention"
	autoCompleteWorkDir  string // working directory for mention completion
	startedAt            time.Time
	inputDrainUntil      time.Time // suppress all KeyPressMsg until this time (after setProgramMsg)
	inputReady           bool      // true after setProgramMsg + drain completes; before that, all KeyPress is discarded
	startupBannerVisible bool
	lastResizeAt         time.Time
	sidebarVisible       bool

	exitConfirmPending    bool
	pending               *pendingQueue
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
	remoteInboundAdapter  string // adapter name that sent the current remote inbound message (for per-channel echo suppression)
	clipboardLoader       func() (imageAttachedMsg, error)
	clipboardWriter       func(string) error
	urlOpener             func(string) error
	updateSvc             *update.Service
	updateInfo            update.CheckResult
	updateError           string
	systemPromptRebuilder func() string // rebuilds and returns the full system prompt
}

// pendingQueue holds the queue of user messages submitted while the agent
// loop is running.  Stored behind a pointer so that Bubble Tea's value-copy
// semantics for Model don't split the queue into independent copies — the
// agent goroutine's closure and the TUI goroutine's Update both reach the
// same underlying slice through the pointer.
type pendingQueue struct {
	mu    sync.Mutex
	items []string
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
// streamMsg wraps a string from the agent goroutine.
func NewModel(a *agent.Agent, policy permission.PermissionPolicy) Model {
	ta := textarea.New()
	ta.Prompt = "❯ "
	ta.Placeholder = tr(LangEnglish, "input.placeholder")
	ta.Focus()
	ta.SetWidth(74)
	ta.SetHeight(1)
	ta.ShowLineNumbers = false
	// DynamicHeight lets the textarea auto-grow/shrink based on content,
	// accounting for soft word-wrapping. MinHeight/MaxHeight bound the range.
	ta.DynamicHeight = true
	ta.MinHeight = 1
	ta.MaxHeight = 10
	// Single-line mode: Enter submits, Shift+Enter inserts newline.
	// Show min 1 line, expand up to 10 lines for multiline input.
	taStyles := textarea.DefaultStyles(true)
	// Clear all background colors — composer sits inside its own styled box.
	taStyles.Focused.Base = lipgloss.NewStyle()
	taStyles.Focused.CursorLine = lipgloss.NewStyle()
	taStyles.Focused.EndOfBuffer = lipgloss.NewStyle()
	taStyles.Focused.LineNumber = lipgloss.NewStyle()
	taStyles.Focused.CursorLineNumber = lipgloss.NewStyle()
	taStyles.Blurred.Base = lipgloss.NewStyle()
	taStyles.Blurred.CursorLine = lipgloss.NewStyle()
	taStyles.Blurred.EndOfBuffer = lipgloss.NewStyle()
	taStyles.Blurred.LineNumber = lipgloss.NewStyle()
	taStyles.Blurred.CursorLineNumber = lipgloss.NewStyle()
	ta.SetStyles(taStyles)
	taStyles.Focused.Text = taStyles.Focused.Text.Bold(true)
	taStyles.Focused.Prompt = taStyles.Focused.Prompt.Bold(true)
	taStyles.Focused.Placeholder = taStyles.Focused.Placeholder.Bold(true)
	taStyles.Blurred.Text = taStyles.Blurred.Text.Bold(true)
	taStyles.Blurred.Prompt = taStyles.Blurred.Prompt.Bold(true)
	ta.SetStyles(taStyles)

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
		input:                ta,
		chatList:             chat.NewList(80, 20),
		chatStyles:           chat.DefaultStyles(),
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
		pending:              &pendingQueue{},
		sessionMu:            &sync.Mutex{},
	}
}

func policyMode(policy permission.PermissionPolicy) permission.PermissionMode {
	if getter, ok := policy.(policyModeGetter); ok {
		return getter.Mode()
	}
	return permission.SupervisedMode
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		func() tea.Msg { return textarea.Blink() },
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
	// Register this instance for multi-instance detection and auto-mute
	// if another instance is already running in the same workspace.
	// This must happen here (not just in SetIMManager) because the session
	// workspace is needed for the instance directory path.
	m.detectAndAutoMute()
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
		workDir, _ := os.Getwd()
		m.imEmitter = im.NewIMEmitter(mgr, lang, workDir)
	}
	m.refreshIMRuntimeHooks()
	m.bindIMSession()
	m.detectAndAutoMute()
}

// detectAndAutoMute registers this instance and auto-mutes IM channels
// if another instance was already running in the same directory.
func (m *Model) detectAndAutoMute() {
	if m.imManager == nil {
		return
	}
	// Already registered via Manager? Skip.
	if d := m.imManager.InstanceDetect(); d != nil && d.IsRegistered() {
		return
	}
	session := m.session
	if session == nil {
		return
	}

	detect, others, err := m.imManager.RegisterInstance(session.Workspace)
	if err != nil {
		return
	}
	m.instanceDetect = detect

	if len(others) == 0 {
		return
	}

	// Other instances exist — auto-mute all active channels
	count, _ := m.imManager.MuteAll()
	if count > 0 {
		primary := others[0] // oldest
		msg := m.t("panel.im.message.auto_mute", count, primary.PID, primary.StartedAt.Format("15:04"))
		m.chatWriteSystem(nextSystemID(), msg)
	}
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
	// Set up restart callback so UnmuteBinding/EnableBinding can reconnect adapters.
	if m.config != nil {
		cfg := m.config
		mgr := m.imManager
		m.imManager.SetOnRestart(func(adapterName string) error {
			return im.StartNamedAdapter(context.Background(), cfg.IM, adapterName, mgr)
		})
	}
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
	case m.imPanel != nil:
		m.closeIMPanel()
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
		// Config is now available — re-register onRestart callback if IM manager
		// was set before config (SetIMManager is called before SetConfig).
		m.refreshIMRuntimeHooks()
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

func (m *Model) SetKnight(k *knight.Knight) {
	m.knight = k
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

// imageAttachedMsg is sent when an image is successfully loaded.
type imageAttachedMsg struct {
	placeholder string
	img         image.Image
	filename    string
	sourcePath  string
}

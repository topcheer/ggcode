package tui

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"golang.org/x/term"

	"github.com/topcheer/ggcode/internal/a2a"
	"github.com/topcheer/ggcode/internal/acpclient"
	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/chat"
	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/harness"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/image"
	"github.com/topcheer/ggcode/internal/knight"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/stream"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/swarm"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tunnel"
	"github.com/topcheer/ggcode/internal/update"
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
	loopStart                       time.Time // when current agent loop started (user sent message)
	quitting                        bool
	restartRequested                bool
	restartDebug                    bool
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
	approvalNotifiedIM              bool // true when approval was pushed to IM
	session                         *session.Session
	sessionStore                    session.Store
	imManager                       *im.Manager
	streamManager                   *stream.Manager
	streamPanel                     *streamPanelState
	knightPanel                     *knightPanelState
	streamViewState                 *streamViewStateData // shared pointer — survives Model copies
	imRuntimeState                  *imRuntimeState
	imEmitter                       *im.IMEmitter
	instanceDetect                  *im.InstanceDetect
	mcpServers                      []MCPInfo
	a2aHandler                      *a2a.TaskHandler
	a2aEventBuf                     []a2a.TaskEventMessage // cached recent events for display
	a2aEventState                   *a2aEventBufferState
	config                          *config.Config
	language                        Language
	startupVendor                   string
	startupEndpoint                 string
	startupModel                    string
	activeVendor                    string
	activeEndpoint                  string
	activeModel                     string
	terminalTitleWriter             func(string)
	lastTerminalTitle               string
	customCmds                      map[string]*commands.Command
	commandMgr                      *commands.Manager
	autoMem                         *memory.AutoMemory
	projMemFiles                    []string
	autoMemFiles                    []string
	pluginMgr                       *plugin.Manager
	subAgentMgr                     *subagent.Manager
	subAgentFollow                  subAgentFollowState
	usageTurnIndex                  int
	lastMetricDigestTurn            int
	metricCollectorFlush            func()
	knight                          *knight.Knight
	mcpManager                      mcpManager
	mode                            permission.PermissionMode
	configSaveScope                 string // "global" or "instance" — where config panel saves go
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
	wechatPanel                     *wechatPanelState
	wecomPanel                      *wecomPanelState
	mattermostPanel                 *mattermostPanelState
	matrixPanel                     *matrixPanelState
	signalPanel                     *signalPanelState
	ircPanel                        *ircPanelState
	nostrPanel                      *nostrPanelState
	twitchPanel                     *twitchPanelState
	whatsappPanel                   *whatsappPanelState
	imPanel                         *imPanelState
	mcpPanel                        *mcpPanelState
	pendingDeviceCodes              []deviceCodeInfo
	skillsPanel                     *skillsPanelState
	statsPanel                      *statsPanelState
	inspectorPanel                  *inspectorPanelState
	swarmMgr                        *swarm.Manager
	acpClientMgr                    *acpclient.ClientManager
	previewPanel                    *previewPanelState
	fileBrowser                     *fileBrowserState
	harnessPanel                    *harnessPanelState
	harnessContextPrompt            *harnessContextPromptState
	impersonatePanel                *impersonatePanelState
	qrOverlay                       *qrOverlayState
	tunnelStarting                  bool
	tunnelGeneration                uint64

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
	shellOutputID       string // ID of the system message for shell command output
	shellOutputIDs      map[string]struct{}
	streamPrefixWritten bool
	harnessRunRemainder string
	harnessRunLiveTail  string

	// Auto-run pending suggestion: when harness.auto_run is "suggest" and the
	// router detects a code-change task, the pending result is saved here.
	// Enter confirms (routes to harness), Esc dismisses (normal agent).
	pendingAutoRun     *harness.AutoRunResult
	pendingAutoRunText string

	// pendingHarnessReview holds a completed task awaiting review approval.
	// Set after harnessRunResultMsg when task is completed+review pending.
	// Enter approves, Esc skips. Similar UX to pendingAutoRun suggest mode.
	pendingHarnessReview *harness.Task

	// pendingHarnessPromote holds an approved task awaiting promotion.
	// Set after harnessReviewResultMsg when task is ReviewApproved.
	// Enter promotes (applies changes), Esc skips.
	pendingHarnessPromote *harness.Task

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
	inputHint            string // placeholder hint shown after cursor (e.g. "<subcommand>")
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
	webuiBridge           WebUIEventBroadcaster
	updateSvc             *update.Service
	updateInfo            update.CheckResult
	updateError           string
	systemPromptRebuilder func() string // rebuilds and returns the full system prompt

	// Mobile tunnel
	tunnelSession             *tunnel.Session
	tunnelBroker              *tunnel.Broker
	tunnelProjectionBroker    *tunnel.Broker
	tunnelProjectionStore     *tunnel.ProjectionStore
	tunnelProjectionBroken    bool
	tunnelMsgID               string
	tunnelMsgNeedsFinalize    bool
	tunnelMainStream          *tunnelMainStreamState
	tunnelShareBootstrap      *tunnelShareBootstrapState
	tunnelPendingApprovalID   string
	tunnelPendingAskUserID    string
	tunnelUserMessageOverride *tunnel.MessageData
	suppressNextTunnelSystem  string
	tunnelClientNoticeShown   bool
	tunnelSpawned             map[string]bool // tracks which subagents have been announced to mobile
}

// pendingQueue holds the queue of user messages submitted while the agent
// loop is running.  Stored behind a pointer so that Bubble Tea's value-copy
// semantics for Model don't split the queue into independent copies — the
// agent goroutine's closure and the TUI goroutine's Update both reach the
// same underlying slice through the pointer.
type pendingSubmission struct {
	Text                  string
	Hidden                bool
	TunnelMessageOverride *tunnel.MessageData
}

type pendingQueue struct {
	items []pendingSubmission
	q     *agentruntime.PendingQueue[*tunnel.MessageData]
}

type a2aEventUpdatedMsg struct{}

type imRuntimeState struct {
	mu             sync.RWMutex
	manager        *im.Manager
	emitter        *im.IMEmitter
	instanceDetect *im.InstanceDetect
}

type a2aEventBufferState struct {
	mu     sync.Mutex
	events []a2a.TaskEventMessage
}

// tunnelMainStreamState keeps the logical mobile/share assistant stream identity
// behind a pointer so Bubble Tea model copies and background stream callbacks
// stay synchronized on the same msg id lifecycle.
type tunnelMainStreamState struct {
	mu            sync.Mutex
	msgID         string
	needsFinalize bool
}

type tunnelShareBootstrapState struct {
	mu         sync.Mutex
	generation uint64
	active     bool
	pending    []tunnel.GatewayMessage
}

// MCPInfo holds display info about a connected MCP server.
// WebUIEventBroadcaster broadcasts agent events to webui subscribers.
type WebUIEventBroadcaster interface {
	BroadcastEvent(event provider.StreamEvent)
}

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
		sidebarVisible:       false,
		activeMCPTools:       make(map[string]ToolStatusMsg),
		clipboardLoader:      loadClipboardImage,
		clipboardWriter:      copyTextToClipboard,
		urlOpener:            openSystemURL,
		pending:              &pendingQueue{},
		sessionMu:            &sync.Mutex{},
		imRuntimeState:       &imRuntimeState{},
		a2aEventState:        &a2aEventBufferState{},
		tunnelMainStream:     &tunnelMainStreamState{},
		tunnelShareBootstrap: &tunnelShareBootstrapState{},
		streamViewState:      &streamViewStateData{},
		terminalTitleWriter:  newTerminalTitleWriter(),
	}
}

func (m *Model) ensureIMRuntimeState() *imRuntimeState {
	if m.imRuntimeState == nil {
		m.imRuntimeState = &imRuntimeState{
			manager:        m.imManager,
			emitter:        m.imEmitter,
			instanceDetect: m.instanceDetect,
		}
	}
	return m.imRuntimeState
}

func (m *Model) syncIMRuntimeCache() {
	state := m.ensureIMRuntimeState()
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.manager == nil && state.emitter == nil && state.instanceDetect == nil &&
		(m.imManager != nil || m.imEmitter != nil || m.instanceDetect != nil) {
		state.manager = m.imManager
		state.emitter = m.imEmitter
		state.instanceDetect = m.instanceDetect
	}
	m.imManager = state.manager
	m.imEmitter = state.emitter
	m.instanceDetect = state.instanceDetect
}

func (m *Model) storeIMRuntime(manager *im.Manager, emitter *im.IMEmitter, detect *im.InstanceDetect) {
	state := m.ensureIMRuntimeState()
	state.mu.Lock()
	state.manager = manager
	state.emitter = emitter
	state.instanceDetect = detect
	m.imManager = state.manager
	m.imEmitter = state.emitter
	m.instanceDetect = state.instanceDetect
	state.mu.Unlock()
}

func (m *Model) storeIMInstanceDetect(detect *im.InstanceDetect) {
	state := m.ensureIMRuntimeState()
	state.mu.Lock()
	state.instanceDetect = detect
	m.imManager = state.manager
	m.imEmitter = state.emitter
	m.instanceDetect = state.instanceDetect
	state.mu.Unlock()
}

func (m *Model) ensureA2AEventState() *a2aEventBufferState {
	if m.a2aEventState == nil {
		events := append([]a2a.TaskEventMessage(nil), m.a2aEventBuf...)
		m.a2aEventState = &a2aEventBufferState{events: events}
	}
	return m.a2aEventState
}

func (m *Model) syncA2AEventCache() {
	state := m.ensureA2AEventState()
	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.events) == 0 && len(m.a2aEventBuf) > 0 {
		state.events = append([]a2a.TaskEventMessage(nil), m.a2aEventBuf...)
	}
	m.a2aEventBuf = append(m.a2aEventBuf[:0], state.events...)
}

func (m *Model) appendA2AEvent(msg a2a.TaskEventMessage) {
	state := m.ensureA2AEventState()
	state.mu.Lock()
	state.events = append(state.events, msg)
	if len(state.events) > 20 {
		state.events = state.events[len(state.events)-20:]
	}
	events := append([]a2a.TaskEventMessage(nil), state.events...)
	state.mu.Unlock()
	m.a2aEventBuf = events
}

func (m *Model) syncAsyncStateCaches() {
	m.syncIMRuntimeCache()
	m.syncA2AEventCache()
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
		func() tea.Msg { return gitBranchTickMsg{} },
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
	m.refreshCachedGitBranch()
}

// startContextProbe silently probes the context window for the current
// provider+model and applies the result to the agent's ContextManager.
// Completely invisible to the user — no UI feedback at all.
// Safe to call at any time; no-ops if agent/provider/config are not ready.
func (m *Model) startContextProbe() {
	if m.config == nil || !m.config.ProbeContext {
		return
	}
	if m.agent == nil {
		debug.Log("probe", "startContextProbe skipped: agent is nil")
		return
	}
	if m.config == nil {
		debug.Log("probe", "startContextProbe skipped: config is nil")
		return
	}
	prov := m.agent.Provider()
	if prov == nil {
		debug.Log("probe", "startContextProbe skipped: provider is nil (no active LLM configured)")
		return
	}
	resolved, err := m.config.ResolveActiveEndpoint()
	if err != nil {
		debug.Log("probe", "startContextProbe skipped: resolve failed: %v", err)
		return
	}
	if resolved.Model == "" {
		debug.Log("probe", "startContextProbe skipped: no model selected")
		return
	}
	if ep := m.config.ActiveEndpointConfig(); ep != nil && ep.ContextWindow > 0 {
		debug.Log("probe", "startContextProbe skipped: explicit context_window=%d configured", ep.ContextWindow)
		return
	}

	debug.Log("probe", "startContextProbe: vendor=%s model=%s baseURL=%s",
		resolved.VendorID, resolved.Model, resolved.BaseURL)

	provider.ProbeContextWindow(context.Background(), prov,
		resolved.VendorID, resolved.BaseURL, resolved.Model,
		func(r provider.ProbeResult) {
			if r.ContextWindow > 0 {
				debug.Log("probe", "applying context_window=%d fromCache=%v to agent",
					r.ContextWindow, r.FromCache)
				m.agent.ContextManager().SetContextWindow(r.ContextWindow)
			} else {
				debug.Log("probe", "probe returned 0 (no result), keeping current context window setting")
			}
		})
}

func (m *Model) SetSession(ses *session.Session, store session.Store) {
	m.session = ses
	m.sessionStore = store
	m.usageTurnIndex = session.LastTurnIndex(ses)
	m.lastMetricDigestTurn = m.usageTurnIndex
	m.bindTunnelProjectionSession()
	m.bindIMSession()
	m.announceTunnelActiveSession()
	// Register this instance for multi-instance detection and auto-mute
	// if another instance is already running in the same workspace.
	// This must happen here (not just in SetIMManager) because the session
	// workspace is needed for the instance directory path.
	m.detectAndAutoMute()
}

func (m *Model) Session() *session.Session {
	return m.session
}

func (m *Model) recordSessionUsage(usage provider.TokenUsage) {
	if m.session == nil || m.sessionStore == nil {
		return
	}
	mu := m.sessionMutex()
	mu.Lock()
	if m.session == nil || m.sessionStore == nil {
		mu.Unlock()
		return
	}
	m.session.TokenUsage = m.session.TokenUsage.Add(usage)
	m.session.AddUsageForEndpoint(m.session.Vendor, m.session.Endpoint, usage)
	m.session.UpdatedAt = time.Now()
	ses := m.session
	store := m.sessionStore
	entry := session.UsageEntry{
		Timestamp: time.Now(),
		TurnIndex: m.usageTurnIndex,
		Model:     ses.Model,
		Vendor:    ses.Vendor,
		Endpoint:  ses.Endpoint,
		Usage:     usage,
	}
	mu.Unlock()

	if jsonlStore, ok := store.(*session.JSONLStore); ok {
		_ = jsonlStore.AppendMetaToDisk(ses)
		_ = jsonlStore.AppendUsageEntry(ses, entry)
	} else {
		_ = store.Save(ses)
	}
}

func (m *Model) recordSessionMetric(ev metrics.MetricEvent) {
	if m.session == nil || m.sessionStore == nil {
		return
	}
	mu := m.sessionMutex()
	mu.Lock()
	if m.session == nil || m.sessionStore == nil {
		mu.Unlock()
		return
	}
	ev.TurnIndex = m.usageTurnIndex
	ev.Model = m.session.Model
	ev.Vendor = m.session.Vendor
	ev.Endpoint = m.session.Endpoint
	m.session.Metrics = append(m.session.Metrics, ev)
	m.session.AppendMetricForEndpoint(m.session.Vendor, m.session.Endpoint, ev)
	m.session.UpdatedAt = time.Now()
	ses := m.session
	store := m.sessionStore
	mu.Unlock()

	if jsonlStore, ok := store.(*session.JSONLStore); ok {
		_ = jsonlStore.AppendMetric(ses, ev)
	} else {
		_ = store.Save(ses)
	}
	m.syncStatsPanelViewport(false)
}

func (m *Model) SetIMManager(mgr *im.Manager) {
	var emitter *im.IMEmitter
	if mgr != nil {
		lang := "en"
		if m.language == LangZhCN {
			lang = "zh-CN"
		}
		workDir, _ := os.Getwd()
		emitter = im.NewIMEmitter(mgr, lang, workDir)
		// If any bound adapter is WeChat, default to summary mode
		// (WeChat iLink has a 5s context_token expiry + 5 msg limit per inbound).
		if im.HasWechatAdapter(mgr) {
			emitter.SetOutputMode(im.WechatDefaultOutputMode)
		}
	}
	m.storeIMRuntime(mgr, emitter, nil)
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
	autoMuteCount := 0
	for _, binding := range m.imManager.CurrentBindings() {
		if binding.Muted || strings.TrimSpace(binding.ChannelID) == "" {
			continue
		}
		autoMuteCount++
	}

	detect, others, err := m.imManager.RegisterInstance(session.Workspace)
	if err != nil {
		return
	}
	m.storeIMInstanceDetect(detect)

	if len(others) == 0 {
		return
	}

	// RegisterInstance already auto-muted active channels; just surface the result.
	if autoMuteCount > 0 {
		primary := others[0] // oldest
		msg := m.t("panel.im.message.auto_mute", autoMuteCount, primary.PID, primary.StartedAt.Format("15:04"))
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
	if m.config != nil {
		adapters := make(map[string]bool)
		for name, cfg := range m.config.IM.Adapters {
			adapters[name] = cfg.Enabled
		}
		m.imManager.ApplyAdapterConfig(adapters)
	}
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
	reply := m.t("pairing.rejected")
	if blacklisted {
		reply = m.t("pairing.blacklisted")
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
	case m.wechatPanel != nil:
		m.closeWechatPanel()
		m.closeIMPanel()
	case m.wecomPanel != nil:
		m.closeWeComPanel()
		m.closeIMPanel()
	case m.mattermostPanel != nil:
		m.closeMattermostPanel()
	case m.matrixPanel != nil:
		m.closeMatrixPanel()
	case m.signalPanel != nil:
		m.closeSignalPanel()
	case m.ircPanel != nil:
		m.closeIRCPanel()
	case m.nostrPanel != nil:
		m.closeNostrPanel()
	case m.twitchPanel != nil:
		m.closeTwitchPanel()
	case m.whatsappPanel != nil:
		m.closeWhatsAppPanel()
	case m.mcpPanel != nil:
		m.closeMCPPanel()
	case m.skillsPanel != nil:
		m.closeSkillsPanel()
	case m.statsPanel != nil:
		m.closeStatsPanel()
	case m.inspectorPanel != nil:
		m.closeInspectorPanel()
	case m.streamPanel != nil:
		m.closeStreamPanel()
	case m.knightPanel != nil:
		m.closeKnightPanel()
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

// SetA2AHandler connects the A2A task handler so the sidebar can show
// active remote tasks and the event callback updates the TUI.
func (m *Model) SetA2AHandler(h *a2a.TaskHandler) {
	m.a2aHandler = h
	if h != nil {
		h.SetOnTaskEvent(func(msg a2a.TaskEventMessage) {
			m.appendA2AEvent(msg)
			if m.program != nil {
				m.program.Send(a2aEventUpdatedMsg{})
			}
		})
	}
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
	// Silently probe context window in background
	m.startContextProbe()
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

func newTerminalTitleWriter() func(string) {
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("TERM")), "dumb") {
		return nil
	}
	return func(title string) {
		title = sanitizeTerminalTitle(title)
		if title == "" {
			return
		}
		_, _ = fmt.Fprintf(os.Stdout, "\x1b]0;%s\x07", title)
	}
}

func sanitizeTerminalTitle(title string) string {
	title = strings.Map(func(r rune) rune {
		switch {
		case r == '\a' || r == 0x1b:
			return -1
		case unicode.IsControl(r):
			return ' '
		default:
			return r
		}
	}, title)
	return strings.Join(strings.Fields(title), " ")
}

func (m Model) terminalTitleLabel() string {
	workspace := strings.TrimSpace(m.currentWorkspacePath())
	workspace = strings.TrimSpace(filepath.Base(workspace))
	switch workspace {
	case "", ".", string(filepath.Separator):
		workspace = ""
	}

	modelName := strings.TrimSpace(m.activeModel)
	if modelName == "" && m.session != nil {
		modelName = strings.TrimSpace(m.session.Model)
	}
	if modelName == "" && m.config != nil {
		modelName = strings.TrimSpace(m.config.Model)
	}

	switch {
	case workspace != "" && modelName != "":
		return fmt.Sprintf("%s [%s]", workspace, modelName)
	case workspace != "":
		return workspace
	case modelName != "":
		return modelName
	default:
		return ""
	}
}

func (m Model) desiredTerminalTitle() string {
	label := m.terminalTitleLabel()
	activity := sanitizeTerminalTitle(strings.TrimSpace(m.statusActivity))
	if activity != "" {
		if label != "" {
			return fmt.Sprintf("> %s — %s", activity, label)
		}
		return fmt.Sprintf("> %s", activity)
	}
	if label != "" {
		return fmt.Sprintf("> ggcode — %s", label)
	}
	return "> ggcode"
}

func (m Model) withTerminalTitleCmd(cmd tea.Cmd) (Model, tea.Cmd) {
	if m.terminalTitleWriter == nil {
		return m, cmd
	}
	title := m.desiredTerminalTitle()
	if title == "" || title == m.lastTerminalTitle {
		return m, cmd
	}
	m.lastTerminalTitle = title
	writeCmd := func() tea.Msg {
		m.terminalTitleWriter(title)
		return nil
	}
	return m, combineCmds(cmd, writeCmd)
}

func asciiLogo() string {
	return "   ____ ____ ____ ___  ____  ______\n  / ___/ ___/ ___/ _ \\/ __ \\/ ____/\n / (_ / (_ / /__/ // / /_/ / /__  \n \\___/\\___/\\___/____/\\____/\\___/  \n"
}

func (m *Model) SetSubAgentManager(mgr *subagent.Manager) {
	m.subAgentMgr = mgr
}

func (m *Model) SetKnight(k *knight.Knight) {
	m.knight = k
	// Wire Knight task events into the TUI chat area via program.Send.
	if k != nil {
		k.SetEventSink(&knight.FuncSink{
			OnStart: func(taskName string) {
				if m.program != nil {
					m.program.Send(knightTaskEventMsg{TaskName: taskName})
				}
			},
			OnComplete: func(taskName string, report string, duration time.Duration) {
				if m.program != nil {
					m.program.Send(knightTaskEventMsg{TaskName: taskName, Report: report, Duration: duration})
				}
			},
		})
	}
}

// saveConfig saves config changes to either global or instance config
// based on the current configSaveScope setting.
func (m *Model) saveConfig() error {
	if m.config == nil {
		return fmt.Errorf("config is nil")
	}
	return m.config.SaveScoped(m.configSaveScope)
}

// toggleConfigSaveScope switches between "global" and "instance" save targets.
// Returns a short status message for display.
func (m *Model) toggleConfigSaveScope() string {
	if m.configSaveScope == "instance" {
		m.configSaveScope = "global"
		return "Save target → Global"
	}
	// Only allow instance scope if instance workspace is attached
	if m.config != nil && m.config.HasInstanceConfigAttached() {
		m.configSaveScope = "instance"
		hasFile := false
		if _, err := os.Stat(m.config.InstanceDirPath()); err == nil {
			hasFile = true
		}
		if hasFile {
			return "Save target → Instance"
		}
		return "Save target → Instance (new config will be created)"
	}
	return "Instance config not available for this workspace"
}

// configSaveScopeLabel returns a display label for the current save scope.
func (m *Model) configSaveScopeLabel() string {
	if m.configSaveScope == "instance" {
		return "Instance"
	}
	return "Global"
}

func (m *Model) vendorNames() string {
	if m.config == nil {
		return ""
	}
	return strings.Join(m.config.VendorNames(), ", ")
}

// imageAttachedMsg is sent when an image is successfully loaded.
type imageAttachedMsg struct {
	placeholder string
	img         image.Image
	filename    string
	sourcePath  string
}

// activeIMPanel returns a pointer to the message field of the currently active
// IM channel panel (if any). Used to forward imPanelResultMsg feedback.
func (m Model) activeIMPanel() *string {
	switch {
	case m.qqPanel != nil:
		return &m.qqPanel.message
	case m.tgPanel != nil:
		return &m.tgPanel.message
	case m.discordPanel != nil:
		return &m.discordPanel.message
	case m.slackPanel != nil:
		return &m.slackPanel.message
	case m.dingtalkPanel != nil:
		return &m.dingtalkPanel.message
	case m.feishuPanel != nil:
		return &m.feishuPanel.message
	case m.wecomPanel != nil:
		return &m.wecomPanel.message
	case m.wechatPanel != nil:
		return &m.wechatPanel.message
	case m.ircPanel != nil:
		return &m.ircPanel.message
	case m.matrixPanel != nil:
		return &m.matrixPanel.message
	case m.whatsappPanel != nil:
		return &m.whatsappPanel.message
	case m.mattermostPanel != nil:
		return &m.mattermostPanel.message
	case m.signalPanel != nil:
		return &m.signalPanel.message
	case m.nostrPanel != nil:
		return &m.nostrPanel.message
	case m.twitchPanel != nil:
		return &m.twitchPanel.message
	default:
		return nil
	}
}

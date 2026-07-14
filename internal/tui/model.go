package tui

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
	"github.com/topcheer/ggcode/internal/cron"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/harness"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/image"
	"github.com/topcheer/ggcode/internal/knight"
	"github.com/topcheer/ggcode/internal/lanchat"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/stream"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/swarm"
	"github.com/topcheer/ggcode/internal/tmux"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tui/cmdpane"
	extpane "github.com/topcheer/ggcode/internal/tui/extpane"
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
	cronScheduler                   *cron.Scheduler
	shellMode                       bool
	shellRunning                    bool // true while a $ shell command is executing (independent of agent loading)
	shellOwnedLoading               bool // true when shell set m.loading (agent wasn't running)
	chatMode                        bool // LAN Chat quick-send mode (# prefix)
	lanChatLastSenderNick           string
	lanChatLastSenderRole           string
	lanChatLastSenderNodeID         string
	loading                         bool
	agentBusy                       *atomic.Bool // shared with REPL for /api/status
	loopStart                       time.Time    // when current agent loop started (user sent message)
	quitting                        bool
	restartRequested                bool
	restartDebug                    bool
	updatePrepared                  *update.PreparedUpdate // set by /update before restart
	tmuxExecRequested               bool
	tmuxExecSession                 string
	tmuxExecSetupLayout             string
	tmuxStartupSetupLayout          string
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

	harnessPanel           *harnessPanelState
	harnessContextPrompt   *harnessContextPromptState
	impersonatePanel       *impersonatePanelState
	lanChatPanel           *lanChatPanelState
	lanChatHub             *lanchat.Hub
	lanChatNotice          string
	lanChatUnread          int
	lanChatPendingComplete string // message ID to send "completed" receipt when agent finishes
	qrOverlay              *qrOverlayState
	tunnelStarting         bool
	tunnelGeneration       uint64

	// Approval selection list
	approvalOptions []approvalOption
	approvalCursor  int

	// Diff confirm selection list
	diffOptions            []approvalOption
	diffCursor             int
	pendingImages          []imageAttachedMsg
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
	reasoningActive     bool // true while reasoning block is expanded in current LLM turn
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
	tmuxClient      *tmux.Client
	tmuxManager     *tmux.Manager
	tmuxEnv         *tmux.Environment
	tmuxMenuOpen    bool
	todoSnapshot    map[string]todoStateItem
	todoOrder       []string // preserves original insertion order from todo_write
	activeTodo      *todoStateItem
	activeMCPTools  map[string]ToolStatusMsg

	// Slash command autocomplete
	autoCompleteItems   []string
	autoCompleteIndex   int
	autoCompleteActive  bool
	autoCompleteKind    string // "slash" or "mention"
	autoCompleteWorkDir string // working directory for mention completion
	inputHint           string // placeholder hint shown after cursor (e.g. "<subcommand>")
	startedAt           time.Time
	inputDrainUntil     time.Time // suppress all KeyPressMsg until this time (after setProgramMsg)
	inputReady          bool      // true after setProgramMsg + drain completes; before that, all KeyPress is discarded
	initPromptActive    bool      // show "create GGCODE.md?" panel at startup
	lastResizeAt        time.Time
	sidebarVisible      bool

	exitConfirmPending   bool
	cancelConfirmPending bool
	lastEscPress         time.Time // for Esc+Esc double-press rewind detection
	compactMode          bool      // Iteration 1: compact mode toggle
	pending              *pendingQueue
	sessionMu            *sync.Mutex
	// persistedMsgCount tracks how many messages from ses.Messages have been
	// written to the JSONL file via AppendMessageToDisk(). Used by
	// persistFullSessionMessages() to only append NEW messages.
	// ⚠️ Must be updated whenever messages are appended to disk outside of
	// persistFullSessionMessages() (e.g. submitMessage).
	persistedMsgCount     int
	projectMemoryLoading  bool
	runCanceled           bool
	runFailed             bool
	subAgentsCanceling    bool   // true while async CancelAll() is in progress after user cancel
	lastUserSubmission    string // last non-slash user prompt, for /retry
	activeAgentRunID      int
	activeShellRunID      int
	shellCommandSubmitter func(command string, addToHistory bool) tea.Cmd
	// sessionLockSwitch is called when /clear or /sessions switches sessions.
	// The REPL registers this callback to release the old session lock and
	// acquire a new one, preventing lock leakage and enabling concurrent
	// instance auto-load to work correctly.
	sessionLockSwitch func(newSessionID string)
	// sessionCronSwitch is called when /clear or /sessions switches sessions.
	// The REPL registers this callback to rebind the cron scheduler to the
	// new session's store path, so cron jobs persist to the correct session.
	sessionCronSwitch     func(newSessionID string)
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
	tunnelHost                *agentruntime.TunnelHost // unified tunnel management
	tunnelSession             *tunnel.Session
	tunnelBroker              *tunnel.Broker
	tunnelMainStream          *tunnelMainStreamState
	tunnelShareBootstrap      *tunnelShareBootstrapState
	tunnelPendingApprovalID   string
	tunnelPendingAskUserID    string
	tunnelUserMessageOverride *tunnel.MessageData
	suppressNextTunnelSystem  string
	tunnelClientNoticeShown   bool
	tunnelSpawned             map[string]bool // tracks which subagents have been announced to mobile

	// External pane manager for sub-agent/teammate output
	extPaneMgr *extpane.Manager

	// Command pane manager for real-time command output mirroring (tmux only)
	cmdPaneMgr *cmdpane.Manager

	// Terminal pet (animated ASCII art at the bottom of the composer)
	petEnabled bool
	pet        *petState
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
	Images                []imageAttachedMsg
}

type pendingQueue struct {
	mu    sync.Mutex
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
	ForceReauth(name string) bool
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
	// Single-line mode: Enter submits, Shift+Enter/Ctrl+J/Alt+Enter inserts newline.
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

	tmuxClient, tmuxEnv := detectTmuxForTUI()
	tmuxWorkspace := ""
	if a != nil {
		tmuxWorkspace = a.WorkingDir()
	}
	tmuxStartupSetupLayout := strings.TrimSpace(os.Getenv("GGCODE_TMUX_SETUP_LAYOUT"))
	if tmuxStartupSetupLayout != "" {
		_ = os.Unsetenv("GGCODE_TMUX_SETUP_LAYOUT")
	}

	m := Model{
		input:                  ta,
		chatList:               chat.NewList(80, 20),
		chatStyles:             chat.DefaultStyles(),
		styles:                 s,
		agent:                  a,
		language:               LangEnglish,
		policy:                 policy,
		spinner:                NewToolSpinner(),
		history:                make([]string, 0, 100),
		viewport:               NewViewportModel(80, 20),
		mode:                   policyMode(policy),
		startedAt:              time.Time{}, // set on first WindowSizeMsg
		sidebarVisible:         false,
		activeMCPTools:         make(map[string]ToolStatusMsg),
		clipboardLoader:        loadClipboardImage,
		clipboardWriter:        copyTextToClipboard,
		urlOpener:              openSystemURL,
		pending:                &pendingQueue{},
		sessionMu:              &sync.Mutex{},
		imRuntimeState:         &imRuntimeState{},
		a2aEventState:          &a2aEventBufferState{},
		tunnelMainStream:       &tunnelMainStreamState{},
		tunnelShareBootstrap:   &tunnelShareBootstrapState{},
		streamViewState:        &streamViewStateData{},
		terminalTitleWriter:    newTerminalTitleWriter(),
		tmuxClient:             tmuxClient,
		tmuxManager:            tmux.SharedManager(tmuxWorkspace),
		tmuxEnv:                tmuxEnv,
		tmuxStartupSetupLayout: tmuxStartupSetupLayout,
		extPaneMgr:             extpane.NewManager(),
		petEnabled:             true,
		pet:                    &petState{},
	}

	if a != nil {
		setupReflection(a)
	}

	return m
}

// setLoading updates both the model's loading field and the shared atomic
// used by /api/status for external process visibility.
func (m *Model) setLoading(val bool) {
	m.loading = val
	if m.agentBusy != nil {
		m.agentBusy.Store(val)
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
	// Clean up stale temp images from previous sessions (best-effort).
	go cleanupOldTempImages()

	cmds := []tea.Cmd{
		func() tea.Msg { return textarea.Blink() },
		func() tea.Msg { return tea.RequestWindowSize() },
		func() tea.Msg { return gitBranchTickMsg{} },
	}
	// Check whether the project has a GGCODE.md (or AGENTS.md, CLAUDE.md,
	// COPILOT.md). If none exist AND the directory has real project files
	// (non-hidden), prompt the user to initialize.
	cmds = append(cmds, func() tea.Msg {
		workDir, _ := os.Getwd()
		target, existing, _ := memory.ResolveProjectMemoryInitTarget(workDir)
		if len(existing) == 0 && dirHasProjectFiles(workDir) {
			return initPromptCheckMsg{needsInit: true, target: target}
		}
		return initPromptCheckMsg{needsInit: false}
	})
	if m.petEnabled {
		cmds = append(cmds, startPetAnim())
	}
	if m.updateSvc != nil {
		cmds = append(cmds, m.checkForUpdateCmd())
		cmds = append(cmds, m.scheduleUpdateCheckCmd())
	}
	if m.tmuxStartupSetupLayout != "" {
		layout := m.tmuxStartupSetupLayout
		cmds = append(cmds, func() tea.Msg { return tmuxStartupSetupMsg{Layout: layout} })
	}
	return tea.Batch(cmds...)
}

// dirHasProjectFiles returns true if dir contains at least one non-hidden
// file or directory (i.e. a real project, not just .git/.DS_Store/etc).
func dirHasProjectFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			return true
		}
	}
	return false
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
	// Skip probe if any explicit context_window override exists:
	// 1. Session-level (restored from JSONL)
	// 2. Per-model override (ModelLimits)
	// 3. Endpoint-level
	if m.session != nil && m.session.ContextWindow > 0 {
		debug.Log("probe", "startContextProbe skipped: session-level context_window=%d", m.session.ContextWindow)
		return
	}
	if ep := m.config.ActiveEndpointConfig(); ep != nil {
		if ep.ContextWindow > 0 {
			debug.Log("probe", "startContextProbe skipped: endpoint-level context_window=%d", ep.ContextWindow)
			return
		}
		if ml, ok := ep.ModelLimits[resolved.Model]; ok && ml.ContextWindow > 0 {
			debug.Log("probe", "startContextProbe skipped: per-model context_window=%d for %s", ml.ContextWindow, resolved.Model)
			return
		}
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
				// Persist probed context_window to session so it survives
				// restarts without re-probing.
				if m.session != nil && m.session.ContextWindow == 0 {
					m.session.ContextWindow = r.ContextWindow
					if m.sessionStore != nil {
						_ = m.sessionStore.AppendMetaToDisk(m.session)
					}
				}
			} else {
				debug.Log("probe", "probe returned 0 (no result), keeping current context window setting")
			}
		})
}

func (m *Model) SetCronScheduler(s *cron.Scheduler) {
	m.cronScheduler = s
}

func (m *Model) SetSession(ses *session.Session, store session.Store) {
	m.session = ses
	m.sessionStore = store
	// Propagate session ID to agent so todos are scoped to this session.
	if m.agent != nil {
		m.agent.SetSessionID(ses.ID)
	}
	// All messages in ses.Messages were loaded from the JSONL file — they
	// are already on disk. Mark them as persisted so persistFullSessionMessages
	// only appends truly new messages going forward.
	m.persistedMsgCount = len(ses.Messages)
	m.usageTurnIndex = session.LastTurnIndex(ses)
	m.lastMetricDigestTurn = m.usageTurnIndex
	// Restore session's model/vendor/endpoint to in-memory config.
	// The session is the source of truth for model selection — the config
	// file is only used for initial onboarding when creating new sessions.
	// Usage records and /cost now use the session's model directly.
	if ses.Vendor != "" && m.config != nil {
		m.config.Vendor = ses.Vendor
	}
	if ses.Endpoint != "" && m.config != nil {
		m.config.Endpoint = ses.Endpoint
	}
	if ses.Model != "" && m.config != nil {
		m.config.Model = ses.Model
	}
	// Mark the runtime selection so usage/cost tracking uses the correct model.
	if m.config != nil {
		m.setActiveRuntimeSelection(m.config.Vendor, m.config.Endpoint, m.config.Model)
	}
	// Activate the provider using the restored model selection.
	if m.config != nil {
		if err := m.tryActivateCurrentSelection(); err != nil {
			debug.Log("repl", "SetSession: activate provider failed: %v", err)
		}
	}
	// Apply session-level ContextWindow/MaxTokens if set. These override
	// the endpoint/per-model config, matching the PermissionMode pattern.
	if m.agent != nil {
		if ses.ContextWindow > 0 {
			m.agent.ContextManager().SetContextWindow(ses.ContextWindow)
		}
		if ses.MaxTokens > 0 {
			m.agent.ContextManager().SetOutputReserve(ses.MaxTokens)
		}
	}
	// Rebuild endpoint-level stats from UsageHistory to populate
	// EndpointUsage (not directly stored in JSONL). This ensures
	// sidebarSessionUsage returns correct values after session load.
	if ses.EndpointUsage == nil || len(ses.EndpointUsage) == 0 {
		ses.RebuildEndpointStats()
	}
	// Persist session meta to JSONL so TokenUsage, vendor, model etc.
	// match the current in-memory state.
	if m.sessionStore != nil {
		_ = m.sessionStore.AppendMetaToDisk(m.session)
	}
	// Probe context window for the restored model/endpoint. Different sessions
	// may use different models with different context window sizes.
	// Skip probing when the session already has an explicit ContextWindow
	// (restored from session-level persistence above).
	if ses.ContextWindow <= 0 {
		m.startContextProbe()
	}

	m.bindTunnelProjectionSession()
	m.bindIMSession()
	m.announceTunnelActiveSession()
	// If detectAndAutoMute was skipped during SetIMManager (because session
	// was nil at that point), call it now that we have a session. This happens
	// in the normal startup sequence: InitRuntime → SetIMManager (session nil)
	// → SetSession. For session switches (/clear, /branch), the instance is
	// already registered so detectAndAutoMute returns early.
	if m.instanceDetect == nil {
		m.detectAndAutoMute()
	}
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
	// Use the ACTUAL model the agent is running with (activeModel),
	// not the session-stored model which may be stale after a model
	// switch. activeModel is set by setActiveRuntimeSelection() every
	// time the provider is activated, so it always reflects reality.
	// For vendor/endpoint, use the session's config keys (not display
	// names from ResolvedEndpoint) so that RebuildEndpointStats produces
	// keys that match AddUsageForEndpoint and UsageForEndpoint.
	model := m.activeModel
	if model == "" {
		model = ses.Model
	}
	entry := session.UsageEntry{
		Timestamp: time.Now(),
		TurnIndex: m.usageTurnIndex,
		Model:     model,
		Vendor:    ses.Vendor,
		Endpoint:  ses.Endpoint,
		Usage:     usage,
	}
	// Keep in-memory UsageHistory in sync with disk. AppendUsageEntry
	// only writes to the JSONL file; without this append, /cost reads
	// stale data that only reflects what was loaded at startup.
	ses.UsageHistory = append(ses.UsageHistory, entry)
	mu.Unlock()

	// Disk I/O is async — the TUI event loop must not block on fsync.
	// The JSONLStore mutex serializes concurrent appends from multiple
	// goroutines, so ordering is safe.
	safego.Go("session.usage", func() {
		if jsonlStore, ok := store.(*session.JSONLStore); ok {
			_ = jsonlStore.AppendMetaToDisk(ses)
			_ = jsonlStore.AppendUsageEntry(ses, entry)
		} else {
			_ = store.Save(ses)
		}
	})
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
	// Use activeModel (actual running model) instead of potentially stale ses.Model
	ev.Model = m.activeModel
	ev.Vendor = m.activeVendor
	ev.Endpoint = m.activeEndpoint
	if ev.Model == "" {
		ev.Model = m.session.Model
		ev.Vendor = m.session.Vendor
		ev.Endpoint = m.session.Endpoint
	}
	m.session.Metrics = append(m.session.Metrics, ev)
	m.session.AppendMetricForEndpoint(m.session.Vendor, m.session.Endpoint, ev)
	m.session.UpdatedAt = time.Now()
	ses := m.session
	store := m.sessionStore
	mu.Unlock()

	// Disk I/O is async — see recordSessionUsage for rationale.
	safego.Go("session.metric", func() {
		if jsonlStore, ok := store.(*session.JSONLStore); ok {
			_ = jsonlStore.AppendMetric(ses, ev)
		} else {
			_ = store.Save(ses)
		}
	})
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
	// reloadBindingLocked preserves muted state across BindSession calls,
	// so there's no need to re-apply MuteAll here. Re-muting unconditionally
	// would override user-initiated unmute on non-primary instances.
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

	detect, others, err := m.imManager.RegisterInstance(session.Workspace, session.ID)
	if err != nil {
		return
	}
	m.storeIMInstanceDetect(detect)

	if len(others) == 0 {
		// No other instances — start any owned bindings that weren't started
		// during StartCurrentBindingAdapter (because sessionID was empty then).
		m.startOwnedAdapters()
		return
	}

	// RegisterInstance already auto-muted active channels; just surface the result.
	if autoMuteCount > 0 {
		primary := others[0] // oldest
		msg := m.t("panel.im.message.auto_mute", autoMuteCount, primary.PID, primary.StartedAt.Format("15:04"))
		m.chatWriteSystem(nextSystemID(), msg)
	}
	// Start owned bindings even when other instances exist — our session may
	// own adapters that need to be active.
	m.startOwnedAdapters()
}

// startOwnedAdapters starts adapters for non-muted bindings that don't have
// an active connection yet. This is called after session-scoped binding
// ownership is resolved in detectAndAutoMute.
func (m *Model) startOwnedAdapters() {
	if m.imManager == nil {
		return
	}
	m.imManager.StartUnstartedOwnedAdapters()
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
	// When session is nil (e.g. SetIMManager called before SetSession),
	// do NOT call UnbindSession — that would destroy currentBindings
	// including auto-mute state from RegisterInstance. The manager retains
	// whatever binding InitRuntime set up (CWD-based). When SetSession is
	// later called, it will BindSession with the correct session workspace.
	if m.session == nil {
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

func (m *Model) hasActivePanel() bool {
	return m.modelPanel != nil ||
		m.providerPanel != nil ||
		m.tgPanel != nil ||
		m.qqPanel != nil ||
		m.pcPanel != nil ||
		m.discordPanel != nil ||
		m.feishuPanel != nil ||
		m.slackPanel != nil ||
		m.dingtalkPanel != nil ||
		m.wechatPanel != nil ||
		m.wecomPanel != nil ||
		m.matrixPanel != nil ||
		m.mattermostPanel != nil ||
		m.signalPanel != nil ||
		m.ircPanel != nil ||
		m.nostrPanel != nil ||
		m.twitchPanel != nil ||
		m.whatsappPanel != nil ||
		m.mcpPanel != nil ||
		m.imPanel != nil ||
		m.inspectorPanel != nil ||
		m.harnessContextPrompt != nil ||
		m.harnessPanel != nil ||
		m.impersonatePanel != nil ||
		m.lanChatPanel != nil ||
		m.skillsPanel != nil ||
		m.streamPanel != nil ||
		m.knightPanel != nil ||
		m.statsPanel != nil ||
		len(m.langOptions) > 0
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
	case m.lanChatPanel != nil:
		m.closeLanChatPanel()
	case len(m.langOptions) > 0:
		m.langOptions = nil
	default:
		return false
	}
	m.resetExitConfirm()
	m.resetCancelConfirm()
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
		return m.t("config.save_scope_global")
	}
	// Only allow instance scope if instance workspace is attached
	if m.config != nil && m.config.HasInstanceConfigAttached() {
		m.configSaveScope = "instance"
		hasFile := false
		if _, err := os.Stat(m.config.InstanceDirPath()); err == nil {
			hasFile = true
		}
		if hasFile {
			return m.t("config.save_scope_instance")
		}
		return m.t("config.save_scope_instance_new")
	}
	return m.t("config.instance_unavailable")
}

// configSaveScopeLabel returns a display label for the current save scope.
func (m *Model) configSaveScopeLabel() string {
	if m.configSaveScope == "instance" {
		return m.t("config.scope_instance")
	}
	return m.t("config.scope_global")
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

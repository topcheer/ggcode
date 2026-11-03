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
	"github.com/topcheer/ggcode/internal/task"
	"github.com/topcheer/ggcode/internal/tmux"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tui/cmdpane"
	extpane "github.com/topcheer/ggcode/internal/tui/extpane"
	"github.com/topcheer/ggcode/internal/tunnel"
	"github.com/topcheer/ggcode/internal/update"
	"github.com/topcheer/ggcode/internal/usage"
)

type logoMsg struct {
	Vendor   string
	Endpoint string
	Model    string
}

type ApprovalMsg struct {
	ToolName string
	Input    string
	Response chan permission.Decision
}

type approvalResponseMsg struct {
	decision permission.Decision
}

type inputBellMsg struct {
	summary string
}

type policyModeGetter interface {
	Mode() permission.PermissionMode
}

const startupInputGateWindow = 500 * time.Millisecond

type Model struct {
	input                   textarea.Model
	chatList                *chat.List
	chatStyles              chat.Styles
	cronScheduler           *cron.Scheduler
	taskMgr                 *task.Manager
	resumeNote              *resumeNoteStore
	lastQueuedChatID        string
	queuedChatIDs           []string
	statusline              *statuslineState
	shellMode               bool
	shellRunning            bool
	shellOwnedLoading       bool
	chatMode                bool
	lanChatLastSenderNick   string
	lanChatLastSenderRole   string
	lanChatLastSenderNodeID string
	loading                 bool
	agentBusy               *atomic.Bool
	loopStart               time.Time
	streamChars             int
	streamTextStart         time.Time
	quitting                bool
	restartRequested        bool
	restartDebug            bool
	pendingRestart          bool
	lastTurnActivityAt      time.Time
	updatePrepared          *update.PreparedUpdate
	tmuxExecRequested       bool
	tmuxExecSession         string
	tmuxExecSetupLayout     string
	tmuxStartupSetupLayout  string
	width                   int
	height                  int
	styles                  styles
	agent                   *agent.Agent
	program                 *tea.Program
	cancelFunc              func()
	shellCancelFunc         func()
	pendingResumeID         string
	knightRunning           int
	lspInstallInFlight      bool
	policy                  permission.PermissionPolicy
	spinner                 *ToolSpinner
	history                 []string
	historyIdx              int
	pendingApproval         *ApprovalMsg
	approvalNotifiedIM      bool
	inputBellFired          bool
	session                 *session.Session
	sessionStore            session.Store

	tuiSend              func(tea.Msg)
	costCache            *sessionCostCache
	imManager            *im.Manager
	streamManager        *stream.Manager
	streamPanel          *streamPanelState
	knightPanel          *knightPanelState
	streamViewState      *streamViewStateData
	imRuntimeState       *imRuntimeState
	imEmitter            *im.IMEmitter
	instanceDetect       *im.InstanceDetect
	mcpServers           []MCPInfo
	a2aHandler           *a2a.TaskHandler
	a2aEventBuf          []a2a.TaskEventMessage
	a2aEventState        *a2aEventBufferState
	config               *config.Config
	language             Language
	startupVendor        string
	cachedPanelHeight    int
	startupEndpoint      string
	startupModel         string
	activeVendor         string
	activeEndpoint       string
	activeModel          string
	terminalTitleWriter  func(string)
	lastTerminalTitle    string
	customCmds           map[string]*commands.Command
	commandMgr           *commands.Manager
	autoMem              *memory.AutoMemory
	projMemFiles         []string
	autoMemFiles         []string
	pluginMgr            *plugin.Manager
	subAgentMgr          *subagent.Manager
	jobManager           *toolpkg.CommandJobManager
	subAgentFollow       subAgentFollowState
	usageTurnIndex       int
	lastMetricDigestTurn int
	metricCollectorFlush func()
	knight               *knight.Knight

	imEnsure *imEnsureGuard

	knightTasks          *knightCancelSet
	mcpManager           mcpManager
	mode                 permission.PermissionMode
	configSaveScope      string
	pendingDiffConfirm   *DiffConfirmMsg
	pendingQuestionnaire *questionnaireState
	modelPanel           *modelPanelState
	providerPanel        *providerPanelState
	qqPanel              *qqPanelState
	tgPanel              *tgPanelState
	pcPanel              *pcPanelState
	discordPanel         *discordPanelState
	feishuPanel          *feishuPanelState
	slackPanel           *slackPanelState
	dingtalkPanel        *dingtalkPanelState
	wechatPanel          *wechatPanelState

	lastWechatAdapterName string
	wecomPanel            *wecomPanelState
	mattermostPanel       *mattermostPanelState
	matrixPanel           *matrixPanelState
	signalPanel           *signalPanelState
	ircPanel              *ircPanelState
	nostrPanel            *nostrPanelState
	twitchPanel           *twitchPanelState
	whatsappPanel         *whatsappPanelState
	imPanel               *imPanelState
	mcpPanel              *mcpPanelState
	pendingDeviceCodes    []deviceCodeInfo
	skillsPanel           *skillsPanelState
	statsPanel            *statsPanelState
	usagePanel            *usagePanelState
	usageService          *usage.Service
	sidebarUsage          *usage.UsageInfo
	usageProbeDirty       bool
	usageSidebarStatus    string
	hooksPanel            *hooksPanelState
	inspectorPanel        *inspectorPanelState
	swarmMgr              *swarm.Manager
	acpClientMgr          *acpclient.ClientManager

	impersonatePanel       *impersonatePanelState
	lanChatPanel           *lanChatPanelState
	lanChatHub             *lanchat.Hub
	lanChatNotice          string
	lanChatUnread          int
	lanChatPendingComplete string
	qrOverlay              *qrOverlayState
	tunnelStarting         bool
	tunnelGeneration       uint64

	approvalOptions []approvalOption
	approvalCursor  int

	diffOptions            []approvalOption
	diffCursor             int
	pendingImages          []imageAttachedMsg
	langOptions            []languageOption
	langCursor             int
	languagePromptRequired bool

	viewport ViewportModel

	streamBuffer        *bytes.Buffer
	shellBuffer         *bytes.Buffer
	shellOutputID       string
	shellOutputIDs      map[string]struct{}
	saSysItemIDs        map[string]string
	streamPrefixWritten bool
	reasoningActive     bool

	statusActivity  string
	statusToolName  string
	statusToolArg   string
	statusToolCount int
	tmuxClient      *tmux.Client
	tmuxManager     *tmux.Manager
	tmuxEnv         *tmux.Environment
	tmuxMenuOpen    bool
	todoSnapshot    map[string]todoStateItem
	todoOrder       []string
	activeTodo      *todoStateItem
	activeMCPTools  map[string]ToolStatusMsg

	autoCompleteItems   []string
	autoCompleteIndex   int
	autoCompleteActive  bool
	autoCompleteKind    string
	autoCompleteWorkDir string
	inputHint           string
	hintsShownAt        time.Time
	startedAt           time.Time
	inputDrainUntil     time.Time
	inputReady          bool
	initPromptActive    bool
	lastResizeAt        time.Time
	sidebarVisible      bool

	exitConfirmPending   bool
	cancelConfirmPending bool
	lastEscPress         time.Time
	historySearch        historySearchState
	compactMode          bool
	pending              *pendingQueue
	sessionMu            *sync.Mutex

	persistedMsgCount    int
	projectMemoryLoading bool
	runCanceled          bool
	runFailed            bool
	runStartTime         time.Time
	subAgentsCanceling   bool
	lastUserSubmission   string
	crashResumeTask      string

	blindSpotRetries int

	activeAgentRunID      int
	activeShellRunID      int
	shellCommandSubmitter func(command string, addToHistory bool) tea.Cmd

	sessionLockSwitch func(newSessionID string)

	sessionCronSwitch func(newSessionID string)

	sessionUpdateCallback func(ses *session.Session)

	sessionCreatedCallback func(sessionID string)
	remoteInboundAdapter   string
	clipboardLoader        func() (imageAttachedMsg, error)
	clipboardWriter        func(string) error
	urlOpener              func(string) error
	webuiBridge            WebUIEventBroadcaster
	updateSvc              *update.Service
	updateInfo             update.CheckResult
	updateError            string
	systemPromptRebuilder  func() string

	tunnelHost                *agentruntime.TunnelHost
	tunnelSession             *tunnel.Session
	tunnelBroker              *tunnel.Broker
	tunnelMainStream          *tunnelMainStreamState
	tunnelShareBootstrap      *tunnelShareBootstrapState
	tunnelPendingApprovalID   string
	tunnelPendingAskUserID    string
	tunnelUserMessageOverride *tunnel.MessageData
	suppressNextTunnelSystem  string
	tunnelClientNoticeShown   bool

	pendingTunnelConnected bool
	tunnelSpawned          map[string]bool

	extPaneMgr *extpane.Manager

	cmdPaneMgr *cmdpane.Manager

	petEnabled  bool
	pet         *petState
	recoveryMsg string
}

type pendingSubmission struct {
	Text                  string
	Hidden                bool
	TunnelMessageOverride *tunnel.MessageData
	Images                []imageAttachedMsg

	RemoteOrigin bool
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
	Refresh(name string) (bool, plugin.RefreshOutcome, int)
	PendingOAuth() *plugin.MCPOAuthRequiredError
	ClearPendingOAuth(name string)
	Snapshot() []plugin.MCPServerInfo
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

type DiffConfirmMsg struct {
	FilePath string
	DiffText string
	Response chan bool
}

type AskUserMsg struct {
	Request  toolpkg.AskUserRequest
	Response chan toolpkg.AskUserResponse
}

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

func NewModel(a *agent.Agent, policy permission.PermissionPolicy) Model {
	ta := textarea.New()
	ta.Prompt = "❯ "
	ta.Placeholder = tr(LangEnglish, "input.placeholder")
	ta.Focus()
	ta.SetWidth(74)
	ta.SetHeight(1)
	ta.ShowLineNumbers = false

	ta.DynamicHeight = true
	ta.MinHeight = 1
	ta.MaxHeight = 10

	taStyles := textarea.DefaultStyles(true)

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
		imEnsure:               &imEnsureGuard{},
		knightTasks:            &knightCancelSet{},
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
		startedAt:              time.Time{},
		hintsShownAt:           time.Now(),
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
		costCache:              &sessionCostCache{},
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

func (m *Model) setLoading(val bool) {
	m.loading = val
	if val {
		m.runStartTime = time.Now()
	}
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
	state.mu.Unlock()

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

	go safego.Run("tui.cleanupTempImages", cleanupOldTempImages)
	m.oscIdle()

	cmds := []tea.Cmd{
		func() tea.Msg { return textarea.Blink() },
		func() tea.Msg { return tea.RequestWindowSize() },
		func() tea.Msg { return gitBranchTickMsg{} },

		func() tea.Msg { return usageSidebarRefreshMsg{} },
	}

	cmds = append(cmds, func() tea.Msg {
		workDir, _ := os.Getwd()
		if isHomeDir(workDir) {
			return initPromptCheckMsg{needsInit: false}
		}
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

func isHomeDir(dir string) bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	dir = filepath.Clean(dir)
	home = filepath.Clean(home)
	if dir == home {
		return true
	}

	if resolvedDir, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolvedDir
	}
	if resolvedHome, err := filepath.EvalSymlinks(home); err == nil {
		home = resolvedHome
	}
	return dir == home
}

func (m *Model) SetProgram(p *tea.Program) {
	m.program = p
	m.refreshIMRuntimeHooks()
	m.refreshCachedGitBranch()
}

func (m *Model) currentProbeKey() (string, bool) {
	res, err := m.config.ResolveActiveEndpoint()
	if err != nil || res.VendorID == "" || res.Model == "" {
		return "", false
	}
	return provider.MakeProbeKey(res.VendorID, res.BaseURL, res.Model), true
}

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
			if m.tuiSend != nil {
				m.tuiSend(contextProbeResultMsg{result: r})
				return
			}

			m.applyProbeResult(r)
		})
}

type contextProbeResultMsg struct {
	result provider.ProbeResult
}

func (m *Model) applyProbeResult(r provider.ProbeResult) {

	if cur, ok := m.currentProbeKey(); ok && cur != r.Key {
		debug.Log("probe", "dropping stale probe result key=%s (now %s)", r.Key, cur)
		return
	}
	if r.ContextWindow > 0 {
		debug.Log("probe", "applying context_window=%d fromCache=%v to agent",
			r.ContextWindow, r.FromCache)
		m.agent.ContextManager().SetContextWindow(r.ContextWindow)

		if m.session != nil && m.session.ContextWindow == 0 {
			m.session.ContextWindow = r.ContextWindow
			if m.sessionStore != nil {
				if err := m.sessionStore.AppendMetaToDisk(m.session); err != nil {
					debug.Log("tui", "contextWindow persist: %v", err)
				}
			}
		}
	} else {
		debug.Log("probe", "probe returned 0 (no result), keeping current context window setting")
	}
}

func (m *Model) SetCronScheduler(s *cron.Scheduler) {
	m.cronScheduler = s
}

func (m *Model) SetSessionCreatedCallback(cb func(sessionID string)) {
	m.sessionCreatedCallback = cb
}

func (m *Model) SetSession(ses *session.Session, store session.Store) {
	m.session = ses
	m.sessionStore = store

	if m.sessionUpdateCallback != nil {
		m.sessionUpdateCallback(ses)
	}

	if m.sessionCreatedCallback != nil && ses.ID != "" {
		m.sessionCreatedCallback(ses.ID)

	}

	if m.agent != nil {
		m.agent.SetSessionID(ses.ID)
	}

	m.persistedMsgCount = len(ses.Messages)
	m.usageTurnIndex = session.LastTurnIndex(ses)
	m.lastMetricDigestTurn = m.usageTurnIndex

	if ses.Vendor != "" && m.config != nil {
		m.config.Vendor = ses.Vendor
	}
	if ses.Endpoint != "" && m.config != nil {
		m.config.Endpoint = ses.Endpoint
	}
	if ses.Model != "" && m.config != nil {
		m.config.Model = ses.Model
	}

	if m.config != nil {
		m.setActiveRuntimeSelection(m.config.Vendor, m.config.Endpoint, m.config.Model)
	}

	if m.config != nil {
		if err := m.tryActivateCurrentSelection(); err != nil {
			debug.Log("repl", "SetSession: activate provider failed: %v", err)
		}
	}

	if m.agent != nil {
		if ses.ContextWindow > 0 {
			m.agent.ContextManager().SetContextWindow(ses.ContextWindow)
		}
		if ses.MaxTokens > 0 {
			m.agent.ContextManager().SetOutputReserve(ses.MaxTokens)
		}
	}

	if ses.EndpointUsage == nil || len(ses.EndpointUsage) == 0 {
		ses.RebuildEndpointStats()
	}

	if m.sessionStore != nil {
		if err := m.sessionStore.AppendMetaToDisk(m.session); err != nil {
			debug.Log("tui", "session restore meta persist: %v", err)
		}
	}

	if ses.ContextWindow <= 0 {
		m.startContextProbe()
	}

	m.bindTunnelProjectionSession()
	m.bindIMSession()
	m.announceTunnelActiveSession()

	if m.lanChatHub != nil && ses.ID != "" {
		m.lanChatHub.SetSessionID(
			filepath.Join(config.ConfigDir(), "lanchat"),
			ses.ID,
		)
	}

	if m.instanceDetect == nil {
		m.detectAndAutoMute()
	}
}

func (m *Model) Session() *session.Session {
	return m.session
}

func (m *Model) recordSessionUsage(usage provider.TokenUsage, source string) {
	if m.session == nil || m.sessionStore == nil {
		return
	}
	mu := m.sessionMutex()
	mu.Lock()
	if m.session == nil || m.sessionStore == nil {
		mu.Unlock()
		return
	}
	ses := m.session
	store := m.sessionStore

	store.WithSessionLocked(m.session, func() {
		m.session.TokenUsage = m.session.TokenUsage.Add(usage)
		m.session.AddUsageForEndpoint(m.session.Vendor, m.session.Endpoint, usage)
		m.session.UpdatedAt = time.Now()
	})

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
		Source:    source,
		Usage:     usage,
	}

	ses.AddUsageHistoryEntry(entry)
	mu.Unlock()

	safego.Go("session.usage", func() {
		if jsonlStore, ok := store.(*session.JSONLStore); ok {
			if err := jsonlStore.AppendMetaToDisk(ses); err != nil {
				debug.Log("tui", "async meta persist: %v", err)
			}
			if err := jsonlStore.AppendUsageEntry(ses, entry); err != nil {
				debug.Log("tui", "async usage persist (session=%s): %v", ses.ID, err)
			}
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

	if ev.Model == "" {

		ev.Model = m.activeModel
		ev.Vendor = m.activeVendor
		ev.Endpoint = m.activeEndpoint
	}
	if ev.Model == "" {
		ev.Model = m.session.Model
		ev.Vendor = m.session.Vendor
		ev.Endpoint = m.session.Endpoint
	}
	m.session.AppendMetricEvent(ev)
	m.session.AppendMetricForEndpoint(m.session.Vendor, m.session.Endpoint, ev)
	m.session.UpdatedAt = time.Now()
	ses := m.session
	store := m.sessionStore
	mu.Unlock()

	safego.Go("session.metric", func() {
		if jsonlStore, ok := store.(*session.JSONLStore); ok {
			if err := jsonlStore.AppendMetric(ses, ev); err != nil {
				debug.Log("tui", "async metric persist (session=%s): %v", ses.ID, err)
			}
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

		if im.HasWechatAdapter(mgr) {
			emitter.SetOutputMode(im.WechatDefaultOutputMode)
		}
	}
	m.storeIMRuntime(mgr, emitter, nil)
	m.refreshIMRuntimeHooks()
	m.bindIMSession()
	m.detectAndAutoMute()
}

func (m *Model) detectAndAutoMute() {
	if m.imManager == nil {
		return
	}

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

		m.startOwnedAdapters()
		return
	}

	if autoMuteCount > 0 {
		primary := others[0]
		msg := m.t("panel.im.message.auto_mute", autoMuteCount, primary.PID, primary.StartedAt.Format("15:04"))
		m.chatWriteSystem(nextSystemID(), msg)
	}

	m.startOwnedAdapters()
}

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

	if m.config != nil {
		snap := m.config.IMSnapshot()
		mgr := m.imManager
		m.imManager.SetOnRestart(func(adapterName string) error {
			return im.StartNamedAdapter(context.Background(), snap, adapterName, mgr)
		})
	}
}

func (m *Model) bindIMSession() {
	if m.imManager == nil {
		return
	}

	if m.session == nil {
		return
	}
	m.imManager.BindSession(im.SessionBinding{
		SessionID: m.session.ID,
		Workspace: m.session.Workspace,
	})
	if m.config != nil {
		adapters := make(map[string]bool)
		for name, cfg := range m.config.IMSnapshot().Adapters {
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

type panelEntry struct {
	name  string
	open  func() bool
	close func()
}

func (m *Model) panelRegistry() []panelEntry {
	return []panelEntry{
		{"modelPanel", func() bool { return m.modelPanel != nil }, m.closeModelPanel},
		{"hooksPanel", func() bool { return m.hooksPanel != nil }, m.closeHooksPanel},
		{"providerPanel", func() bool { return m.providerPanel != nil }, m.closeProviderPanel},
		{"tgPanel", func() bool { return m.tgPanel != nil }, m.closeTGPanel},
		{"qqPanel", func() bool { return m.qqPanel != nil }, m.closeQQPanel},
		{"pcPanel", func() bool { return m.pcPanel != nil }, m.closePCPanel},
		{"discordPanel", func() bool { return m.discordPanel != nil }, m.closeDiscordPanel},
		{"feishuPanel", func() bool { return m.feishuPanel != nil }, m.closeFeishuPanel},
		{"slackPanel", func() bool { return m.slackPanel != nil }, m.closeSlackPanel},
		{"dingtalkPanel", func() bool { return m.dingtalkPanel != nil }, m.closeDingtalkPanel},
		{"imPanel", func() bool { return m.imPanel != nil }, m.closeIMPanel},
		{"wechatPanel", func() bool { return m.wechatPanel != nil }, func() { m.closeWechatPanel(); m.closeIMPanel() }},
		{"wecomPanel", func() bool { return m.wecomPanel != nil }, func() { m.closeWeComPanel(); m.closeIMPanel() }},
		{"mattermostPanel", func() bool { return m.mattermostPanel != nil }, m.closeMattermostPanel},
		{"matrixPanel", func() bool { return m.matrixPanel != nil }, m.closeMatrixPanel},
		{"signalPanel", func() bool { return m.signalPanel != nil }, m.closeSignalPanel},
		{"ircPanel", func() bool { return m.ircPanel != nil }, m.closeIRCPanel},
		{"nostrPanel", func() bool { return m.nostrPanel != nil }, m.closeNostrPanel},
		{"twitchPanel", func() bool { return m.twitchPanel != nil }, m.closeTwitchPanel},
		{"whatsappPanel", func() bool { return m.whatsappPanel != nil }, m.closeWhatsAppPanel},
		{"mcpPanel", func() bool { return m.mcpPanel != nil }, m.closeMCPPanel},
		{"skillsPanel", func() bool { return m.skillsPanel != nil }, m.closeSkillsPanel},
		{"statsPanel", func() bool { return m.statsPanel != nil }, m.closeStatsPanel},
		{"usagePanel", func() bool { return m.usagePanel != nil }, func() { m.usagePanel = nil }},
		{"inspectorPanel", func() bool { return m.inspectorPanel != nil }, m.closeInspectorPanel},
		{"streamPanel", func() bool { return m.streamPanel != nil }, m.closeStreamPanel},
		{"knightPanel", func() bool { return m.knightPanel != nil }, m.closeKnightPanel},
		{"impersonatePanel", func() bool { return m.impersonatePanel != nil }, m.closeImpersonatePanel},
		{"lanChatPanel", func() bool { return m.lanChatPanel != nil }, m.closeLanChatPanel},
		{"langOptions", func() bool { return len(m.langOptions) > 0 }, func() { m.langOptions = nil }},
	}
}

func (m *Model) hasActivePanel() bool {
	for _, p := range m.panelRegistry() {
		if p.open() {
			return true
		}
	}
	return false
}

func (m *Model) closeActivePanel() bool {
	for _, p := range m.panelRegistry() {
		if p.open() {
			p.close()
			m.resetExitConfirm()
			m.resetCancelConfirm()
			return true
		}
	}
	return false
}

func (m *Model) SetMCPServers(servers []MCPInfo) {
	m.mcpServers = servers
	m.convergeMCPSelection()
}

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

		m.refreshIMRuntimeHooks()
		if resolved, err := cfg.ResolveActiveEndpoint(); err == nil && m.activeVendor == "" && m.activeEndpoint == "" && m.activeModel == "" {
			m.setActiveRuntimeSelection(resolved.VendorID, resolved.EndpointID, resolved.Model)
		}
		if cfg.FirstRun {
			m.openLanguageSelector(true)
		}
	}

	m.startContextProbe()
}

func (m *Model) SetSystemPromptRebuilder(fn func() string) {
	m.systemPromptRebuilder = fn
}

func (m *Model) rebuildSystemPrompt() {
	if m.systemPromptRebuilder == nil || m.agent == nil {
		return
	}
	newPrompt := m.systemPromptRebuilder()
	m.agent.UpdateSystemPrompt(newPrompt)
}

func (m *Model) setActiveRuntimeSelection(vendor, endpoint, model string) {
	vendor = strings.TrimSpace(vendor)
	endpoint = strings.TrimSpace(endpoint)

	if m.config != nil && vendor != "" {
		if _, ok := m.config.Vendors[vendor]; !ok && m.config.Vendors != nil {
			matches := []string{}
			for id, vc := range m.config.Vendors {
				if strings.TrimSpace(vc.DisplayName) == vendor || id == vendor {
					matches = append(matches, id)
				}
			}
			if len(matches) == 1 {
				vendor = matches[0]
			}
		}
	}

	if m.config != nil && endpoint != "" {
		if vc, ok := m.config.Vendors[vendor]; ok {
			if _, ok := vc.Endpoints[endpoint]; !ok && vc.Endpoints != nil {
				ematches := []string{}
				for id, ec := range vc.Endpoints {
					if strings.TrimSpace(ec.DisplayName) == endpoint || id == endpoint {
						ematches = append(ematches, id)
					}
				}
				if len(ematches) == 1 {
					endpoint = ematches[0]
				}
			}
		}
	}
	m.activeVendor = vendor
	m.activeEndpoint = strings.TrimSpace(endpoint)
	m.activeModel = strings.TrimSpace(model)

	m.sidebarUsage = nil

	if m.activeVendor != "" && m.activeEndpoint != "" {
		m.usageSidebarStatus = ""
		if m.usagePanel != nil {
			m.usagePanel.infos = map[string]*usage.UsageInfo{}
			m.usagePanel.errs = map[string]string{}
			m.usagePanel.expected = 0
			m.usagePanel.fetching = false
		}
		m.usageProbeDirty = true
	}

	if m.lanChatHub != nil {
		m.lanChatHub.SetModel(m.activeModel)
	}
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

func (m *Model) SetJobManager(jm *toolpkg.CommandJobManager) {
	m.jobManager = jm
}

func (m *Model) SetKnight(k *knight.Knight) {
	m.knight = k

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

func (m *Model) saveConfig() error {
	if m.config == nil {
		return fmt.Errorf("config is nil")
	}
	return m.config.SaveScoped(m.configSaveScope)
}

func (m *Model) toggleConfigSaveScope() string {
	if m.configSaveScope == "instance" {
		m.configSaveScope = "global"
		return m.t("config.save_scope_global")
	}

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

type imageAttachedMsg struct {
	placeholder string
	img         image.Image
	filename    string
	sourcePath  string
}

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

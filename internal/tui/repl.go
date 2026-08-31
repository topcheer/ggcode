package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"

	"github.com/topcheer/ggcode/internal/a2a"
	"github.com/topcheer/ggcode/internal/acpclient"
	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/checkpoint"
	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/cron"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/knight"
	"github.com/topcheer/ggcode/internal/lanchat"
	"github.com/topcheer/ggcode/internal/markdown"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/restart"
	"github.com/topcheer/ggcode/internal/runfile"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/swarm"
	"github.com/topcheer/ggcode/internal/task"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tui/cmdpane"
	"github.com/topcheer/ggcode/internal/update"
	"github.com/topcheer/ggcode/internal/webui"
)

// REPL connects the agent to the TUI model.
type REPL struct {
	// streamCoalescer bounds renders from all high-frequency stream
	// sources (tool progress, subagent tunnel text/reasoning). Created
	// once with the first stream producer registration.
	streamCoalescer     *toolProgressCoalescer
	model               Model
	agent               *agent.Agent
	program             *tea.Program
	programSend         func(tea.Msg)
	planSwitcher        *replModeSwitcher
	store               session.Store
	resumeID            string
	sessionLock         *session.SessionLock
	core                *agentruntime.InteractiveRuntimeCore
	mcpMgr              *plugin.MCPManager
	commandMgr          *commands.Manager
	skillsChangedHook   func()
	imManager           *im.Manager
	projectMemoryLoader func() (string, []string, error)
	systemPromptBuilder func(task, agentType string) string // builds rich prompt for sub-agents
	webuiAddr           string                              // webui listen address
	webuiToken          string                              // webui auth token, displayed in URL fragment
	knightStartupHint   string                              // one-time hint shown at startup (e.g. lock conflict)
	metricCollector     *metrics.Collector
	metricCancel        context.CancelFunc
	workingDir          string
	cfg                 *config.Config
	agentBusy           atomic.Bool
	preExecCleanup      func() // called before syscall.Exec restart/tmux-enter
	cronScheduler       *cron.Scheduler
	currentSessionMu    sync.RWMutex
	currentSession      *session.Session // thread-safe, updated by setCurrentSession
	// Port file management (issue #1189)
	initialSessionID string // session ID used for initial port file write (empty for new sessions)
	portFileMode     string // startup permission mode from config
}

// NewREPL creates a new REPL with optional permission policy.
func NewREPL(a *agent.Agent, policy permission.PermissionPolicy) *REPL {
	m := NewModel(a, policy)
	r := &REPL{
		model: m,
		agent: a,
	}
	// Share the agentBusy atomic between REPL and Model so that
	// /api/status can read the live busy state (Model.loading changes
	// happen inside the tea.Program which REPL doesn't see).
	r.model.agentBusy = &r.agentBusy
	if a != nil {
		a.SetUsageHandler(func(usage provider.TokenUsage) {
			r.recordSessionUsage(usage, a.UsageSource())
		})
		collectorCtx, collectorCancel := context.WithCancel(context.Background())
		r.metricCancel = collectorCancel
		r.metricCollector = metrics.NewCollector(collectorCtx, 256, func(ev metrics.MetricEvent) {
			r.recordMetric(ev)
		})
		a.SetMetricHandler(r.metricCollector.Emit)
		r.model.metricCollectorFlush = r.metricCollector.Flush
	}
	// Register callbacks so /clear and /sessions can release the old session
	// lock, acquire a new one, and rebind the cron scheduler.
	r.model.sessionLockSwitch = r.switchSessionLock
	r.model.sessionCronSwitch = r.switchSessionCron
	// Register session update callback so persistHandler/checkpointHandler
	// always see the correct session (r.model is a value snapshot, not live).
	r.model.sessionUpdateCallback = r.setCurrentSession
	return r
}

// SetSessionStore sets the session persistence store.
func (r *REPL) SetSessionStore(s session.Store) {
	r.store = s
}

func (r *REPL) SessionUsageHandler() func(provider.TokenUsage) {
	return func(usage provider.TokenUsage) { r.recordSessionUsage(usage, "subagent") }
}

// SetMCPServers passes MCP server info to the TUI model.
func (r *REPL) SetMCPServers(servers []MCPInfo) {
	r.model.SetMCPServers(servers)
}

// SetA2AHandler passes the A2A task handler so the sidebar can show remote tasks.
func (r *REPL) SetA2AHandler(h *a2a.TaskHandler) {
	r.model.SetA2AHandler(h)
}

// SetLanChatHub connects the LAN chat hub for /chat panel support and
// registers the lanchat tool so the agent can send messages, list
// participants, and manage approvals programmatically.
// RegisterCallbacks wires the TUI message-delivery callbacks into the agent.
// This must be called after both r.agent and r.program are set, regardless
// of whether LAN chat is configured. Previously these callbacks were inside
// SetLanChatHub, which meant they were never registered when lanchat was
// disabled — causing wait_command streaming and verify progress to silently
// not work.
func (r *REPL) RegisterCallbacks() {
	if r.agent == nil {
		return
	}
	// Use sendProgramMsgs which handles nil program gracefully.
	r.agent.SetVerifyCallbacks(
		func(text string) {
			r.sendProgramMsgs(verifyProgressMsg{text: text})
		},
		func(result agent.VerifyResult) {
			r.sendProgramMsgs(verifyResultMsg{result: result})
		},
	)
	// Coalesce tool-progress sends: latest-wins per toolID, flushed on a
	// 120ms ticker. Every direct program.Send of toolProgressMsg triggered
	// a full Bubble Tea Update+View on the main thread; with per-writer
	// 300ms throttling across stdout/stderr and N parallel tools, the
	// message rate saturated the render loop during command execution -
	// typing felt laggy and spinner animation stuttered. Bounding renders
	// to ~8/sec keeps live output smooth while leaving headroom for input.
	coalesce := &toolProgressCoalescer{
		send:       r.sendProgramMsgs,
		next:       make(map[string]toolProgressMsg),
		appendNext: make(map[string]subAgentTunnelStreamTextMsg),
		appendReas: make(map[string]subAgentTunnelReasoningMsg),
	}
	r.streamCoalescer = coalesce
	r.agent.SetToolProgressCallback(func(toolID, toolName, output string) {
		coalesce.stage(toolID, toolName, output)
	})
	safego.Go("tui.toolProgressCoalescer", coalesce.run)
}

// toolProgressCoalescer bounds main-thread renders from streaming tools.
//
// It supports two merge modes per key:
//   - replace (toolProgressMsg): latest snapshot wins - correct for
//     tail-style updates where the producer already holds the full body.
//   - append (tunnel stream text/reasoning): chunks concatenate in arrival
//     order - required because consumers append per message and producer
//     deltas are irreducible.
type toolProgressCoalescer struct {
	mu   sync.Mutex
	send func(msgs ...tea.Msg)
	next map[string]toolProgressMsg
	// appendNext accumulates tunnel chunks per agentID between flushes.
	appendNext map[string]subAgentTunnelStreamTextMsg
	appendReas map[string]subAgentTunnelReasoningMsg
}

func (c *toolProgressCoalescer) stage(toolID, toolName, output string) {
	c.mu.Lock()
	c.next[toolID] = toolProgressMsg{
		toolID:   toolID,
		toolName: toolName,
		output:   output,
	}
	c.mu.Unlock()
}

// stageTunnelText buffers a subagent tunnel text chunk for the next flush.
// Chunks for the same agent concatenate; N parallel streaming subagents
// collapse to at most N renders per flush tick instead of one per token.
func (c *toolProgressCoalescer) stageTunnelText(agentID, text string) {
	if agentID == "" || text == "" {
		return
	}
	c.mu.Lock()
	m := c.appendNext[agentID]
	m.AgentID = agentID
	m.Text += text
	c.appendNext[agentID] = m
	c.mu.Unlock()
}

// stageTunnelReasoning buffers a reasoning chunk, same append semantics.
func (c *toolProgressCoalescer) stageTunnelReasoning(agentID, text string) {
	if agentID == "" || text == "" {
		return
	}
	c.mu.Lock()
	m := c.appendReas[agentID]
	m.AgentID = agentID
	m.Text += text
	c.appendReas[agentID] = m
	c.mu.Unlock()
}

// run drains staged updates to the TUI at a fixed 120ms cadence. Runs for
// the REPL's lifetime; sends after the program quits are no-ops
// (sendProgramMsgs guards a nil program).
func (c *toolProgressCoalescer) run() {
	ticker := time.NewTicker(120 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		c.mu.Lock()
		if len(c.next) == 0 && len(c.appendNext) == 0 && len(c.appendReas) == 0 {
			c.mu.Unlock()
			continue
		}
		msgs := make([]tea.Msg, 0, len(c.next)+len(c.appendNext)+len(c.appendReas))
		for _, m := range c.next {
			msgs = append(msgs, m)
		}
		// Reasoning before text, mirroring buildBatchedStreamMessages:
		// the TUI expands the reasoning block first, then collapses it
		// when the text chunk arrives.
		for _, m := range c.appendReas {
			msgs = append(msgs, m)
		}
		for _, m := range c.appendNext {
			msgs = append(msgs, m)
		}
		c.next = make(map[string]toolProgressMsg)
		c.appendNext = make(map[string]subAgentTunnelStreamTextMsg)
		c.appendReas = make(map[string]subAgentTunnelReasoningMsg)
		c.mu.Unlock()
		c.send(msgs...)
	}
}

func (r *REPL) SetLanChatHub(hub *lanchat.Hub) {
	r.model.SetLanChatHub(hub, r.sendTUI)
	if r.agent != nil && hub != nil {
		tools := r.agent.ToolRegistry()
		if tools != nil {
			var lc config.LanChatConfig
			if r.cfg != nil {
				lc = r.cfg.LanChat
			}
			tools.Register(tool.NewLanChatTool(hub, lc))
		}
		// Inject dynamic lanchat peers info into the system prompt before
		// each run. Shows all online peers (busy + idle), with same-workspace
		// peers specially marked.
		hubCopy := hub
		wd := r.workingDir
		r.agent.SetSystemPromptInjector(func() string {
			return lanchat.FormatPeersInfo(hubCopy, wd)
		})
	}
	// Register auto-approve callback: inject the message into the TUI event
	// loop as a lanchatApprovalReqMsg so the existing approval→submit flow
	// handles it. This ensures "always approve" policy actually triggers the
	// agent — without this, auto-approved messages are silently dropped
	// because they skip the pendingApproval queue.
	if hub != nil {
		hub.SetOnAutoApprove(func(msg lanchat.Message) {
			r.sendTUI(lanchatAutoApproveMsg{msg: msg})
		})
	}
}

func (r *REPL) SetMCPManager(mgr *plugin.MCPManager) {
	r.mcpMgr = mgr
	r.model.SetMCPManager(mgr)
}

// SetCore stores the runtime core reference for unified background service management.
func (r *REPL) SetCore(core *agentruntime.InteractiveRuntimeCore) {
	r.core = core
	if r.model.tunnelHost != nil {
		r.model.tunnelHost.Close()
	}
	r.model.tunnelHost = core.Tunnel

	// Wire the code index manager from the tool registry to the agent so
	// that @ fuzzy file search (CompleteMention) can use it.
	// Index start happens in Run() after TUI is fully ready.
	if r.agent != nil && core.Registry != nil {
		if cim := core.Registry.CodeIndex(); cim != nil {
			r.agent.SetCodeIndexManager(cim)
		}
	}
}

// SetResumeID sets the session ID to resume.
func (r *REPL) SetResumeID(id string) {
	r.resumeID = id
}

// SetSessionLock passes a pre-acquired session lock to the REPL.
// The REPL will release it on shutdown or restart.
func (r *REPL) SetSessionLock(lock *session.SessionLock) {
	r.sessionLock = lock
}

// switchSessionLock releases the current session lock and acquires a new one
// for the given session ID. Called by Model.handleClearChat via callback.
func (r *REPL) switchSessionLock(newSessionID string) {
	// Acquire the new lock BEFORE releasing the old one. If acquisition fails
	// (session locked by another instance), we keep the old lock rather than
	// ending up with no lock at all.
	var newLock *session.SessionLock
	if storeDir, dirErr := session.DefaultDir(); dirErr == nil {
		if lock, lockErr := session.TryAcquireSessionLock(storeDir, newSessionID); lockErr == nil && lock != nil && lock.Acquired() {
			newLock = lock
		} else {
			debug.Log("repl", "switchSessionLock: could not acquire lock on session %s: %v", newSessionID, lockErr)
		}
	}
	if newLock != nil {
		if r.sessionLock != nil {
			r.sessionLock.Release()
		}
		r.sessionLock = newLock
		debug.Log("repl", "switchSessionLock: acquired lock on new session %s", newSessionID)
	}
}

// setCurrentSession updates the thread-safe current session pointer. Called
// by the Bubble Tea model whenever the session changes (via callback).
func (r *REPL) setCurrentSession(ses *session.Session) {
	r.currentSessionMu.Lock()
	r.currentSession = ses
	r.currentSessionMu.Unlock()
}

// getCurrentSession returns the thread-safe current session pointer.
// Used by persistHandler and checkpointHandler to get the LIVE session
// instead of the stale r.model snapshot.
func (r *REPL) getCurrentSession() *session.Session {
	r.currentSessionMu.RLock()
	defer r.currentSessionMu.RUnlock()
	return r.currentSession
}

func (r *REPL) switchSessionCron(newSessionID string) {
	r.switchSessionCronWithWorkspace(newSessionID, r.workingDir)
}

// switchSessionCronWithWorkspace rebinds the cron scheduler to a new session,
// using the provided workspaceDir for migration. This is used when switching
// sessions, including cross-workspace switches where the workspace may differ
// from r.workingDir.
func (r *REPL) switchSessionCronWithWorkspace(newSessionID, workspaceDir string) {
	if r.cronScheduler != nil {
		sessionPath, legacyPath := agentruntime.CronStorePaths(newSessionID)
		r.cronScheduler.SwitchSession(sessionPath, legacyPath, workspaceDir)
		debug.Log("repl", "switchSessionCron: rebound cron scheduler to session %s (workspace=%s)", newSessionID, workspaceDir)
	}
}

// SetPreExecCleanup registers a cleanup function called before syscall.Exec
// (restart / tmux-enter). Since syscall.Exec replaces the process image,
// deferred cleanups never fire — this hook ensures port files are removed.
func (r *REPL) SetPreExecCleanup(fn func()) {
	r.preExecCleanup = fn
}

// SetConfig passes the config to the model for /model and /provider commands.
func (r *REPL) SetConfig(cfg *config.Config) {
	r.cfg = cfg
	r.model.SetConfig(cfg)
}

// SetWorkingDir stores the workspace path for RuntimeStatus reporting.
func (r *REPL) SetWorkingDir(wd string) {
	r.workingDir = wd
}

// OnConfigProviderChanged is called by the config tool after a provider change.
// It sends a Bubble Tea message to update the TUI state and triggers background tasks.
func (r *REPL) OnConfigProviderChanged() {
	if r.model.config == nil {
		return
	}
	// Probe real context window in background (safe to call from any goroutine)
	r.model.startContextProbe()
	// Trigger Bubble Tea re-render so status bar and terminal title update
	r.sendTUI(providerChangedMsg{})
}

// providerChangedMsg triggers a UI refresh after config tool changes the provider.
type providerChangedMsg struct{}

// systemMsg is a tea.Msg that displays a system message in the chat list.
type systemMsg struct {
	msg string
}

func (r *REPL) sendTUI(msg tea.Msg) {
	if r.programSend != nil {
		r.programSend(msg)
	} else if r.program != nil {
		r.program.Send(msg)
	}
}

func (r *REPL) SetPluginManager(mgr *plugin.Manager) {
	r.model.SetPluginManager(mgr)
}

func (r *REPL) SetUpdateService(svc *update.Service) {
	r.model.SetUpdateService(svc)
}

func (r *REPL) SetCommandsManager(mgr *commands.Manager) {
	r.commandMgr = mgr
	r.model.SetCommandsManager(mgr)
}

func (r *REPL) SetSkillsChangedHook(hook func()) {
	r.skillsChangedHook = hook
}

// SetRuntimeStatusProvider injects the runtime status provider into the
// runtime tool so the LLM can query session ID, IM adapters, mobile status, etc.
func (r *REPL) SetRuntimeStatusProvider() {
	if r.agent != nil {
		if tools := r.agent.ToolRegistry(); tools != nil {
			if t, ok := tools.Get("runtime"); ok {
				if rt, ok := t.(tool.RuntimeTool); ok {
					rt.Provider = &tuiRuntimeProvider{repl: r}
					tools.Unregister("runtime")
					tools.Register(rt)
				}
			}
		}
	}
}

// tuiRuntimeProvider implements tool.RuntimeStatusProvider for the TUI.
type tuiRuntimeProvider struct {
	repl *REPL
}

func (p *tuiRuntimeProvider) RuntimeSessionID() string {
	if p.repl.model.session != nil {
		return p.repl.model.session.ID
	}
	return ""
}

func (p *tuiRuntimeProvider) RuntimePermissionMode() string {
	return p.repl.model.mode.String()
}

func (p *tuiRuntimeProvider) RuntimeVendor() string {
	if p.repl.cfg != nil {
		return p.repl.cfg.Vendor
	}
	return ""
}

func (p *tuiRuntimeProvider) RuntimeEndpoint() string {
	if p.repl.cfg != nil {
		return p.repl.cfg.Endpoint
	}
	return ""
}

func (p *tuiRuntimeProvider) RuntimeModel() string {
	if p.repl.cfg != nil {
		return p.repl.cfg.Model
	}
	return ""
}

func (p *tuiRuntimeProvider) RuntimeLanguage() string {
	if p.repl.cfg != nil {
		return p.repl.cfg.Language
	}
	return ""
}

func (p *tuiRuntimeProvider) RuntimeContextWindow() int {
	if p.repl.model.agent != nil && p.repl.model.agent.ContextManager() != nil {
		return p.repl.model.agent.ContextManager().ContextWindow()
	}
	return 0
}

func (p *tuiRuntimeProvider) RuntimeMaxTokens() int {
	if p.repl.model.agent != nil && p.repl.model.agent.ContextManager() != nil {
		return p.repl.model.agent.ContextManager().OutputReserve()
	}
	return 0
}

func (p *tuiRuntimeProvider) RuntimeIMAdapters() []tool.RuntimeIMAdapterInfo {
	if p.repl.model.imManager == nil {
		return nil
	}
	snap := p.repl.model.imManager.Snapshot()
	// Build adapter name → channel map from bindings
	channels := make(map[string]string)
	for _, b := range snap.CurrentBindings {
		channels[b.Adapter] = b.ChannelID
	}
	var result []tool.RuntimeIMAdapterInfo
	for _, a := range snap.Adapters {
		result = append(result, tool.RuntimeIMAdapterInfo{
			Name:     a.Name,
			Platform: string(a.Platform),
			Online:   a.Healthy,
			Muted:    a.Status == "muted",
			Channel:  channels[a.Name],
		})
	}
	return result
}

func (p *tuiRuntimeProvider) RuntimeMobile() tool.RuntimeMobileInfo {
	var info tool.RuntimeMobileInfo
	// Query tunnelHost directly as the authoritative source (matches desktop
	// implementation). The Model-level tunnelSession/tunnelBroker fields can
	// become stale after relay reconnection or restart, causing false
	// "not connected" reports even when the tunnel is alive.
	if host := p.repl.model.tunnelHost; host != nil {
		if broker := host.OnlineBroker(); broker != nil {
			info.Connected = broker.SessionID() != ""
			info.SessionID = broker.SessionID()
		}
		if shareInfo := host.GetShareInfo(); shareInfo != nil {
			info.RelayURL = shareInfo.ConnectURL
			info.ConnectCode = shareInfo.RoomID
		}
		return info
	}
	// Fallback: Model-level fields (used before tunnelHost is initialized).
	if p.repl.model.tunnelSession != nil {
		info.Connected = p.repl.model.tunnelBroker != nil && p.repl.model.tunnelBroker.SessionID() != ""
		if ti := p.repl.model.tunnelSession.Info(); ti != nil {
			info.RelayURL = ti.ConnectURL
			info.ConnectCode = ti.RoomID
		}
		if p.repl.model.tunnelBroker != nil {
			info.SessionID = p.repl.model.tunnelBroker.SessionID()
		}
	}
	return info
}

func (r *REPL) SetIMManager(mgr *im.Manager) {
	r.imManager = mgr
	r.model.SetIMManager(mgr)
	if mgr != nil {
		mgr.SetBridge(newTUIIMBridge(func() *tea.Program { return r.program }))
		// Inject IM manager into the tool so LLM can manage adapters
		if r.agent != nil {
			if tools := r.agent.ToolRegistry(); tools != nil {
				if t, ok := tools.Get("im"); ok {
					if imTool, ok := t.(tool.IMTool); ok {
						imTool.Manager = im.NewToolManagerAdapter(mgr)
						tools.Unregister("im")
						tools.Register(imTool)
					}
				}
			}
		}
	}
}

func (r *REPL) SetAutoMemory(am *memory.AutoMemory) {
	r.model.SetAutoMemory(am)
}

func (r *REPL) SetKnight(k *knight.Knight) {
	r.model.SetKnight(k)
}

// SetKnightStartupHint sets a one-time hint to show in the chat area at startup.
func (r *REPL) SetKnightStartupHint(hint string) {
	r.knightStartupHint = hint
}

// SetWebUIBridge sets the webui event broadcaster for forwarding agent
// events to webchat subscribers.
func (r *REPL) SetWebUIBridge(b WebUIEventBroadcaster) {
	r.model.webuiBridge = b
}

// InjectWebchatMessage sends a webchat user message into the TUI event loop.
// The message is handled like a normal user input — if the agent is idle,
// it starts a new run; if busy, it queues as a pending interruption.
func (r *REPL) InjectWebchatMessage(text string) {
	if r.program != nil {
		r.program.Send(webchatUserMsg{Text: text})
	}
}

// InjectRestart triggers a clean restart via the Bubble Tea event loop.
// This is the same mechanism used by IM /restart and the TUI /restart slash command.
func (r *REPL) InjectRestart() {
	if r.program != nil {
		// Explicit user/desktop action (#374).
		r.program.Send(remoteRestartMsg{explicit: true})
	}
}

func (r *REPL) recordSessionUsage(usage provider.TokenUsage, source string) {
	if r.program != nil {
		r.program.Send(sessionUsageMsg{Usage: usage, Source: source})
		return
	}
	r.model.recordSessionUsage(usage, source)
}

// recordMetric persists a metric event to the session JSONL.
// Called by the metrics collector goroutine (async, non-blocking for agent).
func (r *REPL) recordMetric(ev metrics.MetricEvent) {
	if r.program != nil {
		r.program.Send(sessionMetricMsg{Metric: ev})
		return
	}
	r.model.recordSessionMetric(ev)
}

// SetWebUIReadyAddr stores the webui address and auth token to be displayed
// in the TUI after startup. The actual program.Send happens in the startup
// goroutine alongside logoMsg to ensure the TUI is ready.
func (r *REPL) SetWebUIReadyAddr(addr, token string) {
	r.webuiAddr = addr
	r.webuiToken = token
}

// SetInitialSessionID sets the session ID used for the initial port file write
// (issue #1189). For new sessions this is empty; for resumed sessions it's the
// actual ID. Used to track which file to remove on exit.
func (r *REPL) SetInitialSessionID(sessionID, mode string) {
	r.currentSessionMu.Lock()
	r.initialSessionID = sessionID
	r.portFileMode = mode
	r.currentSessionMu.Unlock()
}

// CurrentSessionID returns the session ID currently tracked for port file cleanup.
// For new sessions, this updates from empty to the real ID when the session is created.
func (r *REPL) CurrentSessionID() string {
	r.currentSessionMu.RLock()
	id := r.initialSessionID
	r.currentSessionMu.RUnlock()
	return id
}

// SetSessionCreatedCallback registers a callback that fires when the first real session
// ID is set (issue #1189). The callback is invoked with the new session ID.
func (r *REPL) SetSessionCreatedCallback(cb func(sessionID string)) {
	r.model.SetSessionCreatedCallback(cb)
}

// HandleSessionCreated is the callback implementation that rewrites the port file
// when a real session ID is set (issue #1189).
func (r *REPL) HandleSessionCreated(sessionID string) {
	r.onSessionCreated(sessionID)
}

// onSessionCreated is called when the first real session ID is set (issue #1189).
// It rewrites the port file with the actual session ID, replacing the placeholder
// written at startup. It also updates the cleanup key to match.
func (r *REPL) onSessionCreated(sessionID string) {
	if r.webuiAddr == "" {
		// No WebUI started, nothing to rewrite
		return
	}
	// initialSessionID is written by SetInitialSessionID (startup goroutine)
	// and read here from the session-callback goroutine: guard with the same
	// mutex as the rest of the current-session state.
	r.currentSessionMu.RLock()
	initial := r.initialSessionID
	r.currentSessionMu.RUnlock()
	if initial != "" && initial != sessionID {
		// For resumed sessions, the initial ID was already correct
		return
	}
	if initial == "" {
		// Placeholder case: remove any stale __new__ file (legacy safety)
		runfile.Remove("__new__")
	}
	// Write the real port file
	pf := runfile.PortFile{
		Addr:      r.webuiAddr,
		Token:     r.webuiToken,
		PID:       os.Getpid(),
		SessionID: sessionID,
		Workspace: r.workingDir,
		Mode:      r.portFileMode,
	}
	if err := runfile.Write(pf); err != nil {
		debug.Log("repl", "onSessionCreated: failed to write port file for session %s: %v", sessionID, err)
	} else {
		debug.Log("repl", "onSessionCreated: wrote port file for session %s (replacing placeholder)", sessionID)
	}
	// Update the cleanup key so defer removes the correct file
	r.currentSessionMu.Lock()
	r.initialSessionID = sessionID
	r.currentSessionMu.Unlock()
}

// SetSystemPromptRebuilder sets a callback that rebuilds the full system prompt
// when skills or other dynamic parts change.
func (r *REPL) SetSystemPromptRebuilder(fn func() string) {
	r.model.SetSystemPromptRebuilder(fn)
}

func (r *REPL) SetProjectMemoryFiles(files []string) {
	r.model.SetProjectMemoryFiles(files)
}

func (r *REPL) SetProjectMemoryLoader(loader func() (string, []string, error)) {
	r.projectMemoryLoader = loader
	r.model.SetProjectMemoryLoading(loader != nil)
}

func (r *REPL) SetAutoMemoryFiles(files []string) {
	r.model.SetAutoMemoryFiles(files)
}

// SetCheckpointManager wires the checkpoint manager into the agent and REPL.
func (r *REPL) SetCheckpointManager(m *checkpoint.Manager) {
	r.agent.SetCheckpointManager(m)
	r.agent.SetDiffConfirm(func(ctx context.Context, filePath, diffText string) bool {
		return r.requestDiffConfirm(ctx, filePath, diffText)
	})
}

// SetSystemPromptBuilder sets the function used to build rich system prompts for sub-agents.
// Must be called before SetSubAgentManager.
func (r *REPL) SetSystemPromptBuilder(fn func(task, agentType string) string) {
	r.systemPromptBuilder = fn
}

// SetSubAgentManager wires the sub-agent manager and registers sub-agent tools.
func (r *REPL) SetSubAgentManager(mgr *subagent.Manager, prov provider.Provider, tools *tool.Registry) {
	r.model.SetSubAgentManager(mgr)

	factory := func(prov provider.Provider, t interface{}, systemPrompt string, maxTurns int) subagent.AgentRunner {
		return agent.NewAgent(prov, t.(*tool.Registry), systemPrompt, maxTurns)
	}

	providerGetter := func() provider.Provider {
		if r.model.agent != nil {
			return r.model.agent.Provider()
		}
		return prov
	}
	availableModelsGetter := func() []string {
		cfg := r.model.config
		if cfg == nil {
			return nil
		}
		if vc, ok := cfg.Vendors[cfg.Vendor]; ok {
			if ep, ok := vc.Endpoints[cfg.Endpoint]; ok {
				return ep.Models
			}
		}
		return nil
	}
	// Let spawn_agent tool labels show the effective model even when the LLM
	// omits the model param (the sub-agent inherits the parent's runtime
	// model). Mirrors the displayModel fallback in SpawnAgentTool.Execute.
	agentRef := r.model.agent
	spawnAgentModelResolver = func() string {
		if agentRef == nil {
			return ""
		}
		if prov := agentRef.Provider(); prov != nil {
			if mp, ok := prov.(provider.ModelNameProvider); ok {
				return mp.ModelName()
			}
		}
		return ""
	}
	tools.Register(tool.SpawnAgentTool{
		Manager:             mgr,
		Provider:            prov,
		ProviderGetter:      providerGetter,
		AvailableModels:     availableModelsGetter,
		Tools:               tools,
		AgentFactory:        factory,
		WorkingDir:          r.model.agent.WorkingDir(),
		OnUsage:             func(usage provider.TokenUsage) { r.recordSessionUsage(usage, "subagent") },
		SystemPromptBuilder: r.systemPromptBuilder,
	})
	tools.Register(tool.WaitAgentTool{Manager: mgr})
	tools.Register(tool.ListAgentsTool{Manager: mgr})
	tools.Register(tool.CancelAgentTool{Manager: mgr})

	// Named subagent templates (persisted per-workspace)
	tmplStore := subagent.NewTemplateStore(r.model.agent.WorkingDir())
	// Wire the named agent model resolver so tool call labels show the
	// template's configured model (e.g. "cron-runner [glm-4.5-air]").
	namedAgentModelResolver = func(name string) string {
		tmpl, err := tmplStore.Load(name)
		if err != nil {
			return ""
		}
		return tmpl.Model
	}
	debug.Log("tui", "SetSubAgentManager: registering named agent tools, workingDir=%s", r.model.agent.WorkingDir())
	if err := tools.Register(tool.CreateNamedAgentTool{Store: tmplStore}); err != nil {
		debug.Log("tui", "Register create_namedagent FAILED: %v", err)
	}
	if err := tools.Register(tool.DeleteNamedAgentTool{Store: tmplStore}); err != nil {
		debug.Log("tui", "Register delete_namedagent FAILED: %v", err)
	}
	if err := tools.Register(tool.ListNamedAgentTool{Store: tmplStore}); err != nil {
		debug.Log("tui", "Register list_namedagent FAILED: %v", err)
	}
	tools.Register(tool.UseNamedAgentTool{
		Store:               tmplStore,
		Manager:             mgr,
		Provider:            prov,
		ProviderGetter:      providerGetter,
		AvailableModels:     availableModelsGetter,
		Tools:               tools,
		AgentFactory:        factory,
		WorkingDir:          r.model.agent.WorkingDir(),
		OnUsage:             func(usage provider.TokenUsage) { r.recordSessionUsage(usage, "subagent") },
		SystemPromptBuilder: r.systemPromptBuilder,
	})

	// Notify TUI on live updates and completion.
	mgr.SetOnUpdate(func(sa *subagent.SubAgent) {
		r.sendProgramMsgs(subAgentUpdateMsg{AgentID: sa.ID})
	})
	mgr.SetOnComplete(func(sa *subagent.SubAgent) {
		r.sendProgramMsgs(
			subAgentUpdateMsg{AgentID: sa.ID},
			subAgentDoneMsg{
				AgentID:   sa.ID,
				AgentName: sa.Name,
				IsError:   sa.Status == subagent.StatusFailed,
				Kind:      "subagent",
			},
		)
	})
	// Subagent tunnel streams bypass the 80ms batch ticker that the main
	// assistant stream gets in submit.go - each provider delta went
	// straight to program.Send (one full Update+View per token). With N
	// parallel streaming subagents this was an unbounded render rate and
	// the second major source of TUI jank. Route through the coalescer:
	// chunks append per agentID, flushed at the coalescer's 120ms cadence.
	if r.streamCoalescer == nil {
		r.streamCoalescer = &toolProgressCoalescer{
			send:       r.sendProgramMsgs,
			next:       make(map[string]toolProgressMsg),
			appendNext: make(map[string]subAgentTunnelStreamTextMsg),
			appendReas: make(map[string]subAgentTunnelReasoningMsg),
		}
		safego.Go("tui.toolProgressCoalescer", r.streamCoalescer.run)
	}
	mgr.SetOnStreamText(func(agentID, text string) {
		r.streamCoalescer.stageTunnelText(agentID, text)
	})
	mgr.SetOnReasoning(func(agentID, text string) {
		r.streamCoalescer.stageTunnelReasoning(agentID, text)
	})
	mgr.SetOnToolCall(func(agentID, toolID, toolName, displayName, args, detail string) {
		r.sendProgramMsgs(subAgentTunnelToolCallMsg{
			AgentID:     agentID,
			ToolID:      toolID,
			ToolName:    toolName,
			DisplayName: displayName,
			Args:        args,
			Detail:      detail,
		})
	})
	mgr.SetOnToolResult(func(agentID, toolID, toolName, displayName, detail, result string, isError bool) {
		r.sendProgramMsgs(subAgentTunnelToolResultMsg{
			AgentID:     agentID,
			ToolID:      toolID,
			ToolName:    toolName,
			DisplayName: displayName,
			Detail:      detail,
			Result:      result,
			IsError:     isError,
		})
	})

	// Forward sub-agent system events (retry, compaction) to the main panel
	// as system messages, matching how the main agent displays them.
	mgr.SetOnSystem(func(agentID, text string) {
		r.sendProgramMsgs(subAgentSystemMsg{AgentID: agentID, Text: text})
	})

	// Start the background ticker that flushes accumulated stream
	// text/reasoning chunks at ~12.5 Hz instead of per-token (~50-100 Hz
	// per agent). Without this, 2+ concurrent sub-agents flood Bubble Tea's
	// event loop with 200-400 messages/second, causing severe TUI stutter.
	mgr.StartStreamBatcher()
}

// SetTaskManager wires the task manager and registers task tools.
func (r *REPL) SetTaskManager(mgr *task.Manager, tools *tool.Registry) {
	tools.Register(tool.TaskCreateTool{Manager: mgr})
	tools.Register(tool.TaskGetTool{Manager: mgr})
	tools.Register(tool.TaskListTool{Manager: mgr})
	tools.Register(tool.TaskUpdateTool{Manager: mgr})
	tools.Register(tool.TaskStopTool{Manager: mgr})
}

// SetTaskOutputTool registers the task_output tool for reading sub-agent results.
func (r *REPL) SetTaskOutputTool(mgr *subagent.Manager, tools *tool.Registry) {
	tools.Register(tool.TaskOutputTool{Provider: mgr})
}

// SetCronScheduler wires the cron scheduler and registers cron tools.
func (r *REPL) SetCronScheduler(s *cron.Scheduler, tools *tool.Registry) {
	r.cronScheduler = s
	r.model.SetCronScheduler(s)
	s.SetEnqueue(func(prompt string, queueIfBusy bool) {
		if r.program != nil {
			r.program.Send(cronPromptMsg{Prompt: prompt, QueueIfBusy: queueIfBusy})
		}
	})
	tools.Register(tool.CronCreateTool{Scheduler: s})
	tools.Register(tool.CronDeleteTool{Scheduler: s})
	tools.Register(tool.CronListTool{Scheduler: s})
	tools.Register(tool.CronUpdateTool{Scheduler: s})
	tools.Register(tool.CronPauseTool{Scheduler: s})
	tools.Register(tool.CronResumeTool{Scheduler: s})
	tools.Register(tool.CronGetTool{Scheduler: s})
}

// RuntimeStatus returns current runtime state for external monitoring
// via the WebUI /api/status endpoint.
func (r *REPL) RuntimeStatus() webui.RuntimeStatus {
	m := webui.RuntimeStatus{
		PID:            os.Getpid(),
		Workspace:      r.workingDir,
		AgentBusy:      r.agentBusy.Load(),
		PermissionMode: r.model.mode.String(),
	}
	if r.cfg != nil {
		m.Vendor = r.cfg.Vendor
		m.Endpoint = r.cfg.Endpoint
		m.Model = r.cfg.Model
		m.Language = r.cfg.Language
	}
	// Use getCurrentSession for the live session, not the stale r.model snapshot.
	ses := r.getCurrentSession()
	if ses != nil {
		m.Workspace = ses.Workspace
		m.PermissionMode = ses.PermissionMode
	}
	// agent is a pointer field — even in the stale r.model copy, it points
	// to the same agent object (agent is never replaced after init).
	if r.model.agent != nil && r.model.agent.ContextManager() != nil {
		m.ContextWindow = r.model.agent.ContextManager().ContextWindow()
		m.MaxTokens = r.model.agent.ContextManager().OutputReserve()
	}

	// IM adapter status
	if r.model.imManager != nil {
		snap := r.model.imManager.Snapshot()
		for _, a := range snap.Adapters {
			m.IMAdapters = append(m.IMAdapters, webui.IMAdapterInfo{
				Name:    a.Name,
				Type:    string(a.Platform),
				Online:  a.Healthy,
				Muted:   a.Status == "muted",
				Channel: a.ContactURI,
			})
		}
	}

	// Mobile tunnel connection status — query tunnelHost directly (authoritative).
	if host := r.model.tunnelHost; host != nil {
		if broker := host.OnlineBroker(); broker != nil {
			m.MobileConn.Connected = broker.SessionID() != ""
			m.MobileConn.SessionID = broker.SessionID()
		}
		if shareInfo := host.GetShareInfo(); shareInfo != nil {
			m.MobileConn.RelayURL = shareInfo.ConnectURL
			m.MobileConn.ConnectCode = shareInfo.RoomID
		}
	} else if r.model.tunnelSession != nil {
		m.MobileConn.Connected = r.model.tunnelBroker != nil && r.model.tunnelBroker.SessionID() != ""
		if info := r.model.tunnelSession.Info(); info != nil {
			m.MobileConn.RelayURL = info.ConnectURL
			m.MobileConn.ConnectCode = info.RoomID
		}
		if r.model.tunnelBroker != nil {
			m.MobileConn.SessionID = r.model.tunnelBroker.SessionID()
		}
	}

	return m
}

// SetPlanModeTools registers plan mode tools with a mode switcher that
// updates both the Model's mode and the ConfigPolicy. The switcher
// remembers the previous mode so exit_plan_mode can restore it.
// updates both the Model's mode and the ConfigPolicy. The switcher
// remembers the previous mode so exit_plan_mode can restore it.
func (r *REPL) SetPlanModeTools(tools *tool.Registry) {
	switcher := &replModeSwitcher{model: &r.model}
	r.planSwitcher = switcher
	tools.Register(tool.EnterPlanModeTool{Switcher: switcher})
	tools.Register(tool.ExitPlanModeTool{Switcher: switcher, DefaultMode: permission.SupervisedMode})

	// Inject the same switcher into the already-registered switch_mode tool
	// so that LLM-initiated mode changes also update the TUI badge.
	if sm, ok := tools.Get("switch_mode"); ok {
		if smt, ok := sm.(*tool.SwitchModeTool); ok {
			smt.SetSwitcher(switcher)
		}
	}

	// Inject the restart requester so the LLM restart tool reuses the
	// /restart machinery (session-preserving exec restart). The requester
	// ARMS a pending restart via program.Send (never mutates Model directly —
	// Bubble Tea models are value-copied, a direct write from the tool
	// goroutine lands on a stale copy). armRestartMsg handling in model_update
	// defers the quit until the current agent turn finishes (sibling tool
	// results and trailing assistant text are persisted first), with a timeout
	// fallback (#347).
	if rt, ok := tools.Get("restart"); ok {
		if rtt, ok := rt.(*tool.RestartTool); ok {
			rtt.Requester = &replRestartRequester{repl: r}
		}
	}
}

// replRestartRequester adapts the restart tool to the SAME proven path used
// by IM /restart (QQ/Telegram/etc) and the TUI /restart slash command:
// program.Send(...) → quit flags → tea.Quit → Run() returns → execRestart
// (session-preserving execve).
//
// Turn-awareness (#347): instead of a fixed 1s timer that execs mid-turn and
// loses sibling tool results / trailing assistant text, the requester sends
// armRestartMsg immediately. The model then defers the actual quit until the
// agent turn completes (handleAgentDoneMsg / handleDoneMsg fire the pending
// restart after persistFullSessionMessages), with a 30s fallback timer that
// force-restarts if the turn hangs.
type replRestartRequester struct {
	repl *REPL
}

func (rr *replRestartRequester) RequestRestart(debugMode bool) {
	debug.Log("restart", "agent-requested restart armed (turn-aware, fires at turn end or 30s fallback)")
	if debugMode {
		os.Setenv("GGCODE_DEBUG", "1")
	}
	// Arm the restart. The tool result is persisted synchronously in the agent
	// loop before this goroutine runs, so arming immediately is safe.
	go safego.Run("tui.restart.arm", func() {
		rr.repl.sendProgramMsgs(armRestartMsg{debug: debugMode})
	})
}

// SetSendMessageTool registers the send_message tool for agent communication.
func (r *REPL) SetSendMessageTool(mgr *subagent.Manager, tools *tool.Registry) {
	tools.Register(tool.SendMessageTool{Manager: mgr})
}

// SetACPClientManager wires the ACP client manager for clean shutdown.
func (r *REPL) SetACPClientManager(mgr *acpclient.ClientManager) {
	r.model.acpClientMgr = mgr
	if mgr == nil {
		return
	}
	mgr.SetApprovalHandler(func(ctx context.Context, toolName string, input string) permission.Decision {
		if r.program == nil {
			return permission.Deny
		}
		resp := make(chan permission.Decision, 1)
		r.program.Send(ApprovalMsg{
			ToolName: toolName,
			Input:    input,
			Response: resp,
		})
		select {
		case d := <-resp:
			return d
		case <-ctx.Done():
			return permission.Deny
		}
	})
}

// SetSwarmManager wires the swarm manager and registers swarm tools.
func (r *REPL) SetSwarmManager(mgr *swarm.Manager, tools *tool.Registry) {
	r.model.swarmMgr = mgr

	tools.Register(tool.TeamCreateTool{Manager: mgr})
	tools.Register(tool.TeamDeleteTool{Manager: mgr})
	tools.Register(tool.TeammateSpawnTool{Manager: mgr})
	tools.Register(tool.TeammateListTool{Manager: mgr})
	tools.Register(tool.TeammateShutdownTool{Manager: mgr})
	tools.Register(tool.TeammateResultsTool{Manager: mgr})
	tools.Register(tool.SwarmTaskCreateTool{Manager: mgr})
	tools.Register(tool.SwarmTaskListTool{Manager: mgr})
	tools.Register(tool.SwarmTaskClaimTool{Manager: mgr})
	tools.Register(tool.SwarmTaskCompleteTool{Manager: mgr})

	// Re-register send_message with SwarmMgr so it can route to swarm teammates.
	tools.Unregister("send_message")
	tools.Register(tool.SendMessageTool{Manager: r.model.subAgentMgr, SwarmMgr: mgr})

	// Notify TUI on swarm state changes.
	// teammate_text events are high-frequency (one per streaming token).
	// We throttle them to ~2 Hz per teammate to avoid flooding Bubble Tea's
	// event loop with messages that trigger expensive snapshot operations.
	// Status-change events (tool_call, idle, etc.) are sent immediately.
	swarmTextThrottle := newTextThrottleMap(500 * time.Millisecond)

	mgr.SetOnUpdate(func(ev swarm.Event) {
		if r.program == nil && r.programSend == nil {
			return
		}
		msgs := []tea.Msg{swarmTunnelEventMsg{Event: ev}}
		switch ev.Type {
		case "teammate_text":
			// Throttle: at most one subAgentUpdateMsg per teammate per 500ms.
			if !swarmTextThrottle.Allow(ev.TeammateID) {
				r.sendProgramMsgs(msgs...)
				return
			}
			msgs = append(msgs, subAgentUpdateMsg{AgentID: ev.TeammateID})
		case "teammate_idle":
			if ev.Result != "" {
				msgs = append(msgs,
					subAgentUpdateMsg{AgentID: ev.TeammateID},
					subAgentDoneMsg{
						AgentID:   ev.TeammateID,
						AgentName: ev.TeammateName,
						IsError:   ev.Error != nil,
						Kind:      "teammate",
					},
				)
			}
		case "teammate_spawned", "teammate_working", "teammate_shutdown",
			"teammate_tool_call", "teammate_tool_result", "teammate_error":
			// Status-change events: send immediately so strip updates promptly.
			msgs = append(msgs, subAgentUpdateMsg{AgentID: ev.TeammateID})
		}
		r.sendProgramMsgs(msgs...)
	})

	// Start the background ticker that flushes accumulated teammate
	// text/reasoning at ~12.5 Hz instead of per-token (~50-100 Hz
	// per teammate). Same pattern as sub-agent stream batching.
	mgr.StartStreamBatcher()
}

func (r *REPL) sendProgramMsgs(msgs ...tea.Msg) {
	if len(msgs) == 0 {
		return
	}
	send := r.programSend
	if send == nil {
		if r.program == nil {
			return
		}
		send = r.program.Send
	}
	for _, msg := range msgs {
		send(msg)
	}
}

// replModeSwitcher implements tool.ModeSwitcher by delegating to the TUI Model.
type replModeSwitcher struct {
	model        *Model
	program      *tea.Program
	previousMode permission.PermissionMode
}

func (s *replModeSwitcher) Mode() permission.PermissionMode {
	if cp, ok := s.model.policy.(*permission.ConfigPolicy); ok {
		return cp.CurrentMode()
	}
	return s.model.mode
}

func (s *replModeSwitcher) SetMode(mode permission.PermissionMode) {
	// ConfigPolicy.SetMode is thread-safe (has its own mutex)
	if cp, ok := s.model.policy.(*permission.ConfigPolicy); ok {
		cp.SetMode(mode)
	}
	// Update Model.mode via program.Send for thread safety
	if s.program != nil {
		s.program.Send(modeChangeMsg{Mode: mode})
	}
}

// RememberMode saves the current mode as "previous" and returns what was saved.
// This is called by enter_plan_mode to remember the mode before switching.
func (s *replModeSwitcher) RememberMode(currentMode permission.PermissionMode) permission.PermissionMode {
	// Read the actual current mode from ConfigPolicy (thread-safe, always up-to-date)
	// rather than s.model.mode which may be stale (Bubble Tea copies the model).
	actualCurrent := currentMode // fallback to the argument
	if cp, ok := s.model.policy.(*permission.ConfigPolicy); ok {
		actualCurrent = cp.CurrentMode()
	}
	s.previousMode = actualCurrent
	return actualCurrent
}

// RestoreMode returns the remembered mode, or the given fallback.
func (s *replModeSwitcher) RestoreMode(fallback permission.PermissionMode) permission.PermissionMode {
	if s.previousMode != permission.SupervisedMode && s.previousMode != permission.PlanMode {
		return s.previousMode
	}
	return fallback
}

// modeChangeMsg is sent to update the Model's mode from a goroutine.
type modeChangeMsg struct {
	Mode permission.PermissionMode
}

func (r *REPL) SetAskUserTool(tools *tool.Registry) {
	tl, ok := tools.Get("ask_user")
	if !ok {
		return
	}
	askTool, ok := tl.(*tool.AskUserTool)
	if !ok {
		return
	}
	askTool.SetHandler(func(ctx context.Context, req tool.AskUserRequest) (tool.AskUserResponse, error) {
		return r.requestAskUser(ctx, req)
	})
}

// SetCommandPane wires the command pane manager into the run_command tool
// for real-time output mirroring in tmux environments.
func (r *REPL) SetCommandPane(tools *tool.Registry, workingDir string) {
	if os.Getenv("TMUX") == "" {
		return // only active in tmux
	}
	mgr := cmdpane.NewManager(workingDir)
	r.model.cmdPaneMgr = mgr

	tl, ok := tools.Get("run_command")
	if !ok {
		return
	}
	rc, ok := tl.(*tool.RunCommand)
	if !ok {
		return
	}

	writer, err := mgr.Writer()
	if err != nil {
		debug.Logf("cmdpane: failed to get writer: %v", err)
		return
	}

	preExecFn := func(command, description string) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := mgr.EnsurePane(ctx); err != nil {
			debug.Logf("cmdpane: ensure pane: %v", err)
		}
		mgr.WriteHeader(command, description)
	}

	rc.OutputTee = writer
	rc.OnPreExec = preExecFn
	rc.OnPostExec = mgr.WriteFooter

	// Also wire start_command for long-running/streaming commands.
	if tl2, ok := tools.Get("start_command"); ok {
		if sc, ok := tl2.(*tool.StartCommandTool); ok {
			sc.OutputTee = writer
			sc.OnPreExec = preExecFn
			// start_command is async — no OnPostExec here (job completion
			// is checked separately via read_command_output/wait_command).
		}
	}
}

// requestDiffConfirm sends a diff confirmation request to the TUI and waits for response.
// Honors ctx so the agent goroutine doesn't leak if the TUI shuts down or the
// run is cancelled while a confirmation prompt is in flight.
func (r *REPL) requestDiffConfirm(ctx context.Context, filePath, diffText string) bool {
	if r.program == nil {
		// Non-interactive (pipe) mode: auto-approve
		return true
	}
	resp := make(chan bool, 1)
	r.program.Send(DiffConfirmMsg{
		FilePath: filePath,
		DiffText: diffText,
		Response: resp,
	})
	select {
	case ok := <-resp:
		return ok
	case <-ctx.Done():
		return false
	}
}

func (r *REPL) requestAskUser(ctx context.Context, req tool.AskUserRequest) (tool.AskUserResponse, error) {
	if r.program == nil {
		return tool.AskUserResponse{}, fmt.Errorf("interactive questionnaire unavailable")
	}
	resp := make(chan tool.AskUserResponse, 1)
	r.program.Send(AskUserMsg{
		Request:  req,
		Response: resp,
	})
	select {
	case result := <-resp:
		return result, nil
	case <-ctx.Done():
		return tool.AskUserResponse{}, ctx.Err()
	}
}

// Program returns the underlying tea.Program for external callers that need to send messages.
func (r *REPL) Program() *tea.Program {
	return r.program
}

// cronPromptMsg is sent when a cron job fires, injecting a prompt into the conversation.
type cronPromptMsg struct {
	Prompt      string
	QueueIfBusy bool
}

// withDocSyncReminder appends a documentation sync reminder to cron prompts
// that don't already mention docs. This ensures cron-triggered tasks keep
// documentation up to date without requiring the prompt author to remember.
func withDocSyncReminder(prompt string) string {
	lower := strings.ToLower(prompt)
	// Skip if prompt already mentions documentation.
	for _, kw := range []string{"doc", "文档", "readme", "changelog", "release note"} {
		if strings.Contains(lower, kw) {
			return prompt
		}
	}
	return prompt + "\n\n[Reminder] If this task involves code changes, update related documentation (docs/guide/, README.md, docs/design/) to keep them in sync."
}

// Run starts the REPL event loop.
func (r *REPL) Run() error {
	traceStart := time.Now()
	traceLast := traceStart
	traceMark := func(label string) {
		now := time.Now()
		debug.Log("repl", "startup timing repl.Run %-40s delta=%s total=%s", label, now.Sub(traceLast).Round(time.Millisecond), now.Sub(traceStart).Round(time.Millisecond))
		traceLast = now
	}
	debug.Log("repl", "Run() START resumeID=%q", r.resumeID)
	traceMark("start")
	if r.core != nil {
		defer r.core.Close()
	}
	// Initialize session
	if r.store != nil {
		if r.resumeID != "" && r.resumeID != "__new__" {
			// Explicit --resume <id>
			r.loadSession(r.resumeID)
			traceMark("load session")
		} else {
			// Auto-load: try to resume the most recent workspace session.
			// Skip auto-load when resumeID is "__new__" (picker cancelled).
			if r.resumeID != "__new__" && r.tryAutoLoadSession() {
				traceMark("auto-load session")
			} else {
				r.createSession()
				traceMark("create session")
			}
		}
	}
	// Switch lanchat nick persistence to per-session path
	// Note: lanChatHub.SetSessionID is now called from Model.SetSession,
	// which covers all session creation/switch paths (startup, /clear,
	// /sessions resume, /branch).
	r.primeInitialWindowSize(term.GetSize)
	traceMark("prime initial window size")

	// TTY hygiene: drain any pending stdin bytes (e.g. terminal probe responses
	// from the previous shell, paste residue) before bubbletea grabs the TTY.
	// Also enable bubbletea v2's internal trace log so we can see readLoop /
	// cancelReader activity in the next debug bundle.
	enableBubbleteaTrace()
	drainStdinResidual()
	traceMark("tty hygiene")

	// Pre-initialize the glamour markdown renderer so the first LLM response
	// doesn't freeze the TUI while glamour initializes its parser/highlighter.
	markdown.Warmup()
	traceMark("markdown warmup")

	r.program = tea.NewProgram(r.model)
	if r.planSwitcher != nil {
		r.planSwitcher.program = r.program
	}
	// Register TUI callbacks (verify progress, tool streaming) now that
	// r.program is set. Previously inside SetLanChatHub which was called
	// before Run() — so r.program was always nil at registration time.
	r.RegisterCallbacks()
	traceMark("new bubbletea program")
	debug.Log("repl", "program created stdin_is_term=%v stdout_is_term=%v",
		term.IsTerminal(os.Stdin.Fd()), term.IsTerminal(os.Stdout.Fd()))

	// Watchdog that detects if bubbletea's raw mode is silently lost
	// (readLoop dead → terminal echoes typed bytes → looks like a frozen UI).
	// Detection only — we log loudly so the next bug report has a smoking gun.
	watchdogCtx, watchdogCancel := context.WithCancel(context.Background())
	stopWatchdog := startTTYWatchdog(watchdogCtx)
	stopStdoutMonitor := startStdoutHealthMonitor(watchdogCtx, func(msg interface{}) {
		if r.program != nil {
			r.program.Send(msg)
		}
	})
	traceMark("start tty monitors")
	defer func() {
		stopWatchdog()
		stopStdoutMonitor()
		watchdogCancel()
	}()
	if r.mcpMgr != nil {
		r.mcpMgr.SetOnUpdate(func(servers []plugin.MCPServerInfo) {
			if r.program != nil {
				r.program.Send(mcpServersMsg{Servers: servers})
			}
		})
	}
	traceMark("wire mcp callbacks")
	if r.commandMgr != nil {
		stop := make(chan struct{})
		defer close(stop)
		safego.Go("tui.repl.commandReload", func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					if r.commandMgr.Reload() && r.program != nil {
						if r.skillsChangedHook != nil {
							r.skillsChangedHook()
						}
						r.program.Send(skillsChangedMsg{})
					}
				case <-stop:
					return
				}
			}
		})
	}
	traceMark("start command reload loop")

	// Wire the agent's approval handler into the TUI via channel bridge.
	// Honors ctx — if the TUI exits or the run is cancelled while waiting
	// for the user's decision, the agent goroutine returns Deny instead of
	// blocking forever on <-resp.
	r.agent.SetApprovalHandler(func(ctx context.Context, toolName string, input string) permission.Decision {
		if r.program == nil {
			return permission.Deny
		}
		resp := make(chan permission.Decision, 1)
		r.program.Send(ApprovalMsg{
			ToolName: toolName,
			Input:    input,
			Response: resp,
		})
		select {
		case d := <-resp:
			return d
		case <-ctx.Done():
			return permission.Deny
		}
	})
	traceMark("wire approval handler")

	// Wire checkpoint handler — persist compacted state after summarize.
	// Acquires the model's sessionMutex while reading m.session and calling
	// AppendCheckpoint (which mutates ses.UpdatedAt and rewrites the index)
	// because the TUI thread also mutates the same session under that mutex
	// (see appendUserMessage in submit.go).
	r.agent.SetCheckpointHandler(func(summaryMsgID, lastMsgID string, tokenCount int) {
		if r.store == nil {
			return
		}
		mu := r.model.sessionMutex()
		mu.Lock()
		// Use getCurrentSession — returns the LIVE session pointer.
		ses := r.getCurrentSession()
		if ses == nil {
			ses = r.model.Session()
		}
		if ses == nil {
			mu.Unlock()
			return
		}
		// Mutate Session object under sessionMutex.
		ses.UpdatedAt = time.Now()
		store := r.store
		mu.Unlock()

		// Persist to disk outside sessionMutex.
		if jsonlStore, ok := store.(*session.JSONLStore); ok {
			if err := jsonlStore.AppendCheckpointToDisk(ses, summaryMsgID, lastMsgID, tokenCount); err != nil {
				debug.Log("repl", "checkpoint save failed: %v", err)
			} else {
				debug.Log("repl", "checkpoint saved: summary_msg_id=%s last_msg_id=%s tokens=%d", summaryMsgID, lastMsgID, tokenCount)
			}
		} else {
			mu.Lock()
			if err := store.AppendCheckpoint(ses, summaryMsgID, lastMsgID, tokenCount); err != nil {
				debug.Log("repl", "checkpoint save failed: %v", err)
			} else {
				debug.Log("repl", "checkpoint saved: summary_msg_id=%s last_msg_id=%s tokens=%d", summaryMsgID, lastMsgID, tokenCount)
			}
			mu.Unlock()
		}
	})
	traceMark("wire checkpoint handler")

	// Per-message persistence: every Add() triggers an async JSONL append.
	// This replaces the batch persistFullSessionMessages at run end.
	r.agent.SetPersistHandler(func(msg provider.Message) {
		if r.store == nil {
			return
		}
		// Use getCurrentSession — it returns the LIVE session pointer
		// (updated atomically on /clear, /sessions switch), not the stale
		// r.model snapshot which always points to the initial session.
		ses := r.getCurrentSession()
		if ses == nil {
			return
		}
		if jsonlStore, ok := r.store.(*session.JSONLStore); ok {
			if err := jsonlStore.AppendMessageToDisk(ses, msg); err != nil {
				debug.Log("tui", "persist handler: AppendMessageToDisk failed: %v", err)
			}
		}
	})

	// NewProgram copies the model, so SetProgram on r.model is useless.
	// We can't Send before Run (deadlock). Instead, run in a goroutine and
	// send the reference once the event loop is up.
	debug.Log("repl", "scheduling setProgramMsg")
	// Send the startup logo with vendor/endpoint/model info.
	vendorName := ""
	endpointName := ""
	if r.model.config != nil {
		vendorName = r.model.config.Vendor
		endpointName = r.model.config.Endpoint
	}
	modelName := ""
	if r.model.config != nil {
		modelName = r.model.config.Model
	}
	safego.Go("tui.repl.startupMsg", func() {
		start := time.Now()
		// Wait for Bubble Tea to complete initialization (raw mode, alt screen,
		// mouse mode, renderer start, readLoop start) before sending any messages.
		// Too short and messages arrive before the event loop is ready.
		time.Sleep(100 * time.Millisecond)
		r.program.Send(setProgramMsg{Program: r.program})
		r.program.Send(logoMsg{Vendor: vendorName, Endpoint: endpointName, Model: modelName})
		debug.Log("repl", "startup timing repl.startupMsg sent initial messages duration=%s", time.Since(start).Round(time.Millisecond))
		if r.webuiAddr != "" {
			r.program.Send(webuiReadyMsg{Addr: r.webuiAddr, Token: r.webuiToken})
		}
		if r.knightStartupHint != "" {
			r.program.Send(knightStartupHintMsg{Hint: r.knightStartupHint})
		}
		// Start code index after TUI is ready, not on a fixed timer.
		// Loading large sessions can take far longer than 3s.
		if r.agent != nil {
			if cim := r.agent.CodeIndexManager(); cim != nil {
				if !cim.IsReady() {
					r.program.Send(systemMsg{msg: "Building code index for @ fuzzy search..."})
				}
				cim.SetOnReady(func(stats tool.CodeIndexStats) {
					if stats.IndexedFiles > 0 && r.program != nil {
						r.program.Send(systemMsg{msg: fmt.Sprintf("Code index ready: %d files indexed - @ fuzzy search enabled", stats.IndexedFiles)})
					}
				})
				cim.StartBackgroundIndex()
			}
		}
		if r.projectMemoryLoader != nil {
			loader := r.projectMemoryLoader
			safego.Go("tui.repl.projectMemory", func() {
				start := time.Now()
				content, files, err := loader()
				debug.Log("repl", "startup timing repl.projectMemory files=%d bytes=%d err=%v duration=%s", len(files), len(content), err, time.Since(start).Round(time.Millisecond))
				if r.program != nil {
					r.program.Send(projectMemoryLoadedMsg{Content: content, Files: files, Err: err})
				}
			})
		}
		if r.mcpMgr != nil {
			start := time.Now()
			r.core.StartBackgroundServices()
			debug.Log("repl", "startup timing repl.mcp StartBackground duration=%s", time.Since(start).Round(time.Millisecond))
		}
	})
	traceMark("schedule startup messages")

	traceMark("before bubbletea Run")

	// SIGHUP handler: when the terminal closes (SSH disconnect, terminal tab
	// close, display sleep on some macOS versions), the OS sends SIGHUP to the
	// foreground process group. BubbleTea only handles SIGINT/SIGTERM, so
	// without this handler SIGHUP kills the process immediately — no session
	// save, no cleanup, no graceful shutdown. We catch SIGHUP and send a
	// QuitMsg to BubbleTea so the normal shutdown path runs.
	sighupCh := make(chan os.Signal, 1)
	signal.Notify(sighupCh, syscall.SIGHUP)
	sighupDone := make(chan struct{})
	defer func() {
		signal.Stop(sighupCh)
		close(sighupDone)
	}()
	safego.Go("tui.sighupHandler", func() {
		select {
		case <-sighupCh:
			debug.Log("repl", "SIGHUP received (terminal closed?) — initiating graceful shutdown")
			r.program.Send(tea.QuitMsg{})
		case <-sighupDone:
		}
	})

	finalModel, err := r.program.Run()
	traceMark("after bubbletea Run")
	debug.Log("repl", "program.Run() returned err=%v", err)
	if errors.Is(err, tea.ErrInterrupted) {
		err = nil
	}
	// Drain remaining metrics before session save.
	if r.metricCollector != nil {
		if r.metricCancel != nil {
			r.metricCancel()
		}
		r.metricCollector.Stop()
	}
	if r.imManager != nil {
		r.imManager.UnbindSession()
	}
	// Broadcast leave to LAN peers so they detect offline immediately.
	if r.model.lanChatHub != nil {
		r.model.lanChatHub.Close()
	}
	// Sync final model state from Bubble Tea so exit cleanup uses the
	// correct session (r.model is a stale snapshot from before Run()).
	if m, ok := finalModel.(Model); ok {
		r.model = m
	}

	// Use getCurrentSession for the live session pointer (works even if
	// r.model sync above failed due to type assertion).
	exitSes := r.getCurrentSession()
	if exitSes == nil {
		exitSes = r.model.session // fallback to finalModel sync
	}

	if r.model.acpClientMgr != nil {
		r.model.acpClientMgr.CloseAll()
	}
	if r.model.instanceDetect != nil {
		r.model.instanceDetect.Unregister()
	}
	if err == nil && r.store != nil && exitSes != nil {
		// Persist new messages on clean exit (incremental, no full rewrite).
		r.model.persistFullSessionMessages()
		// Clean up empty session files — sessions without any user interaction
		// are deleted to avoid cluttering the sessions directory.
		if jsonlStore, ok := r.store.(*session.JSONLStore); ok {
			wasDeleted := jsonlStore.WillCleanupIfEmpty(exitSes)
			if err := jsonlStore.CleanupIfEmpty(exitSes); err != nil {
				debug.Log("repl", "exit cleanup: failed to delete empty session: %v", err)
			} else if wasDeleted && r.imManager != nil {
				// Session was deleted — clear any IM adapter bindings that
				// reference this session to prevent orphaned bindings.
				r.imManager.ClearSessionBindings(exitSes.ID)
			}
		}
	}

	// Release the session lock so another instance can resume this session.
	if r.sessionLock != nil {
		r.sessionLock.Release()
		r.sessionLock = nil
	}

	if m, ok := finalModel.(Model); ok {
		if m.terminalTitleWriter != nil {
			m.statusActivity = ""
			m.terminalTitleWriter(m.desiredTerminalTitle())
		}
		m.closeTunnelGracefully(2 * time.Second)
		finalModel = m
	}

	if m, ok := finalModel.(Model); ok && m.tmuxExecRequested {
		sid := ""
		if m.session != nil {
			sid = m.session.ID
		}
		debug.Log("tmux", "finalModel: tmuxExecRequested=%v sessionID=%q tmuxSession=%q", m.tmuxExecRequested, sid, m.tmuxExecSession)
		r.model = m
		return r.execTmuxEnter()
	}

	// Check if the final model requested a self-restart.
	// program.Run() returns the final model state, but r.model is a
	// snapshot from before Run() — we must read from finalModel.
	if m, ok := finalModel.(Model); ok && m.restartRequested {
		sid := ""
		if m.session != nil {
			sid = m.session.ID
		}
		debug.Log("restart", "finalModel: restartRequested=%v sessionID=%q updateSvc=%v",
			m.restartRequested, sid, m.updateSvc != nil)
		r.model = m
		return r.execRestart()
	}

	return err
}

func (r *REPL) primeInitialWindowSize(getSize func(fd uintptr) (int, int, error)) {
	width, height, err := getSize(os.Stdout.Fd())
	if err != nil || width <= 0 || height <= 0 {
		return
	}
	r.model.handleResize(width, height)
}

// createSession creates a fresh session and wires it into the model.
// tryAutoLoadSession attempts to load the most recent workspace session.
// Returns true if a session was loaded, false if it should create a new one.
// If the latest session is locked by another process, shows the session picker.
func (r *REPL) tryAutoLoadSession() bool {
	if r.store == nil {
		return false
	}
	// If root.go already acquired a lock (from the picker path), skip.
	if r.sessionLock != nil && r.sessionLock.Acquired() {
		return false
	}
	workspace := r.model.agent.WorkingDir()
	if workspace == "" {
		return false
	}

	latest, err := r.store.LatestForWorkspace(workspace)
	if err != nil {
		debug.Log("repl", "tryAutoLoadSession: LatestForWorkspace error: %v", err)
		return false
	}
	if latest == nil {
		debug.Log("repl", "tryAutoLoadSession: no sessions for workspace %q", workspace)
		return false
	}

	// Try to acquire a lock on the session.
	storeDir, err := session.DefaultDir()
	if err != nil {
		debug.Log("repl", "tryAutoLoadSession: DefaultDir error: %v", err)
		return false
	}
	lock, err := session.TryAcquireSessionLock(storeDir, latest.ID)
	if err != nil {
		debug.Log("repl", "tryAutoLoadSession: lock error: %v", err)
		return false
	}
	if lock != nil && lock.Acquired() {
		// We got the lock — auto-resume this session.
		r.sessionLock = lock
		r.loadSession(latest.ID)
		debug.Log("repl", "tryAutoLoadSession: auto-loaded session %s", latest.ID)
		return true
	}

	// Session is locked by another instance — create new session.
	// (The picker flow is handled in root.go before the TUI starts.)
	debug.Log("repl", "tryAutoLoadSession: session %s is locked (PID %d), creating new session",
		latest.ID, lock.HolderPID())
	return false
}

func (r *REPL) createSession() {
	start := time.Now()
	vendor := ""
	endpoint := ""
	model := ""
	if r.model.config != nil {
		vendor = r.model.config.Vendor
		endpoint = r.model.config.Endpoint
		model = r.model.config.Model
	}
	ses := session.NewSession(vendor, endpoint, model)
	debug.Log("repl", "startup timing repl.createSession session.NewSession workspace=%q duration=%s", ses.Workspace, time.Since(start).Round(time.Millisecond))
	saveStart := time.Now()
	if err := r.store.Save(ses); err == nil {
		debug.Log("repl", "startup timing repl.createSession store.Save duration=%s", time.Since(saveStart).Round(time.Millisecond))
		r.model.SetSession(ses, r.store)
		r.model.chatWriteSystem(nextSystemID(), r.model.t("session.new", ses.ID))

		// Acquire a session lock so concurrent instances in the same workspace
		// cannot auto-load this session. This mirrors the desktop Wails path
		// (chat.go EnsureSession).
		if r.sessionLock != nil {
			r.sessionLock.Release()
			r.sessionLock = nil
		}
		if storeDir, dirErr := session.DefaultDir(); dirErr == nil {
			if lock, lockErr := session.TryAcquireSessionLock(storeDir, ses.ID); lockErr == nil && lock != nil && lock.Acquired() {
				r.sessionLock = lock
				debug.Log("repl", "createSession: acquired lock on new session %s", ses.ID)
			}
		}

		// Bind cron scheduler to this session for persistence.
		// Use SwitchSession (not SetSession) because createSession may be
		// called as a fallback after loadSession fails, where the scheduler
		// may already be bound to a previous session.
		if r.cronScheduler != nil {
			sessionPath, legacyPath := agentruntime.CronStorePaths(ses.ID)
			r.cronScheduler.SwitchSession(sessionPath, legacyPath, r.workingDir)
		}

		debug.Log("repl", "startup timing repl.createSession total=%s", time.Since(start).Round(time.Millisecond))
	} else {
		debug.Log("repl", "startup timing repl.createSession store.Save err=%v duration=%s", err, time.Since(saveStart).Round(time.Millisecond))
	}
}

// loadSession loads a previous session and restores messages into the agent.
func (r *REPL) loadSession(id string) {
	start := time.Now()
	ses, err := r.store.Load(id)
	debug.Log("repl", "startup timing repl.loadSession store.Load id=%q messages=%d err=%v duration=%s", id, messageCount(ses), err, time.Since(start).Round(time.Millisecond))
	if err != nil {
		r.model.chatWriteSystem(nextSystemID(), r.model.t("session.resume_failed", id, err))
		r.model.chatWriteSystem(nextSystemID(), r.model.t("session.resume_fallback"))
		r.createSession()
		return
	}
	// Acquire session lock — mirrors createSession and tryAutoLoadSession.
	// This is critical for the /restart path: execRestart releases the lock
	// before syscall.Exec, and the new process enters via loadSession with
	// --resume <id>. Without re-acquiring here, the session is left unlocked.
	// #1389-B: acquire the NEW lock BEFORE releasing the old one (the
	// switchSessionLock pattern at :338). The old order released first,
	// then on TryAcquire failure merely logged and continued UNLOCKED -
	// the /restart window let a concurrent --resume instance grab the lock
	// mid-gap, and two processes then appended to the same JSONL.
	var newLock *session.SessionLock
	if storeDir, dirErr := session.DefaultDir(); dirErr == nil {
		if lock, lockErr := session.TryAcquireSessionLock(storeDir, ses.ID); lockErr == nil && lock != nil && lock.Acquired() {
			newLock = lock
			debug.Log("repl", "loadSession: acquired lock on session %s", ses.ID)
		} else if lock != nil && !lock.Acquired() {
			pid := lock.HolderPID()
			debug.Log("repl", "loadSession: session %s is locked by PID %d; continuing WITHOUT lock (restore path - execRestart released for this successor)", ses.ID, pid)
		}
	}
	if newLock != nil {
		if r.sessionLock != nil {
			r.sessionLock.Release()
		}
		r.sessionLock = newLock
	} else if r.sessionLock != nil {
		// Keep the old lock rather than none: it at least serializes THIS
		// process's exit path, and releasing it would widen the gap the
		// old code left fully open.
		debug.Log("repl", "loadSession: keeping previous lock (new session lock unavailable)")
	}

	compacted, beforeTokens, afterTokens := agentruntime.RestoreSessionIntoAgent(r.agent, ses)
	r.model.SetSession(ses, r.store)

	if compacted {
		r.model.chatWriteSystem(nextSystemID(), fmt.Sprintf("Restored session was oversized (%d tokens), truncated to %d tokens to fit context window", beforeTokens, afterTokens))
	}

	// Switch CWD if the session belongs to a different workspace.
	if ses.Workspace != "" && ses.Workspace != r.workingDir {
		oldDir := r.workingDir
		r.workingDir = ses.Workspace
		if r.agent != nil {
			r.agent.SetWorkingDir(ses.Workspace)
		}
		debug.Log("repl", "loadSession: switched workingDir from %q to %q (session workspace)", oldDir, ses.Workspace)
	}

	// Bind cron scheduler to this session (stop old timers, load new jobs).
	r.switchSessionCronWithWorkspace(ses.ID, r.workingDir)

	// This overrides the global default_mode for this session only.
	if ses.PermissionMode != "" {
		sessionMode := permission.ParsePermissionMode(ses.PermissionMode)
		if cp, ok := r.model.policy.(*permission.ConfigPolicy); ok {
			cp.SetMode(sessionMode)
		}
		r.model.mode = sessionMode
		debug.Log("repl", "loadSession: restored permission mode %s from session", sessionMode)
	}

	// Restore session-scoped sidebar visibility (if set).
	// This overrides the global sidebar_visible for this session only.
	if ses.SidebarVisible != nil {
		r.model.sidebarVisible = *ses.SidebarVisible
		debug.Log("repl", "loadSession: restored sidebar_visible=%v from session", *ses.SidebarVisible)
	}

	r.model.rebuildConversationFromMessages(ses.Messages)
	r.model.restoreHistoryFromMessages(ses.Messages)

	// Refresh cached git branch — sessions from other workspaces may have
	// different active branches.
	r.model.refreshCachedGitBranch()

	// Notify mobile client of session switch.
	r.model.publishTunnelSnapshotForCurrentSession(true)

	title := ses.Title
	if title == "" {
		title = r.model.t("session.untitled")
	}
	r.model.chatWriteSystem(nextSystemID(), r.model.t("session.resume", ses.ID, title, len(ses.Messages)))
	debug.Log("repl", "startup timing repl.loadSession total=%s", time.Since(start).Round(time.Millisecond))
}

func messageCount(ses *session.Session) int {
	if ses == nil {
		return 0
	}
	return len(ses.Messages)
}

// execRestart replaces the current process in-place using syscall.Exec.
// No child process or helper is spawned — the same PID continues with a
// fresh binary image, keeping the same terminal and file descriptors.
//
// For /update, ApplyBinary writes the new binary to all target paths first.
// On Unix, install.WriteExecutable uses temp-file + rename (atomic), which
// works even on the currently running binary because the kernel preserves
// the old inode. syscall.Exec then loads the new file at the same path.
//
// Must be called after program.Run() returns (terminal already restored).
func (r *REPL) execRestart() error {
	// Release session lock — the new process will re-acquire it.
	if r.sessionLock != nil {
		r.sessionLock.Release()
		r.sessionLock = nil
	}

	// Clean up port file before exec — syscall.Exec skips defers.
	if r.preExecCleanup != nil {
		r.preExecCleanup()
	}

	// For /update: write new binary to all target paths BEFORE exec.
	// This must happen while the process is still alive (before syscall.Exec).
	if r.model.updatePrepared != nil && r.model.updateSvc != nil {
		debug.Log("restart", "applying binary update")
		if err := r.model.updateSvc.ApplyBinary(*r.model.updatePrepared); err != nil {
			return fmt.Errorf("restart: apply binary: %w", err)
		}
	}

	binary, err := restart.ResolveBinary()
	if err != nil {
		return fmt.Errorf("restart: resolve binary: %w", err)
	}

	args := r.model.buildRestartArgs()
	env := os.Environ()
	if r.model.restartDebug {
		env = append(env, "GGCODE_DEBUG=1")
	}

	sessionID := ""
	if r.model.session != nil {
		sessionID = r.model.session.ID
	}
	debug.Log("restart", "exec binary=%s session=%s args=%v", binary, sessionID, args)

	return restart.ExecRestart(binary, args, env)
}

func (r *REPL) execTmuxEnter() error {
	binary, err := restart.ResolveBinary()
	if err != nil {
		return fmt.Errorf("tmux enter: resolve binary: %w", err)
	}
	args := r.model.buildRestartArgs()
	sessionName := sanitizeTmuxSessionName(r.model.tmuxExecSession)
	if sessionName == "" {
		sessionName = defaultTmuxSessionName(r.model.tmuxWorkspace())
	}
	wd := r.model.tmuxWorkspace()
	cmdArgs := append([]string{"new-session", "-A", "-s", sessionName, "-c", wd, binary}, args...)
	debug.Log("tmux", "exec tmux session=%q binary=%s args=%v wd=%s", sessionName, binary, args, wd)
	cmd := exec.Command("tmux", cmdArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	cmd.Env = removeEnv(cmd.Env, "GGCODE_TMUX_SETUP_LAYOUT")
	if strings.TrimSpace(r.model.tmuxExecSetupLayout) != "" {
		cmd.Env = append(cmd.Env, "GGCODE_TMUX_SETUP_LAYOUT="+r.model.tmuxExecSetupLayout)
	}
	cmd.Dir = wd
	if r.model.restartDebug {
		cmd.Env = append(cmd.Env, "GGCODE_DEBUG=1")
	}
	return cmd.Run()
}

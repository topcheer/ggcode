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

type REPL struct {
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
	systemPromptBuilder func(task, agentType string) string
	webuiAddr           string
	webuiToken          string
	knightStartupHint   string
	metricCollector     *metrics.Collector
	metricCancel        context.CancelFunc
	workingDir          string
	cfg                 *config.Config
	agentBusy           atomic.Bool
	preExecCleanup      func()
	cronScheduler       *cron.Scheduler
	currentSessionMu    sync.RWMutex
	currentSession      *session.Session

	initialSessionID string
	portFileMode     string
}

func NewREPL(a *agent.Agent, policy permission.PermissionPolicy, startupNotices []string) *REPL {
	m := NewModel(a, policy)
	r := &REPL{
		model: m,
		agent: a,
	}

	r.model.agentBusy = &r.agentBusy

	for i, notice := range startupNotices {
		r.model.chatWriteSystem(fmt.Sprintf("startup-notice-%d", i), notice)
	}
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

	r.model.sessionLockSwitch = r.switchSessionLock
	r.model.sessionCronSwitch = r.switchSessionCron

	r.model.sessionUpdateCallback = r.setCurrentSession
	return r
}

func (r *REPL) SetSessionStore(s session.Store) {
	r.store = s
}

func (r *REPL) SessionUsageHandler() func(provider.TokenUsage) {
	return func(usage provider.TokenUsage) { r.recordSessionUsage(usage, "subagent") }
}

func (r *REPL) SetMCPServers(servers []MCPInfo) {
	r.model.SetMCPServers(servers)
}

func (r *REPL) SetA2AHandler(h *a2a.TaskHandler) {
	r.model.SetA2AHandler(h)
}

func (r *REPL) RegisterCallbacks() {
	if r.agent == nil {
		return
	}

	r.agent.SetVerifyCallbacks(
		func(text string) {
			r.sendProgramMsgs(verifyProgressMsg{text: text})
		},
		func(result agent.VerifyResult) {
			r.sendProgramMsgs(verifyResultMsg{result: result})
		},
	)

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

type toolProgressCoalescer struct {
	mu   sync.Mutex
	send func(msgs ...tea.Msg)
	next map[string]toolProgressMsg

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

		hubCopy := hub
		wd := r.workingDir
		lang := ""
		if r.cfg != nil {
			lang = r.cfg.Language
		}
		r.agent.SetSystemPromptInjector(func() string {
			alert := lanchat.SharedWorkspaceAlert(hubCopy, hubCopy.NodeID(), wd, lanchat.DirHasVCS(wd), lang)
			peers := lanchat.FormatPeersInfo(hubCopy, wd)
			switch {
			case alert == "":
				return peers
			case peers == "":
				return alert
			default:
				return alert + "\n\n" + peers
			}
		})
	}

	if hub != nil {
		hub.SetOnAutoApprove(func(msg lanchat.Message) {
			r.sendTUI(lanchatAutoApproveMsg{msg: msg})
		})
	}
}

func (r *REPL) SetMCPManager(mgr *plugin.MCPManager) {
	r.mcpMgr = mgr
	r.model.SetMCPManager(mgr)

	mgr.SetOnUpdate(func(infos []plugin.MCPServerInfo) {
		r.sendTUI(mcpServersUpdatedMsg{servers: toMCPInfos(infos)})
	})
}

func (r *REPL) SetCore(core *agentruntime.InteractiveRuntimeCore) {
	r.core = core
	if r.model.tunnelHost != nil {
		r.model.tunnelHost.Close()
	}
	r.model.tunnelHost = core.Tunnel

	r.injectShareController()

	if r.agent != nil && core.Registry != nil {
		if cim := core.Registry.CodeIndex(); cim != nil {
			r.agent.SetCodeIndexManager(cim)
		}
	}
}

func (r *REPL) SetResumeID(id string) {
	r.resumeID = id
}

func (r *REPL) SetSessionLock(lock *session.SessionLock) {
	r.sessionLock = lock
}

func (r *REPL) switchSessionLock(newSessionID string) {

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

func (r *REPL) setCurrentSession(ses *session.Session) {
	r.currentSessionMu.Lock()
	r.currentSession = ses
	r.currentSessionMu.Unlock()
}

func (r *REPL) getCurrentSession() *session.Session {
	r.currentSessionMu.RLock()
	defer r.currentSessionMu.RUnlock()
	return r.currentSession
}

func (r *REPL) switchSessionCron(newSessionID string) {
	r.switchSessionCronWithWorkspace(newSessionID, r.workingDir)
}

func (r *REPL) switchSessionCronWithWorkspace(newSessionID, workspaceDir string) {
	if r.cronScheduler != nil {
		sessionPath, legacyPath := agentruntime.CronStorePaths(newSessionID)
		r.cronScheduler.SwitchSession(sessionPath, legacyPath, workspaceDir)
		debug.Log("repl", "switchSessionCron: rebound cron scheduler to session %s (workspace=%s)", newSessionID, workspaceDir)
	}
}

func (r *REPL) SetPreExecCleanup(fn func()) {
	r.preExecCleanup = fn
}

func (r *REPL) SetConfig(cfg *config.Config) {
	r.cfg = cfg
	r.model.SetConfig(cfg)
}

func (r *REPL) SetWorkingDir(wd string) {
	r.workingDir = wd
}

func (r *REPL) OnConfigProviderChanged() {
	if r.model.config == nil {
		return
	}

	r.model.startContextProbe()

	r.sendTUI(providerChangedMsg{})
}

type providerChangedMsg struct{}

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

func (r *REPL) SetKnightStartupHint(hint string) {
	r.knightStartupHint = hint
}

func (r *REPL) SetWebUIBridge(b WebUIEventBroadcaster) {
	r.model.webuiBridge = b
}

func (r *REPL) InjectWebchatMessage(text string, images []provider.ContentBlock) {
	if r.program != nil {
		r.program.Send(webchatUserMsg{Text: text, Images: images})
	}
}

func (r *REPL) InjectRestart() {
	if r.program != nil {

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

func (r *REPL) recordMetric(ev metrics.MetricEvent) {
	if r.program != nil {
		r.program.Send(sessionMetricMsg{Metric: ev})
		return
	}
	r.model.recordSessionMetric(ev)
}

func (r *REPL) SetWebUIReadyAddr(addr, token string) {
	r.webuiAddr = addr
	r.webuiToken = token
}

// SetWebUITunnelCtl 注入 /webui tunnel 控制器(webuiSrv 侧 TunnelCtl 单例)
func (r *REPL) SetWebUITunnelCtl(ctl *webui.TunnelCtl) {
	r.model.webuiTunnel = ctl
}

func (r *REPL) SetInitialSessionID(sessionID, mode string) {
	r.currentSessionMu.Lock()
	r.initialSessionID = sessionID
	r.portFileMode = mode
	r.currentSessionMu.Unlock()
}

func (r *REPL) CurrentSessionID() string {
	r.currentSessionMu.RLock()
	id := r.initialSessionID
	r.currentSessionMu.RUnlock()
	return id
}

func (r *REPL) SetSessionCreatedCallback(cb func(sessionID string)) {
	r.model.SetSessionCreatedCallback(cb)
}

func (r *REPL) HandleSessionCreated(sessionID string) {
	r.onSessionCreated(sessionID)
}

func (r *REPL) onSessionCreated(sessionID string) {
	if r.webuiAddr == "" {

		return
	}

	r.currentSessionMu.RLock()
	initial := r.initialSessionID
	r.currentSessionMu.RUnlock()
	if initial != "" && initial != sessionID {

		debug.Log("repl", "onSessionCreated: session switched %s -> %s, rewriting port file", initial, sessionID)
	}
	if initial == "" {

		runfile.Remove("__new__")
	}

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

	r.currentSessionMu.Lock()
	r.initialSessionID = sessionID
	r.currentSessionMu.Unlock()
}

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

func (r *REPL) SetCheckpointManager(m *checkpoint.Manager) {
	r.agent.SetCheckpointManager(m)
	r.agent.SetDiffConfirm(func(ctx context.Context, filePath, diffText string) bool {
		return r.requestDiffConfirm(ctx, filePath, diffText)
	})
}

func (r *REPL) SetSystemPromptBuilder(fn func(task, agentType string) string) {
	r.systemPromptBuilder = fn
}

func (r *REPL) SetJobManager(jm *tool.CommandJobManager) {
	if jm == nil {
		return
	}
	r.model.SetJobManager(jm)
}

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
	spawnTool := tool.SpawnAgentTool{
		Manager:             mgr,
		Provider:            prov,
		ProviderGetter:      providerGetter,
		AvailableModels:     availableModelsGetter,
		Tools:               tools,
		AgentFactory:        factory,
		WorkingDir:          r.model.agent.WorkingDir(),
		OnUsage:             func(usage provider.TokenUsage) { r.recordDelegatedUsage(usage) },
		OnMetric:            r.metricCollector.Emit,
		SystemPromptBuilder: r.systemPromptBuilder,
	}
	tools.Register(spawnTool)

	// Single-shot context-isolated LLM calls (translate/polish/SVG/...) share
	// the spawn provider wiring. #5187 registered these only into the
	// sub-agent registry (agentruntime/subsystems.go); the main session's
	// explicit registration list is the only path TUI/interactive sessions
	// load tools from, so register here too.
	tools.Register(tool.LLMCallTool{
		Provider:        prov,
		ProviderGetter:  providerGetter,
		AvailableModels: availableModelsGetter,
		WorkingDir:      r.model.agent.WorkingDir(),
	})

	r.model.agent.SetSubagentCompletionFeed(func() []agent.SubagentCompletion {
		var out []agent.SubagentCompletion
		for _, st := range mgr.Statuses() {
			switch st.Status {
			case subagent.StatusCompleted:
			case subagent.StatusFailed, subagent.StatusCancelled:
				c := agent.SubagentCompletion{ID: st.ID, Name: st.Name, Failed: true}
				if res, ok := mgr.GetTaskOutput(st.ID); ok {
					c.Summary = res
				}
				out = append(out, c)
				continue
			default:
				continue
			}
			c := agent.SubagentCompletion{ID: st.ID, Name: st.Name}
			if res, ok := mgr.GetTaskOutput(st.ID); ok {
				c.Summary = res
			}
			out = append(out, c)
		}
		return out
	})
	tools.Register(tool.BestOfNTool{
		Manager: mgr,
		Run:     agentruntime.BestOfNRunnerFor(spawnTool, mgr),
	})

	tools.Register(tool.WorkflowRunTool{
		Manager: mgr,
		Run:     agentruntime.WorkflowRunnerFor(spawnTool, mgr),
	})
	cascadeHints := tool.NewCascadeHintTracker()
	tools.Register(tool.WaitAgentTool{
		Manager:      mgr,
		ParentModel:  spawnAgentModelResolver,
		CascadeHints: cascadeHints,
	})
	tools.Register(tool.ListAgentsTool{
		Manager:      mgr,
		ParentModel:  spawnAgentModelResolver,
		CascadeHints: cascadeHints,
	})
	tools.Register(tool.CancelAgentTool{Manager: mgr})

	tmplStore := subagent.NewTemplateStore(r.model.agent.WorkingDir())

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
		OnUsage:             func(usage provider.TokenUsage) { r.recordDelegatedUsage(usage) },
		SystemPromptBuilder: r.systemPromptBuilder,
	})

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

	mgr.SetOnSystem(func(agentID, text string) {
		r.sendProgramMsgs(subAgentSystemMsg{AgentID: agentID, Text: text})
	})

	mgr.StartStreamBatcher()
}

func (r *REPL) SetTaskManager(mgr *task.Manager, tools *tool.Registry) {

	r.model.taskMgr = mgr
	tools.Register(tool.TaskCreateTool{Manager: mgr})
	tools.Register(tool.TaskGetTool{Manager: mgr})
	tools.Register(tool.TaskListTool{Manager: mgr})
	tools.Register(tool.TaskUpdateTool{Manager: mgr})
	tools.Register(tool.TaskStopTool{Manager: mgr})
}

func (r *REPL) SetTaskOutputTool(mgr *subagent.Manager, tools *tool.Registry) {
	tools.Register(tool.TaskOutputTool{Provider: mgr})
}

// CronJobsSnapshot 当前会话的任务快照(webui /api/cron/jobs 真相源;nil 安全)
func (r *REPL) CronJobsSnapshot() []cron.Job {
	if r.cronScheduler == nil {
		return nil
	}
	return r.cronScheduler.List()
}

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

	ses := r.getCurrentSession()
	if ses != nil {
		m.Workspace = ses.Workspace
		m.PermissionMode = ses.PermissionMode
	}

	if r.model.agent != nil && r.model.agent.ContextManager() != nil {
		m.ContextWindow = r.model.agent.ContextManager().ContextWindow()
		m.MaxTokens = r.model.agent.ContextManager().OutputReserve()
	}

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

func (r *REPL) SetPlanModeTools(tools *tool.Registry) {
	switcher := &replModeSwitcher{model: &r.model}
	r.planSwitcher = switcher
	tools.Register(tool.EnterPlanModeTool{Switcher: switcher})
	tools.Register(tool.ExitPlanModeTool{Switcher: switcher, DefaultMode: permission.SupervisedMode})

	if sm, ok := tools.Get("switch_mode"); ok {
		if smt, ok := sm.(*tool.SwitchModeTool); ok {
			smt.SetSwitcher(switcher)
		}
	}

	if rt, ok := tools.Get("restart"); ok {
		if rtt, ok := rt.(*tool.RestartTool); ok {
			rtt.Requester = &replRestartRequester{repl: r}
		}
	}
}

type replRestartRequester struct {
	repl *REPL
}

func (rr *replRestartRequester) RequestRestart(reason string, debugMode bool) {
	debug.Log("restart", "agent-requested restart armed (turn-aware, fires at turn end or 30s fallback): %s", reason)
	if debugMode {
		os.Setenv("GGCODE_DEBUG", "1")
	}

	go safego.Run("tui.restart.arm", func() {
		rr.repl.sendProgramMsgs(armRestartMsg{debug: debugMode, reason: reason})
	})
}

func (r *REPL) SetSendMessageTool(mgr *subagent.Manager, tools *tool.Registry) {
	tools.Register(tool.SendMessageTool{Manager: mgr})
}

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

			return permission.DecisionFromContext(ctx.Err())
		}
	})
}

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
	tools.Register(tool.TeamReviewDigestTool{Manager: mgr})
	tools.Register(tool.TeamReviewDecideTool{Manager: mgr})

	tools.Unregister("send_message")
	tools.Register(tool.SendMessageTool{Manager: r.model.subAgentMgr, SwarmMgr: mgr})

	swarmTextThrottle := newTextThrottleMap(500 * time.Millisecond)

	mgr.SetOnUpdate(func(ev swarm.Event) {
		if r.program == nil && r.programSend == nil {
			return
		}
		msgs := []tea.Msg{swarmTunnelEventMsg{Event: ev}}
		switch ev.Type {
		case "teammate_text":

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

			msgs = append(msgs, subAgentUpdateMsg{AgentID: ev.TeammateID})
		}
		r.sendProgramMsgs(msgs...)
	})

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

	if cp, ok := s.model.policy.(*permission.ConfigPolicy); ok {
		cp.SetMode(mode)
	}

	if s.program != nil {
		s.program.Send(modeChangeMsg{Mode: mode})
	}
}

func (s *replModeSwitcher) RememberMode(currentMode permission.PermissionMode) permission.PermissionMode {

	actualCurrent := currentMode
	if cp, ok := s.model.policy.(*permission.ConfigPolicy); ok {
		actualCurrent = cp.CurrentMode()
	}
	s.previousMode = actualCurrent
	return actualCurrent
}

func (s *replModeSwitcher) RestoreMode(fallback permission.PermissionMode) permission.PermissionMode {
	if s.previousMode != permission.SupervisedMode && s.previousMode != permission.PlanMode {
		return s.previousMode
	}
	return fallback
}

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

func (r *REPL) SetCommandPane(tools *tool.Registry, workingDir string) {
	if os.Getenv("TMUX") == "" {
		return
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

	if tl2, ok := tools.Get("start_command"); ok {
		if sc, ok := tl2.(*tool.StartCommandTool); ok {
			sc.OutputTee = writer
			sc.OnPreExec = preExecFn

		}
	}
}

func (r *REPL) requestDiffConfirm(ctx context.Context, filePath, diffText string) bool {
	if r.program == nil {

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

func (r *REPL) Program() *tea.Program {
	return r.program
}

type cronPromptMsg struct {
	Prompt      string
	QueueIfBusy bool
}

func withDocSyncReminder(prompt string) string {
	lower := strings.ToLower(prompt)

	for _, kw := range []string{"doc", "文档", "readme", "changelog", "release note"} {
		if strings.Contains(lower, kw) {
			return prompt
		}
	}
	return prompt + "\n\n[Reminder] If this task involves code changes, update related documentation (docs/guide/, README.md, docs/design/) to keep them in sync."
}

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

	if r.store != nil {
		if r.resumeID != "" && r.resumeID != "__new__" {

			r.loadSession(r.resumeID)
			traceMark("load session")
		} else {

			if r.resumeID != "__new__" && r.tryAutoLoadSession() {
				traceMark("auto-load session")
			} else {
				r.createSession()
				traceMark("create session")
			}
		}
	}

	r.primeInitialWindowSize(term.GetSize)
	traceMark("prime initial window size")

	enableBubbleteaTrace()
	drainStdinResidual()
	traceMark("tty hygiene")

	markdown.Warmup()
	traceMark("markdown warmup")

	r.program = tea.NewProgram(r.model, tea.WithOutput(stdoutGuardWriter{File: os.Stdout}))

	r.model.tuiSend = func(msg tea.Msg) { r.sendTUI(msg) }
	if r.planSwitcher != nil {
		r.planSwitcher.program = r.program
	}

	r.RegisterCallbacks()
	traceMark("new bubbletea program")
	debug.Log("repl", "program created stdin_is_term=%v stdout_is_term=%v",
		term.IsTerminal(os.Stdin.Fd()), term.IsTerminal(os.Stdout.Fd()))

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

			return permission.DecisionFromContext(ctx.Err())
		}
	})
	traceMark("wire approval handler")

	r.agent.SetCheckpointHandler(func(summaryMsgID, lastMsgID string, tokenCount int) {
		if r.store == nil {
			return
		}
		mu := r.model.sessionMutex()
		mu.Lock()

		ses := r.getCurrentSession()
		if ses == nil {
			ses = r.model.Session()
		}
		if ses == nil {
			mu.Unlock()
			return
		}

		ses.UpdatedAt = time.Now()
		store := r.store
		mu.Unlock()

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

	r.agent.SetPersistHandler(func(msg provider.Message) {
		if r.store == nil {
			return
		}

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

	debug.Log("repl", "scheduling setProgramMsg")

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

		if r.agent != nil {
			if cim := r.agent.CodeIndexManager(); cim != nil {
				if !cim.IsReady() {
					r.program.Send(systemNotifyMsg{ItemID: "codeindex-progress", Replace: true, Text: "Building code index for @ fuzzy search..."})
				}

				cim.SetOnProgress(func(done, total int) {
					if r.program != nil {
						r.program.Send(systemNotifyMsg{ItemID: "codeindex-progress", Replace: true,
							Text: fmt.Sprintf("Building code index... %d/%d files", done, total)})
					}
				})
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

	if r.metricCollector != nil {
		if r.metricCancel != nil {
			r.metricCancel()
		}
		r.metricCollector.Stop()
	}
	if r.imManager != nil {
		r.imManager.UnbindSession()
	}

	if r.model.lanChatHub != nil {
		r.model.lanChatHub.Close()
	}

	if m, ok := finalModel.(Model); ok {
		r.model = m
	}

	exitSes := r.getCurrentSession()
	if exitSes == nil {
		exitSes = r.model.session
	}

	if r.model.acpClientMgr != nil {
		r.model.acpClientMgr.CloseAll()
	}
	if r.model.instanceDetect != nil {
		r.model.instanceDetect.Unregister()
	}
	if err == nil && r.store != nil && exitSes != nil {

		r.model.persistFullSessionMessages()

		if jsonlStore, ok := r.store.(*session.JSONLStore); ok {
			wasDeleted := jsonlStore.WillCleanupIfEmpty(exitSes)
			if err := jsonlStore.CleanupIfEmpty(exitSes); err != nil {
				debug.Log("repl", "exit cleanup: failed to delete empty session: %v", err)
			} else if wasDeleted && r.imManager != nil {

				r.imManager.ClearSessionBindings(exitSes.ID)
			}
		}
	}

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

func (r *REPL) tryAutoLoadSession() bool {
	if r.store == nil {
		return false
	}

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

		r.sessionLock = lock
		r.loadSession(latest.ID)
		debug.Log("repl", "tryAutoLoadSession: auto-loaded session %s", latest.ID)
		return true
	}

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

		if r.cronScheduler != nil {
			sessionPath, legacyPath := agentruntime.CronStorePaths(ses.ID)
			r.cronScheduler.SwitchSession(sessionPath, legacyPath, r.workingDir)
		}

		debug.Log("repl", "startup timing repl.createSession total=%s", time.Since(start).Round(time.Millisecond))
	} else {
		debug.Log("repl", "startup timing repl.createSession store.Save err=%v duration=%s", err, time.Since(saveStart).Round(time.Millisecond))
	}
}

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

		debug.Log("repl", "loadSession: keeping previous lock (new session lock unavailable)")
	}

	compacted, beforeTokens, afterTokens := agentruntime.RestoreSessionIntoAgent(r.agent, ses)
	r.model.SetSession(ses, r.store)

	if compacted {
		r.model.chatWriteSystem(nextSystemID(), fmt.Sprintf("Restored session was oversized (%d tokens), truncated to %d tokens to fit context window", beforeTokens, afterTokens))
	}

	if ses.Workspace != "" && ses.Workspace != r.workingDir {
		oldDir := r.workingDir
		r.workingDir = ses.Workspace
		if r.agent != nil {
			r.agent.SetWorkingDir(ses.Workspace)
		}
		debug.Log("repl", "loadSession: switched workingDir from %q to %q (session workspace)", oldDir, ses.Workspace)
	}

	r.switchSessionCronWithWorkspace(ses.ID, r.workingDir)

	if ses.PermissionMode != "" {
		sessionMode := permission.ParsePermissionMode(ses.PermissionMode)
		if cp, ok := r.model.policy.(*permission.ConfigPolicy); ok {
			cp.SetMode(sessionMode)
		}
		r.model.mode = sessionMode
		debug.Log("repl", "loadSession: restored permission mode %s from session", sessionMode)
	}

	if ses.SidebarVisible != nil {
		r.model.sidebarVisible = *ses.SidebarVisible
		debug.Log("repl", "loadSession: restored sidebar_visible=%v from session", *ses.SidebarVisible)
	}

	r.model.rebuildConversationFromMessages(ses.Messages)
	r.model.restoreHistoryFromMessages(ses.Messages)

	r.model.refreshCachedGitBranch()

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

func (r *REPL) execRestart() error {

	if r.sessionLock != nil {
		r.sessionLock.Release()
		r.sessionLock = nil
	}

	if r.preExecCleanup != nil {
		r.preExecCleanup()
	}

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

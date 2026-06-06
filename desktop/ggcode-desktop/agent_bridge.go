package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"

	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	"github.com/topcheer/ggcode/internal/acpclient"
	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/cron"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/relaycatalog"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/swarm"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tunnel"
)

// AgentBridge wraps the agent loop with sub-agent and swarm support.
type AgentBridge struct {
	cfg      *config.Config
	prov     provider.Provider
	resolved *config.ResolvedEndpoint
	agent    *agent.Agent
	ui       *UIState

	mu        sync.Mutex
	cancel    context.CancelFunc
	cancelled bool
	working   bool

	pendingMsgs *agentruntime.PendingQueue[struct{}]

	startTime time.Time // when current agent loop started

	Emitter *im.IMEmitter

	imRound        agentruntime.IMRoundState // per-round IM emission state
	mainWindow     fyne.Window
	permissionMode permission.PermissionMode

	registry             *tool.Registry
	workingDir           string
	sessionStore         session.Store
	currentSes           *session.Session
	rebuildCB            func()
	systemPromptBuilder  func() string
	usageTurnIndex       int
	lastMetricDigestTurn int
	metricCollector      *metrics.Collector
	metricCancel         context.CancelFunc
	acpClientMgr         *acpclient.ClientManager

	// Sub-agent and swarm managers.
	subAgentMgr *subagent.Manager
	swarmMgr    *swarm.Manager

	// Throttle state for high-frequency swarm teammate_text events.
	swarmTextMu      sync.Mutex
	swarmTextLast    map[string]time.Time // per-teammate last notify time
	swarmEventCounts map[string]int       // per-teammate cached event count for incremental updates

	// Mobile tunnel broker (nil if not sharing).
	tunnelBroker           *tunnel.Broker
	tunnelProjectionBroker *tunnel.Broker
	tunnelProjectionStore  *tunnel.ProjectionStore
	tunnelProjectionBroken bool
	tunnelMsgID            string
	tunnelMsgNeedsFinalize bool
	spawnedSet             map[string]bool // tracks which subagents have been announced to mobile
	approvalRequestID      string
	approvalToolName       string
	approvalDialog         dialog.Dialog
	askUserRequestID       string
	askUserRequest         tool.AskUserRequest
	askUserDialog          dialog.Dialog
	interactions           *agentruntime.InteractionBroker
	cronScheduler          *cron.Scheduler
}

type pendingMessage struct {
	Text   string
	Hidden bool
}

func (b *AgentBridge) currentTunnelBroker() *tunnel.Broker {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.tunnelProjectionBroker != nil {
		return b.tunnelProjectionBroker
	}
	return b.tunnelBroker
}

func (b *AgentBridge) currentShareTunnelBroker() *tunnel.Broker {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.tunnelBroker
}

func (b *AgentBridge) ensureTunnelProjectionBroker() *tunnel.Broker {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.tunnelProjectionBroker == nil {
		b.tunnelProjectionBroker = tunnel.NewBroker(nil)
	}
	return b.tunnelProjectionBroker
}

func (b *AgentBridge) bindTunnelProjectionSession() {
	b.mu.Lock()
	currentSes := b.currentSes
	store := b.tunnelProjectionStore
	b.mu.Unlock()
	if currentSes == nil || strings.TrimSpace(currentSes.ID) == "" {
		return
	}
	broker := b.ensureTunnelProjectionBroker()
	b.mu.Lock()
	b.tunnelProjectionBroken = false
	b.mu.Unlock()
	if store == nil {
		var err error
		store, err = tunnel.NewDefaultProjectionStore()
		if err != nil {
			b.mu.Lock()
			b.tunnelProjectionBroken = true
			b.mu.Unlock()
			log.Printf("[desktop] projection store init failed for %s: %v", currentSes.ID, err)
		} else {
			b.mu.Lock()
			if b.tunnelProjectionStore == nil {
				b.tunnelProjectionStore = store
			}
			store = b.tunnelProjectionStore
			b.mu.Unlock()
		}
	}

	_, err := agentruntime.PrepareProjectionBroker(broker, store, currentSes, func(ev tunnel.GatewayMessage) {
		b.recordProjectionEvent(ev)
	})
	if err != nil {
		b.mu.Lock()
		b.tunnelProjectionBroken = true
		b.mu.Unlock()
		log.Printf("[desktop] projection replay prep failed for %s: %v", currentSes.ID, err)
	}

	b.mu.Lock()
	if b.tunnelMsgID == "" {
		b.tunnelMsgID = broker.NextMessageID()
	}
	b.mu.Unlock()
}

func (b *AgentBridge) recordProjectionEvent(msg tunnel.GatewayMessage) {
	b.mu.Lock()
	store := b.tunnelProjectionStore
	b.mu.Unlock()
	if err := agentruntime.AppendProjectionEvent(store, msg); err != nil {
		b.mu.Lock()
		b.tunnelProjectionBroken = true
		b.mu.Unlock()
		log.Printf("[desktop] projection append failed for %s event=%s: %v", msg.SessionID, msg.EventID, err)
		b.RecordTunnelEvent(msg)
		return
	}
	b.RecordTunnelEvent(msg)
	if broker := b.currentShareTunnelBroker(); broker != nil {
		broker.PublishRecordedEvent(msg)
	}
}

func (b *AgentBridge) ensureTunnelMsgID(broker *tunnel.Broker) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	state := agentruntime.EnsureTunnelMainStream(agentruntime.TunnelMainStream{
		MessageID:     b.tunnelMsgID,
		NeedsFinalize: b.tunnelMsgNeedsFinalize,
	}, broker)
	b.tunnelMsgID = state.MessageID
	b.tunnelMsgNeedsFinalize = state.NeedsFinalize
	return b.tunnelMsgID
}

func (b *AgentBridge) tunnelReasoningMsgID(broker *tunnel.Broker) string {
	return agentruntime.TunnelReasoningMsgID(b.ensureTunnelMsgID(broker))
}

func (b *AgentBridge) markTunnelMainStreamActive() {
	b.mu.Lock()
	defer b.mu.Unlock()
	state := agentruntime.MarkTunnelMainStreamActive(agentruntime.TunnelMainStream{
		MessageID:     b.tunnelMsgID,
		NeedsFinalize: b.tunnelMsgNeedsFinalize,
	})
	b.tunnelMsgID = state.MessageID
	b.tunnelMsgNeedsFinalize = state.NeedsFinalize
}

func tunnelSubagentTextID(agentID string) string {
	return fmt.Sprintf("sa-%s", agentID)
}

func tunnelSubagentReasoningID(agentID string) string {
	return fmt.Sprintf("sa-%s-reasoning", agentID)
}

func (b *AgentBridge) flushTunnelTextStream(broker *tunnel.Broker, force bool) {
	b.mu.Lock()
	state := agentruntime.FlushTunnelMainStream(agentruntime.TunnelMainStream{
		MessageID:     b.tunnelMsgID,
		NeedsFinalize: b.tunnelMsgNeedsFinalize,
	}, broker, force)
	b.tunnelMsgID = state.MessageID
	b.tunnelMsgNeedsFinalize = state.NeedsFinalize
	b.mu.Unlock()
}

func (b *AgentBridge) AttachTunnelBroker(broker *tunnel.Broker) {
	var (
		working    bool
		resolved   *config.ResolvedEndpoint
		cfg        *config.Config
		mode       string
		status     tunnel.StatusData
		currentSes *session.Session
		attachCfg  agentruntime.TunnelAttachConfig
	)
	b.mu.Lock()
	b.tunnelBroker = broker
	working = b.working
	resolved = b.resolved
	cfg = b.cfg
	mode = b.permissionMode.String()
	currentSes = b.currentSes
	if working {
		state := agentruntime.EnsureTunnelMainStream(agentruntime.TunnelMainStream{
			MessageID:     b.tunnelMsgID,
			NeedsFinalize: b.tunnelMsgNeedsFinalize,
		}, broker)
		b.tunnelMsgID = state.MessageID
		b.tunnelMsgNeedsFinalize = state.NeedsFinalize
	}
	b.mu.Unlock()

	if broker == nil {
		return
	}
	b.bindTunnelProjectionSession()
	attachCfg.ReplayProvider = func() []tunnel.GatewayMessage {
		return b.CurrentSessionTunnelEvents()
	}
	if currentSes != nil && currentSes.ID != "" {
		attachCfg.SessionID = currentSes.ID
		attachCfg.AuthorityEpoch = b.currentSessionTunnelAuthorityEpoch()
	}
	if resolved != nil && cfg != nil {
		info := tunnel.SessionInfoData{
			Workspace: b.workingDir,
			Model:     resolved.Model,
			Provider:  resolved.VendorName,
			Mode:      mode,
			Version:   Version,
			Language:  cfg.Language,
		}
		if working {
			attachCfg.SessionInfo = &info
		}
	}
	if working {
		status = b.CurrentTunnelStatus()
		attachCfg.Status = &status
		activity := b.CurrentTunnelActivity()
		attachCfg.Activity = &activity
	}
	agentruntime.AttachTunnelBroker(broker, attachCfg)
}

func (b *AgentBridge) DetachTunnelBroker() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tunnelBroker = nil
}

func (b *AgentBridge) ClearCurrentSession() {
	b.mu.Lock()
	projectionBroker := b.tunnelProjectionBroker
	b.tunnelMsgID = ""
	b.tunnelMsgNeedsFinalize = false
	defer b.mu.Unlock()
	b.currentSes = nil
	if b.ui != nil {
		b.ui.SetSessionUsage(provider.TokenUsage{})
		b.ui.SetSessionMetrics(nil)
	}
	if projectionBroker != nil {
		projectionBroker.ResetSession()
	}
}

func (b *AgentBridge) markTunnelSubagentSpawned(id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.spawnedSet[id] {
		return false
	}
	b.spawnedSet[id] = true
	return true
}

func (b *AgentBridge) pushTunnelSubagentEvent(sa *subagent.SubAgent) {
	agentruntime.PushTunnelSubagentEvent(b.currentTunnelBroker, b.markTunnelSubagentSpawned, sa)
}

func NewAgentBridge(cfg *config.Config, prov provider.Provider, resolved *config.ResolvedEndpoint, workingDir string, ui *UIState) *AgentBridge {
	b := &AgentBridge{
		cfg:          cfg,
		prov:         prov,
		resolved:     resolved,
		ui:           ui,
		workingDir:   workingDir,
		spawnedSet:   make(map[string]bool),
		pendingMsgs:  agentruntime.NewPendingQueue[struct{}](),
		interactions: agentruntime.NewInteractionBroker(),
	}

	// Initialize session store (session created lazily in ensureSession).
	if store, err := session.NewDefaultStore(); err == nil {
		b.sessionStore = store
	}

	return b
}

func (b *AgentBridge) registerSubagentCallbacks() {
	if b.subAgentMgr == nil {
		return
	}

	// Forward sub-agent events to UI.
	b.subAgentMgr.SetOnUpdate(func(sa *subagent.SubAgent) {
		b.ui.UpdateAgentPanel(sa.ID, agentPanelFromSubAgent(sa))
		if sa.Status == subagent.StatusRunning {
			b.pushTunnelSubagentEvent(sa)
		}
	})
	b.subAgentMgr.SetOnComplete(func(sa *subagent.SubAgent) {
		b.ui.UpdateAgentPanel(sa.ID, agentPanelFromSubAgent(sa))
		b.pushTunnelSubagentEvent(sa)
	})

	// Forward sub-agent text chunks to mobile (unthrottled).
	b.subAgentMgr.SetOnStreamText(func(agentID, text string) {
		agentruntime.PushTunnelSubagentText(b.currentTunnelBroker, agentID, text)
	})
	b.subAgentMgr.SetOnReasoning(func(agentID, text string) {
		agentruntime.PushTunnelSubagentReasoning(b.currentTunnelBroker, agentID, text)
	})

	// Forward sub-agent tool calls/results to mobile.
	b.subAgentMgr.SetOnToolCall(func(agentID, toolID, toolName, displayName, args, detail string) {
		if displayName == "" {
			displayName = toolDisplayName(toolName, args)
		}
		if detail == "" {
			detail = toolArgSummary(toolName, args)
		}
		agentruntime.PushTunnelSubagentToolCall(b.currentTunnelBroker, agentID, toolID, toolName, displayName, args, detail)
	})
	b.subAgentMgr.SetOnToolResult(func(agentID, toolID, toolName, displayName, detail, result string, isError bool) {
		agentruntime.PushTunnelSubagentToolResult(b.currentTunnelBroker, agentID, toolID, toolName, displayName, detail, result, isError)
	})
}

func (b *AgentBridge) setupAgent() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.agent != nil {
		return nil
	}

	modeStr := b.cfg.DefaultMode
	if modeStr == "" {
		modeStr = "auto"
	}
	mode := permission.ParsePermissionMode(modeStr)
	policy := agentruntime.BuildInteractivePermissionPolicy(b.cfg, b.workingDir, false)
	b.permissionMode = mode

	// Apply impersonation from config (same as TUI startup).
	if b.cfg != nil && b.cfg.Impersonation.Preset != "" && b.cfg.Impersonation.Preset != "none" {
		if preset := provider.FindPresetByID(b.cfg.Impersonation.Preset); preset != nil {
			provider.SetActiveImpersonation(preset, b.cfg.Impersonation.CustomVersion, b.cfg.Impersonation.CustomHeaders)
		}
	}
	core, err := agentruntime.BuildInteractiveRuntimeCore(b.cfg, b.workingDir, policy)
	if err != nil {
		return fmt.Errorf("build runtime core: %w", err)
	}
	b.registry = core.Registry
	if b.cronScheduler == nil {
		b.cronScheduler = cron.NewScheduler(nil)
	}
	b.cronScheduler.SetEnqueue(func(prompt string) {
		b.handleCronPrompt(prompt)
	})
	_ = b.registry.Register(tool.CronCreateTool{Scheduler: b.cronScheduler})
	_ = b.registry.Register(tool.CronDeleteTool{Scheduler: b.cronScheduler})
	_ = b.registry.Register(tool.CronListTool{Scheduler: b.cronScheduler})
	mcpMgr := core.MCPManager
	autoMem := core.AutoMemory
	projectAutoMem := core.ProjectAutoMem
	commandMgr := core.CommandManager
	saveMemoryTool := core.SaveMemoryTool

	if b.acpClientMgr != nil {
		b.acpClientMgr.CloseAll()
	}
	b.acpClientMgr = acpclient.NewClientManager(b.workingDir, policy)
	b.acpClientMgr.SetApprovalHandler(func(ctx context.Context, toolName string, input string) permission.Decision {
		return b.requestToolApproval(ctx, toolName, input)
	})
	// Sub-agent manager.
	agentFactory := func(prov provider.Provider, t interface{}, systemPrompt string, maxTurns int) subagent.AgentRunner {
		return agent.NewAgent(prov, t.(*tool.Registry), systemPrompt, maxTurns)
	}
	b.registry.Register(agentruntime.NewSkillTool(commandMgr, mcpMgr, b.prov, b.registry, agentFactory, b.workingDir, b.recordSessionUsage))
	b.subAgentMgr = agentruntime.NewSubAgentManager(b.cfg.SubAgents, b.registry, b.prov, b.workingDir, b.recordSessionUsage, agentFactory)
	agentruntime.RegisterDelegateTool(b.registry, b.acpClientMgr, func() *subagent.Manager { return b.subAgentMgr }, b.workingDir, func() string {
		if b.agent != nil {
			return b.agent.WorkingDir()
		}
		return b.workingDir
	})
	b.registerSubagentCallbacks()

	// Swarm manager.
	swarmFactory := func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) swarm.AgentRunner {
		return agent.NewAgent(prov, tools.(*tool.Registry), systemPrompt, maxTurns)
	}
	toolBuilder := func(allowedTools []string) interface{} {
		reg := tool.NewRegistry()
		_ = tool.RegisterBuiltinTools(reg, nil, b.workingDir)
		return reg
	}
	b.swarmMgr = agentruntime.NewSwarmManager(b.cfg.Swarm, b.prov, b.registry, b.recordSessionUsage, swarmFactory, toolBuilder)

	// Re-register send_message with both managers.
	b.registry.Register(tool.SendMessageTool{Manager: b.subAgentMgr, SwarmMgr: b.swarmMgr})

	// Forward swarm events to UI and mobile tunnel.
	// teammate_text events are throttled to 500ms per teammate to avoid
	// flooding the UI with full-snapshot updates on every streaming token.
	// Status-change events (tool_call, idle, etc.) are sent immediately.
	b.swarmTextLast = make(map[string]time.Time)
	b.swarmEventCounts = make(map[string]int)

	b.swarmMgr.SetOnUpdate(func(ev swarm.Event) {
		switch ev.Type {
		case "teammate_text":
			// Throttle: at most one UpdateAgentPanel per teammate per 500ms.
			b.swarmTextMu.Lock()
			last := b.swarmTextLast[ev.TeammateID]
			now := time.Now()
			if !last.IsZero() && now.Sub(last) < 500*time.Millisecond {
				b.swarmTextMu.Unlock()
				// Still push to mobile tunnel (lightweight)
				if broker := b.currentTunnelBroker(); broker != nil {
					msgID := fmt.Sprintf("tm-%s", ev.TeammateID)
					broker.PushSubagentText(ev.TeammateID, msgID, ev.Result, false)
				}
				return
			}
			b.swarmTextLast[ev.TeammateID] = now
			cachedCount := b.swarmEventCounts[ev.TeammateID]
			b.swarmTextMu.Unlock()

			panel, newTotal := agentPanelFromSwarmEventIncremental(b.swarmMgr, ev, cachedCount)
			b.ui.UpdateAgentPanel(ev.TeammateID, panel)

			// Update the cached event count for next incremental update
			b.swarmTextMu.Lock()
			b.swarmEventCounts[ev.TeammateID] = newTotal
			b.swarmTextMu.Unlock()

		case "teammate_idle":
			if ev.Result != "" {
				// Clear cached event count on completion
				b.swarmTextMu.Lock()
				delete(b.swarmEventCounts, ev.TeammateID)
				b.swarmTextMu.Unlock()
				b.ui.UpdateAgentPanel(ev.TeammateID, agentPanelFromSwarmEvent(b.swarmMgr, ev))
			}

		case "teammate_spawned", "teammate_working", "teammate_shutdown",
			"teammate_tool_call", "teammate_tool_result", "teammate_error":
			// Status-change events: send immediately with full snapshot
			b.ui.UpdateAgentPanel(ev.TeammateID, agentPanelFromSwarmEvent(b.swarmMgr, ev))
		}

		// Push to mobile client
		if broker := b.currentTunnelBroker(); broker != nil {
			_ = broker
			agentruntime.PushTunnelSwarmEvent(b.currentTunnelBroker, b.swarmMgr, ev, toolDisplayName, toolArgSummary)
		}
	})

	b.systemPromptBuilder = func() string {
		return buildSystemPrompt(b.cfg, b.workingDir, autoMem, projectAutoMem, commandMgr)
	}
	systemPrompt := b.systemPromptBuilder()
	maxIter := b.cfg.MaxIterations
	if maxIter == 0 {
		maxIter = 200
	}
	b.agent = agent.NewAgent(b.prov, b.registry, systemPrompt, maxIter)
	saveMemoryTool.SetAfterSave(b.refreshSystemPrompt)

	b.agent.SetPermissionPolicy(policy)
	b.agent.SetUsageHandler(b.recordSessionUsage)

	// Metric collector — async, non-blocking for agent.
	collectorCtx, collectorCancel := context.WithCancel(context.Background())
	b.metricCancel = collectorCancel
	b.metricCollector = metrics.NewCollector(collectorCtx, 256, func(ev metrics.MetricEvent) {
		b.recordMetric(ev)
	})
	b.agent.SetMetricHandler(b.metricCollector.Emit)

	// Approval handler — popup dialog for tool approval
	b.agent.SetApprovalHandler(func(ctx context.Context, toolName string, input string) permission.Decision {
		return b.requestToolApproval(ctx, toolName, input)
	})

	// Ask user handler — popup dialog for questions
	if tl, ok := b.registry.Get("ask_user"); ok {
		if askTool, ok := tl.(*tool.AskUserTool); ok {
			askTool.SetHandler(func(ctx context.Context, req tool.AskUserRequest) (tool.AskUserResponse, error) {
				return b.handleAskUser(ctx, req)
			})
		}
	}

	_, _ = agentruntime.ApplyProjectMemoryToAgent(b.agent, b.workingDir)

	agentruntime.ApplyResolvedLimitsToAgent(b.agent, b.resolved)
	agentruntime.StartAsyncRelayModelLimitRefresh(b.cfg, b.resolved, b.agent, func(resp relaycatalog.ResolveResponse) {
		b.mu.Lock()
		if b.resolved != nil {
			if resp.ContextWindow > 0 {
				b.resolved.ContextWindow = resp.ContextWindow
			}
			if resp.MaxOutputTokens > 0 {
				b.resolved.MaxTokens = resp.MaxOutputTokens
			}
		}
		b.mu.Unlock()
	})
	b.ensureSession()
	return nil
}

func (b *AgentBridge) Send(userMsg string) error {
	log.Printf("[agent-bridge] Send called: %q", userMsg)
	return b.sendContent([]provider.ContentBlock{provider.TextBlock(userMsg)}, true)
}

func (b *AgentBridge) requestToolApproval(ctx context.Context, toolName string, input string) permission.Decision {
	if b.mainWindow == nil {
		return permission.Deny
	}
	requestID := ""
	if broker := b.currentTunnelBroker(); broker != nil {
		requestID = b.nextTunnelRequestID()
		agentruntime.PushTunnelApprovalRequest(broker, requestID, toolName, input, agentruntime.TunnelStateUpdate{
			HasStatus:   true,
			Status:      tunnel.StatusBusy,
			HasActivity: true,
			Activity:    b.CurrentTunnelActivity(),
		})
	}
	b.setPendingApproval(requestID, toolName)
	req := agentruntime.ApprovalRequest{ID: requestID, ToolName: toolName, Input: input}
	fyne.Do(func() {
		var d dialog.Dialog
		denyBtn := widget.NewButton(t("approval.deny"), func() {
			b.clearPendingApproval(requestID)
			b.pushTunnelApprovalResult(requestID, tunnel.DecisionDeny)
			b.interactions.ResolveApproval(requestID, permission.Deny)
			d.Hide()
		})
		allowBtn := widget.NewButton(t("approval.allow"), func() {
			b.clearPendingApproval(requestID)
			b.pushTunnelApprovalResult(requestID, tunnel.DecisionAllow)
			b.interactions.ResolveApproval(requestID, permission.Allow)
			d.Hide()
		})
		allowBtn.Importance = widget.HighImportance
		alwaysBtn := widget.NewButton(t("approval.always_allow"), func() {
			if b.agent != nil {
				if p, ok := b.agent.PermissionPolicy().(*permission.ConfigPolicy); ok {
					p.SetOverride(toolName, permission.Allow)
				}
			}
			b.clearPendingApproval(requestID)
			b.pushTunnelApprovalResult(requestID, tunnel.DecisionAlwaysAllow)
			b.interactions.ResolveApproval(requestID, permission.Allow)
			d.Hide()
		})
		alwaysBtn.Importance = widget.SuccessImportance

		var displayArgs string
		var raw map[string]interface{}
		if json.Unmarshal([]byte(input), &raw) == nil {
			for k, v := range raw {
				displayArgs += fmt.Sprintf("  %s: %v\n", k, v)
			}
			displayArgs = strings.TrimSpace(displayArgs)
		} else {
			displayArgs = truncate(input, 800)
		}
		content := widget.NewLabel(fmt.Sprintf("Tool: %s\n\n%s", toolName, displayArgs))
		content.Wrapping = fyne.TextWrapWord

		d = dialog.NewCustomWithoutButtons("Tool Approval",
			container.NewVBox(
				content,
				widget.NewSeparator(),
				container.NewHBox(layout.NewSpacer(), denyBtn, allowBtn, alwaysBtn),
			), b.mainWindow)
		if b.attachApprovalDialog(requestID, d) {
			d.Show()
		}
	})
	decision := b.interactions.AwaitApproval(ctx, req)
	if ctx.Err() != nil {
		b.hideDialog(b.clearPendingApproval(requestID))
	}
	b.clearPendingApproval(requestID)
	return decision
}

func (b *AgentBridge) SendContent(content []provider.ContentBlock) error {
	return b.sendContent(content, true)
}

func (b *AgentBridge) sendContent(content []provider.ContentBlock, persistUser bool) error {
	if err := b.setupAgent(); err != nil {
		return err
	}
	b.refreshSystemPrompt()
	if b.agent != nil {
		b.agent.SetInterruptionHandler(func() string {
			return b.drainPendingInterrupt()
		})
	}

	b.mu.Lock()
	b.working = true
	b.startTime = time.Now()
	b.usageTurnIndex++
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel
	b.mu.Unlock()

	// Persist visible user messages immediately (incremental), same as TUI.
	if persistUser {
		b.appendUserMessageContent(content)
	}

	go func() {
		var runErr error
		if broker := b.currentTunnelBroker(); broker != nil {
			b.ensureTunnelMsgID(broker)
			broker.SendSessionInfo(tunnel.SessionInfoData{
				Workspace: b.workingDir,
				Model:     b.resolved.Model,
				Provider:  b.resolved.VendorName,
				Mode:      b.permissionMode.String(),
				Version:   Version,
				Language:  b.cfg.Language,
			})
			broker.PushStatus(tunnel.StatusBusy, "")
			broker.PushActivity(b.CurrentTunnelActivity())
		}

		defer func() {
			if b.metricCollector != nil {
				b.metricCollector.Flush()
			}
			cancel()
			b.ui.FinalizeStreaming()
			if runErr == nil {
				b.appendTurnMetricsDigest(b.usageTurnIndex)
			}
			b.saveSession()

			// Fallback: clear all sub-agent/teammate panels now that
			// the agent loop is done. This ensures no stale tabs remain
			// even if per-panel completion callbacks were missed.
			b.ui.ClearAllAgentPanels()
			b.ui.notify(UIEvent{Type: EventAgentUpdate})

			b.mu.Lock()
			wasCancelled := b.cancelled
			b.working = false
			b.cancel = nil
			b.cancelled = false
			b.mu.Unlock()
			if wasCancelled {
				b.ui.AppendChat(ChatMessage{
					Role:    "system",
					Content: "(cancelled)",
					Time:    time.Now(),
				})
				if broker := b.currentTunnelBroker(); broker != nil {
					broker.PushSystemMessage("(cancelled)")
				}
			}

			// Check for queued message from user while busy.
			if pending, ok := b.drainPending(); ok {
				if pending.Hidden {
					_ = b.SendHiddenText(pending.Text)
				} else {
					b.ui.AppendChat(ChatMessage{
						Role:    "system",
						Content: "Processing queued message...",
						Time:    time.Now(),
					})
					if broker := b.currentTunnelBroker(); broker != nil {
						broker.PushSystemMessage("Processing queued message...")
					}
					_ = b.Send(pending.Text)
				}
			}
		}()

		onEvent := func(ev provider.StreamEvent) {
			defer logPanic("agent event handler")

			switch ev.Type {
			case provider.StreamEventSystem:
				b.ui.FinalizeStreaming()
				b.ui.AppendChat(ChatMessage{
					Role:    "system",
					Content: ev.Text,
					Time:    time.Now(),
				})
				// Mirror TUI: just close the current text stream and rotate.
				if broker := b.currentTunnelBroker(); broker != nil {
					b.flushTunnelTextStream(broker, true)
				}

			default:
				semantic, ok := agentruntime.HandleDesktopStreamEvent(ev, &b.imRound,
					agentruntime.NewDesktopEmitterAdapter(agentruntime.DesktopEmitterCallbacks{
						TriggerTypingFn: func() {
							if b.Emitter != nil {
								b.Emitter.TriggerTyping()
							}
						},
						EmitToolResultFn: func(toolName, rawArgs, result string, isError bool) {
							if b.Emitter == nil {
								return
							}
							b.Emitter.EmitEvent(im.OutboundEvent{
								Kind: im.OutboundEventToolResult,
								ToolRes: &im.ToolResultInfo{
									ToolName: toolName,
									Args:     rawArgs,
									Result:   result,
									IsError:  isError,
								},
							})
						},
						EmitRoundSummaryFn: func(text string, toolCalls, toolSuccesses, toolFailures int) {
							if b.Emitter != nil {
								b.Emitter.EmitRoundSummary(text, toolCalls, toolSuccesses, toolFailures)
							}
						},
					}),
					agentruntime.NewDesktopMirrorAdapter(agentruntime.DesktopMirrorCallbacks{
						CurrentBroker:   b.currentTunnelBroker,
						EnsureMessageID: b.ensureTunnelMsgID,
						ReasoningMsgID:  b.tunnelReasoningMsgID,
						MarkMainActive:  b.markTunnelMainStreamActive,
						FlushMainStream: b.flushTunnelTextStream,
					}),
				)
				if !ok {
					return
				}
				switch semantic.Type {
				case provider.StreamEventText:
					b.ui.AppendAssistantText(semantic.Text)

				case provider.StreamEventToolCallDone:
					b.ui.FinalizeStreaming()
					b.ui.AppendChat(ChatMessage{
						Role:     "tool",
						ToolName: semantic.ToolCall.Name,
						ToolID:   semantic.ToolCall.ID,
						ToolDesc: semantic.ToolCall.Inline,
						ToolArgs: semantic.ToolCall.Detail,
						ToolRaw:  semantic.ToolCall.RawArgs,
						Content:  "",
						Time:     time.Now(),
					})

				case provider.StreamEventToolResult:
					b.ui.UpdateToolResult(semantic.ToolResult.ID, semantic.ToolResult.Content, semantic.ToolResult.IsError)
					if semantic.ToolResult.Name == "spawn_agent" && b.subAgentMgr != nil {
						b.syncAgentPanels()
					}

				case provider.StreamEventReasoning:
					b.ui.AppendReasoning(semantic.Text)
				}
			}
		}

		runErr = b.agent.RunStreamWithContent(ctx, content, onEvent)
		if runErr != nil {
			b.mu.Lock()
			c := b.cancelled
			b.mu.Unlock()
			if !c {
				b.ui.AppendChat(ChatMessage{
					Role:    "error",
					Content: runErr.Error(),
					Time:    time.Now(),
				})
				if broker := b.currentTunnelBroker(); broker != nil {
					b.flushTunnelTextStream(broker, false)
					broker.PushError(runErr.Error())
				}
			}
		}
		// Mirror TUI handleDoneMsg: always push idle + clear activity
		// when the entire agent run finishes (success or error).
		if broker := b.currentTunnelBroker(); broker != nil {
			b.flushTunnelTextStream(broker, false)
			broker.PushStatus(tunnel.StatusIdle, "")
			broker.PushActivity("")
		}
	}()
	return nil
}

func (b *AgentBridge) SendHiddenText(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	return b.sendContent([]provider.ContentBlock{provider.TextBlock(text)}, false)
}

// syncAgentPanels reads all sub-agents and updates UIState.
func (b *AgentBridge) syncAgentPanels() {
	if b.subAgentMgr == nil {
		return
	}
	for _, sa := range b.subAgentMgr.List() {
		b.ui.UpdateAgentPanel(sa.ID, agentPanelFromSubAgent(sa))
	}
}

func (b *AgentBridge) Cancel() {
	b.mu.Lock()
	if b.cancel != nil {
		b.cancelled = true
		b.cancel()
	}
	b.mu.Unlock()
	// Notify mobile client
	if broker := b.currentTunnelBroker(); broker != nil {
		b.flushTunnelTextStream(broker, true)
		broker.PushStatus(tunnel.StatusIdle, "cancelled")
		broker.PushActivity("")
	}
}

func (b *AgentBridge) Close() {
	b.Cancel()
	if b.acpClientMgr != nil {
		b.acpClientMgr.CloseAll()
	}
	if b.metricCancel != nil {
		b.metricCancel()
	}
	if b.metricCollector != nil {
		b.metricCollector.Stop()
	}
	if b.cronScheduler != nil {
		b.cronScheduler.Shutdown()
	}
}

func (b *AgentBridge) CurrentTunnelStatus() tunnel.StatusData {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.working {
		return tunnel.StatusData{Status: tunnel.StatusBusy}
	}
	return tunnel.StatusData{Status: tunnel.StatusIdle}
}

func (b *AgentBridge) CurrentTunnelActivity() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch {
	case b.approvalRequestID != "":
		return "approval"
	case b.askUserRequestID != "":
		return "ask_user"
	case b.working:
		return "processing"
	default:
		return ""
	}
}

// PushUserMessageToMobile pushes a user message to the mobile client.
// Called from ChatView when the desktop user types — NOT from onCommand
// (mobile-initiated messages) to avoid echo.
func (b *AgentBridge) PushUserMessageToMobile(text string) {
	if broker := b.currentTunnelBroker(); broker != nil {
		broker.PushUserMessage(text)
	}
}

func (b *AgentBridge) HandleTunnelUserMessage(data tunnel.MessageData) {
	text := strings.TrimSpace(data.Text)
	if text == "" {
		return
	}
	if b.ui != nil {
		b.ui.AppendChat(ChatMessage{Role: "user", Content: text, Time: time.Now()})
	}
	data.Text = text
	data.MessageID = tunnel.NormalizeClientMessageID(data.MessageID)
	if broker := b.currentTunnelBroker(); broker != nil {
		broker.PushUserMessageData(data)
	}
	_ = b.Send(text)
}

func (b *AgentBridge) BindShareCommands(broker *tunnel.Broker, onLanguage func(string), onTheme func(string)) {
	if broker == nil {
		return
	}
	broker.OnCommand(func(cmd tunnel.GatewayMessage) {
		agentruntime.RouteTunnelCommand(cmd, agentruntime.TunnelCommandHooks{
			OnUserMessage: func(data tunnel.MessageData) {
				b.HandleTunnelUserMessage(data)
			},
			OnApprovalResponse: func(data tunnel.ApprovalResponseData) {
				b.handleMobileApprovalResponse(data)
			},
			OnAskUserResponse: func(data tunnel.AskUserResponseData) {
				b.handleMobileAskUserResponse(data)
			},
			OnLanguageChange: func(data tunnel.LanguageChangeData) {
				if onLanguage != nil {
					onLanguage(data.Language)
				}
			},
			OnThemeChange: func(data tunnel.ThemeChangeData) {
				if onTheme != nil {
					onTheme(data.Theme)
				}
			},
			OnInterrupt: func() {
				b.Cancel()
			},
			OnServerAck: func(messageID string) {
				broker.PushServerAck(messageID)
			},
		})
	})
}

func (b *AgentBridge) PushSystemMessageToMobile(text string) {
	if broker := b.currentTunnelBroker(); broker != nil && strings.TrimSpace(text) != "" {
		broker.PushSystemMessage(text)
	}
}

func (b *AgentBridge) handleCronPrompt(prompt string) {
	sysMsg := t("cron.firing")
	if strings.TrimSpace(sysMsg) == "" || sysMsg == "cron.firing" {
		sysMsg = "⏰ Cron job triggered"
	}
	if b.ui != nil {
		b.ui.AppendChat(ChatMessage{Role: "system", Content: sysMsg, Time: time.Now()})
	}
	if broker := b.currentTunnelBroker(); broker != nil {
		broker.PushUserMessageData(tunnel.MessageData{
			Text:        prompt,
			DisplayText: sysMsg,
			Kind:        tunnel.MessageKindCron,
		})
	}
	if strings.TrimSpace(prompt) == "" {
		return
	}
	if b.IsWorking() {
		b.QueueHiddenMessage(prompt)
		return
	}
	_ = b.SendHiddenText(prompt)
}

func (b *AgentBridge) PushErrorToMobile(text string) {
	if broker := b.currentTunnelBroker(); broker != nil && strings.TrimSpace(text) != "" {
		broker.PushError(text)
	}
}

// QueueMessage stores a user message to be sent after the current agent turn.
func (b *AgentBridge) QueueMessage(msg string) {
	b.pendingMsgs.Enqueue(msg, false, struct{}{})
}

func (b *AgentBridge) QueueHiddenMessage(msg string) {
	b.pendingMsgs.Enqueue(msg, true, struct{}{})
}

// drainPending returns and clears the next queued message.
func (b *AgentBridge) drainPending() (pendingMessage, bool) {
	msg, ok := b.pendingMsgs.Consume()
	if !ok {
		return pendingMessage{}, false
	}
	return pendingMessage{Text: msg.Text, Hidden: msg.Hidden}, true
}

func (b *AgentBridge) drainPendingInterrupt() string {
	pending, ok := b.drainPending()
	if !ok {
		return ""
	}
	if !pending.Hidden {
		b.appendUserMessageContent([]provider.ContentBlock{provider.TextBlock(pending.Text)})
	}
	return strings.TrimSpace(pending.Text)
}

func (b *AgentBridge) IsWorking() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.working
}

func (b *AgentBridge) Elapsed() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.working {
		return 0
	}
	return time.Since(b.startTime)
}

func (b *AgentBridge) ContextWindow() int {
	if b.agent == nil {
		return b.resolved.ContextWindow
	}
	return b.agent.ContextManager().ContextWindow()
}

func (b *AgentBridge) TokenCount() int {
	if b.agent == nil {
		return 0
	}
	return b.agent.ContextManager().TokenCount()
}

func (b *AgentBridge) AutoCompactThreshold() int {
	if b.agent == nil {
		return 0
	}
	return b.agent.ContextManager().AutoCompactThreshold()
}

func (b *AgentBridge) recordSessionUsage(usage provider.TokenUsage) {
	b.mu.Lock()
	if b.currentSes == nil || b.sessionStore == nil {
		b.mu.Unlock()
		return
	}
	b.currentSes.TokenUsage = b.currentSes.TokenUsage.Add(usage)
	b.currentSes.AddUsageForEndpoint(b.currentSes.Vendor, b.currentSes.Endpoint, usage)
	b.currentSes.UpdatedAt = time.Now()
	ses := b.currentSes
	store := b.sessionStore
	current := b.currentSes.UsageForEndpoint(b.currentSes.Vendor, b.currentSes.Endpoint)
	entry := session.UsageEntry{
		Timestamp: time.Now(),
		TurnIndex: b.usageTurnIndex,
		Model:     ses.Model,
		Vendor:    ses.Vendor,
		Endpoint:  ses.Endpoint,
		Usage:     usage,
	}
	b.mu.Unlock()

	if b.ui != nil {
		b.ui.SetSessionUsage(current)
	}
	if jsonlStore, ok := store.(*session.JSONLStore); ok {
		_ = jsonlStore.AppendMetaToDisk(ses)
		_ = jsonlStore.AppendUsageEntry(ses, entry)
	} else {
		_ = store.Save(ses)
	}
}

// recordMetric persists a metric event to the session JSONL.
// Called by the metrics collector goroutine (async, non-blocking for agent).
func (b *AgentBridge) recordMetric(ev metrics.MetricEvent) {
	b.mu.Lock()
	ses := b.currentSes
	store := b.sessionStore
	ev.TurnIndex = b.usageTurnIndex
	if ses != nil {
		ev.Model = ses.Model
		ev.Vendor = ses.Vendor
		ev.Endpoint = ses.Endpoint
		ses.Metrics = append(ses.Metrics, ev)
		ses.AppendMetricForEndpoint(ses.Vendor, ses.Endpoint, ev)
		ses.UpdatedAt = time.Now()
	}
	b.mu.Unlock()
	if ses == nil || store == nil {
		return
	}
	if b.ui != nil {
		b.ui.SetSessionMetrics(ses.MetricsForEndpoint(ses.Vendor, ses.Endpoint))
	}
	if jsonlStore, ok := store.(*session.JSONLStore); ok {
		_ = jsonlStore.AppendMetric(ses, ev)
		_ = jsonlStore.AppendMetaToDisk(ses)
	} else {
		_ = store.Save(ses)
	}
}

func (b *AgentBridge) Resolved() *config.ResolvedEndpoint {
	return b.resolved
}

// SubAgentPanels returns a snapshot of all active/finished agent panels.
func (b *AgentBridge) SubAgentPanels() []AgentPanelData {
	if b.subAgentMgr == nil {
		return nil
	}
	agents := b.subAgentMgr.List()
	panels := make([]AgentPanelData, 0, len(agents))
	for _, sa := range agents {
		panels = append(panels, agentPanelFromSubAgent(sa))
	}
	return panels
}

// SwarmPanels returns a snapshot of all teammate panels.
func (b *AgentBridge) SwarmPanels() []AgentPanelData {
	if b.swarmMgr == nil {
		return nil
	}
	panels := b.ui.GetAgentPanels()
	result := make([]AgentPanelData, 0)
	for _, p := range panels {
		if p.Kind == "teammate" {
			result = append(result, p)
		}
	}
	return result
}

// ── Helpers ──────────────────────────────────────────

func toolDisplayName(toolName, rawArgs string) string {
	return tool.DescribeTool(toolName, rawArgs).DisplayName
}

func toolDescription(toolName, rawArgs string) string {
	present := tool.DescribeTool(toolName, rawArgs)
	return tool.FormatToolInline(present.DisplayName, present.Detail)
}

func toolArgSummary(toolName, rawArgs string) string {
	return tool.DescribeTool(toolName, rawArgs).Detail
}

func extractJSONField(rawArgs, field string) string {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return ""
	}
	if v, ok := args[field].(string); ok {
		return v
	}
	return ""
}

func agentPanelFromSubAgent(sa *subagent.SubAgent) AgentPanelData {
	name := sa.Name
	if name == "" {
		name = "agent"
	}
	task := sa.Task
	if sa.DisplayTask != "" {
		task = sa.DisplayTask
	}
	// Use Events() for full panel construction (first call or after completion).
	// This is called at ~10Hz (subagent notifyUpdate throttle), not per-token.
	events := make([]AgentEventEntry, 0)
	for _, ev := range sa.Events() {
		entry := AgentEventEntry{
			Type:            agentEventTypeStr(ev.Type),
			ToolName:        ev.ToolName,
			ToolID:          ev.ToolID,
			ToolArgs:        ev.ToolArgs,
			ToolDisplayName: ev.ToolDisplayName,
			ToolDetail:      ev.ToolDetail,
		}
		switch ev.Type {
		case subagent.AgentEventToolResult:
			entry.Content = ev.Result
			entry.IsError = ev.IsError
		case subagent.AgentEventToolCall:
			// ToolCall has no Text field; use toolArgSummary as description.
			entry.Content = ev.ToolDetail
			if entry.Content == "" {
				entry.Content = toolArgSummary(ev.ToolName, ev.ToolArgs)
			}
		default:
			entry.Content = ev.Text
		}
		events = append(events, entry)
	}
	errStr := ""
	if sa.Error != nil {
		errStr = sa.Error.Error()
	}
	p := AgentPanelData{
		ID:     sa.ID,
		Name:   name,
		Kind:   "subagent",
		Status: string(sa.Status),
		Task:   task,
		Result: sa.Result,
		Error:  errStr,
		Events: events,
	}
	if sa.Status == subagent.StatusCompleted || sa.Status == subagent.StatusFailed {
		if !sa.EndedAt.IsZero() {
			p.CompletedAt = sa.EndedAt
		} else {
			p.CompletedAt = time.Now()
		}
	}
	return p
}

func agentPanelFromSwarmEvent(mgr *swarm.Manager, ev swarm.Event) AgentPanelData {
	snap, ok := mgr.TeammateSnapshot(ev.TeammateID)
	if !ok {
		return AgentPanelData{ID: ev.TeammateID, Name: ev.TeammateName, Kind: "teammate", TeamID: ev.TeamID}
	}
	events := make([]AgentEventEntry, 0, len(snap.Events))
	for _, e := range snap.Events {
		entry := AgentEventEntry{
			Type:     teammateEventTypeStr(e.Type),
			ToolName: e.ToolName,
			ToolID:   e.ToolID,
			ToolArgs: e.ToolArgs,
		}
		switch e.Type {
		case swarm.TeammateEventToolResult:
			entry.Content = e.Result
			entry.IsError = e.IsError
		case swarm.TeammateEventToolCall:
			entry.Content = toolArgSummary(e.ToolName, e.ToolArgs)
		default:
			entry.Content = e.Text
		}
		events = append(events, entry)
	}
	errStr := ""
	if ev.Error != nil {
		errStr = ev.Error.Error()
	}
	p := AgentPanelData{
		ID:     snap.ID,
		Name:   snap.Name,
		Kind:   "teammate",
		Status: string(snap.Status),
		Task:   snap.CurrentTask,
		Result: snap.LastResult,
		Error:  errStr,
		TeamID: ev.TeamID,
		Events: events,
	}
	if snap.Status == swarm.TeammateIdle && !snap.EndedAt.IsZero() {
		p.CompletedAt = snap.EndedAt
	}
	return p
}

// agentPanelFromSwarmEventIncremental builds panel data using incremental events
// instead of a full TeammateSnapshot. fromIdx is the cached event count from the
// last update — only new events (index >= fromIdx) are fetched. Returns the panel
// data and the new total event count to cache for the next call.
func agentPanelFromSwarmEventIncremental(mgr *swarm.Manager, ev swarm.Event, fromIdx int) (AgentPanelData, int) {
	// Fetch only incremental events
	newEvents, totalCount, ok := mgr.TeammateEventsSince(ev.TeammateID, fromIdx)
	if !ok {
		return AgentPanelData{ID: ev.TeammateID, Name: ev.TeammateName, Kind: "teammate", Status: "working", TeamID: ev.TeamID}, 0
	}

	entries := make([]AgentEventEntry, 0, len(newEvents))
	for _, e := range newEvents {
		entry := AgentEventEntry{
			Type:     teammateEventTypeStr(e.Type),
			ToolName: e.ToolName,
			ToolID:   e.ToolID,
			ToolArgs: e.ToolArgs,
		}
		switch e.Type {
		case swarm.TeammateEventToolResult:
			entry.Content = e.Result
			entry.IsError = e.IsError
		case swarm.TeammateEventToolCall:
			entry.Content = toolArgSummary(e.ToolName, e.ToolArgs)
		default:
			entry.Content = e.Text
		}
		entries = append(entries, entry)
	}

	p := AgentPanelData{
		ID:     ev.TeammateID,
		Name:   ev.TeammateName,
		Kind:   "teammate",
		Status: "working",
		TeamID: ev.TeamID,
		Events: entries,
	}

	return p, totalCount
}

func teammateEventTypeStr(t swarm.TeammateEventType) string {
	switch t {
	case swarm.TeammateEventText:
		return "text"
	case swarm.TeammateEventToolCall:
		return "tool_call"
	case swarm.TeammateEventToolResult:
		return "tool_result"
	case swarm.TeammateEventError:
		return "error"
	}
	return "unknown"
}

func agentEventTypeStr(t subagent.AgentEventType) string {
	switch t {
	case subagent.AgentEventText:
		return "text"
	case subagent.AgentEventToolCall:
		return "tool_call"
	case subagent.AgentEventToolResult:
		return "tool_result"
	case subagent.AgentEventError:
		return "error"
	case subagent.AgentEventReasoning:
		return "reasoning"
	}
	return "unknown"
}

func buildSystemPrompt(cfg *config.Config, workingDir string, globalAutoMem, projectAutoMem *memory.AutoMemory, commandMgr *commands.Manager) string {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	return agentruntime.BuildInteractiveSystemPrompt(cfg, workingDir, permission.ParsePermissionMode(cfg.DefaultMode), nil, commandMgr, globalAutoMem, projectAutoMem, "")
}

func (b *AgentBridge) refreshSystemPrompt() {
	b.mu.Lock()
	agent := b.agent
	builder := b.systemPromptBuilder
	b.mu.Unlock()
	if agent == nil || builder == nil {
		return
	}
	agent.UpdateSystemPrompt(builder())
}

// saveSession persists the current conversation to the session store.
// If the session has no messages, it is deleted instead.
func (b *AgentBridge) saveSession() {
	if b.sessionStore == nil || b.currentSes == nil {
		return
	}
	b.mu.Lock()
	agent := b.agent
	b.mu.Unlock()
	if agent == nil {
		return
	}
	_ = agentruntime.SaveSessionMessages(b.sessionStore, b.currentSes, agent.Messages())
}

// ensureSession creates a new session if one doesn't exist yet.
func (b *AgentBridge) ensureSession() {
	if b.sessionStore == nil {
		return
	}
	vendor := ""
	endpoint := ""
	model := ""
	if b.cfg != nil {
		vendor = b.cfg.Vendor
		endpoint = b.cfg.Endpoint
		model = b.cfg.Model
	}
	state, created, err := agentruntime.EnsureSession(b.sessionStore, b.currentSes, vendor, endpoint, model, b.workingDir)
	if err != nil || !created {
		return
	}
	b.currentSes = state.Session
	b.usageTurnIndex = state.UsageTurnIndex
	b.lastMetricDigestTurn = state.LastMetricDigestTurn
	if b.ui != nil {
		b.ui.SetSessionUsage(state.Session.UsageForEndpoint(state.Session.Vendor, state.Session.Endpoint))
		b.ui.SetSessionMetrics(state.Session.MetricsForEndpoint(state.Session.Vendor, state.Session.Endpoint))
	}
	b.bindTunnelProjectionSession()
	if broker := b.currentShareTunnelBroker(); broker != nil {
		broker.AnnounceActiveSession(state.Session.ID)
	}
}

// SessionStore returns the session store for external use (e.g., sidebar).
func (b *AgentBridge) SessionStore() session.Store {
	return b.sessionStore
}

// CurrentSession returns the current session.
func (b *AgentBridge) CurrentSession() *session.Session {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.currentSes
}

func (b *AgentBridge) PrepareShareBroker(broker *tunnel.Broker, snapshotProvider func() tunnel.BrokerSnapshot) {
	if broker == nil || snapshotProvider == nil {
		return
	}
	b.ensureSession()
	snapshot := snapshotProvider()
	sessionID := ""
	if current := b.CurrentSession(); current != nil {
		sessionID = current.ID
	}
	b.PrepareCurrentSessionTunnelLedger()
	replayedCanonical := agentruntime.PublishShareState(broker, sessionID, snapshot, b.CurrentSessionTunnelEvents(), true)
	broker.SetSnapshotProvider(snapshotProvider)
	b.AttachTunnelBroker(broker)
	if !replayedCanonical {
		latest := snapshotProvider()
		if !agentruntime.ShareSnapshotMatches(snapshot, latest) {
			agentruntime.PublishShareState(broker, sessionID, latest, nil, true)
		}
	}
}

func (b *AgentBridge) PublishCurrentShareSnapshot(broker *tunnel.Broker, snapshotProvider func() tunnel.BrokerSnapshot, reset bool, resetLedger bool) {
	if broker == nil || snapshotProvider == nil {
		return
	}
	current := b.CurrentSession()
	if current == nil {
		return
	}
	if resetLedger {
		b.ResetCurrentSessionTunnelLedger()
	}
	agentruntime.PublishShareState(broker, current.ID, snapshotProvider(), nil, reset)
}

func (b *AgentBridge) CurrentSessionTunnelEvents() []tunnel.GatewayMessage {
	b.mu.Lock()
	currentSes := b.currentSes
	store := b.tunnelProjectionStore
	broken := b.tunnelProjectionBroken
	b.mu.Unlock()
	if currentSes == nil || broken {
		return nil
	}
	if store != nil && strings.TrimSpace(currentSes.ID) != "" {
		events, err := agentruntime.ProjectionReplay(store, currentSes.ID)
		if err != nil {
			b.mu.Lock()
			b.tunnelProjectionBroken = true
			b.mu.Unlock()
			log.Printf("[desktop] projection replay load failed for %s: %v", currentSes.ID, err)
			return nil
		}
		if events != nil {
			return events
		}
	}
	return nil
}

func (b *AgentBridge) hydrateProjectionReplayFromSessionLedger(currentSes *session.Session, store *tunnel.ProjectionStore, replay []tunnel.GatewayMessage) []tunnel.GatewayMessage {
	updated, err := agentruntime.HydrateProjectionReplayFromSessionLedger(store, currentSes, replay)
	if err != nil {
		sessionID := ""
		if currentSes != nil {
			sessionID = currentSes.ID
		}
		log.Printf("[desktop] projection hydrate from session ledger failed for %s: %v", sessionID, err)
		return replay
	}
	return updated
}

func (b *AgentBridge) currentSessionTunnelAuthorityEpoch() uint64 {
	b.mu.Lock()
	currentSes := b.currentSes
	store := b.tunnelProjectionStore
	b.mu.Unlock()
	if currentSes == nil || strings.TrimSpace(currentSes.ID) == "" || store == nil {
		return 1
	}
	epoch, err := agentruntime.ProjectionAuthorityEpoch(store, currentSes.ID)
	if err != nil || epoch == 0 {
		return 1
	}
	return epoch
}

func desktopSessionMessagesToTunnelHistory(messages []provider.Message) []tunnel.HistoryEntry {
	history := make([]tunnel.HistoryEntry, 0, len(messages)*2)
	for _, msg := range messages {
		if msg.Role == "user" || msg.Role == "tool" {
			var textParts []string
			for _, block := range msg.Content {
				switch block.Type {
				case "text":
					if strings.TrimSpace(block.Text) != "" {
						textParts = append(textParts, strings.TrimSpace(block.Text))
					}
				case "tool_result":
					result := block.Output
					if len(result) > 500 {
						result = result[:500] + "..."
					}
					history = append(history, tunnel.HistoryEntry{
						Role:     "tool_result",
						ToolID:   block.ToolID,
						ToolName: block.ToolName,
						Result:   result,
						IsError:  block.IsError,
					})
				}
			}
			if len(textParts) > 0 {
				history = append(history, tunnel.HistoryEntry{
					Role:    "user",
					Content: strings.Join(textParts, "\n"),
				})
			}
			continue
		}
		if msg.Role != "assistant" {
			continue
		}
		for _, block := range msg.Content {
			if reasoning := desktopContentBlockReasoningText(block); reasoning != "" {
				history = append(history, tunnel.HistoryEntry{
					Role:    "reasoning",
					Content: reasoning,
				})
			}
			switch block.Type {
			case "text":
				if strings.TrimSpace(block.Text) != "" {
					history = append(history, tunnel.HistoryEntry{
						Role:    "assistant",
						Content: strings.TrimSpace(block.Text),
					})
				}
			case "tool_use":
				argsStr := string(block.Input)
				if len(argsStr) > 200 {
					argsStr = argsStr[:200] + "..."
				}
				title := toolDisplayName(block.ToolName, string(block.Input))
				detail := toolArgSummary(block.ToolName, string(block.Input))
				if detail == title {
					detail = ""
				}
				history = append(history, tunnel.HistoryEntry{
					Role:            "tool_call",
					ToolID:          block.ToolID,
					ToolName:        block.ToolName,
					ToolDisplayName: title,
					ToolArgs:        argsStr,
					ToolDetail:      detail,
				})
			}
		}
	}
	return history
}

func desktopContentBlockReasoningText(block provider.ContentBlock) string {
	if text := tunnel.NormalizeReasoningChunk(block.ReasoningContent); text != "" {
		return text
	}
	if strings.TrimSpace(block.ThinkingData) != "" {
		return tunnel.RedactedReasoningPlaceholder
	}
	return ""
}

func desktopChatMessagesToTunnelHistory(messages []ChatMessage) []tunnel.HistoryEntry {
	history := make([]tunnel.HistoryEntry, 0, len(messages)*2)
	for _, msg := range messages {
		switch msg.Role {
		case "user":
			if strings.TrimSpace(msg.Content) != "" {
				history = append(history, tunnel.HistoryEntry{Role: "user", Content: strings.TrimSpace(msg.Content)})
			}
		case "assistant":
			if strings.TrimSpace(msg.Content) != "" {
				history = append(history, tunnel.HistoryEntry{Role: "assistant", Content: strings.TrimSpace(msg.Content)})
			}
		case "reasoning":
			if strings.TrimSpace(msg.Content) != "" {
				history = append(history, tunnel.HistoryEntry{Role: "reasoning", Content: strings.TrimSpace(msg.Content)})
			}
		case "system":
			if strings.TrimSpace(msg.Content) != "" {
				history = append(history, tunnel.HistoryEntry{Role: "system", Content: strings.TrimSpace(msg.Content)})
			}
		case "error":
			if strings.TrimSpace(msg.Content) != "" {
				history = append(history, tunnel.HistoryEntry{Role: "error", Content: strings.TrimSpace(msg.Content)})
			}
		case "tool":
			argsStr := msg.ToolRaw
			if len(argsStr) > 200 {
				argsStr = argsStr[:200] + "..."
			}
			history = append(history, tunnel.HistoryEntry{
				Role:            "tool_call",
				ToolID:          msg.ToolID,
				ToolName:        msg.ToolName,
				ToolDisplayName: toolDisplayName(msg.ToolName, msg.ToolRaw),
				ToolArgs:        argsStr,
				ToolDetail:      msg.ToolArgs,
			})
			if strings.TrimSpace(msg.Content) != "" {
				history = append(history, tunnel.HistoryEntry{
					Role:     "tool_result",
					ToolID:   msg.ToolID,
					ToolName: msg.ToolName,
					Result:   strings.TrimSpace(msg.Content),
					IsError:  msg.IsError,
				})
			}
		}
	}
	return history
}

func desktopTunnelEventsToHistory(events []session.TunnelEvent) []tunnel.HistoryEntry {
	var history []tunnel.HistoryEntry
	textByID := make(map[string]string)
	reasoningByID := make(map[string]string)
	finalizeText := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		text := strings.TrimSpace(textByID[id])
		delete(textByID, id)
		if text == "" {
			return
		}
		history = append(history, tunnel.HistoryEntry{Role: "assistant", Content: text})
	}
	finalizeReasoning := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		text := strings.TrimSpace(reasoningByID[id])
		delete(reasoningByID, id)
		if text == "" {
			return
		}
		history = append(history, tunnel.HistoryEntry{Role: "reasoning", Content: text})
	}
	finalizeAllReasoning := func() {
		if len(reasoningByID) == 0 {
			return
		}
		ids := make([]string, 0, len(reasoningByID))
		for id := range reasoningByID {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			finalizeReasoning(id)
		}
	}

	for _, ev := range events {
		switch ev.Type {
		case tunnel.EventUserMessage:
			var data tunnel.MessageData
			if err := json.Unmarshal(ev.Data, &data); err != nil {
				continue
			}
			if data.Kind == "cron" {
				text := strings.TrimSpace(data.DisplayText)
				if text == "" {
					text = strings.TrimSpace(data.Text)
				}
				if text != "" {
					history = append(history, tunnel.HistoryEntry{Role: "system", Content: text})
				}
				continue
			}
			text := strings.TrimSpace(data.Text)
			if text == "" {
				text = strings.TrimSpace(data.DisplayText)
			}
			if text != "" {
				history = append(history, tunnel.HistoryEntry{Role: "user", Content: text})
			}
		case tunnel.EventSystemMessage:
			var data tunnel.MessageData
			if err := json.Unmarshal(ev.Data, &data); err != nil {
				continue
			}
			text := strings.TrimSpace(data.Text)
			if text == "" {
				text = strings.TrimSpace(data.DisplayText)
			}
			if text != "" {
				history = append(history, tunnel.HistoryEntry{Role: "system", Content: text})
			}
		case tunnel.EventText:
			var data tunnel.TextData
			if err := json.Unmarshal(ev.Data, &data); err != nil {
				continue
			}
			if strings.TrimSpace(data.ID) == "" || data.Chunk == "" {
				continue
			}
			if _, seen := textByID[data.ID]; !seen {
				finalizeAllReasoning()
			}
			textByID[data.ID] += data.Chunk
			if data.Done {
				finalizeText(data.ID)
			}
		case tunnel.EventTextDone:
			var data tunnel.TextData
			if err := json.Unmarshal(ev.Data, &data); err != nil {
				continue
			}
			id := data.ID
			if strings.TrimSpace(id) == "" {
				id = ev.StreamID
			}
			finalizeText(id)
		case tunnel.EventReasoning:
			var data tunnel.TextData
			if err := json.Unmarshal(ev.Data, &data); err != nil {
				continue
			}
			if strings.TrimSpace(data.ID) == "" || data.Chunk == "" {
				continue
			}
			reasoningByID[data.ID] += data.Chunk
			if data.Done {
				finalizeReasoning(data.ID)
			}
		case tunnel.EventReasoningDone:
			var data tunnel.TextData
			if err := json.Unmarshal(ev.Data, &data); err != nil {
				continue
			}
			id := data.ID
			if strings.TrimSpace(id) == "" {
				id = ev.StreamID
			}
			finalizeReasoning(id)
		case tunnel.EventToolCall:
			finalizeAllReasoning()
			var data tunnel.ToolCallData
			if err := json.Unmarshal(ev.Data, &data); err != nil {
				continue
			}
			history = append(history, tunnel.HistoryEntry{
				Role:            "tool_call",
				ToolID:          data.ToolID,
				ToolName:        data.ToolName,
				ToolDisplayName: data.DisplayName,
				ToolArgs:        data.Args,
				ToolDetail:      data.Detail,
			})
		case tunnel.EventToolResult:
			finalizeAllReasoning()
			var data tunnel.ToolResultData
			if err := json.Unmarshal(ev.Data, &data); err != nil {
				continue
			}
			history = append(history, tunnel.HistoryEntry{
				Role:     "tool_result",
				ToolID:   data.ToolID,
				ToolName: data.ToolName,
				Result:   data.Result,
				IsError:  data.IsError,
			})
		case tunnel.EventError:
			finalizeAllReasoning()
			var data tunnel.ErrorData
			if err := json.Unmarshal(ev.Data, &data); err != nil {
				continue
			}
			if text := strings.TrimSpace(data.Message); text != "" {
				history = append(history, tunnel.HistoryEntry{Role: "error", Content: text})
			}
		}
	}

	for id := range reasoningByID {
		finalizeReasoning(id)
	}
	return history
}

func desktopTunnelHistoryMatches(a, b []tunnel.HistoryEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Role != b[i].Role ||
			a[i].Content != b[i].Content ||
			a[i].Kind != b[i].Kind ||
			a[i].ToolID != b[i].ToolID ||
			a[i].ToolName != b[i].ToolName ||
			a[i].ToolDisplayName != b[i].ToolDisplayName ||
			a[i].ToolArgs != b[i].ToolArgs ||
			a[i].ToolDetail != b[i].ToolDetail ||
			a[i].Result != b[i].Result ||
			a[i].IsError != b[i].IsError {
			return false
		}
	}
	return true
}

func desktopMergeTunnelHistory(base, tail []tunnel.HistoryEntry) []tunnel.HistoryEntry {
	if len(tail) == 0 {
		return base
	}
	if len(base) == 0 {
		return append([]tunnel.HistoryEntry(nil), tail...)
	}
	maxOverlap := len(base)
	if len(tail) < maxOverlap {
		maxOverlap = len(tail)
	}
	overlap := 0
	for size := maxOverlap; size > 0; size-- {
		if desktopTunnelHistoryMatches(base[len(base)-size:], tail[:size]) {
			overlap = size
			break
		}
	}
	out := append([]tunnel.HistoryEntry(nil), base...)
	out = append(out, tail[overlap:]...)
	return out
}

func (b *AgentBridge) CurrentTunnelHistory() []tunnel.HistoryEntry {
	if b.ui != nil {
		if msgs := b.ui.TakeMessages(); len(msgs) > 0 {
			return desktopChatMessagesToTunnelHistory(msgs)
		}
	}
	if b.currentSes != nil {
		return desktopSessionMessagesToTunnelHistory(b.currentSes.Messages)
	}
	return nil
}

func (b *AgentBridge) currentIncompleteTunnelHistoryTail() []tunnel.HistoryEntry {
	b.mu.Lock()
	if b.currentSes == nil || b.currentSes.TunnelEventsComplete || len(b.currentSes.TunnelEvents) == 0 {
		b.mu.Unlock()
		return nil
	}
	events := append([]session.TunnelEvent(nil), b.currentSes.TunnelEvents...)
	b.mu.Unlock()
	return desktopTunnelEventsToHistory(events)
}

func (b *AgentBridge) CurrentTunnelSnapshotHistory() []tunnel.HistoryEntry {
	history := b.CurrentTunnelHistory()
	if tail := b.currentIncompleteTunnelHistoryTail(); len(tail) > 0 {
		history = desktopMergeTunnelHistory(history, tail)
	}
	return history
}

func (b *AgentBridge) PrepareCurrentSessionTunnelLedger() {
	b.mu.Lock()
	if b.currentSes == nil || b.sessionStore == nil {
		b.mu.Unlock()
		return
	}
	snapshotHistory := b.CurrentTunnelHistory()
	needsSave := false
	switch {
	case b.currentSes.TunnelEventsComplete:
		if desktopTunnelHistoryMatches(desktopTunnelEventsToHistory(b.currentSes.TunnelEvents), snapshotHistory) {
			b.mu.Unlock()
			return
		}
		b.currentSes.TunnelEvents = nil
		b.currentSes.TunnelEventsComplete = false
		needsSave = true
	case len(snapshotHistory) == 0:
		b.currentSes.TunnelEvents = nil
		b.currentSes.TunnelEventsComplete = true
		needsSave = true
	case len(b.currentSes.TunnelEvents) > 0:
		b.currentSes.TunnelEvents = nil
		needsSave = true
	}
	if !needsSave {
		b.mu.Unlock()
		return
	}
	ses := b.currentSes
	store := b.sessionStore
	projectionStore := b.tunnelProjectionStore
	projectionBroker := b.tunnelProjectionBroker
	shareBroker := b.tunnelBroker
	b.mu.Unlock()

	_ = store.Save(ses)
	if projectionStore != nil {
		if epoch, err := projectionStore.CutAuthority(ses.ID); err == nil {
			if projectionBroker != nil {
				projectionBroker.SetAuthorityEpoch(epoch)
			}
			if shareBroker != nil {
				shareBroker.SetAuthorityEpoch(epoch)
			}
		}
	}
}

func (b *AgentBridge) ResetCurrentSessionTunnelLedger() {
	b.mu.Lock()
	if b.currentSes == nil || b.sessionStore == nil {
		b.mu.Unlock()
		return
	}
	b.currentSes.TunnelEvents = nil
	b.currentSes.TunnelEventsComplete = false
	ses := b.currentSes
	store := b.sessionStore
	projectionStore := b.tunnelProjectionStore
	projectionBroker := b.tunnelProjectionBroker
	shareBroker := b.tunnelBroker
	b.mu.Unlock()

	_ = store.Save(ses)
	if projectionStore != nil {
		if epoch, err := projectionStore.CutAuthority(ses.ID); err == nil {
			if projectionBroker != nil {
				projectionBroker.SetAuthorityEpoch(epoch)
			}
			if shareBroker != nil {
				shareBroker.SetAuthorityEpoch(epoch)
			}
		}
	}
}

func (b *AgentBridge) RecordTunnelEvent(msg tunnel.GatewayMessage) {
	if msg.EventID == "" || msg.Type == tunnel.EventSnapshotReset {
		return
	}
	b.mu.Lock()
	if b.currentSes == nil || b.sessionStore == nil {
		b.mu.Unlock()
		return
	}
	record := session.TunnelEvent{
		EventID:  msg.EventID,
		StreamID: msg.StreamID,
		Type:     msg.Type,
		Data:     append([]byte(nil), msg.Data...),
	}
	b.currentSes.TunnelEvents = append(b.currentSes.TunnelEvents, record)
	ses := b.currentSes
	store := b.sessionStore
	b.mu.Unlock()

	if jsonlStore, ok := store.(*session.JSONLStore); ok {
		_ = jsonlStore.AppendTunnelEventToDisk(ses, record)
	} else {
		_ = store.Save(ses)
	}
}

// ResetAgent clears the cached agent so the next request recreates it
// with fresh provider settings (e.g. new impersonation headers).
func (b *AgentBridge) ResetAgent() {
	b.mu.Lock()
	b.agent = nil
	b.mu.Unlock()
	if b.acpClientMgr != nil {
		b.acpClientMgr.CloseAll()
		b.acpClientMgr = nil
	}
}

// ResumeSession loads a session by ID and restores its messages into the agent.
func (b *AgentBridge) ResumeSession(id string) error {
	if b.sessionStore == nil {
		return fmt.Errorf("no session store")
	}
	if err := b.setupAgent(); err != nil {
		return err
	}

	state, err := agentruntime.LoadSession(b.sessionStore, id)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}
	ses := state.Session

	agentruntime.RestoreSessionIntoAgent(b.agent, ses)

	b.mu.Lock()
	b.currentSes = state.Session
	b.usageTurnIndex = state.UsageTurnIndex
	b.lastMetricDigestTurn = state.LastMetricDigestTurn
	b.mu.Unlock()
	b.bindTunnelProjectionSession()
	if b.ui != nil {
		b.ui.SetSessionUsage(ses.UsageForEndpoint(ses.Vendor, ses.Endpoint))
		b.ui.SetSessionMetrics(ses.MetricsForEndpoint(ses.Vendor, ses.Endpoint))
	}
	if broker := b.currentShareTunnelBroker(); broker != nil {
		broker.AnnounceActiveSession(ses.ID)
	}
	return nil
}

// RebuildCallback is set by the UI to rebuild the chat view after resume.
func (b *AgentBridge) SetRebuildCallback(cb func()) {
	b.rebuildCB = cb
}

// appendUserMessageContent persists the user message to disk immediately (incremental),
// matching TUI's appendUserMessage behavior. Accepts full content blocks (text + image).
func (b *AgentBridge) appendUserMessageContent(content []provider.ContentBlock) {
	if b.sessionStore == nil || b.currentSes == nil {
		return
	}
	msg := provider.Message{
		Role:    "user",
		Content: content,
	}
	b.currentSes.Messages = append(b.currentSes.Messages, msg)
	b.currentSes.UpdatedAt = time.Now()

	// Auto-generate title from first text block.
	if b.currentSes.Title == "" || b.currentSes.Title == "New session" {
		for _, block := range content {
			if block.Type == "text" && block.Text != "" {
				text := block.Text
				if len([]rune(text)) > 60 {
					text = string([]rune(text)[:57]) + "..."
				}
				b.currentSes.Title = text
				break
			}
		}
	}

	// Incremental append to disk (same as TUI).
	if jsonlStore, ok := b.sessionStore.(*session.JSONLStore); ok {
		_ = jsonlStore.AppendMessageToDisk(b.currentSes, msg)
	} else {
		_ = b.sessionStore.Save(b.currentSes)
	}
}

// extractTextFromBlocks extracts plain text from content blocks.
func extractTextFromBlocks(blocks []provider.ContentBlock) string {
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// handleAskUser shows a dialog for ask_user tool questions.
func (b *AgentBridge) handleAskUser(ctx context.Context, req tool.AskUserRequest) (tool.AskUserResponse, error) {
	if b.mainWindow == nil || len(req.Questions) == 0 {
		return tool.AskUserResponse{Status: "skipped"}, nil
	}

	requestID := ""
	// Push to mobile tunnel client
	if broker := b.currentTunnelBroker(); broker != nil {
		requestID = b.nextTunnelRequestID()
		agentruntime.PushTunnelAskUserRequest(broker, requestID, req, agentruntime.TunnelStateUpdate{
			HasStatus:   true,
			Status:      tunnel.StatusBusy,
			HasActivity: true,
			Activity:    b.CurrentTunnelActivity(),
		})
	}
	b.setPendingAskUser(requestID, req)
	request := agentruntime.AskUserRequest{ID: requestID, Request: req}
	fyne.Do(func() {
		// Build form from questions
		var answers []tool.AskUserAnswer
		var items []*widget.FormItem
		// labelToID maps choice label back to choice ID for each question index.
		labelToID := make(map[int]map[string]string)

		for _, q := range req.Questions {
			switch q.Kind {
			case "text":
				entry := widget.NewMultiLineEntry()
				entry.PlaceHolder = q.Placeholder
				items = append(items, &widget.FormItem{Text: q.Title, Widget: entry})
				answers = append(answers, tool.AskUserAnswer{ID: q.ID, Title: q.Title, Kind: q.Kind, Answered: true})

			case "single":
				choices := make([]string, len(q.Choices))
				for i, c := range q.Choices {
					choices[i] = c.Label
				}
				sel := widget.NewSelect(choices, nil)
				labelToID[len(items)] = make(map[string]string)
				for _, c := range q.Choices {
					labelToID[len(items)][c.Label] = c.ID
				}
				if len(choices) > 0 {
					sel.SetSelectedIndex(0)
				}
				notesEntry := widget.NewEntry()
				notesEntry.PlaceHolder = "Additional notes (optional)"
				box := container.NewVBox(sel, notesEntry)
				items = append(items, &widget.FormItem{Text: q.Title, Widget: box})
				answers = append(answers, tool.AskUserAnswer{ID: q.ID, Title: q.Title, Kind: q.Kind, Answered: true})

			case "multi":
				labels := make([]string, len(q.Choices))
				for i, c := range q.Choices {
					labels[i] = c.Label
				}
				checks := widget.NewCheckGroup(labels, nil)
				labelToID[len(items)] = make(map[string]string)
				for _, c := range q.Choices {
					labelToID[len(items)][c.Label] = c.ID
				}
				notesEntry := widget.NewEntry()
				notesEntry.PlaceHolder = "Additional notes (optional)"
				box := container.NewVBox(checks, notesEntry)
				items = append(items, &widget.FormItem{Text: q.Title, Widget: box})
				answers = append(answers, tool.AskUserAnswer{ID: q.ID, Title: q.Title, Kind: q.Kind, Answered: true})

			default:
				entry := widget.NewEntry()
				entry.PlaceHolder = q.Placeholder
				items = append(items, &widget.FormItem{Text: q.Title, Widget: entry})
				answers = append(answers, tool.AskUserAnswer{ID: q.ID, Title: q.Title, Kind: q.Kind, Answered: true})
			}
		}

		d := dialog.NewForm(req.Title, "Submit", "Skip",
			items,
			func(ok bool) {
				if !ok {
					response := tool.AskUserResponse{
						Status:        tool.AskUserStatusCancelled,
						Title:         req.Title,
						QuestionCount: len(req.Questions),
					}
					b.clearPendingAskUser(requestID)
					b.pushTunnelAskUserResponse(requestID, response)
					b.interactions.ResolveAskUser(requestID, response)
					return
				}
				// Collect answers from form items
				for i, item := range items {
					switch w := item.Widget.(type) {
					case *widget.Entry:
						answers[i].FreeformText = w.Text
					case *widget.Select:
						if m := labelToID[i]; m != nil {
							if id, ok := m[w.Selected]; ok {
								answers[i].SelectedChoiceIDs = []string{id}
							}
						}
						answers[i].SelectedChoices = []string{w.Selected}
					case *fyne.Container:
						// VBox with main widget + notes entry
						for _, obj := range w.Objects {
							switch obj.(type) {
							case *widget.Select:
								sel := obj.(*widget.Select)
								if m := labelToID[i]; m != nil {
									if id, ok := m[sel.Selected]; ok {
										answers[i].SelectedChoiceIDs = []string{id}
									}
								}
								answers[i].SelectedChoices = []string{sel.Selected}
							case *widget.CheckGroup:
								cg := obj.(*widget.CheckGroup)
								answers[i].SelectedChoices = cg.Selected
								var ids []string
								if m := labelToID[i]; m != nil {
									for _, lbl := range cg.Selected {
										if id, ok := m[lbl]; ok {
											ids = append(ids, id)
										}
									}
								}
								answers[i].SelectedChoiceIDs = ids
							case *widget.Entry:
								answers[i].FreeformText = obj.(*widget.Entry).Text
							}
						}
					}
				}
				finalAnswers := make([]tool.AskUserAnswer, len(req.Questions))
				answeredCount := 0
				for i, question := range req.Questions {
					finalAnswers[i] = agentruntime.BuildAskUserAnswer(question, answers[i].SelectedChoiceIDs, answers[i].FreeformText)
					if finalAnswers[i].Answered {
						answeredCount++
					}
				}
				response := tool.AskUserResponse{
					Status:        tool.AskUserStatusSubmitted,
					Title:         req.Title,
					QuestionCount: len(req.Questions),
					AnsweredCount: answeredCount,
					Answers:       finalAnswers,
				}
				b.clearPendingAskUser(requestID)
				b.pushTunnelAskUserResponse(requestID, response)
				b.interactions.ResolveAskUser(requestID, response)
			}, b.mainWindow)
		d.Resize(fyne.NewSize(500, 400))
		if b.attachAskUserDialog(requestID, d) {
			d.Show()
		}
	})

	response, err := b.interactions.AwaitAskUser(ctx, request)
	if ctx.Err() != nil {
		b.hideDialog(b.clearPendingAskUser(requestID))
	}
	b.clearPendingAskUser(requestID)
	return response, err
}

// truncate shortens a string for display.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// SetMainWindow sets the main window reference for dialogs.
func (b *AgentBridge) SetMainWindow(w fyne.Window) {
	b.mainWindow = w
}

// SetPermissionMode updates the agent permission mode at runtime.
func (b *AgentBridge) SetPermissionMode(mode permission.PermissionMode) {
	if b.agent == nil {
		return
	}
	b.permissionMode = mode
	policy := permission.NewConfigPolicyWithMode(nil, []string{b.workingDir}, mode)
	b.agent.SetPermissionPolicy(policy)
}

// SwitchModel hot-swaps the model on the running agent without losing conversation
// history. If the agent is currently running, the new provider takes effect on the
// next LLM call in the agent loop.
func (b *AgentBridge) SwitchModel(model string) error {
	if model == "" || b.cfg == nil {
		return fmt.Errorf("model is empty or config is nil")
	}
	resolved, prov, err := agentruntime.ActivateCurrentSelection(b.cfg, b.cfg.Vendor, b.cfg.Endpoint, model)
	if err != nil {
		return fmt.Errorf("activate current selection: %w", err)
	}
	agentruntime.ApplyProviderToAgent(b.agent, prov, resolved)

	// Update bridge state so status bar reflects the new model.
	b.mu.Lock()
	b.prov = prov
	b.resolved = resolved
	if b.currentSes != nil {
		b.currentSes.Model = resolved.Model
	}
	b.mu.Unlock()
	agentruntime.StartAsyncRelayModelLimitRefresh(b.cfg, resolved, b.agent, func(resp relaycatalog.ResolveResponse) {
		b.mu.Lock()
		if b.resolved != nil {
			if resp.ContextWindow > 0 {
				b.resolved.ContextWindow = resp.ContextWindow
			}
			if resp.MaxOutputTokens > 0 {
				b.resolved.MaxTokens = resp.MaxOutputTokens
			}
		}
		b.mu.Unlock()
	})

	return nil
}

func (b *AgentBridge) nextTunnelRequestID() string {
	broker := b.currentTunnelBroker()
	if broker == nil {
		return ""
	}
	return broker.NextMessageID()
}

func (b *AgentBridge) setPendingApproval(id, toolName string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.approvalRequestID = id
	b.approvalToolName = toolName
	b.approvalDialog = nil
}

func (b *AgentBridge) attachApprovalDialog(id string, dlg dialog.Dialog) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.approvalToolName == "" && b.approvalRequestID == "" {
		return false
	}
	if strings.TrimSpace(id) != "" && b.approvalRequestID != id {
		return false
	}
	b.approvalDialog = dlg
	return true
}

func (b *AgentBridge) consumePendingApproval(id string) (string, dialog.Dialog, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.approvalToolName == "" && b.approvalRequestID == "" {
		return "", nil, false
	}
	if strings.TrimSpace(id) != "" && b.approvalRequestID != "" && b.approvalRequestID != id {
		return "", nil, false
	}
	toolName := b.approvalToolName
	dlg := b.approvalDialog
	b.approvalRequestID = ""
	b.approvalToolName = ""
	b.approvalDialog = nil
	return toolName, dlg, true
}

func (b *AgentBridge) clearPendingApproval(id string) dialog.Dialog {
	b.mu.Lock()
	defer b.mu.Unlock()
	if strings.TrimSpace(id) != "" && b.approvalRequestID != "" && b.approvalRequestID != id {
		return nil
	}
	dlg := b.approvalDialog
	b.approvalRequestID = ""
	b.approvalToolName = ""
	b.approvalDialog = nil
	return dlg
}

func (b *AgentBridge) PendingApprovalRequest() (string, string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.approvalToolName == "" && b.approvalRequestID == "" {
		return "", "", false
	}
	return b.approvalRequestID, b.approvalToolName, true
}

func (b *AgentBridge) setPendingAskUser(id string, req tool.AskUserRequest) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.askUserRequestID = id
	b.askUserRequest = req
	b.askUserDialog = nil
}

func (b *AgentBridge) attachAskUserDialog(id string, dlg dialog.Dialog) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.askUserRequest.Questions) == 0 && b.askUserRequestID == "" {
		return false
	}
	if strings.TrimSpace(id) != "" && b.askUserRequestID != id {
		return false
	}
	b.askUserDialog = dlg
	return true
}

func (b *AgentBridge) consumePendingAskUser(id string) (tool.AskUserRequest, dialog.Dialog, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.askUserRequest.Questions) == 0 && b.askUserRequestID == "" {
		return tool.AskUserRequest{}, nil, false
	}
	if strings.TrimSpace(id) != "" && b.askUserRequestID != "" && b.askUserRequestID != id {
		return tool.AskUserRequest{}, nil, false
	}
	req := b.askUserRequest
	dlg := b.askUserDialog
	b.askUserRequestID = ""
	b.askUserRequest = tool.AskUserRequest{}
	b.askUserDialog = nil
	return req, dlg, true
}

func (b *AgentBridge) clearPendingAskUser(id string) dialog.Dialog {
	b.mu.Lock()
	defer b.mu.Unlock()
	if strings.TrimSpace(id) != "" && b.askUserRequestID != "" && b.askUserRequestID != id {
		return nil
	}
	dlg := b.askUserDialog
	b.askUserRequestID = ""
	b.askUserRequest = tool.AskUserRequest{}
	b.askUserDialog = nil
	return dlg
}

func (b *AgentBridge) PendingAskUserRequest() (string, tool.AskUserRequest, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.askUserRequest.Questions) == 0 && b.askUserRequestID == "" {
		return "", tool.AskUserRequest{}, false
	}
	return b.askUserRequestID, b.askUserRequest, true
}

func (b *AgentBridge) RespondApproval(requestID, decision string) {
	toolName, dlg, ok := b.consumePendingApproval(requestID)
	if !ok {
		return
	}
	b.hideDialog(dlg)
	decisionValue := agentruntime.ResolveTunnelApproval(decision, toolName, func(toolName string) {
		if b.agent != nil {
			if p, ok := b.agent.PermissionPolicy().(*permission.ConfigPolicy); ok {
				p.SetOverride(toolName, permission.Allow)
			}
		}
	})
	b.interactions.ResolveApproval(requestID, decisionValue)
	agentruntime.ApplyTunnelStateUpdate(b.currentTunnelBroker(), agentruntime.TunnelStateUpdate{
		HasStatus:   true,
		Status:      tunnel.StatusBusy,
		HasActivity: true,
		Activity:    b.CurrentTunnelActivity(),
	})
}

func (b *AgentBridge) RespondAskUser(requestID string, response tool.AskUserResponse) {
	_, dlg, ok := b.consumePendingAskUser(requestID)
	if !ok {
		return
	}
	b.hideDialog(dlg)
	b.interactions.ResolveAskUser(requestID, response)
	agentruntime.ApplyTunnelStateUpdate(b.currentTunnelBroker(), agentruntime.TunnelStateUpdate{
		HasStatus:   true,
		Status:      tunnel.StatusBusy,
		HasActivity: true,
		Activity:    b.CurrentTunnelActivity(),
	})
}

func (b *AgentBridge) hideDialog(dlg dialog.Dialog) {
	if dlg == nil {
		return
	}
	fyne.Do(func() {
		dlg.Hide()
	})
}

func (b *AgentBridge) pushTunnelApprovalResult(id, decision string) {
	agentruntime.PushTunnelApprovalResult(b.currentTunnelBroker(), id, decision, agentruntime.TunnelStateUpdate{
		HasStatus:   true,
		Status:      tunnel.StatusBusy,
		HasActivity: true,
		Activity:    b.CurrentTunnelActivity(),
	})
}

func (b *AgentBridge) pushTunnelAskUserResponse(id string, response tool.AskUserResponse) {
	agentruntime.PushTunnelAskUserResponse(b.currentTunnelBroker(), id, response, agentruntime.TunnelStateUpdate{
		HasStatus:   true,
		Status:      tunnel.StatusBusy,
		HasActivity: true,
		Activity:    b.CurrentTunnelActivity(),
	})
}

func (b *AgentBridge) handleMobileApprovalResponse(data tunnel.ApprovalResponseData) {
	toolName, dlg, ok := b.consumePendingApproval(data.ID)
	if !ok {
		return
	}
	b.hideDialog(dlg)
	decision := agentruntime.ResolveTunnelApproval(data.Decision, toolName, func(toolName string) {
		if b.agent != nil {
			if p, ok := b.agent.PermissionPolicy().(*permission.ConfigPolicy); ok {
				p.SetOverride(toolName, permission.Allow)
			}
		}
	})
	b.interactions.ResolveApproval(data.ID, decision)
	agentruntime.ApplyTunnelStateUpdate(b.currentTunnelBroker(), agentruntime.TunnelStateUpdate{
		HasStatus:   true,
		Status:      tunnel.StatusBusy,
		HasActivity: true,
		Activity:    b.CurrentTunnelActivity(),
	})
}

func (b *AgentBridge) handleMobileAskUserResponse(data tunnel.AskUserResponseData) {
	req, dlg, ok := b.consumePendingAskUser(data.ID)
	if !ok {
		return
	}
	b.hideDialog(dlg)
	response := buildAskUserResponseFromTunnel(req, data.Status, data.Answers)
	b.interactions.ResolveAskUser(data.ID, response)
	agentruntime.ApplyTunnelStateUpdate(b.currentTunnelBroker(), agentruntime.TunnelStateUpdate{
		HasStatus:   true,
		Status:      tunnel.StatusBusy,
		HasActivity: true,
		Activity:    b.CurrentTunnelActivity(),
	})
}

func buildTunnelAskUserQuestions(req tool.AskUserRequest) []tunnel.AskUserQuestion {
	return agentruntime.BuildTunnelAskUserQuestions(req)
}

func buildAskUserResponseFromTunnel(req tool.AskUserRequest, status string, answers []tunnel.AskUserAnswer) tool.AskUserResponse {
	return agentruntime.BuildAskUserResponseFromTunnel(req, status, answers)
}

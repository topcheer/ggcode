package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"

	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/cron"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/provider"
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

	pendingMu   sync.Mutex
	pendingMsgs []pendingMessage

	startTime time.Time // when current agent loop started

	Emitter *im.IMEmitter

	imRound        imRoundState // per-round IM emission state
	mainWindow     fyne.Window
	permissionMode permission.PermissionMode

	registry        *tool.Registry
	workingDir      string
	sessionStore    session.Store
	currentSes      *session.Session
	rebuildCB       func()
	usageTurnIndex  int
	metricCollector *metrics.Collector

	// Sub-agent and swarm managers.
	subAgentMgr *subagent.Manager
	swarmMgr    *swarm.Manager

	// Throttle state for high-frequency swarm teammate_text events.
	swarmTextMu      sync.Mutex
	swarmTextLast    map[string]time.Time // per-teammate last notify time
	swarmEventCounts map[string]int       // per-teammate cached event count for incremental updates

	// Mobile tunnel broker (nil if not sharing).
	tunnelBroker      *tunnel.Broker
	tunnelMsgID       string
	spawnedSet        map[string]bool // tracks which subagents have been announced to mobile
	approvalRespCh    chan permission.Decision
	approvalRequestID string
	approvalToolName  string
	approvalDialog    dialog.Dialog
	askUserRespCh     chan tool.AskUserResponse
	askUserRequestID  string
	askUserRequest    tool.AskUserRequest
	askUserDialog     dialog.Dialog
	cronScheduler     *cron.Scheduler
}

type pendingMessage struct {
	Text   string
	Hidden bool
}

func (b *AgentBridge) currentTunnelBroker() *tunnel.Broker {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.tunnelBroker
}

func (b *AgentBridge) ensureTunnelMsgID(broker *tunnel.Broker) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.tunnelMsgID == "" && broker != nil {
		b.tunnelMsgID = broker.NextMessageID()
	}
	return b.tunnelMsgID
}

func (b *AgentBridge) rotateTunnelMsgID(broker *tunnel.Broker) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if broker == nil {
		b.tunnelMsgID = ""
		return
	}
	b.tunnelMsgID = broker.NextMessageID()
}

func (b *AgentBridge) tunnelReasoningMsgID(broker *tunnel.Broker) string {
	msgID := b.ensureTunnelMsgID(broker)
	if msgID == "" {
		return ""
	}
	return msgID + "-reasoning"
}

func tunnelSubagentTextID(agentID string) string {
	return fmt.Sprintf("sa-%s", agentID)
}

func tunnelSubagentReasoningID(agentID string) string {
	return fmt.Sprintf("sa-%s-reasoning", agentID)
}

func (b *AgentBridge) flushTunnelTextStream(broker *tunnel.Broker) {
	if broker == nil {
		return
	}
	broker.PushReasoningDone(b.tunnelReasoningMsgID(broker))
	msgID := b.ensureTunnelMsgID(broker)
	broker.PushTextDone(msgID)
	b.rotateTunnelMsgID(broker)
}

func (b *AgentBridge) AttachTunnelBroker(broker *tunnel.Broker) {
	var (
		working    bool
		resolved   *config.ResolvedEndpoint
		cfg        *config.Config
		mode       string
		status     tunnel.StatusData
		currentSes *session.Session
	)
	b.mu.Lock()
	b.tunnelBroker = broker
	working = b.working
	resolved = b.resolved
	cfg = b.cfg
	mode = b.permissionMode.String()
	currentSes = b.currentSes
	if working && broker != nil && b.tunnelMsgID == "" {
		b.tunnelMsgID = broker.NextMessageID()
	}
	b.mu.Unlock()

	if broker == nil {
		return
	}
	if currentSes != nil && currentSes.ID != "" {
		broker.AnnounceActiveSession(currentSes.ID)
	}
	if !working {
		return
	}
	if resolved != nil && cfg != nil {
		broker.SendSessionInfo(tunnel.SessionInfoData{
			Workspace: b.workingDir,
			Model:     resolved.Model,
			Provider:  resolved.VendorName,
			Mode:      mode,
			Version:   Version,
			Language:  cfg.Language,
		})
	}
	status = b.CurrentTunnelStatus()
	if status.Status != "" {
		broker.PushStatus(status.Status, status.Message)
	}
	broker.PushActivity(b.CurrentTunnelActivity())
}

func (b *AgentBridge) DetachTunnelBroker() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tunnelBroker = nil
}

func (b *AgentBridge) ClearCurrentSession() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.currentSes = nil
	if b.ui != nil {
		b.ui.SetSessionUsage(provider.TokenUsage{})
		b.ui.SetSessionMetrics(nil)
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

func NewAgentBridge(cfg *config.Config, prov provider.Provider, resolved *config.ResolvedEndpoint, workingDir string, ui *UIState) *AgentBridge {
	b := &AgentBridge{
		cfg:        cfg,
		prov:       prov,
		resolved:   resolved,
		ui:         ui,
		workingDir: workingDir,
		spawnedSet: make(map[string]bool),
	}

	// Initialize session store (session created lazily in ensureSession).
	if store, err := session.NewDefaultStore(); err == nil {
		b.sessionStore = store
	}

	return b
}

func (b *AgentBridge) setupAgent() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.agent != nil {
		return nil
	}

	b.registry = tool.NewRegistry()

	// Apply impersonation from config (same as TUI startup).
	if b.cfg != nil && b.cfg.Impersonation.Preset != "" && b.cfg.Impersonation.Preset != "none" {
		if preset := provider.FindPresetByID(b.cfg.Impersonation.Preset); preset != nil {
			provider.SetActiveImpersonation(preset, b.cfg.Impersonation.CustomVersion, b.cfg.Impersonation.CustomHeaders)
		}
	}

	if err := tool.RegisterBuiltinTools(b.registry, nil, b.workingDir); err != nil {
		return fmt.Errorf("register builtin tools: %w", err)
	}
	if b.cronScheduler == nil {
		b.cronScheduler = cron.NewScheduler(nil)
	}
	b.cronScheduler.SetEnqueue(func(prompt string) {
		b.handleCronPrompt(prompt)
	})
	_ = b.registry.Register(tool.CronCreateTool{Scheduler: b.cronScheduler})
	_ = b.registry.Register(tool.CronDeleteTool{Scheduler: b.cronScheduler})
	_ = b.registry.Register(tool.CronListTool{Scheduler: b.cronScheduler})

	mergedServers, _ := mcp.MergeStartupServers(b.workingDir, b.cfg.MCPServers)
	mcpMgr := plugin.NewMCPManager(mergedServers, b.registry)
	_ = b.registry.Register(tool.ListMCPCapabilitiesTool{Runtime: mcpMgr})
	_ = b.registry.Register(tool.GetMCPPromptTool{Runtime: mcpMgr})
	_ = b.registry.Register(tool.ReadMCPResourceTool{Runtime: mcpMgr})

	pluginMgr := plugin.NewManager()
	pluginMgr.LoadAll(b.cfg.Plugins)
	_ = pluginMgr.RegisterTools(b.registry)

	autoMem := memory.NewAutoMemory()
	_ = b.registry.Register(tool.NewSaveMemoryTool(autoMem, nil))

	// Sub-agent manager.
	b.subAgentMgr = subagent.NewManager(b.cfg.SubAgents)
	agentFactory := func(prov provider.Provider, t interface{}, systemPrompt string, maxTurns int) subagent.AgentRunner {
		return agent.NewAgent(prov, t.(*tool.Registry), systemPrompt, maxTurns)
	}
	b.registry.Register(tool.SpawnAgentTool{
		Manager:      b.subAgentMgr,
		Provider:     b.prov,
		Tools:        b.registry,
		AgentFactory: agentFactory,
		WorkingDir:   b.workingDir,
		OnUsage:      b.recordSessionUsage,
	})
	b.registry.Register(tool.WaitAgentTool{Manager: b.subAgentMgr})
	b.registry.Register(tool.ListAgentsTool{Manager: b.subAgentMgr})

	// Forward sub-agent events to UI.
	b.subAgentMgr.SetOnUpdate(func(sa *subagent.SubAgent) {
		b.ui.UpdateAgentPanel(sa.ID, agentPanelFromSubAgent(sa))

		// Push to mobile client
		if broker := b.currentTunnelBroker(); broker != nil {
			switch sa.Status {
			case subagent.StatusRunning:
				if b.markTunnelSubagentSpawned(sa.ID) {
					broker.PushSubagentSpawn(sa.ID, sa.Name, sa.Task, "", "")
				}
				broker.PushSubagentStatus(sa.ID, tunnel.StatusRunning, sa.CurrentTool)

			case subagent.StatusCompleted:
				broker.PushReasoningDone(tunnelSubagentReasoningID(sa.ID))
				if sa.Result != "" {
					msgID := tunnelSubagentTextID(sa.ID)
					broker.PushSubagentText(sa.ID, msgID, sa.Result, true)
				}
				broker.PushSubagentComplete(sa.ID, sa.Name, sa.Result, true)

			case subagent.StatusFailed:
				broker.PushReasoningDone(tunnelSubagentReasoningID(sa.ID))
				errMsg := ""
				if sa.Error != nil {
					errMsg = sa.Error.Error()
				}
				broker.PushSubagentComplete(sa.ID, sa.Name, errMsg, false)

			case subagent.StatusCancelled:
				broker.PushReasoningDone(tunnelSubagentReasoningID(sa.ID))
				broker.PushSubagentComplete(sa.ID, sa.Name, "cancelled", false)
			}
		}
	})

	// Forward sub-agent text chunks to mobile (unthrottled).
	b.subAgentMgr.SetOnStreamText(func(agentID, text string) {
		if broker := b.currentTunnelBroker(); broker != nil {
			broker.PushReasoningDone(tunnelSubagentReasoningID(agentID))
			msgID := tunnelSubagentTextID(agentID)
			broker.PushSubagentText(agentID, msgID, text, false)
		}
	})
	b.subAgentMgr.SetOnReasoning(func(agentID, text string) {
		if broker := b.currentTunnelBroker(); broker != nil {
			if chunk := tunnel.NormalizeReasoningChunk(text); chunk != "" {
				broker.PushSubagentReasoning(agentID, tunnelSubagentReasoningID(agentID), chunk, false)
			}
		}
	})

	// Forward sub-agent tool calls/results to mobile.
	b.subAgentMgr.SetOnToolCall(func(agentID, toolID, toolName, args, detail string) {
		if broker := b.currentTunnelBroker(); broker != nil {
			broker.PushReasoningDone(tunnelSubagentReasoningID(agentID))
			summary := detail
			if summary == "" {
				summary = toolArgSummary(toolName, args)
			}
			broker.PushSubagentToolCall(agentID, toolID, toolName, toolDisplayName(toolName, args), args, summary)
		}
	})
	b.subAgentMgr.SetOnToolResult(func(agentID, toolID, toolName, result string, isError bool) {
		if broker := b.currentTunnelBroker(); broker != nil {
			broker.PushReasoningDone(tunnelSubagentReasoningID(agentID))
			broker.PushSubagentToolResult(agentID, toolID, toolName, result, isError)
		}
	})

	// Swarm manager.
	swarmFactory := func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) swarm.AgentRunner {
		return agent.NewAgent(prov, tools.(*tool.Registry), systemPrompt, maxTurns)
	}
	toolBuilder := func(allowedTools []string) interface{} {
		reg := tool.NewRegistry()
		_ = tool.RegisterBuiltinTools(reg, nil, b.workingDir)
		return reg
	}
	b.swarmMgr = swarm.NewManager(b.cfg.Swarm, b.prov, swarmFactory, toolBuilder)
	b.swarmMgr.SetUsageHandler(b.recordSessionUsage)

	b.registry.Register(tool.TeamCreateTool{Manager: b.swarmMgr})
	b.registry.Register(tool.TeamDeleteTool{Manager: b.swarmMgr})
	b.registry.Register(tool.TeammateSpawnTool{Manager: b.swarmMgr})
	b.registry.Register(tool.TeammateListTool{Manager: b.swarmMgr})
	b.registry.Register(tool.TeammateShutdownTool{Manager: b.swarmMgr})
	b.registry.Register(tool.TeammateResultsTool{Manager: b.swarmMgr})
	b.registry.Register(tool.SwarmTaskCreateTool{Manager: b.swarmMgr})
	b.registry.Register(tool.SwarmTaskListTool{Manager: b.swarmMgr})
	b.registry.Register(tool.SwarmTaskClaimTool{Manager: b.swarmMgr})
	b.registry.Register(tool.SwarmTaskCompleteTool{Manager: b.swarmMgr})

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
			switch ev.Type {
			case "teammate_tool_call":
				detail := toolArgSummary(ev.CurrentTool, ev.ToolArgs)
				broker.PushSubagentToolCall(ev.TeammateID, ev.ToolID, ev.CurrentTool, toolDisplayName(ev.CurrentTool, ev.ToolArgs), ev.ToolArgs, detail)
				broker.PushSubagentStatus(ev.TeammateID, tunnel.StatusRunning, ev.CurrentTool)

			case "teammate_tool_result":
				broker.PushSubagentToolResult(ev.TeammateID, ev.ToolID, ev.CurrentTool, ev.ToolArgs, ev.IsError)

			case "teammate_text":
				// Already handled above in throttle block if skipped
				msgID := fmt.Sprintf("tm-%s", ev.TeammateID)
				broker.PushSubagentText(ev.TeammateID, msgID, ev.Result, false)

			case "teammate_spawned":
				snap, ok := b.swarmMgr.TeammateSnapshot(ev.TeammateID)
				color := ""
				if ok {
					color = snap.Color
				}
				broker.PushSubagentSpawn(ev.TeammateID, ev.TeammateName, "teammate", color, ev.TeamID)

			case "teammate_working":
				broker.PushSubagentStatus(ev.TeammateID, tunnel.StatusRunning, ev.TeammateName)
				snap, ok := b.swarmMgr.TeammateSnapshot(ev.TeammateID)
				if ok && len(snap.Events) > 0 {
					last := snap.Events[len(snap.Events)-1]
					if last.Type == swarm.TeammateEventText && last.Text != "" {
						msgID := fmt.Sprintf("tm-%s", ev.TeammateID)
						broker.PushSubagentText(ev.TeammateID, msgID, last.Text, false)
					}
				}

			case "teammate_idle":
				if ev.Result != "" {
					msgID := fmt.Sprintf("tm-%s", ev.TeammateID)
					broker.PushSubagentText(ev.TeammateID, msgID, ev.Result, true)
				}
				success := ev.Error == nil
				summary := ev.Result
				if ev.Error != nil {
					summary = ev.Error.Error()
				}
				broker.PushSubagentComplete(ev.TeammateID, ev.TeammateName, summary, success)

			case "teammate_shutdown":
				broker.PushSubagentComplete(ev.TeammateID, ev.TeammateName, "shutdown", true)
			}
		}
	})

	systemPrompt := buildSystemPrompt(b.workingDir)
	maxIter := b.cfg.MaxIterations
	if maxIter == 0 {
		maxIter = 200
	}
	b.agent = agent.NewAgent(b.prov, b.registry, systemPrompt, maxIter)

	// Permission policy — default to "auto" for desktop.
	modeStr := b.cfg.DefaultMode
	if modeStr == "" {
		modeStr = "auto"
	}
	mode := permission.ParsePermissionMode(modeStr)
	policy := permission.NewConfigPolicyWithMode(nil, []string{b.workingDir}, mode)
	b.agent.SetPermissionPolicy(policy)
	b.permissionMode = mode
	b.agent.SetUsageHandler(b.recordSessionUsage)

	// Metric collector — async, non-blocking for agent.
	b.metricCollector = metrics.NewCollector(256, func(ev metrics.MetricEvent) {
		b.recordMetric(ev)
	})
	b.agent.SetMetricHandler(b.metricCollector.Emit)

	// Approval handler — popup dialog for tool approval
	b.agent.SetApprovalHandler(func(ctx context.Context, toolName string, input string) permission.Decision {
		if b.mainWindow == nil {
			return permission.Deny
		}
		resp := make(chan permission.Decision, 1)
		requestID := ""
		b.setPendingApproval(requestID, toolName, resp)
		// Push to mobile tunnel client
		if broker := b.currentTunnelBroker(); broker != nil {
			requestID = b.nextTunnelRequestID()
			b.setPendingApproval(requestID, toolName, resp)
			broker.PushApprovalRequest(requestID, toolName, input)
			broker.PushStatus(tunnel.StatusBusy, "")
			broker.PushActivity(b.CurrentTunnelActivity())
		}
		fyne.Do(func() {
			var d dialog.Dialog
			denyBtn := widget.NewButton(t("approval.deny"), func() {
				b.clearPendingApproval(requestID)
				b.pushTunnelApprovalResult(requestID, tunnel.DecisionDeny)
				resp <- permission.Deny
				d.Hide()
			})
			allowBtn := widget.NewButton(t("approval.allow"), func() {
				b.clearPendingApproval(requestID)
				b.pushTunnelApprovalResult(requestID, tunnel.DecisionAllow)
				resp <- permission.Allow
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
				resp <- permission.Allow
				d.Hide()
			})
			alwaysBtn.Importance = widget.SuccessImportance

			// Format tool arguments as readable key-value pairs
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
		select {
		case d := <-resp:
			b.clearPendingApproval(requestID)
			return d
		case <-ctx.Done():
			b.hideDialog(b.clearPendingApproval(requestID))
			return permission.Deny
		}
	})

	// Ask user handler — popup dialog for questions
	if tl, ok := b.registry.Get("ask_user"); ok {
		if askTool, ok := tl.(*tool.AskUserTool); ok {
			askTool.SetHandler(func(ctx context.Context, req tool.AskUserRequest) (tool.AskUserResponse, error) {
				return b.handleAskUser(ctx, req)
			})
		}
	}

	if b.resolved.ContextWindow > 0 {
		b.agent.ContextManager().SetContextWindow(b.resolved.ContextWindow)
	}
	if b.resolved.MaxTokens > 0 {
		b.agent.ContextManager().SetOutputReserve(b.resolved.MaxTokens)
	}
	b.ensureSession()
	return nil
}

func (b *AgentBridge) Send(userMsg string) error {
	log.Printf("[agent-bridge] Send called: %q", userMsg)
	return b.sendContent([]provider.ContentBlock{provider.TextBlock(userMsg)}, true)
}

func (b *AgentBridge) SendContent(content []provider.ContentBlock) error {
	return b.sendContent(content, true)
}

func (b *AgentBridge) sendContent(content []provider.ContentBlock, persistUser bool) error {
	if err := b.setupAgent(); err != nil {
		return err
	}
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
			cancel()
			b.ui.FinalizeStreaming()
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
			case provider.StreamEventText:
				b.ui.AppendAssistantText(ev.Text)
				b.imRound.Text.WriteString(ev.Text)
				if broker := b.currentTunnelBroker(); broker != nil {
					broker.PushReasoningDone(b.tunnelReasoningMsgID(broker))
					broker.PushText(b.ensureTunnelMsgID(broker), ev.Text)
				}

			case provider.StreamEventToolCallDone:
				b.ui.FinalizeStreaming()

				name := ev.Tool.Name
				if name == "" {
					name = "tool"
				}
				description := toolDescription(name, string(ev.Tool.Arguments))
				args := toolArgSummary(name, string(ev.Tool.Arguments))

				b.ui.AppendChat(ChatMessage{
					Role:     "tool",
					ToolName: name,
					ToolID:   ev.Tool.ID,
					ToolDesc: description,
					ToolArgs: args,
					ToolRaw:  string(ev.Tool.Arguments),
					Content:  "",
					Time:     time.Now(),
				})

				// Track tool calls in round state.
				b.imRound.ToolCalls++

				// Do NOT emit intermediate tool_call event to IM — only
				// final results via OutboundEventToolResult. This mirrors
				// the daemon bridge behavior (two events merged into one).
				if b.Emitter != nil {
					b.Emitter.TriggerTyping()
				}
				if broker := b.currentTunnelBroker(); broker != nil {
					b.flushTunnelTextStream(broker)
					broker.PushToolCall(ev.Tool.ID, name, toolDisplayName(name, string(ev.Tool.Arguments)), string(ev.Tool.Arguments), args)
				}

			case provider.StreamEventToolResult:
				content := ev.Result
				if len([]rune(content)) > 2000 {
					content = truncateRunes(content, 2000, "\n...(truncated)")
				}
				b.ui.UpdateToolResult(ev.Tool.ID, content, ev.IsError)
				if ev.IsError {
					b.imRound.ToolFailures++
				} else {
					b.imRound.ToolSuccesses++
				}

				// Emit tool result event to IM (includes tool call info
				// so the start+result is delivered as a single message).
				if b.Emitter != nil {
					b.Emitter.EmitEvent(im.OutboundEvent{
						Kind: im.OutboundEventToolResult,
						ToolRes: &im.ToolResultInfo{
							ToolName: ev.Tool.Name,
							Args:     string(ev.Tool.Arguments),
							Result:   content,
							IsError:  ev.IsError,
						},
					})
					b.Emitter.TriggerTyping()
				}
				if broker := b.currentTunnelBroker(); broker != nil {
					broker.PushReasoningDone(b.tunnelReasoningMsgID(broker))
					broker.PushToolResult(ev.Tool.ID, ev.Tool.Name, content, ev.IsError)
				}

				// After spawn_agent completes, sync agent panels.
				if ev.Tool.Name == "spawn_agent" && b.subAgentMgr != nil {
					b.syncAgentPanels()
				}

			case provider.StreamEventSystem:
				b.ui.FinalizeStreaming()
				b.ui.AppendChat(ChatMessage{
					Role:    "system",
					Content: ev.Text,
					Time:    time.Now(),
				})
				// Mirror TUI: just close the current text stream and rotate.
				if broker := b.currentTunnelBroker(); broker != nil {
					b.flushTunnelTextStream(broker)
				}

			case provider.StreamEventReasoning:
				if chunk := tunnel.NormalizeReasoningChunk(ev.Text); chunk != "" {
					b.ui.AppendReasoning(chunk)
					if broker := b.currentTunnelBroker(); broker != nil {
						broker.PushReasoning(b.tunnelReasoningMsgID(broker), chunk)
					}
				}

			case provider.StreamEventDone:
				// Each LLM turn ends with Done. Emit round summary to IM.
				if b.Emitter != nil {
					text := strings.TrimSpace(b.imRound.Text.String())
					if text != "" || b.imRound.ToolCalls > 0 {
						b.Emitter.EmitRoundSummary(text, b.imRound.ToolCalls, b.imRound.ToolSuccesses, b.imRound.ToolFailures)
					}
					b.imRound.Text.Reset()
					b.imRound.ToolCalls = 0
					b.imRound.ToolSuccesses = 0
					b.imRound.ToolFailures = 0
				}
				// Mirror TUI: only close text stream + rotate msgID.
				// Do NOT push idle/clear-activity here — that happens only
				// when the entire agent run finishes (via RunStreamWithContent
				// returning).
				if broker := b.currentTunnelBroker(); broker != nil {
					b.flushTunnelTextStream(broker)
				}

			case provider.StreamEventError:
				// Mirror TUI: close text stream, push error, rotate.
				if broker := b.currentTunnelBroker(); broker != nil {
					b.flushTunnelTextStream(broker)
					if ev.Error != nil {
						broker.PushError(ev.Error.Error())
					}
				}
			}
		}

		err := b.agent.RunStreamWithContent(ctx, content, onEvent)
		if err != nil {
			b.mu.Lock()
			c := b.cancelled
			b.mu.Unlock()
			if !c {
				b.ui.AppendChat(ChatMessage{
					Role:    "error",
					Content: err.Error(),
					Time:    time.Now(),
				})
				if broker := b.currentTunnelBroker(); broker != nil {
					b.flushTunnelTextStream(broker)
					broker.PushError(err.Error())
				}
			}
		}
		// Mirror TUI handleDoneMsg: always push idle + clear activity
		// when the entire agent run finishes (success or error).
		if broker := b.currentTunnelBroker(); broker != nil {
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
		b.flushTunnelTextStream(broker)
		broker.PushStatus(tunnel.StatusIdle, "cancelled")
		broker.PushActivity("")
	}
}

func (b *AgentBridge) Close() {
	b.Cancel()
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
	b.pendingMu.Lock()
	b.pendingMsgs = append(b.pendingMsgs, pendingMessage{Text: msg})
	b.pendingMu.Unlock()
}

func (b *AgentBridge) QueueHiddenMessage(msg string) {
	b.pendingMu.Lock()
	b.pendingMsgs = append(b.pendingMsgs, pendingMessage{Text: msg, Hidden: true})
	b.pendingMu.Unlock()
}

// drainPending returns and clears the next queued message.
func (b *AgentBridge) drainPending() (pendingMessage, bool) {
	b.pendingMu.Lock()
	defer b.pendingMu.Unlock()
	if len(b.pendingMsgs) == 0 {
		return pendingMessage{}, false
	}
	msg := b.pendingMsgs[0]
	b.pendingMsgs = b.pendingMsgs[1:]
	return msg, true
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

func (b *AgentBridge) recordSessionUsage(usage provider.TokenUsage) {
	b.mu.Lock()
	if b.currentSes == nil || b.sessionStore == nil {
		b.mu.Unlock()
		return
	}
	b.currentSes.TokenUsage.Add(usage)
	b.currentSes.UpdatedAt = time.Now()
	ses := b.currentSes
	store := b.sessionStore
	total := b.currentSes.TokenUsage
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
		b.ui.SetSessionUsage(total)
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
		ses.Metrics = append(ses.Metrics, ev)
		ses.UpdatedAt = time.Now()
	}
	b.mu.Unlock()
	if ses == nil || store == nil {
		return
	}
	if b.ui != nil {
		b.ui.SetSessionMetrics(ses.Metrics)
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
	if toolName == "swarm_task_create" {
		if subject := tool.SwarmTaskCreateSubject(rawArgs); subject != "" {
			return subject
		}
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rawArgs), &args); err == nil {
		if v, ok := args["description"]; ok {
			var desc string
			if json.Unmarshal(v, &desc) == nil && desc != "" {
				return desc
			}
		}
	}
	return prettifyToolName(toolName)
}

func toolDescription(toolName, rawArgs string) string {
	var args map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return ""
	}

	// Helper to extract a string field.
	str := func(field string) string {
		if v, ok := args[field]; ok {
			var s string
			if json.Unmarshal(v, &s) == nil {
				return s
			}
		}
		return ""
	}

	if toolName == "swarm_task_create" {
		return tool.SwarmTaskCreateSubject(rawArgs)
	}

	// First check for explicit description field from LLM.
	// If present, use it as displayName with PrettyName in parentheses.
	if desc := str("description"); desc != "" {
		return desc + " (" + prettifyToolName(toolName) + ")"
	}

	switch toolName {
	// File operations
	case "read_file":
		if p := str("path"); p != "" {
			return shortPath(p)
		}
	case "write_file":
		if p := str("path"); p != "" {
			return shortPath(p)
		}
	case "edit_file", "multi_edit_file":
		if p := str("file_path"); p != "" {
			return shortPath(p)
		}
	case "notebook_edit":
		if p := str("notebook_path"); p != "" {
			return "Notebook: " + shortPath(p)
		}

	// Search / listing
	case "search_files", "grep":
		pat := str("pattern")
		dir := str("directory")
		if pat != "" {
			d := "Grep: " + truncateRunes(pat, 60, "...")
			if dir != "" {
				d += " in " + shortPath(dir)
			}
			return d
		}
	case "glob":
		pat := str("pattern")
		dir := str("directory")
		if pat != "" {
			d := "Glob: " + truncateRunes(pat, 60, "...")
			if dir != "" {
				d += " in " + shortPath(dir)
			}
			return d
		}
	case "list_directory":
		if p := str("path"); p != "" {
			return shortPath(p)
		}

	// Web
	case "web_search":
		if q := str("query"); q != "" {
			return truncateRunes(q, 80, "...")
		}
	case "web_fetch":
		if u := str("url"); u != "" {
			return truncateRunes(u, 80, "...")
		}

	// Commands
	case "run_command", "start_command":
		if c := str("command"); c != "" {
			if comment := firstCommentLine(c); comment != "" {
				return comment
			}
			return truncateRunes(strings.SplitN(c, "\n", 2)[0], 60, "...")
		}
	case "stop_command":
		if id := str("job_id"); id != "" {
			return "Stop Job: " + id
		}
	case "read_command_output":
		if id := str("job_id"); id != "" {
			return "Read Output: " + id
		}
	case "wait_command":
		if id := str("job_id"); id != "" {
			return "Wait: " + id
		}
	case "write_command_input":
		if id := str("job_id"); id != "" {
			return "Write Input: " + id
		}
	case "list_commands":
		return "Background Jobs"

	// Git
	case "git_status":
		return ""
	case "git_diff":
		if f := str("file"); f != "" {
			return shortPath(f)
		}
		return ""
	case "git_log":
		return ""
	case "git_show":
		if r := str("revision"); r != "" {
			return truncateRunes(r, 40, "...")
		}
		return ""
	case "git_blame":
		if f := str("file"); f != "" {
			return shortPath(f)
		}
		return ""
	case "git_add":
		return ""
	case "git_commit":
		return ""
	case "git_branch_list":
		return ""
	case "git_remote":
		return ""
	case "git_stash":
		return ""
	case "git_stash_list":
		return "Git Stash List"

	// Agent tools
	case "spawn_agent":
		task := truncateRunes(str("task"), 80, "...")
		stype := str("subagent_type")
		d := "(Spawn Sub-Agent)"
		if stype != "" {
			d += " " + stype
		}
		if task != "" {
			d += " — " + task
		}
		return d
	case "wait_agent":
		if id := str("agent_id"); id != "" {
			return "(Wait Agent) " + id
		}
		return "(Wait Agent)"
	case "list_agents":
		return "(List Agents)"

	// Messaging
	case "send_message":
		to := str("to")
		summary := str("summary")
		if summary != "" {
			return summary
		}
		if to != "" {
			return "Send to: " + to
		}
		return "Send Message"

	// Swarm / Team
	case "swarm_task_create":
		subj := str("subject")
		assignee := str("assignee")
		d := "Create Task"
		if subj != "" {
			d = truncateRunes(subj, 80, "...")
		}
		if assignee != "" {
			d += " → " + assignee
		}
		return d
	case "swarm_task_claim":
		tid := str("task_id")
		if tid != "" {
			return "Claim Task: " + tid
		}
		return "Claim Task"
	case "swarm_task_complete":
		tid := str("task_id")
		if tid != "" {
			return "Complete Task: " + tid
		}
		return "Complete Task"
	case "swarm_task_list":
		tid := str("team_id")
		if tid != "" {
			return "List Tasks: " + tid
		}
		return "List Tasks"
	case "team_create":
		if n := str("name"); n != "" {
			return "Create Team: " + n
		}
		return "Create Team"
	case "team_delete":
		if tid := str("team_id"); tid != "" {
			return "Delete Team: " + tid
		}
		return "Delete Team"

	// Teammate
	case "teammate_spawn":
		if n := str("name"); n != "" {
			return "(Spawn Teammate) " + n
		}
		return "(Spawn Teammate)"
	case "teammate_shutdown":
		if tid := str("teammate_id"); tid != "" {
			return "(Shutdown Teammate) " + tid
		}
		return "(Shutdown Teammate)"
	case "teammate_list":
		return "(List Teammates)"
	case "teammate_results":
		if tid := str("teammate_id"); tid != "" {
			return "(Get Results) " + tid
		}
		return "(Get Results)"

	// Other tools
	case "save_memory":
		if k := str("key"); k != "" {
			return "Save Memory: " + k
		}
		return "Save Memory"
	case "config":
		if s := str("setting"); s != "" {
			return "Config: " + s
		}
		return "Config"
	case "skill":
		if s := str("skill"); s != "" {
			return "Skill: " + s
		}
		return "Skill"
	case "ask_user":
		return "Ask User"
	case "todo_write":
		return "Update Todos"
	case "enter_plan_mode":
		return "Enter Plan Mode"
	case "exit_plan_mode":
		return "Exit Plan Mode"
	case "enter_worktree":
		return "Enter Worktree"
	case "exit_worktree":
		return "Exit Worktree"
	case "task_create":
		if s := str("subject"); s != "" {
			return s
		}
		return "Create Task"
	case "task_get", "task_update", "task_stop", "task_list":
		if s := str("subject"); s != "" {
			return s
		}
		return prettifyToolName(toolName)
	case "cron_create":
		return "Schedule Job"
	case "cron_delete":
		return "Delete Job"
	case "cron_list":
		return "Scheduled Jobs"
	case "list_mcp_capabilities":
		return "MCP Capabilities"
	case "get_mcp_prompt":
		if n := str("name"); n != "" {
			return "MCP Prompt: " + n
		}
		return "MCP Prompt"
	case "read_mcp_resource":
		if u := str("uri"); u != "" {
			return "MCP Resource: " + truncateRunes(u, 60, "...")
		}
		return "MCP Resource"
	}

	// LSP tools
	if strings.HasPrefix(toolName, "lsp_") {
		op := strings.ReplaceAll(toolName[4:], "_", " ")
		return "LSP: " + strings.Title(op)
	}

	// MCP tools (mcp__server__tool)
	if strings.HasPrefix(toolName, "mcp__") {
		parts := strings.Split(toolName, "__")
		if len(parts) >= 3 {
			return "MCP: " + strings.ReplaceAll(parts[len(parts)-1], "_", " ")
		}
	}

	return prettifyToolName(toolName)
}

func shortPath(p string) string {
	if len(p) > 60 {
		// Try to shorten from the left: keep last 57 chars with "…"
		runes := []rune(p)
		if len(runes) > 57 {
			return "…" + string(runes[len(runes)-57:])
		}
	}
	return p
}

func toolArgSummary(toolName, rawArgs string) string {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return ""
	}
	switch toolName {
	case "read_file", "write_file", "edit_file":
		if p, ok := args["path"].(string); ok {
			return p
		}
	case "run_command", "start_command":
		if c, ok := args["command"].(string); ok {
			return c
		}
	case "search_files", "grep":
		if p, ok := args["pattern"].(string); ok {
			return p
		}
	case "glob":
		if p, ok := args["pattern"].(string); ok {
			return p
		}
	case "list_directory":
		if p, ok := args["path"].(string); ok {
			return p
		}
	case "swarm_task_create":
		if assignee, ok := args["assignee"].(string); ok && strings.TrimSpace(assignee) != "" {
			return strings.TrimSpace(assignee)
		}
		return ""
	}
	for _, v := range args {
		if s, ok := v.(string); ok && len(s) > 0 {
			if len([]rune(s)) > 60 {
				return truncateRunes(s, 60, "...")
			}
			return s
		}
	}
	return ""
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
			Type:     agentEventTypeStr(ev.Type),
			ToolName: ev.ToolName,
			ToolID:   ev.ToolID,
			ToolArgs: ev.ToolArgs,
		}
		switch ev.Type {
		case subagent.AgentEventToolResult:
			entry.Content = ev.Result
			entry.IsError = ev.IsError
		case subagent.AgentEventToolCall:
			// ToolCall has no Text field; use toolArgSummary as description.
			entry.Content = toolArgSummary(ev.ToolName, ev.ToolArgs)
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

func buildSystemPrompt(workingDir string) string {
	hostname, _ := os.Hostname()
	cwd := workingDir
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	return fmt.Sprintf(`You are ggcode, an AI coding assistant running as a desktop application.

## Environment
- OS: %s
- Working directory: %s

## Instructions
- Be precise, concise, and proactive.
- Prefer small, reversible changes over broad rewrites.
- Read before you edit, and inspect results before claiming success.
`, hostname, cwd)
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
	msgs := agent.Messages()
	b.currentSes.Messages = msgs

	// If no user messages, delete the empty session.
	if len(b.currentSes.Messages) == 0 {
		_ = b.sessionStore.Delete(b.currentSes.ID)
		return
	}

	// Auto-generate title from first user message if still default.
	if b.currentSes.Title == "" || b.currentSes.Title == "New session" {
		for _, m := range b.currentSes.Messages {
			if m.Role == "user" {
				for _, block := range m.Content {
					if block.Type == "text" && block.Text != "" {
						text := block.Text
						if len([]rune(text)) > 60 {
							text = string([]rune(text)[:57]) + "..."
						}
						b.currentSes.Title = text
						break
					}
				}
				break
			}
		}
	}

	_ = b.sessionStore.Save(b.currentSes)
}

// ensureSession creates a new session if one doesn't exist yet.
func (b *AgentBridge) ensureSession() {
	if b.currentSes != nil || b.sessionStore == nil {
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
	ses := session.NewSession(vendor, endpoint, model)
	_ = b.sessionStore.Save(ses)
	b.currentSes = ses
	b.usageTurnIndex = 0
	if b.ui != nil {
		b.ui.SetSessionUsage(ses.TokenUsage)
		b.ui.SetSessionMetrics(ses.Metrics)
	}
	if broker := b.currentTunnelBroker(); broker != nil {
		broker.AnnounceActiveSession(ses.ID)
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

func (b *AgentBridge) CurrentSessionTunnelEvents() []tunnel.GatewayMessage {
	b.mu.Lock()
	currentSes := b.currentSes
	b.mu.Unlock()
	if currentSes == nil || !currentSes.TunnelEventsComplete || len(currentSes.TunnelEvents) == 0 {
		return nil
	}
	out := make([]tunnel.GatewayMessage, 0, len(currentSes.TunnelEvents))
	for _, ev := range currentSes.TunnelEvents {
		out = append(out, tunnel.GatewayMessage{
			SessionID: currentSes.ID,
			EventID:   ev.EventID,
			StreamID:  ev.StreamID,
			Type:      ev.Type,
			Data:      ev.Data,
		})
	}
	return out
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
	b.mu.Unlock()

	_ = store.Save(ses)
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
	b.mu.Unlock()

	_ = store.Save(ses)
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
}

// ResumeSession loads a session by ID and restores its messages into the agent.
func (b *AgentBridge) ResumeSession(id string) error {
	if b.sessionStore == nil {
		return fmt.Errorf("no session store")
	}
	if err := b.setupAgent(); err != nil {
		return err
	}

	ses, err := b.sessionStore.Load(id)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}

	// Feed all messages into the agent context.
	for _, msg := range ses.Messages {
		b.agent.AddMessage(msg)
	}

	b.mu.Lock()
	b.currentSes = ses
	b.usageTurnIndex = session.LastTurnIndex(ses)
	b.mu.Unlock()
	if b.ui != nil {
		b.ui.SetSessionUsage(ses.TokenUsage)
		b.ui.SetSessionMetrics(ses.Metrics)
	}
	if broker := b.currentTunnelBroker(); broker != nil {
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

// imRoundState tracks per-LLM-turn state for IM emission.
type imRoundState struct {
	Text          strings.Builder
	ToolCalls     int
	ToolSuccesses int
	ToolFailures  int
}

// handleAskUser shows a dialog for ask_user tool questions.
func (b *AgentBridge) handleAskUser(ctx context.Context, req tool.AskUserRequest) (tool.AskUserResponse, error) {
	if b.mainWindow == nil || len(req.Questions) == 0 {
		return tool.AskUserResponse{Status: "skipped"}, nil
	}

	resp := make(chan tool.AskUserResponse, 1)
	requestID := ""
	b.setPendingAskUser(requestID, req, resp)
	// Push to mobile tunnel client
	if broker := b.currentTunnelBroker(); broker != nil {
		requestID = b.nextTunnelRequestID()
		b.setPendingAskUser(requestID, req, resp)
		broker.PushAskUserRequest(requestID, req.Title, buildTunnelAskUserQuestions(req))
		broker.PushStatus(tunnel.StatusBusy, "")
		broker.PushActivity(b.CurrentTunnelActivity())
	}
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
					resp <- response
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
					finalAnswers[i] = buildAskUserAnswer(question, answers[i].SelectedChoiceIDs, answers[i].FreeformText)
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
				resp <- response
			}, b.mainWindow)
		d.Resize(fyne.NewSize(500, 400))
		if b.attachAskUserDialog(requestID, d) {
			d.Show()
		}
	})

	select {
	case r := <-resp:
		b.clearPendingAskUser(requestID)
		return r, nil
	case <-ctx.Done():
		b.hideDialog(b.clearPendingAskUser(requestID))
		return tool.AskUserResponse{Status: "cancelled"}, ctx.Err()
	}
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

	// Update config with the new model selection.
	if err := b.cfg.SetActiveSelection(b.cfg.Vendor, b.cfg.Endpoint, model); err != nil {
		return fmt.Errorf("set active selection: %w", err)
	}
	_ = b.cfg.Save()

	// Re-resolve endpoint (picks up new context window, etc.).
	resolved, err := b.cfg.ResolveActiveEndpoint()
	if err != nil {
		return fmt.Errorf("resolve endpoint: %w", err)
	}

	// Create a new provider for the updated model.
	prov, err := provider.NewProvider(resolved)
	if err != nil {
		return fmt.Errorf("create provider: %w", err)
	}

	// Swap provider on the live agent (thread-safe).
	if b.agent != nil {
		b.agent.SetProvider(prov)
		if resolved.ContextWindow > 0 {
			b.agent.ContextManager().SetContextWindow(resolved.ContextWindow)
		}
		if resolved.MaxTokens > 0 {
			b.agent.ContextManager().SetOutputReserve(resolved.MaxTokens)
		}
	}

	// Update bridge state so status bar reflects the new model.
	b.mu.Lock()
	b.prov = prov
	b.resolved = resolved
	b.mu.Unlock()

	return nil
}

func (b *AgentBridge) nextTunnelRequestID() string {
	broker := b.currentTunnelBroker()
	if broker == nil {
		return ""
	}
	return broker.NextMessageID()
}

func (b *AgentBridge) setPendingApproval(id, toolName string, ch chan permission.Decision) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.approvalRequestID = id
	b.approvalToolName = toolName
	b.approvalRespCh = ch
	b.approvalDialog = nil
}

func (b *AgentBridge) attachApprovalDialog(id string, dlg dialog.Dialog) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.approvalRespCh == nil {
		return false
	}
	if strings.TrimSpace(id) != "" && b.approvalRequestID != id {
		return false
	}
	b.approvalDialog = dlg
	return true
}

func (b *AgentBridge) consumePendingApproval(id string) (string, chan permission.Decision, dialog.Dialog, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.approvalRespCh == nil {
		return "", nil, nil, false
	}
	if strings.TrimSpace(id) != "" && b.approvalRequestID != "" && b.approvalRequestID != id {
		return "", nil, nil, false
	}
	toolName := b.approvalToolName
	ch := b.approvalRespCh
	dlg := b.approvalDialog
	b.approvalRespCh = nil
	b.approvalRequestID = ""
	b.approvalToolName = ""
	b.approvalDialog = nil
	return toolName, ch, dlg, true
}

func (b *AgentBridge) clearPendingApproval(id string) dialog.Dialog {
	b.mu.Lock()
	defer b.mu.Unlock()
	if strings.TrimSpace(id) != "" && b.approvalRequestID != "" && b.approvalRequestID != id {
		return nil
	}
	dlg := b.approvalDialog
	b.approvalRespCh = nil
	b.approvalRequestID = ""
	b.approvalToolName = ""
	b.approvalDialog = nil
	return dlg
}

func (b *AgentBridge) setPendingAskUser(id string, req tool.AskUserRequest, ch chan tool.AskUserResponse) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.askUserRequestID = id
	b.askUserRequest = req
	b.askUserRespCh = ch
	b.askUserDialog = nil
}

func (b *AgentBridge) attachAskUserDialog(id string, dlg dialog.Dialog) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.askUserRespCh == nil {
		return false
	}
	if strings.TrimSpace(id) != "" && b.askUserRequestID != id {
		return false
	}
	b.askUserDialog = dlg
	return true
}

func (b *AgentBridge) consumePendingAskUser(id string) (tool.AskUserRequest, chan tool.AskUserResponse, dialog.Dialog, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.askUserRespCh == nil {
		return tool.AskUserRequest{}, nil, nil, false
	}
	if strings.TrimSpace(id) != "" && b.askUserRequestID != "" && b.askUserRequestID != id {
		return tool.AskUserRequest{}, nil, nil, false
	}
	req := b.askUserRequest
	ch := b.askUserRespCh
	dlg := b.askUserDialog
	b.askUserRequestID = ""
	b.askUserRequest = tool.AskUserRequest{}
	b.askUserRespCh = nil
	b.askUserDialog = nil
	return req, ch, dlg, true
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
	b.askUserRespCh = nil
	b.askUserDialog = nil
	return dlg
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
	broker := b.currentTunnelBroker()
	if broker == nil || strings.TrimSpace(id) == "" {
		return
	}
	broker.PushApprovalResult(id, decision)
	broker.PushStatus(tunnel.StatusBusy, "")
	broker.PushActivity(b.CurrentTunnelActivity())
}

func (b *AgentBridge) pushTunnelAskUserResponse(id string, response tool.AskUserResponse) {
	broker := b.currentTunnelBroker()
	if broker == nil || strings.TrimSpace(id) == "" {
		return
	}
	answers := make([]tunnel.AskUserAnswer, len(response.Answers))
	for i, answer := range response.Answers {
		answers[i] = tunnel.AskUserAnswer{
			QuestionID:   answer.ID,
			ChoiceIDs:    append([]string(nil), answer.SelectedChoiceIDs...),
			FreeformText: answer.FreeformText,
		}
	}
	broker.PushAskUserResponse(id, response.Status, answers)
	broker.PushStatus(tunnel.StatusBusy, "")
	broker.PushActivity(b.CurrentTunnelActivity())
}

func (b *AgentBridge) handleMobileApprovalResponse(data tunnel.ApprovalResponseData) {
	toolName, ch, dlg, ok := b.consumePendingApproval(data.ID)
	if !ok {
		return
	}
	b.hideDialog(dlg)
	if data.Decision == tunnel.DecisionAlwaysAllow && b.agent != nil {
		if p, ok := b.agent.PermissionPolicy().(*permission.ConfigPolicy); ok {
			p.SetOverride(toolName, permission.Allow)
		}
	}
	decision := permission.Deny
	switch data.Decision {
	case tunnel.DecisionAllow:
		decision = permission.Allow
	case tunnel.DecisionAlwaysAllow, "always":
		decision = permission.Allow
	default:
		decision = permission.Deny
	}
	select {
	case ch <- decision:
	default:
	}
	if broker := b.currentTunnelBroker(); broker != nil {
		broker.PushStatus(tunnel.StatusBusy, "")
		broker.PushActivity(b.CurrentTunnelActivity())
	}
}

func (b *AgentBridge) handleMobileAskUserResponse(data tunnel.AskUserResponseData) {
	req, ch, dlg, ok := b.consumePendingAskUser(data.ID)
	if !ok {
		return
	}
	b.hideDialog(dlg)
	response := buildAskUserResponseFromTunnel(req, data.Status, data.Answers)
	select {
	case ch <- response:
	default:
	}
	if broker := b.currentTunnelBroker(); broker != nil {
		broker.PushStatus(tunnel.StatusBusy, "")
		broker.PushActivity(b.CurrentTunnelActivity())
	}
}

func buildTunnelAskUserQuestions(req tool.AskUserRequest) []tunnel.AskUserQuestion {
	questions := make([]tunnel.AskUserQuestion, len(req.Questions))
	for i, q := range req.Questions {
		choices := make([]tunnel.AskUserChoice, len(q.Choices))
		for j, c := range q.Choices {
			choices[j] = tunnel.AskUserChoice{ID: c.ID, Label: c.Label}
		}
		questions[i] = tunnel.AskUserQuestion{
			ID:            q.ID,
			Prompt:        q.Prompt,
			Kind:          q.Kind,
			Choices:       choices,
			AllowFreeform: q.AllowFreeform,
			Placeholder:   q.Placeholder,
		}
	}
	return questions
}

func buildAskUserResponseFromTunnel(req tool.AskUserRequest, status string, answers []tunnel.AskUserAnswer) tool.AskUserResponse {
	normalizedStatus := strings.TrimSpace(status)
	if normalizedStatus == "" {
		normalizedStatus = tool.AskUserStatusSubmitted
	}
	answerByQuestion := make(map[string]tunnel.AskUserAnswer, len(answers))
	for _, answer := range answers {
		answerByQuestion[answer.QuestionID] = answer
	}
	out := tool.AskUserResponse{
		Status:        normalizedStatus,
		Title:         req.Title,
		QuestionCount: len(req.Questions),
		Answers:       make([]tool.AskUserAnswer, 0, len(req.Questions)),
	}
	for _, question := range req.Questions {
		raw := answerByQuestion[question.ID]
		answer := buildAskUserAnswer(question, raw.ChoiceIDs, raw.FreeformText)
		if answer.Answered {
			out.AnsweredCount++
		}
		out.Answers = append(out.Answers, answer)
	}
	return out
}

func buildAskUserAnswer(question tool.AskUserQuestion, selectedIDs []string, freeform string) tool.AskUserAnswer {
	selectedSet := make(map[string]struct{}, len(selectedIDs))
	for _, id := range selectedIDs {
		selectedSet[id] = struct{}{}
	}
	orderedIDs := make([]string, 0, len(selectedSet))
	orderedLabels := make([]string, 0, len(selectedSet))
	for _, choice := range question.Choices {
		if _, ok := selectedSet[choice.ID]; ok {
			orderedIDs = append(orderedIDs, choice.ID)
			orderedLabels = append(orderedLabels, choice.Label)
		}
	}
	freeform = strings.TrimSpace(freeform)
	answerMode := tool.AskUserAnswerModeNone
	completionStatus := tool.AskUserCompletionUnanswered
	switch {
	case len(orderedIDs) == 0 && freeform == "":
		answerMode = tool.AskUserAnswerModeNone
		completionStatus = tool.AskUserCompletionUnanswered
	case len(orderedIDs) == 0 && freeform != "":
		answerMode = tool.AskUserAnswerModeFreeformOnly
		if question.Kind == tool.AskUserKindText {
			completionStatus = tool.AskUserCompletionAnswered
		} else {
			completionStatus = tool.AskUserCompletionPartial
		}
	case len(orderedIDs) > 0 && freeform == "":
		answerMode = tool.AskUserAnswerModeSelectionOnly
		completionStatus = tool.AskUserCompletionAnswered
	default:
		answerMode = tool.AskUserAnswerModeSelectionAndFreeform
		completionStatus = tool.AskUserCompletionAnswered
	}
	return tool.AskUserAnswer{
		ID:                question.ID,
		Title:             question.Title,
		Kind:              question.Kind,
		CompletionStatus:  completionStatus,
		AnswerMode:        answerMode,
		Answered:          completionStatus == tool.AskUserCompletionAnswered,
		SelectedChoiceIDs: orderedIDs,
		SelectedChoices:   orderedLabels,
		FreeformText:      freeform,
	}
}

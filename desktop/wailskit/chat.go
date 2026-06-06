// Package wailskit provides a public facade for the Wails desktop app.
package wailskit

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

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
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/relaycatalog"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/swarm"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tunnel"
	"github.com/topcheer/ggcode/internal/uiusage"
)

// ChatBridge manages the full agent chat loop for the Wails frontend,
// mirroring the Fyne desktop's AgentBridge tool registration and session management.
type ChatBridge struct {
	cfg            *config.Config
	resolved       *config.ResolvedEndpoint
	agent          *agent.Agent
	registry       *tool.Registry
	mcpManager     *plugin.MCPManager
	workingDir     string
	sessionStore   session.Store
	currentSes     *session.Session
	permissionMode permission.PermissionMode

	mu        sync.Mutex
	cancel    context.CancelFunc
	cancelled bool
	startTime time.Time

	// Pending messages (mirrors Fyne pendingMsgs)
	pendingMsgs *agentruntime.PendingQueue[*tunnel.MessageData]

	// Subsystems
	cronScheduler *cron.Scheduler
	subAgentMgr   *subagent.Manager
	acpClientMgr  *acpclient.ClientManager
	swarmMgr      *swarm.Manager

	// Metrics
	metricCancel         context.CancelFunc
	metricCollector      *metrics.Collector
	usageTurnIndex       int
	lastMetricDigestTurn int

	// UI event emitter — set by app.go via SetEmitEvent
	emitEvent func(name string, payload ...interface{})

	// Sub-agent tunnel tracking
	spawnedSet map[string]bool

	// IM outbound push — same as Fyne agentBridge.Emitter
	Emitter *im.IMEmitter

	// IM round accumulator for emitter (mirrors Fyne agentBridge.imRound)
	imRound agentruntime.IMRoundState

	// Mobile tunnel broker for outbound push
	tunnelBroker           *tunnel.Broker
	tunnelProjectionBroker *tunnel.Broker // offline broker for event recording before Share
	tunnelMsgID            string
	tunnelMsgNeedsFinalize bool
	tunnelProjectionBroken bool
	projectionStore        *tunnel.ProjectionStore
	shareTunnelBroker      *tunnel.Broker

	// Pending approval/ask_user requests from agent
	interactions *agentruntime.InteractionBroker

	// Callback for emitting events to frontend.
	OnStreamEvent func(eventType string, data json.RawMessage)

	liveHistory []SessionMessage
}

// NewChatBridge creates a new chat bridge using the global config.
func NewChatBridge() (*ChatBridge, error) {
	cfg := GetGlobalConfig()
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	wd, _ := os.Getwd()
	modeStr := cfg.DefaultMode
	if modeStr == "" {
		modeStr = "auto"
	}
	return &ChatBridge{
		cfg:            cfg,
		workingDir:     wd,
		permissionMode: permission.ParsePermissionMode(modeStr),
		pendingMsgs:    agentruntime.NewPendingQueue[*tunnel.MessageData](),
		interactions:   agentruntime.NewInteractionBroker(),
	}, nil
}

// SendMessage sends a user message and streams events to the frontend.
// If agent is already running, queues the message for processing after the current turn.
func (b *ChatBridge) SendMessage(userMsg string) error {
	return b.sendMessageData(tunnel.MessageData{Text: userMsg}, false)
}

func (b *ChatBridge) HandleTunnelUserMessage(data tunnel.MessageData) error {
	if strings.TrimSpace(data.Text) == "" {
		return nil
	}
	data.MessageID = tunnel.NormalizeClientMessageID(data.MessageID)
	if broker := b.currentTunnelBroker(); broker != nil {
		broker.PushUserMessageData(data)
		broker.PushStatus(tunnel.StatusBusy, "")
		b.resetTunnelRoundState()
	}
	return b.sendMessageData(data, true)
}

func (b *ChatBridge) BindShareCommands(broker *tunnel.Broker, onLanguage func(string), currentAskUserRequest func() tool.AskUserRequest, clearAskUserRequest func()) {
	if broker == nil {
		return
	}
	broker.OnCommand(func(cmd tunnel.GatewayMessage) {
		agentruntime.RouteTunnelCommand(cmd, agentruntime.TunnelCommandHooks{
			OnUserMessage: func(data tunnel.MessageData) {
				_ = b.HandleTunnelUserMessage(data)
			},
			OnApprovalResponse: func(data tunnel.ApprovalResponseData) {
				b.HandleMobileApprovalResponse(data)
			},
			OnAskUserResponse: func(data tunnel.AskUserResponseData) {
				req := tool.AskUserRequest{}
				if currentAskUserRequest != nil {
					req = currentAskUserRequest()
				}
				b.HandleMobileAskUserResponse(data, req)
				if clearAskUserRequest != nil {
					clearAskUserRequest()
				}
			},
			OnInterrupt: func() {
				b.Cancel()
			},
			OnLanguageChange: func(data tunnel.LanguageChangeData) {
				if onLanguage != nil {
					onLanguage(data.Language)
				}
			},
			OnServerAck: func(messageID string) {
				broker.PushServerAck(messageID)
			},
		})
	})
}

func (b *ChatBridge) sendMessageData(data tunnel.MessageData, skipMobilePush bool) error {
	userMsg := strings.TrimSpace(data.Text)
	if userMsg == "" {
		return nil
	}
	b.mu.Lock()
	if b.cancel != nil {
		// Agent is busy — queue the message (mirrors Fyne QueueMessage)
		meta := &data
		if !skipMobilePush {
			meta = nil
		}
		b.pendingMsgs.Enqueue(userMsg, false, meta)
		b.mu.Unlock()
		if b.OnStreamEvent != nil {
			raw, _ := json.Marshal(map[string]string{"message": "Message queued"})
			b.OnStreamEvent("message_queued", raw)
		}
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel
	b.cancelled = false
	b.usageTurnIndex++
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		b.cancel = nil
		b.mu.Unlock()

		// Process queued messages (mirrors Fyne line 906-919)
		if pending, ok := b.drainPending(); ok {
			if pending.Hidden {
				_ = b.SendHiddenText(pending.Text)
			} else {
				if broker := b.currentTunnelBroker(); broker != nil {
					broker.PushSystemMessage("Processing queued message...")
				}
				data := tunnel.MessageData{Text: pending.Text}
				skipPush := false
				if pending.Meta != nil {
					data = *pending.Meta
					skipPush = true
				}
				_ = b.sendMessageData(data, skipPush)
			}
		}
	}()

	if b.agent == nil {
		if err := b.initAgent(ctx); err != nil {
			return fmt.Errorf("init agent: %w", err)
		}
	}

	// Ensure we have a session (mirrors Fyne bridge.ensureSession)
	if err := b.ensureSession(); err != nil {
		return fmt.Errorf("ensure session: %w", err)
	}
	// Rebind the per-session projection broker before every run so subsequent
	// turns cannot inherit a stale broker callback/session binding.
	b.bindTunnelProjectionSession()
	b.appendLiveUserMessage(userMsg)

	// Notify mobile client: user message + busy status
	if broker := b.currentTunnelBroker(); broker != nil && !skipMobilePush {
		if strings.TrimSpace(data.MessageID) == "" {
			data.MessageID = broker.NextMessageID()
		}
		broker.PushUserMessageData(data)
		broker.PushStatus(tunnel.StatusBusy, "")
		b.resetTunnelRoundState()
	}

	err := b.agent.RunStream(ctx, userMsg, func(ev provider.StreamEvent) {
		if b.OnStreamEvent == nil {
			return
		}
		b.emit(ev)
	})
	if err != nil {
		b.appendLiveError(err.Error())
	}
	// Save session after each message (mirrors Fyne bridge)
	b.saveSession()

	// Mirror TUI handleDoneMsg: always push idle + clear activity
	// when the entire agent run finishes (success or error).
	if broker := b.currentTunnelBroker(); broker != nil {
		b.flushTunnelTextStream(broker, false)
		broker.PushStatus(tunnel.StatusIdle, "")
		broker.PushActivity("")
	}

	// Signal run complete (the entire agent run, not just one turn)
	if b.OnStreamEvent != nil {
		raw, _ := json.Marshal(map[string]interface{}{"error": ""})
		if err != nil {
			raw, _ = json.Marshal(map[string]interface{}{"error": err.Error()})
		}
		b.OnStreamEvent("run_done", raw)
	}

	return err
}

// Cancel stops the current agent run.
// Mirrors Fyne AgentBridge.Cancel exactly.
func (b *ChatBridge) Cancel() {
	b.mu.Lock()
	if b.cancel != nil {
		b.cancel()
		b.cancel = nil
	}
	b.cancelled = true
	b.mu.Unlock()

	if b.interactions != nil {
		b.interactions.CancelAll()
	}

	// Notify frontend to close dialogs
	if b.OnStreamEvent != nil {
		b.OnStreamEvent("approval:cancel", json.RawMessage(`{}`))
		b.OnStreamEvent("ask_user:cancel", json.RawMessage(`{}`))
	}

	// Push cancelled status to mobile (mirrors Fyne line 1108-1115)
	if broker := b.currentTunnelBroker(); broker != nil {
		b.flushTunnelTextStream(broker, false)
		broker.PushStatus(tunnel.StatusIdle, "cancelled")
		broker.PushActivity("")
	}
}

// ClearCurrentSession resets the current session so next chat creates a fresh one.
func (b *ChatBridge) ClearCurrentSession() {
	state := agentruntime.ClearSession()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.currentSes = state.Session
	b.usageTurnIndex = state.UsageTurnIndex
	b.lastMetricDigestTurn = state.LastMetricDigestTurn
	b.liveHistory = nil
	b.tunnelMsgID = ""
	b.tunnelMsgNeedsFinalize = false
	if b.tunnelProjectionBroker != nil {
		b.tunnelProjectionBroker.ResetSession()
	}
}

// LoadSession loads an existing session by ID.
func (b *ChatBridge) LoadSession(id string) error {
	if b.sessionStore == nil {
		store, err := session.NewDefaultStore()
		if err != nil {
			return fmt.Errorf("init session store: %w", err)
		}
		b.sessionStore = store
	}
	state, err := agentruntime.LoadSession(b.sessionStore, id)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}
	b.ResetAgent()
	b.mu.Lock()
	b.currentSes = state.Session
	b.usageTurnIndex = state.UsageTurnIndex
	b.lastMetricDigestTurn = state.LastMetricDigestTurn
	b.liveHistory = buildSessionHistoryFromMessages(state.Session.Messages)
	b.mu.Unlock()
	if err := b.initAgent(context.Background()); err != nil {
		return fmt.Errorf("init agent for session load: %w", err)
	}
	agentruntime.RestoreSessionIntoAgent(b.agent, state.Session)
	// Rebind projection session for the loaded session
	b.bindTunnelProjectionSession()
	return nil
}

// ensureSession creates a new session if none exists (mirrors Fyne bridge).
func (b *ChatBridge) ensureSession() error {
	if b.sessionStore == nil {
		store, err := session.NewDefaultStore()
		if err != nil {
			return fmt.Errorf("create session store: %w", err)
		}
		b.sessionStore = store
	}
	vendor, endpoint, model := "", "", ""
	if b.cfg != nil {
		vendor = b.cfg.Vendor
		endpoint = b.cfg.Endpoint
		model = b.cfg.Model
	}
	state, created, err := agentruntime.EnsureSession(b.sessionStore, b.currentSes, vendor, endpoint, model, b.workingDir)
	if err != nil {
		return fmt.Errorf("save new session: %w", err)
	}
	if !created {
		return nil
	}
	b.currentSes = state.Session
	b.usageTurnIndex = state.UsageTurnIndex
	b.lastMetricDigestTurn = state.LastMetricDigestTurn
	b.liveHistory = buildSessionHistoryFromMessages(state.Session.Messages)
	return nil
}

// saveSession persists the current session (mirrors Fyne bridge).
func (b *ChatBridge) saveSession() {
	b.mu.Lock()
	ses := b.currentSes
	store := b.sessionStore
	agent := b.agent
	b.mu.Unlock()
	if agent != nil {
		_ = agentruntime.SaveAgentSessionSnapshot(store, ses, agent)
		return
	}
	_ = agentruntime.SaveSessionMessages(store, ses, ses.Messages)
}

// CurrentSessionID returns the current session ID.
func (b *ChatBridge) CurrentSessionID() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.currentSes == nil {
		return ""
	}
	return b.currentSes.ID
}

// EnsureSession creates a default session if none exists (mirrors Fyne's ensureSession).
func (b *ChatBridge) EnsureSession() {
	vendor, endpoint, model := "", "", ""
	if b.cfg != nil {
		vendor = b.cfg.Vendor
		endpoint = b.cfg.Endpoint
		model = b.cfg.Model
	}
	state, created, err := agentruntime.EnsureSession(b.sessionStore, b.currentSes, vendor, endpoint, model, b.workingDir)
	if err != nil || !created {
		return
	}
	b.mu.Lock()
	b.currentSes = state.Session
	b.usageTurnIndex = state.UsageTurnIndex
	b.lastMetricDigestTurn = state.LastMetricDigestTurn
	b.mu.Unlock()
}

// initAgent sets up provider, tools, and agent — full parity with Fyne bridge.
func (b *ChatBridge) initAgent(ctx context.Context) error {
	// Permission policy (auto mode)
	modeStr := b.cfg.DefaultMode
	if modeStr == "" {
		modeStr = "auto"
	}
	mode := permission.ParsePermissionMode(modeStr)
	b.permissionMode = mode
	policy := agentruntime.BuildInteractivePermissionPolicy(b.cfg, b.workingDir, false)

	// Impersonation
	if b.cfg.Impersonation.Preset != "" && b.cfg.Impersonation.Preset != "none" {
		if preset := provider.FindPresetByID(b.cfg.Impersonation.Preset); preset != nil {
			provider.SetActiveImpersonation(preset, b.cfg.Impersonation.CustomVersion, b.cfg.Impersonation.CustomHeaders)
		}
	}

	resolved, p, err := agentruntime.ResolveCurrentSelection(b.cfg)
	if err != nil {
		return fmt.Errorf("resolve provider selection: %w", err)
	}
	b.resolved = resolved

	core, err := agentruntime.BuildInteractiveRuntimeCore(b.cfg, b.workingDir, policy)
	if err != nil {
		return fmt.Errorf("build runtime core: %w", err)
	}
	b.registry = core.Registry

	// Cron tools
	b.cronScheduler = cron.NewScheduler(nil)
	b.cronScheduler.SetEnqueue(func(prompt string) {
		log.Printf("[cron] enqueued prompt: %s", prompt)
	})
	_ = b.registry.Register(tool.CronCreateTool{Scheduler: b.cronScheduler})
	_ = b.registry.Register(tool.CronDeleteTool{Scheduler: b.cronScheduler})
	_ = b.registry.Register(tool.CronListTool{Scheduler: b.cronScheduler})
	mcpMgr := core.MCPManager
	b.mcpManager = mcpMgr
	autoMem := core.AutoMemory
	projectAutoMem := core.ProjectAutoMem
	commandMgr := core.CommandManager
	saveMemoryTool := core.SaveMemoryTool
	// When save_memory saves, rebuild system prompt so agent sees new memory
	// (mirrors Fyne setupAgent line 710)
	saveMemoryTool.SetAfterSave(func() {
		newPrompt := buildWailsSystemPrompt(b.cfg, b.workingDir, b.permissionMode, autoMem, projectAutoMem, commandMgr)
		b.mu.Lock()
		if b.agent != nil {
			b.agent.UpdateSystemPrompt(newPrompt)
		}
		b.mu.Unlock()
	})

	// ACP client manager (mirrors Fyne setupAgent)
	if b.acpClientMgr != nil {
		b.acpClientMgr.CloseAll()
	}
	b.acpClientMgr = acpclient.NewClientManager(b.workingDir, policy)
	b.acpClientMgr.SetApprovalHandler(func(ctx context.Context, toolName string, input string) permission.Decision {
		return b.RequestApproval(ctx, "", toolName, input)
	})
	// Sub-agent manager
	agentFactory := func(prov provider.Provider, t interface{}, systemPrompt string, maxTurns int) subagent.AgentRunner {
		return agent.NewAgent(prov, t.(*tool.Registry), systemPrompt, maxTurns)
	}
	b.subAgentMgr = agentruntime.NewSubAgentManager(b.cfg.SubAgents, b.registry, p, b.workingDir, b.recordSessionUsage, agentFactory)
	_ = b.registry.Register(agentruntime.NewSkillTool(commandMgr, mcpMgr, p, b.registry, agentFactory, b.workingDir, b.recordSessionUsage))
	agentruntime.RegisterDelegateTool(b.registry, b.acpClientMgr, func() *subagent.Manager { return b.subAgentMgr }, b.workingDir, func() string {
		if b.agent != nil {
			return b.agent.WorkingDir()
		}
		return b.workingDir
	})

	// Forward sub-agent events to frontend
	b.subAgentMgr.SetOnStreamText(func(agentID, text string) {
		if b.OnStreamEvent == nil {
			return
		}
		raw, _ := json.Marshal(map[string]string{"agentID": agentID, "title": b.subagentPanelTitle(agentID), "content": text})
		b.OnStreamEvent("subagent_text", raw)
		agentruntime.PushTunnelSubagentText(b.currentTunnelBroker, agentID, text)
	})
	b.subAgentMgr.SetOnReasoning(func(agentID, text string) {
		if b.OnStreamEvent == nil {
			agentruntime.PushTunnelSubagentReasoning(b.currentTunnelBroker, agentID, text)
			return
		}
		raw, _ := json.Marshal(map[string]string{"agentID": agentID, "title": b.subagentPanelTitle(agentID), "content": text})
		b.OnStreamEvent("subagent_reasoning", raw)
		agentruntime.PushTunnelSubagentReasoning(b.currentTunnelBroker, agentID, text)
	})
	b.subAgentMgr.SetOnToolCall(func(agentID, toolID, toolName, displayName, args, detail string) {
		if displayName == "" {
			pres := tool.DescribeTool(toolName, args)
			displayName = pres.DisplayName
			detail = pres.Detail
		}
		if b.OnStreamEvent != nil {
			raw, _ := json.Marshal(map[string]string{
				"agentID": agentID, "title": b.subagentPanelTitle(agentID), "id": toolID, "name": toolName,
				"displayName": displayName, "arguments": args, "detail": detail,
			})
			b.OnStreamEvent("subagent_tool_call", raw)
		}
		agentruntime.PushTunnelSubagentToolCall(b.currentTunnelBroker, agentID, toolID, toolName, displayName, args, detail)
	})
	b.subAgentMgr.SetOnToolResult(func(agentID, toolID, toolName, displayName, detail, result string, isError bool) {
		if displayName == "" {
			pres := tool.DescribeTool(toolName, "")
			displayName = pres.DisplayName
			detail = pres.Detail
		}
		if b.OnStreamEvent != nil {
			raw, _ := json.Marshal(map[string]interface{}{
				"agentID": agentID, "title": b.subagentPanelTitle(agentID), "id": toolID, "name": toolName,
				"displayName": displayName, "detail": detail,
				"result": result, "isError": isError,
			})
			b.OnStreamEvent("subagent_tool_result", raw)
		}
		agentruntime.PushTunnelSubagentToolResult(b.currentTunnelBroker, agentID, toolID, toolName, displayName, detail, result, isError)
	})

	// Swarm manager
	swarmFactory := func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) swarm.AgentRunner {
		return agent.NewAgent(prov, tools.(*tool.Registry), systemPrompt, maxTurns)
	}
	toolBuilder := func(allowedTools []string) interface{} {
		reg := tool.NewRegistry()
		_ = tool.RegisterBuiltinTools(reg, nil, b.workingDir)
		return reg
	}
	b.swarmMgr = agentruntime.NewSwarmManager(b.cfg.Swarm, p, b.registry, nil, swarmFactory, toolBuilder)

	b.registry.Register(tool.SendMessageTool{Manager: b.subAgentMgr, SwarmMgr: b.swarmMgr})

	// Forward swarm events to frontend AND mobile tunnel (mirrors Fyne line 605-698)
	b.swarmMgr.SetOnUpdate(func(ev swarm.Event) {
		// Push to frontend
		if b.OnStreamEvent != nil {
			switch ev.Type {
			case "teammate_text":
				raw, _ := json.Marshal(map[string]string{
					"teammateID": ev.TeammateID, "teammateName": ev.TeammateName,
					"teamID": ev.TeamID, "content": ev.Result,
				})
				b.OnStreamEvent("swarm_text", raw)
			case "teammate_tool_call":
				pres := tool.DescribeTool(ev.CurrentTool, ev.ToolArgs)
				raw, _ := json.Marshal(map[string]string{
					"teammateID": ev.TeammateID, "teammateName": ev.TeammateName,
					"teamID": ev.TeamID, "id": ev.ToolID, "name": ev.CurrentTool,
					"arguments": ev.ToolArgs, "displayName": pres.DisplayName, "detail": pres.Detail,
				})
				b.OnStreamEvent("swarm_tool_call", raw)
			case "teammate_tool_result":
				pres := tool.DescribeTool(ev.CurrentTool, "")
				raw, _ := json.Marshal(map[string]interface{}{
					"teammateID": ev.TeammateID, "teammateName": ev.TeammateName,
					"teamID": ev.TeamID, "id": ev.ToolID, "name": ev.CurrentTool,
					"displayName": pres.DisplayName, "detail": pres.Detail,
					"result": ev.Result, "isError": ev.IsError,
				})
				b.OnStreamEvent("swarm_tool_result", raw)
			case "teammate_spawned":
				raw, _ := json.Marshal(map[string]string{
					"teammateID": ev.TeammateID, "teammateName": ev.TeammateName, "teamID": ev.TeamID,
				})
				b.OnStreamEvent("swarm_spawned", raw)
			case "teammate_idle":
				raw, _ := json.Marshal(map[string]string{
					"teammateID": ev.TeammateID, "teammateName": ev.TeammateName, "teamID": ev.TeamID,
					"content": ev.Result,
				})
				b.OnStreamEvent("swarm_idle", raw)
			}
		}

		// Push to mobile tunnel (mirrors Fyne line 648-698)
		if broker := b.currentTunnelBroker(); broker != nil {
			_ = broker
			agentruntime.PushTunnelSwarmEvent(
				b.currentTunnelBroker,
				b.swarmMgr,
				ev,
				func(toolName, args string) string {
					pres := tool.DescribeTool(toolName, args)
					return pres.DisplayName
				},
				func(toolName, args string) string {
					pres := tool.DescribeTool(toolName, args)
					return pres.Detail
				},
			)
		}
	})

	// Create agent — mirror Fyne setupAgent exactly
	systemPrompt := buildWailsSystemPrompt(b.cfg, b.workingDir, b.permissionMode, autoMem, projectAutoMem, commandMgr)
	maxIter := b.cfg.MaxIterations
	if maxIter == 0 {
		maxIter = 200
	}
	a := agent.NewAgent(p, b.registry, systemPrompt, maxIter)
	core.SetConfigAgent(a)
	core.SetConfigUINotify(func() {
		b.onConfigProviderChanged()
	})
	a.SetPermissionPolicy(policy)

	// Usage handler — accumulate token usage per session (mirrors Fyne recordSessionUsage)
	a.SetUsageHandler(func(usage provider.TokenUsage) {
		b.recordSessionUsage(usage)
	})

	// Metric collector — async, non-blocking (mirrors Fyne line 715-721)
	collectorCtx, collectorCancel := context.WithCancel(context.Background())
	b.metricCancel = collectorCancel
	b.metricCollector = metrics.NewCollector(collectorCtx, 256, func(ev metrics.MetricEvent) {
		b.recordMetric(ev)
	})
	a.SetMetricHandler(b.metricCollector.Emit)

	// Context window — critical for context compaction (mirrors Fyne line 737-742)
	agentruntime.ApplyResolvedLimitsToAgent(a, resolved)
	agentruntime.StartAsyncRelayModelLimitRefresh(b.cfg, resolved, a, func(resp relaycatalog.ResolveResponse) {
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

	// Wire approval handler
	a.SetApprovalHandler(func(ctx context.Context, toolName string, input string) permission.Decision {
		requestID := ""
		if broker := b.currentTunnelBroker(); broker != nil {
			requestID = broker.NextMessageID()
		}
		return b.RequestApproval(ctx, requestID, toolName, input)
	})

	// Wire ask_user handler
	if askTool, ok := b.registry.Get("ask_user"); ok {
		if aut, ok := askTool.(*tool.AskUserTool); ok {
			aut.SetHandler(func(ctx context.Context, req tool.AskUserRequest) (tool.AskUserResponse, error) {
				requestID := ""
				if broker := b.currentTunnelBroker(); broker != nil {
					requestID = broker.NextMessageID()
				}
				return b.RequestAskUser(ctx, requestID, req)
			})
		}
	}

	_, _ = agentruntime.ApplyProjectMemoryToAgent(a, b.workingDir)

	b.agent = a
	// Set interruption handler — agent checks for pending messages during compact etc.
	// (mirrors Fyne line 836-839)
	a.SetInterruptionHandler(func() string {
		return b.drainPendingInterrupt()
	})
	b.EnsureSession()               // mirrors Fyne setupAgent line 743
	b.bindTunnelProjectionSession() // record events even before Share (mirrors Fyne line 303)
	return nil
}

func (b *ChatBridge) subagentPanelTitle(agentID string) string {
	if b.subAgentMgr == nil {
		return agentID
	}
	snap, ok := b.subAgentMgr.SnapshotByID(agentID)
	if !ok {
		return agentID
	}
	switch {
	case strings.TrimSpace(snap.DisplayTask) != "":
		return snap.DisplayTask
	case strings.TrimSpace(snap.Task) != "":
		return snap.Task
	case strings.TrimSpace(snap.Name) != "":
		return snap.Name
	default:
		return agentID
	}
}

func (b *ChatBridge) emit(ev provider.StreamEvent) {
	var eventType string
	var data interface{}
	var semantic agentruntime.DesktopStreamSemantic
	var ok bool

	switch ev.Type {
	case provider.StreamEventToolCallChunk:
		eventType = "tool_call_chunk"
		data = map[string]interface{}{
			"id":   ev.Tool.ID,
			"name": ev.Tool.Name,
		}

	default:
		semantic, ok = agentruntime.HandleDesktopStreamEvent(ev, &b.imRound,
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
			nil,
		)
		if !ok {
			return
		}
		b.applySemanticToLiveHistory(semantic)
		switch semantic.Type {
		case provider.StreamEventText:
			eventType = "text"
			data = map[string]string{"content": semantic.Text}
		case provider.StreamEventToolCallDone:
			eventType = "tool_call_done"
			data = map[string]interface{}{
				"id":          semantic.ToolCall.ID,
				"name":        semantic.ToolCall.Name,
				"arguments":   semantic.ToolCall.RawArgs,
				"displayName": semantic.ToolCall.DisplayName,
				"detail":      semantic.ToolCall.Detail,
			}
		case provider.StreamEventToolResult:
			eventType = "tool_result"
			data = map[string]interface{}{
				"id":      semantic.ToolResult.ID,
				"name":    semantic.ToolResult.Name,
				"result":  semantic.ToolResult.Preview,
				"isError": semantic.ToolResult.IsError,
			}
		case provider.StreamEventDone:
			eventType = "done"
			data = semantic.UsageData
			b.resetTunnelRoundState()
		case provider.StreamEventError:
			eventType = "error"
			data = map[string]string{"message": semantic.ErrorText}
		case provider.StreamEventReasoning:
			eventType = "reasoning"
			data = map[string]string{"content": semantic.Text}
		default:
			return
		}
	}

	raw, _ := json.Marshal(data)
	if b.OnStreamEvent != nil {
		b.OnStreamEvent(eventType, raw)
	}
}

func (b *ChatBridge) CurrentSessionHistory() []SessionMessage {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.liveHistory) > 0 {
		out := make([]SessionMessage, len(b.liveHistory))
		copy(out, b.liveHistory)
		return out
	}
	if b.currentSes == nil {
		return nil
	}
	return buildSessionHistoryFromMessages(b.currentSes.Messages)
}

func (b *ChatBridge) appendLiveUserMessage(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.liveHistory) == 0 && b.currentSes != nil {
		b.liveHistory = buildSessionHistoryFromMessages(b.currentSes.Messages)
	}
	b.liveHistory = append(b.liveHistory, SessionMessage{
		Role:    "user",
		Content: text,
	})
}

func (b *ChatBridge) appendLiveError(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.liveHistory) == 0 && b.currentSes != nil {
		b.liveHistory = buildSessionHistoryFromMessages(b.currentSes.Messages)
	}
	b.liveHistory = append(b.liveHistory, SessionMessage{
		Role:    "error",
		Content: text,
	})
}

func (b *ChatBridge) applySemanticToLiveHistory(semantic agentruntime.DesktopStreamSemantic) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.liveHistory) == 0 && b.currentSes != nil {
		b.liveHistory = buildSessionHistoryFromMessages(b.currentSes.Messages)
	}
	switch semantic.Type {
	case provider.StreamEventReasoning:
		if semantic.Text == "" {
			return
		}
		if n := len(b.liveHistory); n > 0 && b.liveHistory[n-1].Role == "reasoning" && b.liveHistory[n-1].Streaming {
			b.liveHistory[n-1].Content += semantic.Text
			return
		}
		b.liveHistory = append(b.liveHistory, SessionMessage{
			Role:      "reasoning",
			Content:   semantic.Text,
			Streaming: true,
		})
	case provider.StreamEventText:
		b.finalizeLiveReasoningLocked()
		if n := len(b.liveHistory); n > 0 && b.liveHistory[n-1].Role == "assistant" && b.liveHistory[n-1].Streaming {
			b.liveHistory[n-1].Content += semantic.Text
			return
		}
		b.liveHistory = append(b.liveHistory, SessionMessage{
			Role:      "assistant",
			Content:   semantic.Text,
			Streaming: true,
		})
	case provider.StreamEventToolCallDone:
		b.finalizeLiveReasoningLocked()
		b.finalizeStreamingAssistantLocked()
		if semantic.ToolCall == nil {
			return
		}
		b.liveHistory = append(b.liveHistory, SessionMessage{
			Role:        "tool",
			ToolName:    semantic.ToolCall.Name,
			ToolID:      semantic.ToolCall.ID,
			ToolArgs:    semantic.ToolCall.RawArgs,
			ToolDisplay: semantic.ToolCall.DisplayName,
			ToolDetail:  semantic.ToolCall.Detail,
			Streaming:   true,
		})
	case provider.StreamEventToolResult:
		if semantic.ToolResult == nil {
			return
		}
		for i := len(b.liveHistory) - 1; i >= 0; i-- {
			if b.liveHistory[i].Role == "tool" && b.liveHistory[i].ToolID == semantic.ToolResult.ID {
				b.liveHistory[i].Content = semantic.ToolResult.Preview
				b.liveHistory[i].IsError = semantic.ToolResult.IsError
				b.liveHistory[i].Streaming = false
				break
			}
		}
	case provider.StreamEventDone:
		b.finalizeLiveReasoningLocked()
		b.finalizeStreamingAssistantLocked()
	case provider.StreamEventError:
		b.finalizeLiveReasoningLocked()
		b.finalizeStreamingAssistantLocked()
		if semantic.ErrorText == "" {
			return
		}
		b.liveHistory = append(b.liveHistory, SessionMessage{
			Role:    "error",
			Content: semantic.ErrorText,
		})
	}
}

func (b *ChatBridge) finalizeLiveReasoningLocked() {
	if n := len(b.liveHistory); n > 0 && b.liveHistory[n-1].Role == "reasoning" && b.liveHistory[n-1].Streaming {
		b.liveHistory[n-1].Streaming = false
	}
}

func (b *ChatBridge) finalizeStreamingAssistantLocked() {
	if n := len(b.liveHistory); n > 0 && b.liveHistory[n-1].Role == "assistant" && b.liveHistory[n-1].Streaming {
		b.liveHistory[n-1].Streaming = false
	}
}

// GetModelInfo returns the current model info for the status bar.
func (b *ChatBridge) GetModelInfo() map[string]interface{} {
	if b.cfg == nil {
		return nil
	}
	resolved, err := b.cfg.ResolveActiveEndpoint()
	if err != nil {
		return map[string]interface{}{
			"vendor":       b.cfg.Vendor,
			"model":        b.cfg.Model,
			"mode":         b.GetPermissionMode(),
			"contextTotal": 0,
		}
	}
	payload := map[string]interface{}{
		"vendor":        b.cfg.Vendor,
		"model":         b.cfg.Model,
		"contextWindow": resolved.ContextWindow,
		"contextTotal":  resolved.ContextWindow,
		"mode":          b.GetPermissionMode(),
	}
	for key, value := range b.currentUsagePayload() {
		payload[key] = value
	}
	return payload
}

// ─── Tunnel Broker Integration ──────────────────────────────────────
// Full parity with Fyne AgentBridge tunnel logic.

// AttachTunnelBroker connects the broker for outbound event push to mobile.
// Mirrors Fyne AgentBridge.AttachTunnelBroker exactly.
func (b *ChatBridge) AttachTunnelBroker(broker *tunnel.Broker) {
	var (
		working    bool
		cfg        *config.Config
		currentSes *session.Session
		attachCfg  agentruntime.TunnelAttachConfig
	)
	b.mu.Lock()
	b.tunnelBroker = broker
	b.shareTunnelBroker = broker
	working = b.cancel != nil
	cfg = b.cfg
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

	if cfg != nil {
		resolved, _ := cfg.ResolveActiveEndpoint()
		model := ""
		vendorName := ""
		if resolved != nil {
			model = resolved.Model
			vendorName = resolved.VendorName
		}
		info := tunnel.SessionInfoData{
			Workspace: b.workingDir,
			Model:     model,
			Provider:  vendorName,
			Mode:      cfg.DefaultMode,
		}
		if working {
			attachCfg.SessionInfo = &info
		}
	}
	if working {
		status := b.CurrentTunnelStatus()
		attachCfg.Status = &status
		activity := b.CurrentTunnelActivity()
		attachCfg.Activity = &activity
	}
	agentruntime.AttachTunnelBroker(broker, attachCfg)
}

func (b *ChatBridge) DetachTunnelBroker() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tunnelBroker = nil
	b.shareTunnelBroker = nil
}

func (b *ChatBridge) currentTunnelBroker() *tunnel.Broker {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.tunnelBroker
}

func (b *ChatBridge) currentShareTunnelBroker() *tunnel.Broker {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.shareTunnelBroker != nil {
		return b.shareTunnelBroker
	}
	return b.tunnelBroker
}

func (b *ChatBridge) ensureTunnelProjectionBroker() *tunnel.Broker {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.tunnelProjectionBroker == nil {
		b.tunnelProjectionBroker = tunnel.NewBroker(nil)
	}
	return b.tunnelProjectionBroker
}

func (b *ChatBridge) bindTunnelProjectionSession() {
	b.mu.Lock()
	currentSes := b.currentSes
	b.mu.Unlock()
	if currentSes == nil {
		return
	}

	// Use offline projection broker (mirrors Fyne ensureTunnelProjectionBroker)
	// This works even before Share — events are recorded for later replay.
	broker := b.ensureTunnelProjectionBroker()

	if b.projectionStore == nil {
		if store, err := tunnel.NewDefaultProjectionStore(); err == nil {
			b.projectionStore = store
		}
	}

	b.tunnelProjectionBroken = false
	if _, err := agentruntime.PrepareProjectionBroker(broker, b.projectionStore, currentSes, func(ev tunnel.GatewayMessage) {
		b.recordProjectionEvent(ev)
	}); err != nil {
		log.Printf("[wails-chat] projection replay prep failed for %s: %v", currentSes.ID, err)
	}

	b.mu.Lock()
	if b.tunnelMsgID == "" {
		b.tunnelMsgID = broker.NextMessageID()
	}
	b.mu.Unlock()
}

func (b *ChatBridge) recordProjectionEvent(msg tunnel.GatewayMessage) {
	b.mu.Lock()
	store := b.projectionStore
	b.mu.Unlock()
	if err := agentruntime.AppendProjectionEvent(store, msg); err != nil {
		b.mu.Lock()
		b.tunnelProjectionBroken = true
		b.mu.Unlock()
		log.Printf("[wails-chat] projection append failed for %s event=%s: %v", msg.SessionID, msg.EventID, err)
		b.RecordTunnelEvent(msg)
		return
	}
	b.RecordTunnelEvent(msg)
	if broker := b.currentShareTunnelBroker(); broker != nil {
		broker.PublishRecordedEvent(msg)
	}
}

func (b *ChatBridge) RecordTunnelEvent(msg tunnel.GatewayMessage) {
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

func (b *ChatBridge) CurrentTunnelStatus() tunnel.StatusData {
	if b.cancel != nil {
		return tunnel.StatusData{Status: tunnel.StatusBusy}
	}
	return tunnel.StatusData{Status: tunnel.StatusIdle}
}

func (b *ChatBridge) CurrentTunnelActivity() string {
	switch {
	case b.interactions != nil && b.interactions.ApprovalCount() > 0:
		return "approval"
	case b.interactions != nil && b.interactions.AskUserCount() > 0:
		return "ask_user"
	case b.cancel != nil:
		return "processing"
	default:
		return ""
	}
}

func (b *ChatBridge) ensureTunnelMsgID(broker *tunnel.Broker) string {
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

func (b *ChatBridge) tunnelReasoningMsgID(broker *tunnel.Broker) string {
	return agentruntime.TunnelReasoningMsgID(b.ensureTunnelMsgID(broker))
}

func (b *ChatBridge) markTunnelMainStreamActive() {
	b.mu.Lock()
	defer b.mu.Unlock()
	state := agentruntime.MarkTunnelMainStreamActive(agentruntime.TunnelMainStream{
		MessageID:     b.tunnelMsgID,
		NeedsFinalize: b.tunnelMsgNeedsFinalize,
	})
	b.tunnelMsgID = state.MessageID
	b.tunnelMsgNeedsFinalize = state.NeedsFinalize
}

func (b *ChatBridge) flushTunnelTextStream(broker *tunnel.Broker, force bool) {
	b.mu.Lock()
	state := agentruntime.FlushTunnelMainStream(agentruntime.TunnelMainStream{
		MessageID:     b.tunnelMsgID,
		NeedsFinalize: b.tunnelMsgNeedsFinalize,
	}, broker, force)
	b.tunnelMsgID = state.MessageID
	b.tunnelMsgNeedsFinalize = state.NeedsFinalize
	b.mu.Unlock()
}

func (b *ChatBridge) resetTunnelRoundState() {
	b.mu.Lock()
	b.tunnelMsgNeedsFinalize = false
	b.mu.Unlock()
}

func (b *ChatBridge) currentSessionTunnelAuthorityEpoch() uint64 {
	b.mu.Lock()
	store := b.projectionStore
	ses := b.currentSes
	b.mu.Unlock()
	if store == nil || ses == nil {
		return 1
	}
	if epoch, err := agentruntime.ProjectionAuthorityEpoch(store, ses.ID); err == nil {
		return epoch
	}
	return 1
}

func (b *ChatBridge) CurrentSessionTunnelEvents() []tunnel.GatewayMessage {
	b.mu.Lock()
	store := b.projectionStore
	ses := b.currentSes
	broken := b.tunnelProjectionBroken
	b.mu.Unlock()
	if store == nil || ses == nil || broken {
		return nil
	}
	events, err := agentruntime.ProjectionReplay(store, ses.ID)
	if err != nil {
		b.mu.Lock()
		b.tunnelProjectionBroken = true
		b.mu.Unlock()
		return nil
	}
	return events
}

func (b *ChatBridge) pushTunnelSessionInfo(broker *tunnel.Broker) {
	b.mu.Lock()
	cfg := b.cfg
	ses := b.currentSes
	b.mu.Unlock()
	if broker == nil || cfg == nil || ses == nil {
		return
	}
	resolved, _ := cfg.ResolveActiveEndpoint()
	model := ""
	vendorName := ""
	if resolved != nil {
		model = resolved.Model
		vendorName = resolved.VendorName
	}
	broker.SendSessionInfo(tunnel.SessionInfoData{
		Workspace: b.workingDir,
		Model:     model,
		Provider:  vendorName,
		Mode:      cfg.DefaultMode,
	})
}

func (b *ChatBridge) pushTunnelApprovalResult(id, decision string) {
	agentruntime.PushTunnelApprovalResult(b.currentTunnelBroker(), id, decision, agentruntime.TunnelStateUpdate{})
}

func (b *ChatBridge) pushTunnelAskUserResponse(id string, response tool.AskUserResponse) {
	agentruntime.PushTunnelAskUserResponse(b.currentTunnelBroker(), id, response, agentruntime.TunnelStateUpdate{})
}

func (b *ChatBridge) nextTunnelRequestID() string {
	return fmt.Sprintf("req-%d", time.Now().UnixNano())
}

func (b *ChatBridge) ResetCurrentSessionTunnelLedger() {
	b.mu.Lock()
	ses := b.currentSes
	store := b.projectionStore
	b.mu.Unlock()
	if ses == nil || store == nil {
		return
	}
	_ = store.DeleteSession(ses.ID)
}

func (b *ChatBridge) CurrentTunnelHistory() []tunnel.HistoryEntry {
	// TODO: implement when HistoryEntry fields are needed
	return nil
}

// RequestApproval blocks until the user (desktop or mobile) responds to an
// approval request.  It stores a pending channel, pushes the request to a
// connected tunnel broker, and emits an event so the Wails frontend can show
// an approval dialog.
func (b *ChatBridge) RequestApproval(ctx context.Context, requestID, toolName, input string) permission.Decision {
	req := agentruntime.ApprovalRequest{ID: requestID, ToolName: toolName, Input: input}

	// Push to mobile via tunnel
	if broker := b.currentTunnelBroker(); broker != nil {
		agentruntime.PushTunnelApprovalRequest(broker, requestID, toolName, input, agentruntime.TunnelStateUpdate{
			HasStatus: true,
			Status:    tunnel.StatusWaiting,
		})
	}

	// Emit to Wails frontend
	if b.OnStreamEvent != nil {
		raw, _ := json.Marshal(map[string]string{
			"requestID": requestID,
			"toolName":  toolName,
			"input":     input,
		})
		b.OnStreamEvent("approval:request", raw)
	}

	return b.interactions.AwaitApproval(context.WithoutCancel(ctx), req)
}

// RequestAskUser blocks until the user (desktop or mobile) responds to a
// structured questionnaire.  It mirrors the Fyne handleAskUser flow.
func (b *ChatBridge) RequestAskUser(ctx context.Context, requestID string, req tool.AskUserRequest) (tool.AskUserResponse, error) {
	if len(req.Questions) == 0 {
		return tool.AskUserResponse{Status: tool.AskUserStatusSubmitted}, nil
	}
	request := agentruntime.AskUserRequest{ID: requestID, Request: req}

	// Push to mobile via tunnel
	if broker := b.currentTunnelBroker(); broker != nil {
		agentruntime.PushTunnelAskUserRequest(broker, requestID, req, agentruntime.TunnelStateUpdate{
			HasStatus: true,
			Status:    tunnel.StatusWaiting,
		})
	}

	// Emit to Wails frontend
	if b.OnStreamEvent != nil {
		payload := map[string]interface{}{
			"requestID": requestID,
			"title":     req.Title,
			"questions": req.Questions,
		}
		raw, _ := json.Marshal(payload)
		b.OnStreamEvent("ask_user:request", raw)
	}

	return b.interactions.AwaitAskUser(context.WithoutCancel(ctx), request)
}

// RespondApproval delivers a desktop-originated approval decision to the
// waiting channel.  decision is "allow", "deny", or "always_allow".
func (b *ChatBridge) RespondApproval(requestID, decision string) {
	d := agentruntime.ApprovalDecisionFromTunnel(decision)
	req, ok := b.interactions.ResolveApproval(requestID, d)
	if !ok {
		return
	}

	// Always-allow: persist override on the agent's permission policy
	if (decision == "always_allow" || decision == "always") && b.agent != nil {
		if p, ok := b.agent.PermissionPolicy().(*permission.ConfigPolicy); ok {
			p.SetOverride(req.ToolName, permission.Allow)
		}
	}

	// Push result to mobile
	agentruntime.PushTunnelApprovalResult(b.currentTunnelBroker(), requestID, decision, agentruntime.TunnelStateUpdate{
		HasStatus: true,
		Status:    tunnel.StatusBusy,
	})

}

func (b *ChatBridge) PendingApprovalRequest() (string, string, bool) {
	req, ok := b.interactions.FirstPendingApproval()
	if !ok {
		return "", "", false
	}
	return req.ID, req.ToolName, true
}

// RespondAskUser delivers a desktop-originated ask_user response to the
// waiting channel.
func (b *ChatBridge) RespondAskUser(requestID string, response tool.AskUserResponse) {
	if _, ok := b.interactions.ResolveAskUser(requestID, response); !ok {
		return
	}

	// Push response to mobile
	agentruntime.PushTunnelAskUserResponse(b.currentTunnelBroker(), requestID, response, agentruntime.TunnelStateUpdate{
		HasStatus: true,
		Status:    tunnel.StatusBusy,
	})

}

func (b *ChatBridge) PendingAskUserRequest() (string, tool.AskUserRequest, bool) {
	req, ok := b.interactions.FirstPendingAskUser()
	if !ok {
		return "", tool.AskUserRequest{}, false
	}
	return req.ID, req.Request, true
}

// HandleMobileApprovalResponse processes an approval response received from
// the mobile client via the tunnel.
func (b *ChatBridge) HandleMobileApprovalResponse(data tunnel.ApprovalResponseData) {
	decision := agentruntime.ResolveTunnelApproval(data.Decision, "", nil)
	req, ok := b.interactions.ResolveApproval(data.ID, decision)
	if !ok {
		return
	}
	agentruntime.ResolveTunnelApproval(data.Decision, req.ToolName, func(toolName string) {
		if b.agent != nil {
			if p, ok := b.agent.PermissionPolicy().(*permission.ConfigPolicy); ok {
				p.SetOverride(toolName, permission.Allow)
			}
		}
	})

	// Push result to mobile (for relay persistence)
	agentruntime.PushTunnelApprovalResult(b.currentTunnelBroker(), data.ID, data.Decision, agentruntime.TunnelStateUpdate{
		HasStatus: true,
		Status:    tunnel.StatusBusy,
	})
}

// HandleMobileAskUserResponse processes an ask_user response received from
// the mobile client via the tunnel.
func (b *ChatBridge) HandleMobileAskUserResponse(data tunnel.AskUserResponseData, req tool.AskUserRequest) {
	response := agentruntime.BuildAskUserResponseFromTunnel(req, data.Status, data.Answers)
	if _, ok := b.interactions.ResolveAskUser(data.ID, response); !ok {
		return
	}

	// Push response to mobile (for relay persistence)
	agentruntime.PushTunnelAskUserResponse(b.currentTunnelBroker(), data.ID, response, agentruntime.TunnelStateUpdate{
		HasStatus: true,
		Status:    tunnel.StatusBusy,
	})
}

// CurrentAskUserRequest returns the pending ask_user request for the given ID,
// or nil if none exists.  Used by HandleMobileAskUserResponse to reconstruct
// the full response with completion metadata.
func (b *ChatBridge) CurrentAskUserRequest(requestID string) tool.AskUserRequest {
	// We don't store the request separately — but HandleMobileAskUserResponse
	// takes it as a parameter from app.go which stores the current ask state.
	return tool.AskUserRequest{}
}

// SetApprovalOverride persists a tool-level permission override.
func (b *ChatBridge) SetApprovalOverride(toolName string) {
	if b.agent != nil {
		if p, ok := b.agent.PermissionPolicy().(*permission.ConfigPolicy); ok {
			p.SetOverride(toolName, permission.Allow)
		}
	}
}

// ─── System Prompt ───────────────────────────────────────────────────

// buildWailsSystemPrompt builds the system prompt for the agent.
// Mirrors Fyne buildSystemPrompt exactly.
// buildWailsSystemPrompt builds the system prompt for the agent.
// Mirrors Fyne buildSystemPrompt exactly — includes auto-memory content.
func buildWailsSystemPrompt(cfg *config.Config, workingDir string, mode permission.PermissionMode, globalAutoMem, projectAutoMem *memory.AutoMemory, commandMgr *commands.Manager) string {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	return agentruntime.BuildInteractiveSystemPrompt(cfg, workingDir, mode, nil, commandMgr, globalAutoMem, projectAutoMem, "")
}

// ─── Session Usage Tracking ──────────────────────────────────────────

// recordSessionUsage accumulates token usage into the session.
// Mirrors Fyne AgentBridge.recordSessionUsage and TUI Model.recordSessionUsage exactly.
func (b *ChatBridge) recordSessionUsage(usage provider.TokenUsage) {
	b.mu.Lock()
	if b.currentSes == nil || b.sessionStore == nil {
		b.mu.Unlock()
		return
	}
	ses := b.currentSes
	ses.TokenUsage = ses.TokenUsage.Add(usage)
	ses.AddUsageForEndpoint(ses.Vendor, ses.Endpoint, usage)
	ses.UpdatedAt = time.Now()
	store := b.sessionStore
	turnIdx := b.usageTurnIndex
	entry := session.UsageEntry{
		Timestamp: time.Now(),
		TurnIndex: turnIdx,
		Model:     ses.Model,
		Vendor:    ses.Vendor,
		Endpoint:  ses.Endpoint,
		Usage:     usage,
	}
	b.mu.Unlock()

	if jsonlStore, ok := store.(*session.JSONLStore); ok {
		_ = jsonlStore.AppendMetaToDisk(ses)
		_ = jsonlStore.AppendUsageEntry(ses, entry)
	} else {
		_ = store.Save(ses)
	}

	// Notify frontend of updated usage
	if b.OnStreamEvent != nil {
		raw, _ := json.Marshal(b.currentUsagePayload())
		b.OnStreamEvent("usage_update", raw)
	}
}

func (b *ChatBridge) currentUsagePayload() map[string]interface{} {
	b.mu.Lock()
	defer b.mu.Unlock()

	payload := map[string]interface{}{
		"inputTokens":      0,
		"outputTokens":     0,
		"cacheRead":        0,
		"cacheWrite":       0,
		"cacheHit":         0,
		"contextUsed":      0,
		"contextTotal":     0,
		"usagePercent":     0,
		"remainingPercent": 0,
	}
	if b.currentSes != nil {
		usage := b.currentSes.UsageForEndpoint(b.currentSes.Vendor, b.currentSes.Endpoint)
		payload["inputTokens"] = usage.DisplayInputTokens()
		payload["outputTokens"] = usage.OutputTokens
		payload["cacheRead"] = usage.CacheRead
		payload["cacheWrite"] = usage.CacheWrite
		payload["cacheHit"] = usage.CacheHitPercent()
	}
	if b.agent != nil {
		cm := b.agent.ContextManager()
		display, ok := uiusage.BuildContextDisplay(cm.TokenCount(), cm.ContextWindow(), cm.AutoCompactThreshold())
		if ok {
			payload["contextUsed"] = display.UsedTokens
			payload["contextTotal"] = display.MaxTokens
			payload["usagePercent"] = display.UsagePercent
			payload["remainingPercent"] = display.RemainingPercent
		}
	}
	return payload
}

// ─── Metrics ──────────────────────────────────────────────────────────

// recordMetric records a metric event. Mirrors Fyne recordMetric.
func (b *ChatBridge) recordMetric(ev interface{}) {
	// Metrics are logged for now; can be extended to push to UI or remote
}

// ─── Sub-agent tunnel helpers ────────────────────────────────────────

func tunnelSubagentTextID(agentID string) string {
	return fmt.Sprintf("sa-%s", agentID)
}

func tunnelSubagentReasoningID(agentID string) string {
	return fmt.Sprintf("sa-%s-reasoning", agentID)
}

func (b *ChatBridge) markTunnelSubagentSpawned(id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.spawnedSet == nil {
		b.spawnedSet = make(map[string]bool)
	}
	if b.spawnedSet[id] {
		return false
	}
	b.spawnedSet[id] = true
	return true
}

func (b *ChatBridge) pushTunnelSubagentEvent(sa *subagent.SubAgent) {
	agentruntime.PushTunnelSubagentEvent(b.currentTunnelBroker, b.markTunnelSubagentSpawned, sa)
}

// ─── Permission Mode ──────────────────────────────────────────────────

// GetPermissionMode returns the current permission mode string.
func (b *ChatBridge) GetPermissionMode() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.permissionMode.String()
}

// SetPermissionMode updates the agent permission mode at runtime.
// Mirrors Fyne AgentBridge.SetPermissionMode exactly.
func (b *ChatBridge) SetPermissionMode(modeStr string) {
	mode := permission.ParsePermissionMode(modeStr)
	b.mu.Lock()
	b.permissionMode = mode
	agent := b.agent
	cfg := b.cfg
	b.mu.Unlock()
	if agent != nil {
		policy := permission.NewConfigPolicyWithMode(nil, []string{b.workingDir}, mode)
		agent.SetPermissionPolicy(policy)
		// Update system prompt to include current mode, overriding any stale
		// autopilot continue instructions that may still be in context.
		b.refreshSystemPrompt()
	}
	// Persist to config file (mirrors TUI/Fyne SaveDefaultModePreference)
	if cfg != nil {
		_ = cfg.SaveDefaultModePreference(modeStr)
	}
}

// ─── Pending Messages ────────────────────────────────────────────────

// QueueMessage stores a user message to be sent after the current agent turn.
func (b *ChatBridge) QueueMessage(msg string) {
	b.pendingMsgs.Enqueue(msg, false, nil)
}

// QueueHiddenMessage stores a hidden message (mirrors Fyne).
func (b *ChatBridge) QueueHiddenMessage(msg string) {
	b.pendingMsgs.Enqueue(msg, true, nil)
}

func (b *ChatBridge) drainPending() (agentruntime.PendingMessage[*tunnel.MessageData], bool) {
	return b.pendingMsgs.Consume()
}

func (b *ChatBridge) drainPendingInterrupt() string {
	pending, ok := b.drainPending()
	if !ok {
		return ""
	}
	if !pending.Hidden {
		// Persist visible user message
		b.mu.Lock()
		if b.currentSes != nil {
			msg := provider.Message{Role: "user", Content: []provider.ContentBlock{provider.TextBlock(pending.Text)}}
			b.currentSes.Messages = append(b.currentSes.Messages, msg)
			b.currentSes.UpdatedAt = time.Now()
			if b.sessionStore != nil {
				_ = b.sessionStore.Save(b.currentSes)
			}
		}
		b.mu.Unlock()
	}
	return strings.TrimSpace(pending.Text)
}

// SendHiddenText sends a hidden message to the agent without UI display.
func (b *ChatBridge) SendHiddenText(text string) error {
	b.mu.Lock()
	if b.cancel != nil {
		b.pendingMsgs.Enqueue(text, true, nil)
		b.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel
	b.cancelled = false
	b.usageTurnIndex++
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		b.cancel = nil
		b.mu.Unlock()
	}()

	if b.agent == nil {
		if err := b.initAgent(ctx); err != nil {
			return fmt.Errorf("init agent: %w", err)
		}
	}

	return b.agent.RunStream(ctx, text, func(ev provider.StreamEvent) {
		b.emit(ev)
	})
}

// ─── Agent Lifecycle ──────────────────────────────────────────────────

// Close cleans up all resources (mirrors Fyne AgentBridge.Close).
func (b *ChatBridge) Close() {
	b.mu.Lock()
	if b.metricCancel != nil {
		b.metricCancel()
	}
	if b.acpClientMgr != nil {
		b.acpClientMgr.CloseAll()
	}
	b.mu.Unlock()
}

// IsWorking returns true if the agent is currently running.
func (b *ChatBridge) IsWorking() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cancel != nil
}

// Elapsed returns time since the current agent run started.
func (b *ChatBridge) Elapsed() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cancel == nil {
		return 0
	}
	return time.Since(b.startTime)
}

// ContextWindow returns the current context window size.
func (b *ChatBridge) ContextWindow() int {
	b.mu.Lock()
	agent := b.agent
	resolved := b.resolved
	b.mu.Unlock()
	if agent != nil {
		return agent.ContextManager().ContextWindow()
	}
	if resolved != nil {
		return resolved.ContextWindow
	}
	return 0
}

// TokenCount returns the current token usage.
func (b *ChatBridge) TokenCount() int {
	b.mu.Lock()
	agent := b.agent
	b.mu.Unlock()
	if agent == nil {
		return 0
	}
	return agent.ContextManager().TokenCount()
}

// CurrentSession returns the current session.
func (b *ChatBridge) CurrentSession() *session.Session {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.currentSes
}

func (b *ChatBridge) PrepareShareBroker(broker *tunnel.Broker, snapshotProvider func() tunnel.BrokerSnapshot) {
	if broker == nil || snapshotProvider == nil {
		return
	}
	b.EnsureSession()
	snapshot := snapshotProvider()
	sessionID := ""
	if current := b.CurrentSession(); current != nil {
		sessionID = current.ID
	}
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

// SessionStore returns the session store.
func (b *ChatBridge) SessionStore() session.Store {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sessionStore
}

// Resolved returns the resolved endpoint.
func (b *ChatBridge) Resolved() *config.ResolvedEndpoint {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.resolved
}

// ResetAgent destroys the current agent, forcing a rebuild on next message.
func (b *ChatBridge) ResetAgent() {
	b.mu.Lock()
	b.agent = nil
	if b.metricCancel != nil {
		b.metricCancel()
	}
	b.metricCollector = nil
	b.metricCancel = nil
	b.mu.Unlock()
}

// SwitchModel hot-swaps the model at runtime (mirrors Fyne SwitchModel).
func (b *ChatBridge) SwitchModel(model string) error {
	if model == "" || b.cfg == nil {
		return fmt.Errorf("model is empty or config is nil")
	}
	resolved, prov, err := agentruntime.ActivateCurrentSelection(b.cfg, b.cfg.Vendor, b.cfg.Endpoint, model)
	if err != nil {
		return fmt.Errorf("activate current selection: %w", err)
	}

	b.mu.Lock()
	a := b.agent
	b.mu.Unlock()

	b.mu.Lock()
	b.resolved = resolved
	b.mu.Unlock()
	agentruntime.ApplyProviderToAgent(a, prov, resolved)
	agentruntime.StartAsyncRelayModelLimitRefresh(b.cfg, resolved, a, func(resp relaycatalog.ResolveResponse) {
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

// onConfigProviderChanged syncs Wails bridge state after the config tool
// changes vendor/endpoint/model/api_key. Updates b.resolved and b.currentSes
// so the frontend model picker and status bar reflect the new selection.
func (b *ChatBridge) onConfigProviderChanged() {
	if b.cfg == nil {
		return
	}
	resolved, err := b.cfg.ResolveActiveEndpoint()
	if err != nil {
		return
	}
	b.mu.Lock()
	b.resolved = resolved
	if b.currentSes != nil {
		b.currentSes.Vendor = b.cfg.Vendor
		b.currentSes.Endpoint = b.cfg.Endpoint
		b.currentSes.Model = resolved.Model
	}
	b.mu.Unlock()
	// Notify Wails frontend to refresh model picker and status bar
	if b.emitEvent != nil {
		b.emitEvent("config:updated", nil)
	}
}

// PushErrorToMobile pushes an error message to mobile via tunnel.
func (b *ChatBridge) PushErrorToMobile(msg string) {
	if broker := b.currentTunnelBroker(); broker != nil {
		broker.PushError(msg)
	}
}

// PushSystemMessageToMobile pushes a system message to mobile via tunnel.
func (b *ChatBridge) PushSystemMessageToMobile(msg string) {
	if broker := b.currentTunnelBroker(); broker != nil {
		broker.PushSystemMessage(msg)
	}
}

// PushUserMessageToMobile pushes a user message to mobile via tunnel.
func (b *ChatBridge) PushUserMessageToMobile(msg string) {
	if broker := b.currentTunnelBroker(); broker != nil {
		broker.PushUserMessage(msg)
	}
}

// ResumeSession loads a session and re-initializes the agent for it.
func (b *ChatBridge) ResumeSession(id string) error {
	if err := b.initAgent(context.Background()); err != nil {
		return err
	}
	if err := b.LoadSession(id); err != nil {
		return err
	}
	return nil
}

// SendContent sends multimodal content to the agent.
func (b *ChatBridge) SendContent(content []provider.ContentBlock) error {
	b.mu.Lock()
	if b.cancel != nil {
		b.mu.Unlock()
		return fmt.Errorf("agent is already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel
	b.cancelled = false
	b.usageTurnIndex++
	b.startTime = time.Now()
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		b.cancel = nil
		b.mu.Unlock()

		if pending, ok := b.drainPending(); ok {
			if pending.Hidden {
				_ = b.SendHiddenText(pending.Text)
			} else {
				_ = b.SendMessage(pending.Text)
			}
		}
	}()

	if b.agent == nil {
		if err := b.initAgent(ctx); err != nil {
			return fmt.Errorf("init agent: %w", err)
		}
	}

	if b.currentSes != nil {
		msg := provider.Message{Role: "user", Content: content}
		b.currentSes.Messages = append(b.currentSes.Messages, msg)
		b.currentSes.UpdatedAt = time.Now()
		if b.sessionStore != nil {
			_ = b.sessionStore.Save(b.currentSes)
		}
	}

	return b.agent.RunStreamWithContent(ctx, content, func(ev provider.StreamEvent) {
		b.emit(ev)
	})
}

// GetAvailableModels returns the list of models available for the current endpoint.
func (b *ChatBridge) GetAvailableModels() []string {
	b.mu.Lock()
	resolved := b.resolved
	cfg := b.cfg
	b.mu.Unlock()

	// Try resolved endpoint first
	if resolved != nil && len(resolved.Models) > 0 {
		return resolved.Models
	}

	// Fallback: look up from config vendors
	if cfg != nil {
		if vc, ok := cfg.Vendors[cfg.Vendor]; ok {
			if ep, ok := vc.Endpoints[cfg.Endpoint]; ok {
				if len(ep.Models) > 0 {
					return ep.Models
				}
			}
		}
		// Last resort: just current model
		if cfg.Model != "" {
			return []string{cfg.Model}
		}
	}
	return nil
}

// refreshSystemPrompt rebuilds and updates the agent's system prompt.
func (b *ChatBridge) refreshSystemPrompt() {
	var autoMem, projectAutoMem *memory.AutoMemory
	if am := memory.NewAutoMemory(); am != nil {
		autoMem = am
	}
	if pam := memory.NewProjectAutoMemory(b.workingDir); pam != nil {
		projectAutoMem = pam
	}
	startupAssets := agentruntime.LoadInteractiveStartupAssets(b.workingDir, autoMem, projectAutoMem)
	b.mu.Lock()
	mode := b.permissionMode
	b.mu.Unlock()
	newPrompt := buildWailsSystemPrompt(b.cfg, b.workingDir, mode, autoMem, projectAutoMem, startupAssets.CommandManager)
	b.mu.Lock()
	if b.agent != nil {
		b.agent.UpdateSystemPrompt(newPrompt)
	}
	b.mu.Unlock()
}

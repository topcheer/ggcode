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

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/acpclient"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/cron"
	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/swarm"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tunnel"
)

// pendingMessage holds a user message queued while the agent is busy.
type pendingMessage struct {
	Text   string
	Hidden bool
}

// ChatBridge manages the full agent chat loop for the Wails frontend,
// mirroring the Fyne desktop's AgentBridge tool registration and session management.
type ChatBridge struct {
	cfg          *config.Config
	resolved     *config.ResolvedEndpoint
	agent        *agent.Agent
	registry     *tool.Registry
	workingDir   string
	sessionStore session.Store
	currentSes   *session.Session
	permissionMode permission.PermissionMode

	mu     sync.Mutex
	cancel    context.CancelFunc
	cancelled bool
	startTime time.Time

	// Pending messages (mirrors Fyne pendingMsgs)
	pendingMsgs []pendingMessage

	// Subsystems
	cronScheduler *cron.Scheduler
	subAgentMgr   *subagent.Manager
	acpClientMgr  *acpclient.ClientManager
	swarmMgr      *swarm.Manager

	// Metrics
	metricCancel    context.CancelFunc
	metricCollector *metrics.Collector
	usageTurnIndex  int
	lastMetricDigestTurn int

	// Sub-agent tunnel tracking
	spawnedSet map[string]bool

	// IM outbound push — same as Fyne agentBridge.Emitter
	Emitter *im.IMEmitter

	// IM round accumulator for emitter (mirrors Fyne agentBridge.imRound)
	imRound struct {
		Text          strings.Builder
		ToolCalls     int
		ToolSuccesses int
		ToolFailures  int
	}

	// Mobile tunnel broker for outbound push
	tunnelBroker           *tunnel.Broker
	tunnelProjectionBroker *tunnel.Broker // offline broker for event recording before Share
	tunnelMsgID            string
	tunnelMsgNeedsFinalize bool
	tunnelProjectionBroken bool
	projectionStore        *tunnel.ProjectionStore
	shareTunnelBroker      *tunnel.Broker

	// Pending approval/ask_user requests from agent
	pendingMu        sync.Mutex
	pendingApprovals map[string]chan permission.Decision
	pendingAskUsers  map[string]chan tool.AskUserResponse

	// Callback for emitting events to frontend.
	OnStreamEvent func(eventType string, data json.RawMessage)
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
		cfg:              cfg,
		workingDir:       wd,
		permissionMode:   permission.ParsePermissionMode(modeStr),
		pendingApprovals: make(map[string]chan permission.Decision),
		pendingAskUsers:  make(map[string]chan tool.AskUserResponse),
	}, nil
}

// SendMessage sends a user message and streams events to the frontend.
// If agent is already running, queues the message for processing after the current turn.
func (b *ChatBridge) SendMessage(userMsg string) error {
	b.mu.Lock()
	if b.cancel != nil {
		// Agent is busy — queue the message (mirrors Fyne QueueMessage)
		b.pendingMsgs = append(b.pendingMsgs, pendingMessage{Text: userMsg})
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
				_ = b.SendMessage(pending.Text)
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

	// Notify mobile client: user message + busy status
	if broker := b.currentTunnelBroker(); broker != nil {
		msgID := broker.NextMessageID()
		broker.PushUserMessageData(tunnel.MessageData{
			Text:      userMsg,
			MessageID: msgID,
		})
		broker.PushStatus(tunnel.StatusBusy, "")
		b.resetTunnelRoundState()
	}

	err := b.agent.RunStream(ctx, userMsg, func(ev provider.StreamEvent) {
		if b.OnStreamEvent == nil {
			return
		}
		b.emit(ev)
	})
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

	// Close any pending approval/ask_user dialogs by sending deny/cancel
	// to their channels. This unblocks RequestApproval/RequestAskUser so
	// the agent loop can exit cleanly.
	b.pendingMu.Lock()
	for id, ch := range b.pendingApprovals {
		ch <- permission.Deny
		delete(b.pendingApprovals, id)
	}
	for id, ch := range b.pendingAskUsers {
		ch <- tool.AskUserResponse{Status: tool.AskUserStatusCancelled}
		delete(b.pendingAskUsers, id)
	}
	b.pendingMu.Unlock()

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
	b.mu.Lock()
	defer b.mu.Unlock()
	b.currentSes = nil
	b.usageTurnIndex = 0
	b.lastMetricDigestTurn = 0
	b.tunnelMsgID = ""
	b.tunnelMsgNeedsFinalize = false
	if b.tunnelProjectionBroker != nil {
		b.tunnelProjectionBroker.ResetSession()
	}
}

// LoadSession loads an existing session by ID.
func (b *ChatBridge) LoadSession(id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sessionStore == nil {
		store, err := session.NewDefaultStore()
		if err != nil {
			return fmt.Errorf("init session store: %w", err)
		}
		b.sessionStore = store
	}
	ses, err := b.sessionStore.Load(id)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}
	b.currentSes = ses
	b.usageTurnIndex = session.LastTurnIndex(ses)
	b.lastMetricDigestTurn = b.usageTurnIndex
	// Rebind projection session for the loaded session
	b.bindTunnelProjectionSession()
	return nil
}

// ensureSession creates a new session if none exists (mirrors Fyne bridge).
func (b *ChatBridge) ensureSession() error {
	if b.currentSes != nil {
		return nil
	}
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
	ses := session.NewSession(vendor, endpoint, model)
	ses.Workspace = b.workingDir
	if err := b.sessionStore.Save(ses); err != nil {
		return fmt.Errorf("save new session: %w", err)
	}
	b.currentSes = ses
	return nil
}

// saveSession persists the current session (mirrors Fyne bridge).
func (b *ChatBridge) saveSession() {
	if b.currentSes == nil || b.sessionStore == nil {
		return
	}
	_ = b.sessionStore.Save(b.currentSes)
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
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.currentSes != nil {
		return
	}
	vendor, endpoint, model := "", "", ""
	if b.cfg != nil {
		vendor = b.cfg.Vendor
		endpoint = b.cfg.Endpoint
		model = b.cfg.Model
	}
	ses := session.NewSession(vendor, endpoint, model)
	if b.sessionStore != nil {
		_ = b.sessionStore.Save(ses)
	}
	b.currentSes = ses
}

// initAgent sets up provider, tools, and agent — full parity with Fyne bridge.
func (b *ChatBridge) initAgent(ctx context.Context) error {
	// Resolve provider
	resolved, err := b.cfg.ResolveActiveEndpoint()
	if err != nil {
		return fmt.Errorf("resolve endpoint: %w", err)
	}
	b.resolved = resolved

	p, err := provider.NewProvider(resolved)
	if err != nil {
		return fmt.Errorf("create provider: %w", err)
	}

	// Permission policy (auto mode)
	modeStr := b.cfg.DefaultMode
	if modeStr == "" {
		modeStr = "auto"
	}
	mode := permission.ParsePermissionMode(modeStr)
	b.permissionMode = mode
	policy := permission.NewConfigPolicyWithMode(nil, []string{b.workingDir}, mode)

	// Impersonation
	if b.cfg.Impersonation.Preset != "" && b.cfg.Impersonation.Preset != "none" {
		if preset := provider.FindPresetByID(b.cfg.Impersonation.Preset); preset != nil {
			provider.SetActiveImpersonation(preset, b.cfg.Impersonation.CustomVersion, b.cfg.Impersonation.CustomHeaders)
		}
	}

	// Tool registry
	b.registry = tool.NewRegistry()
	if err := tool.RegisterBuiltinTools(b.registry, nil, b.workingDir); err != nil {
		log.Printf("Warning: some builtin tools failed: %v", err)
	}

	// Cron tools
	b.cronScheduler = cron.NewScheduler(nil)
	b.cronScheduler.SetEnqueue(func(prompt string) {
		log.Printf("[cron] enqueued prompt: %s", prompt)
	})
	_ = b.registry.Register(tool.CronCreateTool{Scheduler: b.cronScheduler})
	_ = b.registry.Register(tool.CronDeleteTool{Scheduler: b.cronScheduler})
	_ = b.registry.Register(tool.CronListTool{Scheduler: b.cronScheduler})

	// MCP tools
	mergedServers, _ := mcp.MergeStartupServers(b.workingDir, b.cfg.MCPServers)
	mcpMgr := plugin.NewMCPManager(mergedServers, b.registry)
	_ = b.registry.Register(tool.ListMCPCapabilitiesTool{Runtime: mcpMgr})
	_ = b.registry.Register(tool.GetMCPPromptTool{Runtime: mcpMgr})
	_ = b.registry.Register(tool.ReadMCPResourceTool{Runtime: mcpMgr})

	// Plugin tools
	pluginMgr := plugin.NewManager()
	pluginMgr.LoadAll(b.cfg.Plugins)
	_ = pluginMgr.RegisterTools(b.registry)

	// Memory tools
	autoMem := memory.NewAutoMemory()
	projectAutoMem := memory.NewProjectAutoMemory(b.workingDir)
	saveMemoryTool := tool.NewSaveMemoryTool(autoMem, projectAutoMem)
	_ = b.registry.Register(saveMemoryTool)
	// When save_memory saves, rebuild system prompt so agent sees new memory
	// (mirrors Fyne setupAgent line 710)
	saveMemoryTool.SetAfterSave(func() {
		newPrompt := buildWailsSystemPrompt(b.workingDir, b.permissionMode, autoMem, projectAutoMem)
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
	if len(b.acpClientMgr.Available()) > 0 {
		_ = b.registry.Register(tool.DelegateTool{
			Manager:           b.acpClientMgr,
			SubAgentManagerFn: func() *subagent.Manager { return b.subAgentMgr },
			WorkingDir:        b.workingDir,
			WorkingDirFn: func() string {
				if b.agent != nil {
					return b.agent.WorkingDir()
				}
				return b.workingDir
			},
		})
	}

	// Sub-agent manager
	b.subAgentMgr = subagent.NewManager(b.cfg.SubAgents)
	agentFactory := func(prov provider.Provider, t interface{}, systemPrompt string, maxTurns int) subagent.AgentRunner {
		return agent.NewAgent(prov, t.(*tool.Registry), systemPrompt, maxTurns)
	}
	b.registry.Register(tool.SpawnAgentTool{
		Manager:      b.subAgentMgr,
		Provider:     p,
		Tools:        b.registry,
		AgentFactory: agentFactory,
		WorkingDir:   b.workingDir,
	})
	b.registry.Register(tool.WaitAgentTool{Manager: b.subAgentMgr})
	b.registry.Register(tool.ListAgentsTool{Manager: b.subAgentMgr})

	// Forward sub-agent events to frontend
	b.subAgentMgr.SetOnStreamText(func(agentID, text string) {
		if b.OnStreamEvent == nil {
			return
		}
		raw, _ := json.Marshal(map[string]string{"agentID": agentID, "content": text})
		b.OnStreamEvent("subagent_text", raw)
	})
	b.subAgentMgr.SetOnReasoning(func(agentID, text string) {
		if b.OnStreamEvent == nil {
			return
		}
		raw, _ := json.Marshal(map[string]string{"agentID": agentID, "content": text})
		b.OnStreamEvent("subagent_reasoning", raw)
	})
	b.subAgentMgr.SetOnToolCall(func(agentID, toolID, toolName, displayName, args, detail string) {
		if b.OnStreamEvent == nil {
			return
		}
		if displayName == "" {
			pres := tool.DescribeTool(toolName, args)
			displayName = pres.DisplayName
			detail = pres.Detail
		}
		raw, _ := json.Marshal(map[string]string{
			"agentID": agentID, "id": toolID, "name": toolName,
			"displayName": displayName, "arguments": args, "detail": detail,
		})
		b.OnStreamEvent("subagent_tool_call", raw)
	})
	b.subAgentMgr.SetOnToolResult(func(agentID, toolID, toolName, displayName, detail, result string, isError bool) {
		if b.OnStreamEvent == nil {
			return
		}
		if displayName == "" {
			pres := tool.DescribeTool(toolName, "")
			displayName = pres.DisplayName
			detail = pres.Detail
		}
		raw, _ := json.Marshal(map[string]interface{}{
			"agentID": agentID, "id": toolID, "name": toolName,
			"displayName": displayName, "detail": detail,
			"result": result, "isError": isError,
		})
		b.OnStreamEvent("subagent_tool_result", raw)
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
	b.swarmMgr = swarm.NewManager(b.cfg.Swarm, p, swarmFactory, toolBuilder)

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
			switch ev.Type {
			case "teammate_tool_call":
				displayName := ""
				detail := ""
				pres := tool.DescribeTool(ev.CurrentTool, ev.ToolArgs)
				displayName = pres.DisplayName
				detail = pres.Detail
				broker.PushSubagentToolCall(ev.TeammateID, ev.ToolID, ev.CurrentTool, displayName, ev.ToolArgs, detail)
				broker.PushSubagentStatus(ev.TeammateID, tunnel.StatusRunning, ev.CurrentTool)
			case "teammate_tool_result":
				broker.PushSubagentToolResult(ev.TeammateID, ev.ToolID, ev.CurrentTool, "", "", ev.ToolArgs, ev.IsError)
			case "teammate_text":
				msgID := fmt.Sprintf("tm-%s", ev.TeammateID)
				broker.PushSubagentText(ev.TeammateID, msgID, ev.Result, false)
			case "teammate_spawned":
				broker.PushSubagentSpawn(ev.TeammateID, ev.TeammateName, "teammate", "", ev.TeamID)
			case "teammate_working":
				broker.PushSubagentStatus(ev.TeammateID, tunnel.StatusRunning, ev.TeammateName)
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
			case "teammate_error":
				errMsg := ""
				if ev.Error != nil {
					errMsg = ev.Error.Error()
				}
				broker.PushSubagentComplete(ev.TeammateID, ev.TeammateName, errMsg, false)
			}
		}
	})

	// Create agent — mirror Fyne setupAgent exactly
	systemPrompt := buildWailsSystemPrompt(b.workingDir, b.permissionMode, autoMem, projectAutoMem)
	maxIter := b.cfg.MaxIterations
	if maxIter == 0 {
		maxIter = 200
	}
	a := agent.NewAgent(p, b.registry, systemPrompt, maxIter)
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
	if resolved.ContextWindow > 0 {
		a.ContextManager().SetContextWindow(resolved.ContextWindow)
	}
	if resolved.MaxTokens > 0 {
		a.ContextManager().SetOutputReserve(resolved.MaxTokens)
	}

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

	b.agent = a
	// Set interruption handler — agent checks for pending messages during compact etc.
	// (mirrors Fyne line 836-839)
	a.SetInterruptionHandler(func() string {
		return b.drainPendingInterrupt()
	})
	b.EnsureSession() // mirrors Fyne setupAgent line 743
	b.bindTunnelProjectionSession() // record events even before Share (mirrors Fyne line 303)
	return nil
}

func (b *ChatBridge) emit(ev provider.StreamEvent) {
	var eventType string
	var data interface{}

	switch ev.Type {
	case provider.StreamEventText:
		eventType = "text"
		data = map[string]string{"content": ev.Text}
		b.imRound.Text.WriteString(ev.Text)
		// Push to mobile via tunnel
		if broker := b.currentTunnelBroker(); broker != nil {
			b.markTunnelMainStreamActive()
			broker.PushReasoningDone(b.tunnelReasoningMsgID(broker))
			broker.PushText(b.ensureTunnelMsgID(broker), ev.Text)
		}

	case provider.StreamEventToolCallChunk:
		eventType = "tool_call_chunk"
		data = map[string]interface{}{
			"id":   ev.Tool.ID,
			"name": ev.Tool.Name,
		}

	case provider.StreamEventToolCallDone:
		pres := tool.DescribeTool(ev.Tool.Name, string(ev.Tool.Arguments))
		eventType = "tool_call_done"
		b.imRound.ToolCalls++
		if b.Emitter != nil {
			b.Emitter.TriggerTyping()
		}
		// Push to mobile via tunnel (mirrors Fyne: flush text before tool call)
		if broker := b.currentTunnelBroker(); broker != nil {
			b.flushTunnelTextStream(broker, true)
			args := string(ev.Tool.Arguments)
			if len([]rune(args)) > 2000 {
				args = string([]rune(args)[:2000])
			}
			broker.PushToolCall(ev.Tool.ID, ev.Tool.Name, pres.DisplayName, args, pres.Detail)
		}
		data = map[string]interface{}{
			"id":          ev.Tool.ID,
			"name":        ev.Tool.Name,
			"arguments":   string(ev.Tool.Arguments),
			"displayName": pres.DisplayName,
			"detail":      pres.Detail,
		}

	case provider.StreamEventToolResult:
		eventType = "tool_result"
		resultPreview := ev.Result
		if len(resultPreview) > 500 {
			resultPreview = resultPreview[:500] + "..."
		}
		data = map[string]interface{}{
			"id":      ev.Tool.ID,
			"name":    ev.Tool.Name,
			"result":  resultPreview,
			"isError": ev.IsError,
		}
		if ev.IsError {
			b.imRound.ToolFailures++
		} else {
			b.imRound.ToolSuccesses++
		}
		// Emit tool result event to IM
		if b.Emitter != nil {
			content := ev.Result
			if len([]rune(content)) > 2000 {
				content = string([]rune(content)[:2000]) + "\n...(truncated)"
			}
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
		// Push to mobile via tunnel (mirrors Fyne: reasoning done before tool result)
		if broker := b.currentTunnelBroker(); broker != nil {
			broker.PushReasoningDone(b.tunnelReasoningMsgID(broker))
			broker.PushToolResult(ev.Tool.ID, ev.Tool.Name, ev.Result, ev.IsError)
		}

	case provider.StreamEventDone:
		eventType = "done"
		// Emit round summary to IM (mirrors Fyne agentBridge)
		if b.Emitter != nil {
			text := strings.TrimSpace(b.imRound.Text.String())
			if text != "" || b.imRound.ToolCalls > 0 {
				b.Emitter.EmitRoundSummary(text, b.imRound.ToolCalls, b.imRound.ToolSuccesses, b.imRound.ToolFailures)
			}
		}
		// Reset round accumulator
		b.imRound.Text.Reset()
		b.imRound.ToolCalls = 0
		b.imRound.ToolSuccesses = 0
		b.imRound.ToolFailures = 0
		b.resetTunnelRoundState()
		// Mirror TUI/Fyne: only close text stream + rotate msgID on Done.
		// Do NOT push idle/clear-activity here — that happens when the
		// entire agent run finishes (in SendMessage after RunStream returns).
		if broker := b.currentTunnelBroker(); broker != nil {
			b.flushTunnelTextStream(broker, true)
		}
		usageData := map[string]interface{}{}
		if ev.Usage != nil {
			cacheTotal := ev.Usage.CacheRead + ev.Usage.CacheWrite + ev.Usage.InputTokens
			cacheHit := 0
			if cacheTotal > 0 && ev.Usage.CacheRead > 0 {
				cacheHit = ev.Usage.CacheRead * 100 / cacheTotal
			}
			usageData = map[string]interface{}{
				"inputTokens":  ev.Usage.InputTokens + ev.Usage.CacheRead,
				"outputTokens": ev.Usage.OutputTokens,
				"cacheRead":    ev.Usage.CacheRead,
				"cacheWrite":   ev.Usage.CacheWrite,
				"cacheHit":     cacheHit,
			}
		}
		data = usageData

	case provider.StreamEventError:
		eventType = "error"
		errMsg := "unknown error"
		if ev.Error != nil {
			errMsg = ev.Error.Error()
		}
		data = map[string]string{"message": errMsg}
		// Push to mobile via tunnel (mirrors Fyne)
		if broker := b.currentTunnelBroker(); broker != nil {
			b.flushTunnelTextStream(broker, true)
			broker.PushError(errMsg)
		}

	case provider.StreamEventReasoning:
		eventType = "reasoning"
		data = map[string]string{"content": ev.Text}
		// Push to mobile via tunnel
		if broker := b.currentTunnelBroker(); broker != nil {
			broker.PushReasoning(b.tunnelReasoningMsgID(broker), ev.Text)
		}

	default:
		return
	}

	raw, _ := json.Marshal(data)
	b.OnStreamEvent(eventType, raw)
}

// GetModelInfo returns the current model info for the status bar.
func (b *ChatBridge) GetModelInfo() map[string]interface{} {
	if b.cfg == nil {
		return nil
	}
	resolved, err := b.cfg.ResolveActiveEndpoint()
	if err != nil {
		return map[string]interface{}{
			"vendor": b.cfg.Vendor,
			"model":  b.cfg.Model,
			"mode":   b.GetPermissionMode(),
		}
	}
	return map[string]interface{}{
		"vendor":        b.cfg.Vendor,
		"model":         b.cfg.Model,
		"contextWindow": resolved.ContextWindow,
		"mode":          b.GetPermissionMode(),
	}
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
	)
	b.mu.Lock()
	b.tunnelBroker = broker
	working = b.cancel != nil
	cfg = b.cfg
	currentSes = b.currentSes
	if working && broker != nil && b.tunnelMsgID == "" {
		b.tunnelMsgID = broker.NextMessageID()
	}
	b.mu.Unlock()

	if broker == nil {
		return
	}

	b.bindTunnelProjectionSession()

	broker.SetReplayProvider(func() []tunnel.GatewayMessage {
		return b.CurrentSessionTunnelEvents()
	})
	broker.SetEventRecorder(nil)

	if currentSes != nil && currentSes.ID != "" {
		broker.BindSession(currentSes.ID)
		broker.SetAuthorityEpoch(b.currentSessionTunnelAuthorityEpoch())
		broker.AnnounceActiveSession(currentSes.ID)
	}

	if !working {
		return
	}

	if cfg != nil {
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

	status := b.CurrentTunnelStatus()
	if status.Status != "" {
		broker.PushStatus(status.Status, status.Message)
	}
	broker.PushActivity(b.CurrentTunnelActivity())
}

func (b *ChatBridge) DetachTunnelBroker() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tunnelBroker = nil
}

func (b *ChatBridge) currentTunnelBroker() *tunnel.Broker {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Mirrors Fyne: projection broker takes priority (works before Share)
	if b.tunnelProjectionBroker != nil {
		return b.tunnelProjectionBroker
	}
	return b.tunnelBroker
}

func (b *ChatBridge) currentShareTunnelBroker() *tunnel.Broker {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.shareTunnelBroker
}

func (b *ChatBridge) ensureTunnelProjectionBroker() *tunnel.Broker {
	return b.currentTunnelBroker()
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
	b.mu.Lock()
	if b.tunnelProjectionBroker == nil {
		b.tunnelProjectionBroker = tunnel.NewBroker(nil)
	}
	broker := b.tunnelProjectionBroker
	b.mu.Unlock()

	if b.projectionStore == nil {
		if store, err := tunnel.NewDefaultProjectionStore(); err == nil {
			b.projectionStore = store
		}
	}

	var authorityEpoch uint64 = 1

	if b.projectionStore != nil {
		if epoch, err := b.projectionStore.AuthorityEpoch(currentSes.ID); err == nil {
			authorityEpoch = epoch
		}
		var replay []tunnel.GatewayMessage
		if r, err := b.projectionStore.ReplayEvents(currentSes.ID); err == nil {
			replay = r
		}
		if len(replay) == 0 {
			replay = b.hydrateProjectionReplayFromSessionLedger(currentSes, replay)
		}
		if len(replay) > 0 {
			broker.PrimeEventIDs(replay)
		}
	}

	b.tunnelProjectionBroken = false
	broker.SwitchSession(currentSes.ID)
	broker.SetAuthorityEpoch(authorityEpoch)
	broker.SetEventRecorder(func(ev tunnel.GatewayMessage) {
		b.recordProjectionEvent(ev)
	})

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
	if store != nil {
		if err := store.Append(msg); err != nil {
			b.mu.Lock()
			b.tunnelProjectionBroken = true
			b.mu.Unlock()
			log.Printf("[wails-chat] projection append failed for %s event=%s: %v", msg.SessionID, msg.EventID, err)
			b.RecordTunnelEvent(msg)
			return
		}
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
	b.pendingMu.Lock()
	defer b.pendingMu.Unlock()
	switch {
	case len(b.pendingApprovals) > 0:
		return "approval"
	case len(b.pendingAskUsers) > 0:
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
	if b.tunnelMsgID == "" && broker != nil {
		b.tunnelMsgID = broker.NextMessageID()
	}
	return b.tunnelMsgID
}

func (b *ChatBridge) rotateTunnelMsgID(broker *tunnel.Broker) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if broker == nil {
		b.tunnelMsgID = ""
		return
	}
	b.tunnelMsgID = broker.NextMessageID()
}

func (b *ChatBridge) tunnelReasoningMsgID(broker *tunnel.Broker) string {
	msgID := b.ensureTunnelMsgID(broker)
	if msgID == "" {
		return ""
	}
	return msgID + "-reasoning"
}

func (b *ChatBridge) markTunnelMainStreamActive() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.tunnelMsgID != "" {
		b.tunnelMsgNeedsFinalize = true
	}
}

func (b *ChatBridge) flushTunnelTextStream(broker *tunnel.Broker, force bool) {
	if broker == nil {
		return
	}
	b.mu.Lock()
	if !force && !b.tunnelMsgNeedsFinalize {
		b.mu.Unlock()
		return
	}
	b.mu.Unlock()
	broker.PushReasoningDone(b.tunnelReasoningMsgID(broker))
	msgID := b.ensureTunnelMsgID(broker)
	broker.PushTextDone(msgID)
	b.rotateTunnelMsgID(broker)
	b.mu.Lock()
	b.tunnelMsgNeedsFinalize = false
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
	if epoch, err := store.AuthorityEpoch(ses.ID); err == nil {
		return epoch
	}
	return 1
}

func (b *ChatBridge) CurrentSessionTunnelEvents() []tunnel.GatewayMessage {
	b.mu.Lock()
	store := b.projectionStore
	ses := b.currentSes
	b.mu.Unlock()
	if store == nil || ses == nil {
		return nil
	}
	events, err := store.ReplayEvents(ses.ID)
	if err != nil {
		return nil
	}
	return events
}

func (b *ChatBridge) hydrateProjectionReplayFromSessionLedger(currentSes *session.Session, replay []tunnel.GatewayMessage) []tunnel.GatewayMessage {
	if currentSes == nil || len(currentSes.TunnelEvents) == 0 {
		return replay
	}
	for _, te := range currentSes.TunnelEvents {
		replay = append(replay, tunnel.GatewayMessage{
			EventID:   te.EventID,
			StreamID:  te.StreamID,
			Type:      te.Type,
			Data:      te.Data,
			SessionID: currentSes.ID,
		})
	}
	return replay
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
	if broker := b.currentTunnelBroker(); broker != nil {
		broker.PushApprovalResult(id, decision)
	}
}

func (b *ChatBridge) pushTunnelAskUserResponse(id string, response tool.AskUserResponse) {
	if broker := b.currentTunnelBroker(); broker != nil {
		var answers []tunnel.AskUserAnswer
		for _, a := range response.Answers {
			answers = append(answers, tunnel.AskUserAnswer{
				QuestionID:   a.ID,
				ChoiceIDs:    a.SelectedChoiceIDs,
				FreeformText: a.FreeformText,
			})
		}
		broker.PushAskUserResponse(id, "", answers)
	}
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
	ch := make(chan permission.Decision, 1)

	b.pendingMu.Lock()
	b.pendingApprovals[requestID] = ch
	b.pendingMu.Unlock()

	// Push to mobile via tunnel
	if broker := b.currentTunnelBroker(); broker != nil {
		broker.PushApprovalRequest(requestID, toolName, input)
		broker.PushStatus(tunnel.StatusWaiting, "")
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

	// Block until user responds. Use background context so the approval
	// outlives the agent's RunStream context — cancelling the agent run
	// should not auto-deny a pending approval request.
	select {
	case d := <-ch:
		return d
	}
}

// RequestAskUser blocks until the user (desktop or mobile) responds to a
// structured questionnaire.  It mirrors the Fyne handleAskUser flow.
func (b *ChatBridge) RequestAskUser(ctx context.Context, requestID string, req tool.AskUserRequest) (tool.AskUserResponse, error) {
	if len(req.Questions) == 0 {
		return tool.AskUserResponse{Status: tool.AskUserStatusSubmitted}, nil
	}

	ch := make(chan tool.AskUserResponse, 1)

	b.pendingMu.Lock()
	b.pendingAskUsers[requestID] = ch
	b.pendingMu.Unlock()

	// Push to mobile via tunnel
	if broker := b.currentTunnelBroker(); broker != nil {
		questions := buildWailsTunnelAskUserQuestions(req)
		broker.PushAskUserRequest(requestID, req.Title, questions)
		broker.PushStatus(tunnel.StatusWaiting, "")
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

	// Block until user responds. Use background context so ask_user
	// outlives the agent's RunStream context — cancelling the agent run
	// should not auto-cancel a pending user question.
	select {
	case resp := <-ch:
		return resp, nil
	}
}

// RespondApproval delivers a desktop-originated approval decision to the
// waiting channel.  decision is "allow", "deny", or "always_allow".
func (b *ChatBridge) RespondApproval(requestID, decision string) {
	b.pendingMu.Lock()
	ch, ok := b.pendingApprovals[requestID]
	if ok {
		delete(b.pendingApprovals, requestID)
	}
	b.pendingMu.Unlock()
	if !ok {
		return
	}

	var d permission.Decision
	switch decision {
	case "allow":
		d = permission.Allow
	case "always_allow", "always":
		d = permission.Allow
	default:
		d = permission.Deny
	}

	// Always-allow: persist override on the agent's permission policy
	if (decision == "always_allow" || decision == "always") && b.agent != nil {
		if p, ok := b.agent.PermissionPolicy().(*permission.ConfigPolicy); ok {
			// We don't have toolName here; the caller (app.go) handles override
			_ = p
		}
	}

	// Push result to mobile
	if broker := b.currentTunnelBroker(); broker != nil && requestID != "" {
		broker.PushApprovalResult(requestID, decision)
		broker.PushStatus(tunnel.StatusBusy, "")
	}

	select {
	case ch <- d:
	default:
	}
}

// RespondAskUser delivers a desktop-originated ask_user response to the
// waiting channel.
func (b *ChatBridge) RespondAskUser(requestID string, response tool.AskUserResponse) {
	b.pendingMu.Lock()
	ch, ok := b.pendingAskUsers[requestID]
	if ok {
		delete(b.pendingAskUsers, requestID)
	}
	b.pendingMu.Unlock()
	if !ok {
		return
	}

	// Push response to mobile
	if broker := b.currentTunnelBroker(); broker != nil && requestID != "" {
		answers := make([]tunnel.AskUserAnswer, len(response.Answers))
		for i, answer := range response.Answers {
			answers[i] = tunnel.AskUserAnswer{
				QuestionID:   answer.ID,
				ChoiceIDs:    append([]string(nil), answer.SelectedChoiceIDs...),
				FreeformText: answer.FreeformText,
			}
		}
		broker.PushAskUserResponse(requestID, response.Status, answers)
		broker.PushStatus(tunnel.StatusBusy, "")
	}

	select {
	case ch <- response:
	default:
	}
}

// HandleMobileApprovalResponse processes an approval response received from
// the mobile client via the tunnel.
func (b *ChatBridge) HandleMobileApprovalResponse(data tunnel.ApprovalResponseData) {
	b.pendingMu.Lock()
	ch, ok := b.pendingApprovals[data.ID]
	if ok {
		delete(b.pendingApprovals, data.ID)
	}
	b.pendingMu.Unlock()
	if !ok {
		return
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

	// Push result to mobile (for relay persistence)
	if broker := b.currentTunnelBroker(); broker != nil && data.ID != "" {
		broker.PushApprovalResult(data.ID, data.Decision)
		broker.PushStatus(tunnel.StatusBusy, "")
	}

	select {
	case ch <- decision:
	default:
	}
}

// HandleMobileAskUserResponse processes an ask_user response received from
// the mobile client via the tunnel.
func (b *ChatBridge) HandleMobileAskUserResponse(data tunnel.AskUserResponseData, req tool.AskUserRequest) {
	b.pendingMu.Lock()
	ch, ok := b.pendingAskUsers[data.ID]
	if ok {
		delete(b.pendingAskUsers, data.ID)
	}
	b.pendingMu.Unlock()
	if !ok {
		return
	}

	response := buildWailsAskUserResponseFromTunnel(req, data.Status, data.Answers)

	// Push response to mobile (for relay persistence)
	if broker := b.currentTunnelBroker(); broker != nil && data.ID != "" {
		answers := make([]tunnel.AskUserAnswer, len(response.Answers))
		for i, answer := range response.Answers {
			answers[i] = tunnel.AskUserAnswer{
				QuestionID:   answer.ID,
				ChoiceIDs:    append([]string(nil), answer.SelectedChoiceIDs...),
				FreeformText: answer.FreeformText,
			}
		}
		broker.PushAskUserResponse(data.ID, response.Status, answers)
		broker.PushStatus(tunnel.StatusBusy, "")
	}

	select {
	case ch <- response:
	default:
	}
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

// ─── Tunnel AskUser conversion helpers ─────────────────────────────

func buildWailsTunnelAskUserQuestions(req tool.AskUserRequest) []tunnel.AskUserQuestion {
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

func buildWailsAskUserResponseFromTunnel(req tool.AskUserRequest, status string, answers []tunnel.AskUserAnswer) tool.AskUserResponse {
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
		answer := buildWailsAskUserAnswer(question, raw.ChoiceIDs, raw.FreeformText)
		if answer.Answered {
			out.AnsweredCount++
		}
		out.Answers = append(out.Answers, answer)
	}
	return out
}

func buildWailsAskUserAnswer(question tool.AskUserQuestion, selectedIDs []string, freeform string) tool.AskUserAnswer {
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

// ─── System Prompt ───────────────────────────────────────────────────

// buildWailsSystemPrompt builds the system prompt for the agent.
// Mirrors Fyne buildSystemPrompt exactly.
// buildWailsSystemPrompt builds the system prompt for the agent.
// Mirrors Fyne buildSystemPrompt exactly — includes auto-memory content.
func buildWailsSystemPrompt(workingDir string, mode permission.PermissionMode, globalAutoMem, projectAutoMem *memory.AutoMemory) string {
	hostname, _ := os.Hostname()
	cwd := workingDir
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	prompt := fmt.Sprintf(`You are ggcode, an AI coding assistant running as a desktop application.

## Environment
- OS: %s
- Working directory: %s

## Instructions
- Be precise, concise, and proactive.
- Prefer small, reversible changes over broad rewrites.
- Read before you edit, and inspect results before claiming success.
`, hostname, cwd)

	// Mode-specific instruction: overrides stale autopilot messages in context
	prompt += "\n## Current Permission Mode\n"
	switch mode {
	case permission.AutopilotMode:
		prompt += "Autopilot mode is active. You may proceed autonomously without waiting for user confirmation. When you need user input, use the ask_user tool.\n"
	case permission.BypassMode:
		prompt += "Bypass mode is active. Most tool calls are auto-approved. Only critical operations require approval.\n"
	case permission.PlanMode:
		prompt += "Plan mode is active. You are in read-only exploration mode. Do NOT write files or execute commands unless explicitly approved.\n"
	case permission.AutoMode:
		prompt += "Auto mode is active. Safe operations are auto-approved, dangerous ones require user approval.\n"
	default: // SupervisedMode
		prompt += "Supervised mode is active. Tool calls require explicit user approval.\n"
	}

	if projectAutoMem != nil {
		if projectContent, _, _ := projectAutoMem.LoadAll(); projectContent != "" {
			prompt += "\n\n## Auto Memory (Project)\n" + projectContent
		}
	}
	if globalAutoMem != nil {
		if globalContent, _, _ := globalAutoMem.LoadAll(); globalContent != "" {
			prompt += "\n\n## Auto Memory (Global)\n" + globalContent
		}
	}
	return prompt
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
	ses.AddUsageForEndpoint(ses.Vendor, ses.Endpoint, usage)
	ses.UpdatedAt = time.Now()
	total := ses.UsageForEndpoint(ses.Vendor, ses.Endpoint)
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
		raw, _ := json.Marshal(map[string]interface{}{
			"input_tokens":  total.InputTokens,
			"output_tokens": total.OutputTokens,
			"cache_read":    total.CacheRead,
			"cache_write":   total.CacheWrite,
		})
		b.OnStreamEvent("usage_update", raw)
	}
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
	if sa == nil {
		return
	}
	broker := b.currentTunnelBroker()
	if broker == nil {
		return
	}

	pushSpawn := func() {
		if b.markTunnelSubagentSpawned(sa.ID) {
			broker.PushSubagentSpawn(sa.ID, sa.Name, sa.Task, "", "")
		}
	}

	switch sa.Status {
	case subagent.StatusRunning:
		pushSpawn()
		broker.PushSubagentStatus(sa.ID, tunnel.StatusRunning, sa.CurrentTool)

	case subagent.StatusCompleted:
		pushSpawn()
		broker.PushReasoningDone(tunnelSubagentReasoningID(sa.ID))
		if sa.Result != "" {
			msgID := tunnelSubagentTextID(sa.ID)
			broker.PushSubagentText(sa.ID, msgID, sa.Result, true)
		}
		broker.PushSubagentComplete(sa.ID, sa.Name, sa.Result, true)

	case subagent.StatusFailed:
		pushSpawn()
		broker.PushReasoningDone(tunnelSubagentReasoningID(sa.ID))
		errMsg := ""
		if sa.Error != nil {
			errMsg = sa.Error.Error()
		}
		broker.PushSubagentComplete(sa.ID, sa.Name, errMsg, false)

	case subagent.StatusCancelled:
		pushSpawn()
		broker.PushReasoningDone(tunnelSubagentReasoningID(sa.ID))
		broker.PushSubagentComplete(sa.ID, sa.Name, "cancelled", false)
	}
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
	b.mu.Lock()
	b.pendingMsgs = append(b.pendingMsgs, pendingMessage{Text: msg})
	b.mu.Unlock()
}

// QueueHiddenMessage stores a hidden message (mirrors Fyne).
func (b *ChatBridge) QueueHiddenMessage(msg string) {
	b.mu.Lock()
	b.pendingMsgs = append(b.pendingMsgs, pendingMessage{Text: msg, Hidden: true})
	b.mu.Unlock()
}

func (b *ChatBridge) drainPending() (pendingMessage, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.pendingMsgs) == 0 {
		return pendingMessage{}, false
	}
	msg := b.pendingMsgs[0]
	b.pendingMsgs = b.pendingMsgs[1:]
	return msg, true
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
		b.pendingMsgs = append(b.pendingMsgs, pendingMessage{Text: text, Hidden: true})
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

	// Update config with the new model selection (mirrors Fyne line 2934-2937)
	if err := b.cfg.SetActiveSelection(b.cfg.Vendor, b.cfg.Endpoint, model); err != nil {
		return fmt.Errorf("set active selection: %w", err)
	}
	_ = b.cfg.Save()

	// Re-resolve endpoint (picks up new context window, etc.)
	resolved, err := b.cfg.ResolveActiveEndpoint()
	if err != nil {
		return fmt.Errorf("resolve endpoint: %w", err)
	}
	b.mu.Lock()
	b.resolved = resolved
	b.mu.Unlock()

	prov, err := provider.NewProvider(resolved)
	if err != nil {
		return fmt.Errorf("create provider: %w", err)
	}

	b.mu.Lock()
	a := b.agent
	b.mu.Unlock()

	if a != nil {
		a.SetProvider(prov)
		if resolved.ContextWindow > 0 {
			a.ContextManager().SetContextWindow(resolved.ContextWindow)
		}
		if resolved.MaxTokens > 0 {
			a.ContextManager().SetOutputReserve(resolved.MaxTokens)
		}
	}
	return nil
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
	b.mu.Lock()
	mode := b.permissionMode
	b.mu.Unlock()
	newPrompt := buildWailsSystemPrompt(b.workingDir, mode, autoMem, projectAutoMem)
	b.mu.Lock()
	if b.agent != nil {
		b.agent.UpdateSystemPrompt(newPrompt)
	}
	b.mu.Unlock()
}

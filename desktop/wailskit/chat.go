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

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/cron"
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

// ChatBridge manages the full agent chat loop for the Wails frontend,
// mirroring the Fyne desktop's AgentBridge tool registration and session management.
type ChatBridge struct {
	cfg          *config.Config
	agent        *agent.Agent
	registry     *tool.Registry
	workingDir   string
	sessionStore session.Store
	currentSes   *session.Session

	mu     sync.Mutex
	cancel context.CancelFunc

	// Subsystems
	cronScheduler *cron.Scheduler
	subAgentMgr   *subagent.Manager
	swarmMgr      *swarm.Manager

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
	tunnelBroker   *tunnel.Broker
	tunnelMsgID    string
	tunnelReasonID string

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
	return &ChatBridge{
		cfg:        cfg,
		workingDir: wd,
	}, nil
}

// SendMessage sends a user message and streams events to the frontend.
func (b *ChatBridge) SendMessage(userMsg string) error {
	b.mu.Lock()
	if b.cancel != nil {
		b.mu.Unlock()
		return fmt.Errorf("agent is already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel
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
func (b *ChatBridge) Cancel() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cancel != nil {
		b.cancel()
		b.cancel = nil
	}
}

// ClearCurrentSession resets the current session so next chat creates a fresh one.
func (b *ChatBridge) ClearCurrentSession() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.currentSes = nil
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

// initAgent sets up provider, tools, and agent — full parity with Fyne bridge.
func (b *ChatBridge) initAgent(ctx context.Context) error {
	// Resolve provider
	resolved, err := b.cfg.ResolveActiveEndpoint()
	if err != nil {
		return fmt.Errorf("resolve endpoint: %w", err)
	}

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
	_ = b.registry.Register(tool.NewSaveMemoryTool(autoMem, projectAutoMem))

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

	// Forward swarm events to frontend
	b.swarmMgr.SetOnUpdate(func(ev swarm.Event) {
		if b.OnStreamEvent == nil {
			return
		}
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
	})

	// Create agent
	a := agent.NewAgent(p, b.registry, "", 100)
	a.SetPermissionPolicy(policy)
	b.agent = a
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
		// Push to mobile via tunnel
		if broker := b.currentTunnelBroker(); broker != nil {
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
		// Push to mobile via tunnel
		if broker := b.currentTunnelBroker(); broker != nil {
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
		// Push idle status to mobile
		if broker := b.currentTunnelBroker(); broker != nil {
			broker.PushTextDone(b.ensureTunnelMsgID(broker))
			broker.PushStatus(tunnel.StatusIdle, "")
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
		}
	}
	return map[string]interface{}{
		"vendor":        b.cfg.Vendor,
		"model":         b.cfg.Model,
		"contextWindow": resolved.ContextWindow,
	}
}

// ─── Tunnel Broker Integration ──────────────────────────────────────

// AttachTunnelBroker connects the broker for outbound event push to mobile.
func (b *ChatBridge) AttachTunnelBroker(broker *tunnel.Broker) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tunnelBroker = broker
}

// DetachTunnelBroker disconnects the broker.
func (b *ChatBridge) DetachTunnelBroker() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tunnelBroker = nil
}

func (b *ChatBridge) currentTunnelBroker() *tunnel.Broker {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.tunnelBroker
}

// CurrentTunnelStatus returns the current agent status for tunnel snapshots.
func (b *ChatBridge) CurrentTunnelStatus() tunnel.StatusData {
	if b.cancel != nil {
		return tunnel.StatusData{Status: tunnel.StatusBusy}
	}
	return tunnel.StatusData{Status: tunnel.StatusIdle}
}

// ensureTunnelMsgID returns a stable message ID for the current agent turn.
func (b *ChatBridge) ensureTunnelMsgID(broker *tunnel.Broker) string {
	if b.tunnelMsgID == "" {
		b.tunnelMsgID = broker.NextMessageID()
	}
	return b.tunnelMsgID
}

func (b *ChatBridge) tunnelReasoningMsgID(broker *tunnel.Broker) string {
	if b.tunnelReasonID == "" {
		b.tunnelReasonID = broker.NextMessageID()
	}
	return b.tunnelReasonID
}

// resetTunnelRoundState resets per-turn tunnel state.
func (b *ChatBridge) resetTunnelRoundState() {
	b.tunnelMsgID = ""
	b.tunnelReasonID = ""
}

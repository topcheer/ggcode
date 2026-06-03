// Package wailskit provides a public facade for the Wails desktop app.
package wailskit

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/cron"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/swarm"
	"github.com/topcheer/ggcode/internal/tool"
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

	err := b.agent.RunStream(ctx, userMsg, func(ev provider.StreamEvent) {
		if b.OnStreamEvent == nil {
			return
		}
		b.emit(ev)
	})
	// Save session after each message (mirrors Fyne bridge)
	b.saveSession()
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

	case provider.StreamEventToolCallChunk:
		eventType = "tool_call_chunk"
		data = map[string]interface{}{
			"id":   ev.Tool.ID,
			"name": ev.Tool.Name,
		}

	case provider.StreamEventToolCallDone:
		eventType = "tool_call_done"
		data = map[string]interface{}{
			"id":   ev.Tool.ID,
			"name": ev.Tool.Name,
		}

	case provider.StreamEventToolResult:
		eventType = "tool_result"
		resultPreview := ev.Result
		if len(resultPreview) > 500 {
			resultPreview = resultPreview[:500] + "..."
		}
		data = map[string]interface{}{
			"name":    ev.Tool.Name,
			"result":  resultPreview,
			"isError": ev.IsError,
		}

	case provider.StreamEventDone:
		eventType = "done"
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

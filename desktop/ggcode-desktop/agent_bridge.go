package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// AgentBridge wraps the agent loop. All UI updates go through UIState
// (binding-based) so no widget is touched from a background goroutine.
type AgentBridge struct {
	cfg      *config.Config
	prov     provider.Provider
	resolved *config.ResolvedEndpoint
	agent    *agent.Agent
	ui       *UIState

	mu      sync.Mutex
	cancel  context.CancelFunc
	working bool

	registry   *tool.Registry
	workingDir string
}

func NewAgentBridge(cfg *config.Config, prov provider.Provider, resolved *config.ResolvedEndpoint, workingDir string, ui *UIState) *AgentBridge {
	return &AgentBridge{
		cfg:        cfg,
		prov:       prov,
		resolved:   resolved,
		ui:         ui,
		workingDir: workingDir,
	}
}

func (b *AgentBridge) setupAgent() error {
	logf("setupAgent: start")
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.agent != nil {
		logf("setupAgent: agent already exists")
		return nil
	}

	b.registry = tool.NewRegistry()
	logf("setupAgent: registering builtin tools...")
	if err := tool.RegisterBuiltinTools(b.registry, nil, b.workingDir); err != nil {
		logf("setupAgent: RegisterBuiltinTools error: %v", err)
		return fmt.Errorf("register builtin tools: %w", err)
	}
	logf("setupAgent: builtin tools registered")

	mergedServers, _ := mcp.MergeStartupServers(b.workingDir, b.cfg.MCPServers)
	mcpMgr := plugin.NewMCPManager(mergedServers, b.registry)
	_ = b.registry.Register(tool.ListMCPCapabilitiesTool{Runtime: mcpMgr})
	_ = b.registry.Register(tool.GetMCPPromptTool{Runtime: mcpMgr})
	_ = b.registry.Register(tool.ReadMCPResourceTool{Runtime: mcpMgr})
	logf("setupAgent: MCP tools registered")

	pluginMgr := plugin.NewManager()
	pluginMgr.LoadAll(b.cfg.Plugins)
	_ = pluginMgr.RegisterTools(b.registry)
	logf("setupAgent: plugins loaded")

	autoMem := memory.NewAutoMemory()
	_ = b.registry.Register(tool.NewSaveMemoryTool(autoMem, nil))

	systemPrompt := buildSystemPrompt(b.workingDir)
	maxIter := b.cfg.MaxIterations
	if maxIter == 0 {
		maxIter = 200
	}
	b.agent = agent.NewAgent(b.prov, b.registry, systemPrompt, maxIter)
	logf("setupAgent: agent created")
	if b.resolved.ContextWindow > 0 {
		b.agent.ContextManager().SetContextWindow(b.resolved.ContextWindow)
	}
	if b.resolved.MaxTokens > 0 {
		b.agent.ContextManager().SetOutputReserve(b.resolved.MaxTokens)
	}
	logf("setupAgent: done, contextWindow=%d", b.resolved.ContextWindow)
	return nil
}

func (b *AgentBridge) Send(userMsg string) error {
	logf("Send: start, msgLen=%d", len(userMsg))
	if err := b.setupAgent(); err != nil {
		logf("Send: setupAgent error: %v", err)
		return err
	}

	b.mu.Lock()
	b.working = true
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel
	b.mu.Unlock()
	logf("Send: goroutine starting")

	go func() {
		defer func() {
			logf("Send: goroutine done")
			cancel()
			b.mu.Lock()
			b.working = false
			b.cancel = nil
			b.mu.Unlock()
		}()

		onEvent := func(ev provider.StreamEvent) {
			defer logPanic("agent event handler")

			switch ev.Type {
			case provider.StreamEventText:
				b.ui.AppendAssistantText(ev.Text)

			case provider.StreamEventToolCallDone:
				name := ev.Tool.Name
				if name == "" {
					name = "tool"
				}
				args := string(ev.Tool.Arguments)
				if len(args) > 100 {
					args = args[:100] + "..."
				}
				b.ui.AppendChat(ChatMessage{
					Role:     "tool",
					ToolName: name,
					ToolArgs: args,
					Content:  fmt.Sprintf("Running %s...", name),
					Time:     time.Now(),
				})

			case provider.StreamEventToolResult:
				name := ev.Tool.Name
				if name == "" {
					name = "tool"
				}
				content := ev.Result
				if len(content) > 200 {
					content = content[:200] + "..."
				}
				b.ui.UpdateLastToolResult(name, content)

			case provider.StreamEventSystem:
				b.ui.AppendChat(ChatMessage{
					Role:    "system",
					Content: ev.Text,
					Time:    time.Now(),
				})

			case provider.StreamEventReasoning:
				if ev.Text != "" {
					b.ui.AppendChat(ChatMessage{
						Role:    "reasoning",
						Content: ev.Text,
						Time:    time.Now(),
					})
				}
			}
		}

		err := b.agent.RunStream(ctx, userMsg, onEvent)
		b.ui.FinalizeStreaming()
		if err != nil {
			b.ui.AppendChat(ChatMessage{
				Role:    "error",
				Content: err.Error(),
				Time:    time.Now(),
			})
		}
	}()

	return nil
}

func (b *AgentBridge) Cancel() {
	b.mu.Lock()
	if b.cancel != nil {
		b.cancel()
	}
	b.mu.Unlock()
}

func (b *AgentBridge) Close() { b.Cancel() }

func (b *AgentBridge) IsWorking() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.working
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

func (b *AgentBridge) Resolved() *config.ResolvedEndpoint {
	return b.resolved
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

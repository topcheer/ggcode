package main

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// AgentEvent is a UI-friendly representation of a streaming event.
type AgentEvent struct {
	Type     string // "text", "tool_call", "tool_result", "system", "done", "error"
	Content  string
	ToolName string
	ToolArgs string // short summary of tool input
}

// AgentBridge wraps the agent loop and delivers events to the UI via a channel.
type AgentBridge struct {
	cfg      *config.Config
	prov     provider.Provider
	resolved *config.ResolvedEndpoint
	agent    *agent.Agent

	eventCh chan AgentEvent
	mu      sync.Mutex
	cancel  context.CancelFunc
	working bool

	// Registry for tool management.
	registry   *tool.Registry
	workingDir string
}

func NewAgentBridge(cfg *config.Config, prov provider.Provider, resolved *config.ResolvedEndpoint, workingDir string) *AgentBridge {
	return &AgentBridge{
		cfg:        cfg,
		prov:       prov,
		resolved:   resolved,
		eventCh:    make(chan AgentEvent, 512),
		workingDir: workingDir,
	}
}

// Events returns a read-only channel for consuming agent events.
func (b *AgentBridge) Events() <-chan AgentEvent {
	return b.eventCh
}

// IsWorking returns whether the agent is currently processing.
func (b *AgentBridge) IsWorking() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.working
}

// setupAgent lazily initializes the agent and registry.
func (b *AgentBridge) setupAgent() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.agent != nil {
		return nil
	}

	// Create registry with same tools as TUI.
	b.registry = tool.NewRegistry()
	if err := tool.RegisterBuiltinTools(b.registry, nil, b.workingDir); err != nil {
		return fmt.Errorf("register builtin tools: %w", err)
	}

	// MCP tools.
	mergedServers, _ := mcp.MergeStartupServers(b.workingDir, b.cfg.MCPServers)
	mcpMgr := plugin.NewMCPManager(mergedServers, b.registry)
	_ = b.registry.Register(tool.ListMCPCapabilitiesTool{Runtime: mcpMgr})
	_ = b.registry.Register(tool.GetMCPPromptTool{Runtime: mcpMgr})
	_ = b.registry.Register(tool.ReadMCPResourceTool{Runtime: mcpMgr})

	// Plugins.
	pluginMgr := plugin.NewManager()
	pluginMgr.LoadAll(b.cfg.Plugins)
	_ = pluginMgr.RegisterTools(b.registry)

	// Auto memory.
	autoMem := memory.NewAutoMemory()
	_ = b.registry.Register(tool.NewSaveMemoryTool(autoMem, nil))

	// Build system prompt.
	systemPrompt := buildSystemPrompt(b.workingDir)

	maxIter := b.cfg.MaxIterations
	if maxIter == 0 {
		maxIter = 200 // reasonable default for desktop
	}
	b.agent = agent.NewAgent(b.prov, b.registry, systemPrompt, maxIter)

	// Set context window and output reserve from resolved config.
	if b.resolved.ContextWindow > 0 {
		b.agent.ContextManager().SetContextWindow(b.resolved.ContextWindow)
	}
	if b.resolved.MaxTokens > 0 {
		b.agent.ContextManager().SetOutputReserve(b.resolved.MaxTokens)
	}

	return nil
}

// Send submits a user message and runs the agent loop.
func (b *AgentBridge) Send(userMsg string) error {
	if err := b.setupAgent(); err != nil {
		return err
	}

	b.mu.Lock()
	b.working = true
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel
	b.mu.Unlock()

	go func() {
		defer func() {
			cancel()
			b.mu.Lock()
			b.working = false
			b.cancel = nil
			b.mu.Unlock()
		}()

		onEvent := func(ev provider.StreamEvent) {
			switch ev.Type {
			case provider.StreamEventText:
				b.eventCh <- AgentEvent{Type: "text", Content: ev.Text}
			case provider.StreamEventToolCallDone:
				name := ev.Tool.Name
				if name == "" {
					name = "tool"
				}
				b.eventCh <- AgentEvent{
					Type:     "tool_call",
					ToolName: name,
					ToolArgs: string(ev.Tool.Arguments),
				}
			case provider.StreamEventToolResult:
				name := ev.Tool.Name
				if name == "" {
					name = "tool"
				}
				content := ev.Result
				if len(content) > 200 {
					content = content[:200] + "..."
				}
				b.eventCh <- AgentEvent{
					Type:     "tool_result",
					ToolName: name,
					Content:  content,
				}
			case provider.StreamEventSystem:
				b.eventCh <- AgentEvent{Type: "system", Content: ev.Text}
			case provider.StreamEventReasoning:
				if ev.Text != "" {
					b.eventCh <- AgentEvent{Type: "reasoning", Content: ev.Text}
				}
			}
		}

		err := b.agent.RunStream(ctx, userMsg, onEvent)
		if err != nil {
			b.eventCh <- AgentEvent{Type: "error", Content: err.Error()}
		}
		b.eventCh <- AgentEvent{Type: "done"}
	}()

	return nil
}

// Cancel stops the current agent run.
func (b *AgentBridge) Cancel() {
	b.mu.Lock()
	if b.cancel != nil {
		b.cancel()
	}
	b.mu.Unlock()
}

// Close cleans up.
func (b *AgentBridge) Close() {
	b.Cancel()
}

// ContextWindow returns the current context window size.
func (b *AgentBridge) ContextWindow() int {
	if b.agent == nil {
		return b.resolved.ContextWindow
	}
	return b.agent.ContextManager().ContextWindow()
}

// TokenCount returns the current token usage.
func (b *AgentBridge) TokenCount() int {
	if b.agent == nil {
		return 0
	}
	return b.agent.ContextManager().TokenCount()
}

// Resolved returns the current resolved endpoint.
func (b *AgentBridge) Resolved() *config.ResolvedEndpoint {
	return b.resolved
}

// buildSystemPrompt creates a system prompt similar to the TUI.
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
- Shell: /bin/bash

## Instructions
- Be precise, concise, and proactive.
- Prefer small, reversible changes over broad rewrites.
- Read before you edit, and inspect results before claiming success.
- Follow user instructions strictly and completely.
`, hostname, cwd)
}

package main

import (
	"context"
	"encoding/json"
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

// AgentBridge wraps the agent loop. All UI updates go through UIState.
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
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.agent != nil {
		return nil
	}

	b.registry = tool.NewRegistry()
	if err := tool.RegisterBuiltinTools(b.registry, nil, b.workingDir); err != nil {
		return fmt.Errorf("register builtin tools: %w", err)
	}

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

	systemPrompt := buildSystemPrompt(b.workingDir)
	maxIter := b.cfg.MaxIterations
	if maxIter == 0 {
		maxIter = 200
	}
	b.agent = agent.NewAgent(b.prov, b.registry, systemPrompt, maxIter)
	if b.resolved.ContextWindow > 0 {
		b.agent.ContextManager().SetContextWindow(b.resolved.ContextWindow)
	}
	if b.resolved.MaxTokens > 0 {
		b.agent.ContextManager().SetOutputReserve(b.resolved.MaxTokens)
	}
	return nil
}

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
			b.ui.FinalizeStreaming()
			b.mu.Lock()
			b.working = false
			b.cancel = nil
			b.mu.Unlock()
		}()

		onEvent := func(ev provider.StreamEvent) {
			defer logPanic("agent event handler")

			switch ev.Type {
			case provider.StreamEventText:
				// Append text chunk to the current assistant message.
				b.ui.AppendAssistantText(ev.Text)

			case provider.StreamEventToolCallDone:
				// Finalize any in-progress assistant text before showing tool.
				b.ui.FinalizeStreaming()

				name := ev.Tool.Name
				if name == "" {
					name = "tool"
				}
				// Try to extract a human-readable description from args.
				description := toolDescription(name, string(ev.Tool.Arguments))
				args := toolArgSummary(name, string(ev.Tool.Arguments))

				b.ui.AppendChat(ChatMessage{
					Role:     "tool",
					ToolName: name,
					ToolDesc: description,
					ToolArgs: args,
					Content:  "",
					Time:     time.Now(),
				})

			case provider.StreamEventToolResult:
				name := ev.Tool.Name
				if name == "" {
					name = "tool"
				}
				content := ev.Result
				if len(content) > 2000 {
					content = content[:2000] + "\n...(truncated)"
				}
				b.ui.UpdateLastToolResult(name, content)

			case provider.StreamEventSystem:
				b.ui.FinalizeStreaming()
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

// toolDescription extracts a human-readable description from tool arguments.
// For tools like read_file/edit_file, the "description" field is the user-visible label.
func toolDescription(toolName, rawArgs string) string {
	var args map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return toolName
	}
	if desc, ok := args["description"]; ok {
		var s string
		if json.Unmarshal(desc, &s) == nil && s != "" {
			return s
		}
	}
	return toolName
}

// toolArgSummary creates a short summary of tool arguments for display.
func toolArgSummary(toolName, rawArgs string) string {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return ""
	}

	// Show key argument depending on tool type.
	switch toolName {
	case "read_file", "write_file", "edit_file":
		if p, ok := args["path"].(string); ok {
			return p
		}
	case "run_command":
		if c, ok := args["command"].(string); ok {
			if len(c) > 80 {
				return c[:80] + "..."
			}
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
	}
	// Generic: show first string arg.
	for _, v := range args {
		if s, ok := v.(string); ok && len(s) > 0 {
			if len(s) > 60 {
				return s[:60] + "..."
			}
			return s
		}
	}
	return ""
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

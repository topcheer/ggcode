package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/subagent"
)

// UseNamedAgentTool implements the use_namedagent tool.
// It loads a persisted template and spawns a one-shot execution.
type UseNamedAgentTool struct {
	Store               *subagent.TemplateStore
	Manager             *subagent.Manager
	Provider            provider.Provider
	Tools               *Registry
	AgentFactory        func(provider.Provider, interface{}, string, int) subagent.AgentRunner
	WorkingDir          string
	OnUsage             func(provider.TokenUsage)
	SystemPromptBuilder func(task, agentType string) string
}

func (t UseNamedAgentTool) Name() string { return "use_namedagent" }

func (t UseNamedAgentTool) Description() string {
	return `Invoke a named subagent template to execute a task. The subagent runs with its predefined system prompt, tools, and model.

Returns an agent_id that can be used with wait_agent and list_agents to monitor progress and retrieve results.
Each invocation is a fresh session — no conversation history is persisted between invocations.`
}

func (t UseNamedAgentTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"name": {
				"type": "string",
				"description": "Name of the named subagent template to use"
			},
			"task": {
				"type": "string",
				"description": "The specific task for this invocation"
			},
			"context": {
				"type": "string",
				"description": "Optional additional context for the task"
			}
		},
		"required": ["name", "task"]
	}`)
}

func (t UseNamedAgentTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Store == nil || t.Manager == nil {
		return Result{IsError: true, Content: "use_namedagent: not properly configured"}, nil
	}
	var args struct {
		Name    string `json:"name"`
		Task    string `json:"task"`
		Context string `json:"context"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if strings.TrimSpace(args.Name) == "" {
		return Result{IsError: true, Content: "name is required"}, nil
	}
	if strings.TrimSpace(args.Task) == "" {
		return Result{IsError: true, Content: "task is required"}, nil
	}

	// Load the template
	tmpl, err := t.Store.Load(args.Name)
	if err != nil {
		// Suggest available templates
		available, _ := t.Store.List()
		if len(available) > 0 {
			names := make([]string, len(available))
			for i, a := range available {
				names[i] = a.Name
			}
			return Result{IsError: true, Content: fmt.Sprintf("named subagent '%s' not found. Available: %s", args.Name, strings.Join(names, ", "))}, nil
		}
		return Result{IsError: true, Content: fmt.Sprintf("named subagent '%s' not found. Use create_namedagent to create one.", args.Name)}, nil
	}

	// Build the task string
	task := args.Task
	if args.Context != "" {
		task = args.Context + "\n\n" + args.Task
	}
	displayTask := args.Task

	// Spawn into the manager (same as spawn_agent)
	// Combine template tools + blocked_tools for the allowedTools parameter
	allowedTools := tmpl.Tools
	if len(tmpl.BlockedTools) > 0 {
		// If only blocked_tools specified (no allowlist), start from all then remove
		if len(allowedTools) == 0 {
			if t.Tools != nil {
				for _, name := range t.Tools.ToolNames() {
					if !sliceContains(tmpl.BlockedTools, name) {
						allowedTools = append(allowedTools, name)
					}
				}
			}
		} else {
			// Both allowlist and denylist: filter allowlist
			filtered := allowedTools[:0]
			for _, name := range allowedTools {
				if !sliceContains(tmpl.BlockedTools, name) {
					filtered = append(filtered, name)
				}
			}
			allowedTools = filtered
		}
	}

	id := t.Manager.Spawn(tmpl.Name, task, displayTask, allowedTools, ctx)

	// Build tool info list
	var allToolInfo []subagent.ToolInfo
	if t.Tools != nil {
		for _, ti := range t.Tools.List() {
			allToolInfo = append(allToolInfo, ti)
		}
	}

	// Determine provider: clone with model override if set
	runProv := provider.CloneProviderWithModel(t.Provider, tmpl.Model)

	// Capture for goroutine closure
	tools := t.Tools
	runCtx := t.Manager.RootContext()

	// Use template system prompt as agentType so SystemPromptBuilder can use it
	// But actually we want to fully override the system prompt — so we pass
	// the template's system_prompt directly via a custom builder
	customBuilder := func(task, agentType string) string {
		// Prepend the template's system prompt to the standard builder output
		standard := ""
		if t.SystemPromptBuilder != nil {
			standard = t.SystemPromptBuilder(task, agentType)
		}
		if tmpl.SystemPrompt != "" {
			if standard != "" {
				return tmpl.SystemPrompt + "\n\n" + standard
			}
			return tmpl.SystemPrompt
		}
		return standard
	}

	safego.Go("tool.useNamedAgent", func() {
		subagent.Run(runCtx, subagent.RunnerConfig{
			Provider:            runProv,
			AllTools:            allToolInfo,
			Task:                task,
			AllowedTools:        allowedTools,
			Manager:             t.Manager,
			SubAgentID:          id,
			AgentFactory:        t.AgentFactory,
			Model:               tmpl.Model,
			WorkingDir:          t.WorkingDir,
			OnUsage:             t.OnUsage,
			SystemPromptBuilder: customBuilder,
			BuildToolSet: func(allowedTools []string, _ []subagent.ToolInfo) interface{} {
				cloned := tools.Clone()
				// Always remove sub-agent blocked tools
				for _, name := range subAgentBlockedTools {
					cloned.Unregister(name)
				}
				// Also block named agent management tools
				cloned.Unregister("create_namedagent")
				cloned.Unregister("use_namedagent")
				cloned.Unregister("list_namedagent")
				if len(allowedTools) > 0 {
					all := cloned.ToolNames()
					for _, name := range all {
						if !sliceContains(allowedTools, name) {
							cloned.Unregister(name)
						}
					}
				}
				return cloned
			},
		})
	})

	return Result{Content: fmt.Sprintf("Named subagent '%s' started with ID: %s\nUse wait_agent or list_agents to monitor progress.", tmpl.Name, id)}, nil
}

// Clone returns an independent copy for use by a different agent.
func (t UseNamedAgentTool) Clone() Tool {
	return UseNamedAgentTool{
		Store:               t.Store,
		Manager:             t.Manager,
		Provider:            t.Provider,
		Tools:               t.Tools,
		AgentFactory:        t.AgentFactory,
		WorkingDir:          t.WorkingDir,
		OnUsage:             t.OnUsage,
		SystemPromptBuilder: t.SystemPromptBuilder,
	}
}

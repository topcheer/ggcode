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

// SpawnAgentTool implements the spawn_agent tool.
type SpawnAgentTool struct {
	Manager      *subagent.Manager
	Provider     provider.Provider
	Tools        *Registry
	AgentFactory subagent.AgentFactory
	WorkingDir   string // working directory to propagate to sub-agent
	OnUsage      func(provider.TokenUsage)
}

func (t SpawnAgentTool) Name() string { return "spawn_agent" }

func (t SpawnAgentTool) Description() string {
	return "Spawn a one-shot sub-agent run to work on an independent task. Put the full task and needed context in the initial request; do not assume the run will accept later work via send_message. Returns an agent_id. Use wait_agent or list_agents to poll status and retrieve the eventual result."
}

func (t SpawnAgentTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"task": {
			"type": "string",
			"description": "The complete task description for the one-shot sub-agent run. Include all context the sub-agent will need."
		},
		"tools": {
			"type": "array",
			"items": {
				"type": "string"
			},
			"description": "Optional list of tool names the sub-agent can use (defaults to all parent tools except sub-agent tools)"
		},
		"context": {
			"type": "string",
			"description": "Optional additional context prepended to the sub-agent's initial task. Prefer including context here rather than trying to send follow-up work later."
		},
		"model": {
			"type": "string",
			"enum": [
				"sonnet",
				"opus",
				"haiku"
			],
			"description": "Optional model override for the sub-agent"
		},
		"subagent_type": {
			"type": "string",
			"description": "Optional type of specialized agent (e.g., 'Explore', 'Plan')"
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Searching for TODO patterns', '检查构建配置'). You MUST always provide this field."
		}
	},
	"required": [
		"task",
		"description"
	]
}`)
}

func (t SpawnAgentTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Manager == nil {
		return Result{IsError: true, Content: "spawn_agent: sub-agent manager not available"}, nil
	}
	var args struct {
		Task         string   `json:"task"`
		Tools        []string `json:"tools"`
		Context      string   `json:"context"`
		Model        string   `json:"model"`
		SubagentType string   `json:"subagent_type"`
		Description  string   `json:"description"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if args.Task == "" {
		return Result{IsError: true, Content: "task is required"}, nil
	}

	name := strings.TrimSpace(args.Description)
	if name == "" {
		name = "sub-agent"
	}

	displayTask := args.Task
	if args.Context != "" {
		args.Task = args.Context + "\n\n" + args.Task
	}

	id := t.Manager.Spawn(name, args.Task, displayTask, args.Tools, ctx)

	// Build tool info list for sub-agent
	var allToolInfo []subagent.ToolInfo
	if t.Tools != nil {
		for _, ti := range t.Tools.List() {
			allToolInfo = append(allToolInfo, ti)
		}
	}

	// Capture for goroutine closure
	tools := t.Tools

	// Use the manager's lifecycle ctx, NOT the per-tool-call ctx, otherwise
	// the moment the parent agent's current turn ends (defer cancel() on the
	// submit ctx) every spawned sub-agent is cancelled mid-stream and any
	// half-applied tool side effect is left in place. See locks.md S6.
	runCtx := t.Manager.RootContext()

	// Capture model and subagent_type for the runner config
	model := args.Model
	subagentType := args.SubagentType

	// Launch the sub-agent in a goroutine
	safego.Go("tool.spawnAgent.subagent", func() {
		subagent.Run(runCtx, subagent.RunnerConfig{
			Provider:     t.Provider,
			AllTools:     allToolInfo,
			Task:         args.Task,
			AllowedTools: args.Tools,
			Manager:      t.Manager,
			SubAgentID:   id,
			AgentFactory: t.AgentFactory,
			Model:        model,
			AgentType:    subagentType,
			WorkingDir:   t.WorkingDir,
			OnUsage:      t.OnUsage,
			BuildToolSet: func(allowedTools []string, _ []subagent.ToolInfo) interface{} {
				// Clone the registry so each sub-agent gets its own tool
				// instances with independent WorkingDir fields. This prevents
				// data races when multiple sub-agents run concurrently in
				// different worktrees.
				cloned := tools.Clone()
				if len(allowedTools) == 0 {
					// Remove agent lifecycle tools from sub-agent's set
					cloned.Unregister("spawn_agent")
					cloned.Unregister("wait_agent")
					cloned.Unregister("list_agents")
				} else {
					// Keep only allowed tools
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

	return Result{Content: fmt.Sprintf("Sub-agent spawned with ID: %s\nUse wait_agent or list_agents to monitor progress and retrieve the result.", id)}, nil
}

// Clone returns an independent copy of SpawnAgentTool for use by a different agent.
// Manager, Provider, AgentFactory, and Tools are intentionally shared across agents
// (they coordinate sub-agent lifecycle). Only WorkingDir is agent-specific.
func (t SpawnAgentTool) Clone() Tool {
	return SpawnAgentTool{
		Manager:      t.Manager,
		Provider:     t.Provider,
		Tools:        t.Tools,
		AgentFactory: t.AgentFactory,
		WorkingDir:   t.WorkingDir,
		OnUsage:      t.OnUsage,
	}
}

// contains checks if a string slice contains a given string.
func sliceContains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

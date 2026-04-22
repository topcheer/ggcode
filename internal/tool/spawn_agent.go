package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/subagent"
)

// SpawnAgentTool implements the spawn_agent tool.
type SpawnAgentTool struct {
	Manager      *subagent.Manager
	Provider     provider.Provider
	Tools        *Registry
	AgentFactory subagent.AgentFactory
}

func (t SpawnAgentTool) Name() string { return "spawn_agent" }

func (t SpawnAgentTool) Description() string {
	return "Spawn a sub-agent to work on a task independently. Returns an agent_id. Use wait_agent or list_agents to poll status and retrieve the eventual result."
}

func (t SpawnAgentTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"task": {
				"type": "string",
				"description": "The task description for the sub-agent"
			},
			"tools": {
				"type": "array",
				"items": {"type": "string"},
				"description": "Optional list of tool names the sub-agent can use (defaults to all parent tools except sub-agent tools)"
			},
			"context": {
				"type": "string",
				"description": "Optional additional context for the sub-agent"
			},
			"model": {
				"type": "string",
				"enum": ["sonnet", "opus", "haiku"],
				"description": "Optional model override for the sub-agent"
			},
			"subagent_type": {
				"type": "string",
				"description": "Optional type of specialized agent (e.g., 'Explore', 'Plan')"
			}
		},
		"required": ["task"]
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
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if args.Task == "" {
		return Result{IsError: true, Content: "task is required"}, nil
	}

	displayTask := args.Task
	if args.Context != "" {
		args.Task = args.Context + "\n\n" + args.Task
	}

	id := t.Manager.Spawn(args.Task, displayTask, args.Tools, ctx)

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
	go subagent.Run(runCtx, subagent.RunnerConfig{
		Provider:     t.Provider,
		AllTools:     allToolInfo,
		Task:         args.Task,
		AllowedTools: args.Tools,
		Manager:      t.Manager,
		SubAgentID:   id,
		AgentFactory: t.AgentFactory,
		Model:        model,
		AgentType:    subagentType,
		BuildToolSet: func(allowedTools []string, _ []subagent.ToolInfo) interface{} {
			subReg := NewRegistry()
			if len(allowedTools) == 0 {
				for _, ti := range allToolInfo {
					name := ti.Name()
					if name != "spawn_agent" && name != "wait_agent" && name != "list_agents" {
						if tl, ok := tools.Get(name); ok {
							subReg.Register(tl)
						}
					}
				}
			} else {
				for _, name := range allowedTools {
					if tl, ok := tools.Get(name); ok {
						subReg.Register(tl)
					}
				}
			}
			return subReg
		},
	})

	return Result{Content: fmt.Sprintf("Sub-agent spawned with ID: %s\nUse wait_agent or list_agents to monitor progress and retrieve the result.", id)}, nil
}

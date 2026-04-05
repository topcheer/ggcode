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
	return "Spawn a sub-agent to work on a task independently. Returns an agent_id. Use wait_agent to retrieve the result."
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
			}
		},
		"required": ["task"]
	}`)
}

func (t SpawnAgentTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Task    string   `json:"task"`
		Tools   []string `json:"tools"`
		Context string   `json:"context"`
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

	// Launch the sub-agent in a goroutine
	go subagent.Run(ctx, subagent.RunnerConfig{
		Provider:     t.Provider,
		AllTools:     allToolInfo,
		Task:         args.Task,
		AllowedTools: args.Tools,
		Manager:      t.Manager,
		SubAgentID:   id,
		AgentFactory: t.AgentFactory,
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

	return Result{Content: fmt.Sprintf("Sub-agent spawned with ID: %s\nUse wait_agent to retrieve the result.", id)}, nil
}

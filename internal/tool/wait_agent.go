package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/topcheer/ggcode/internal/subagent"
)

// WaitAgentTool implements the wait_agent tool.
type WaitAgentTool struct {
	Manager *subagent.Manager
}

func (t WaitAgentTool) Name() string { return "wait_agent" }

func (t WaitAgentTool) Description() string {
	return "Wait for a spawned sub-agent to complete and return its result. Blocks until the agent finishes, times out, or is cancelled."
}

func (t WaitAgentTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"agent_id": {
				"type": "string",
				"description": "The ID of the sub-agent to wait for (returned by spawn_agent)"
			}
		},
		"required": ["agent_id"]
	}`)
}

func (t WaitAgentTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if args.AgentID == "" {
		return Result{IsError: true, Content: "agent_id is required"}, nil
	}

	result, err := subagent.Wait(ctx, t.Manager, args.AgentID)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("wait failed: %v", err)}, nil
	}

	return Result{Content: result}, nil
}

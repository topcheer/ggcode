package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/topcheer/ggcode/internal/subagent"
)

// CancelAgentTool implements the cancel_agent tool.
// It cancels a specific running or pending sub-agent by its agent_id,
// stopping its goroutine and releasing its concurrency slot.
type CancelAgentTool struct {
	Manager *subagent.Manager
}

func (t CancelAgentTool) Name() string { return "cancel_agent" }

func (t CancelAgentTool) Description() string {
	return "Cancel a running or pending sub-agent by its agent_id. The sub-agent's context is cancelled and its goroutine terminates. Use this when a sub-agent is no longer needed, is stuck, or was spawned by mistake. The result of a cancelled sub-agent is discarded."
}

func (t CancelAgentTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"agent_id": {
			"type": "string",
			"description": "The ID of the agent run to cancel (returned by spawn_agent or delegate)"
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Cancelling research agent', '取消子代理'). You MUST always provide this field."
		}
	},
	"required": [
		"agent_id",
		"description"
	]
}`)
}

func (t CancelAgentTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Manager == nil {
		return Result{IsError: true, Content: "cancel_agent: agent manager not available"}, nil
	}
	var args struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if args.AgentID == "" {
		return Result{IsError: true, Content: "agent_id is required"}, nil
	}

	// Check if the agent exists and is in a cancellable state
	snap, ok := t.Manager.Snapshot(args.AgentID)
	if !ok {
		return Result{IsError: true, Content: fmt.Sprintf("agent not found: %s", args.AgentID)}, nil
	}

	if snap.Status == subagent.StatusCompleted || snap.Status == subagent.StatusFailed || snap.Status == subagent.StatusCancelled {
		return Result{Content: fmt.Sprintf("agent %s is already in terminal state: %s (no action taken)", args.AgentID, snap.Status)}, nil
	}

	if t.Manager.Cancel(args.AgentID) {
		return Result{Content: fmt.Sprintf("agent %s cancelled successfully. Any partial work has been discarded.", args.AgentID)}, nil
	}
	return Result{IsError: true, Content: fmt.Sprintf("failed to cancel agent %s (it may have just completed)", args.AgentID)}, nil
}

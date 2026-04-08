package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/topcheer/ggcode/internal/subagent"
)

// WaitAgentTool implements the wait_agent tool.
type WaitAgentTool struct {
	Manager *subagent.Manager
}

func (t WaitAgentTool) Name() string { return "wait_agent" }

func (t WaitAgentTool) Description() string {
	return "Wait briefly for a spawned sub-agent, then return its current status snapshot. Completed agents include their result; running agents include phase and progress."
}

func (t WaitAgentTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"agent_id": {
				"type": "string",
				"description": "The ID of the sub-agent to wait for (returned by spawn_agent)"
			},
			"wait_seconds": {
				"type": "integer",
				"description": "How long to wait before returning a status snapshot (default: 30)"
			}
		},
		"required": ["agent_id"]
	}`)
}

func (t WaitAgentTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		AgentID     string `json:"agent_id"`
		WaitSeconds int    `json:"wait_seconds"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if args.AgentID == "" {
		return Result{IsError: true, Content: "agent_id is required"}, nil
	}

	wait := 30 * time.Second
	if args.WaitSeconds > 0 {
		wait = time.Duration(args.WaitSeconds) * time.Second
	}
	snap, err := subagent.WaitForSnapshot(ctx, t.Manager, args.AgentID, wait)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("wait failed: %v", err)}, nil
	}

	if snap.Status == subagent.StatusCompleted && snap.ProgressSummary == "" && snap.CurrentTool == "" && snap.Result != "" {
		return Result{Content: snap.Result}, nil
	}
	return Result{Content: formatSubAgentSnapshot(snap)}, nil
}

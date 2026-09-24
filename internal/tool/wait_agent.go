package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/topcheer/ggcode/internal/subagent"
)

// WaitAgentTool implements the wait_agent tool.
type WaitAgentTool struct {
	Manager *subagent.Manager
	// ParentModel resolves the parent agent's current model name. When a
	// failed run used an alternate model, the snapshot is annotated with a
	// one-shot cascade escalation hint (see subagent_cascade.go).
	ParentModel func() string
	// CascadeHints deduplicates escalation hints across wait_agent and
	// list_agents so each failed run is annotated at most once.
	CascadeHints *CascadeHintTracker
}

func (t WaitAgentTool) Name() string { return "wait_agent" }

func (t WaitAgentTool) Description() string {
	return "Wait briefly (15-60s, default 30) for an agent run, then return its status snapshot (completed runs include their result). Keep wait_seconds short and re-poll instead of waiting the full expected runtime: runs fail or stall early."
}

func (t WaitAgentTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"agent_id": {
			"type": "string",
			"description": "The ID of the agent run to wait for (returned by spawn_agent or delegate)"
		},
		"wait_seconds": {
			"type": "integer",
			"description": "How long to wait before returning a status snapshot (default: 30). Keep short (15-60s) and re-poll."
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Searching for TODO patterns', '检查构建配置'). You MUST always provide this field."
		}
	},
	"required": [
		"agent_id",
		"description"
	]
}`)
}

func (t WaitAgentTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Manager == nil {
		return Result{IsError: true, Content: "wait_agent: agent manager not available"}, nil
	}
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
		// #1349: cap aligned with the sleep tool's 30-minute maximum.
		// Without it an LLM passing 86400 blocks the agent loop for 24h -
		// the only escape hatches are ctx cancellation or the child agent
		// reaching a terminal state.
		if args.WaitSeconds > 1800 {
			return Result{IsError: true, Content: fmt.Sprintf("wait_seconds %d exceeds maximum of 1800 (30 minutes); wait in shorter increments and re-poll instead", args.WaitSeconds)}, nil
		}
		wait = time.Duration(args.WaitSeconds) * time.Second
	}

	// Extract progress callback from context (if available) for live streaming.
	var progressFn subagent.SnapshotProgressFunc
	if tpf, ok := ctx.Value(ToolProgressKey{}).(ToolProgressFunc); ok {
		progressFn = func(summary string) {
			tpf("", "wait_agent", summary)
		}
	}

	snap, err := subagent.WaitForSnapshotWithProgress(ctx, t.Manager, args.AgentID, wait, progressFn)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("wait failed: %v", err)}, nil
	}

	if snap.Status == subagent.StatusCompleted && snap.ProgressSummary == "" && snap.CurrentTool == "" && snap.Result != "" {
		return Result{Content: annotateWorktree(snap.Result, snap)}, nil
	}
	return Result{Content: annotateWorktree(appendCascadeHint(t.CascadeHints, t.ParentModel, snap), snap)}, nil
}

// annotateWorktree appends the isolation worktree path to a wait_agent
// snapshot so the parent can locate the sub-agent's edits after the run.
func annotateWorktree(content string, snap subagent.Snapshot) string {
	if snap.Worktree == "" {
		return content
	}
	return content + fmt.Sprintf("\n\nIsolated worktree: %s (branch: %s)", snap.Worktree, filepath.Base(snap.Worktree))
}

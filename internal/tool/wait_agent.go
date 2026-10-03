package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
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
	return "Wait briefly (15-60s, default 30) for an agent run, then return its status snapshot (completed runs include their result). Keep wait_seconds short and re-poll instead of waiting the full expected runtime: runs fail or stall early. Waiting only observes - it never sends instructions to the run. When a run completes, check the result against the original task's acceptance criteria before treating it as done."
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
		// #3237: snap.Task carries the nudge spawn appended; strip it so
		// both reminders extract the ORIGINAL acceptance criteria.
		task := stripReadBackNudge(snap.Task)
		return Result{Content: annotateWorktree(snap.Result+formatExploreRegions(snap.Result)+readBackReminder(task, snap.Result)+acceptanceReminder(task, snap.Result), snap)}, nil
	}
	var reminder string
	if snap.Status == subagent.StatusCompleted {
		// r453: front-end handshake report first (did the sub-agent
		// restate the criteria before starting?), then the r384
		// validator-side reminder - both on completed runs only.
		// #3237: same strip as above - reminders see the original task.
		task := stripReadBackNudge(snap.Task)
		reminder = readBackReminder(task, snap.Result) + acceptanceReminder(task, snap.Result) + formatExploreRegions(snap.Result)
	}
	return Result{Content: annotateWorktree(appendCascadeHint(t.CascadeHints, t.ParentModel, snap)+reminder, snap)}, nil
}

// annotateWorktree appends the isolation worktree path to a wait_agent
// snapshot so the parent can locate the sub-agent's edits after the run.
func annotateWorktree(content string, snap subagent.Snapshot) string {
	if snap.Worktree == "" {
		return content
	}
	return content + fmt.Sprintf("\n\nIsolated worktree: %s (branch: %s)", snap.Worktree, filepath.Base(snap.Worktree))
}

// exploreRegionRe matches the "path:startLine-endLine" contract lines an
// Explore sub-agent must emit under its "## Regions" section (r388,
// FastContext Kim et al. 2026: structured region handoff beats free-form
// notes; the parent consumes regions directly as targeted reads).
var exploreRegionRe = regexp.MustCompile(`(?m)^\s*(\S+?):(\d+)-(\d+)\b`)

// formatExploreRegions turns the region list into an explicit targeted-read
// block (path + offset + limit) so the parent can issue offset/limit reads
// without re-scanning the free-text result for paths. Returns "" when the
// "## Regions" marker is absent - non-Explore runs pass through unchanged,
// and results whose region section failed to parse also stay untouched.
func formatExploreRegions(result string) string {
	idx := strings.Index(result, "## Regions")
	if idx < 0 {
		return ""
	}
	matches := exploreRegionRe.FindAllStringSubmatch(result[idx:], 8)
	if len(matches) == 0 {
		return ""
	}
	lines := make([]string, 0, len(matches))
	for _, m := range matches {
		start, err1 := strconv.Atoi(m[2])
		end, err2 := strconv.Atoi(m[3])
		if err1 != nil || err2 != nil || start < 1 || end < start {
			continue
		}
		lines = append(lines, fmt.Sprintf("- read_file %s (offset %d, limit %d)", m[1], start, end-start+1))
	}
	if len(lines) == 0 {
		return ""
	}
	return "\n\nTargeted reads (structured handoff from the Explore sub-agent):\n" + strings.Join(lines, "\n")
}

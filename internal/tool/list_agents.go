package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/util"
)

// ListAgentsTool implements the list_agents tool.
type ListAgentsTool struct {
	Manager *subagent.Manager
}

func (t ListAgentsTool) Name() string { return "list_agents" }

func (t ListAgentsTool) Description() string {
	return "List all spawned sub-agents and their current status."
}

func (t ListAgentsTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {}}`)
}

func (t ListAgentsTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Manager == nil {
		return Result{IsError: true, Content: "list_agents: sub-agent manager not available"}, nil
	}
	agents := t.Manager.List()
	if len(agents) == 0 {
		return Result{Content: "No sub-agents have been spawned."}, nil
	}

	// Sort by creation time
	sort.Slice(agents, func(i, j int) bool {
		return agents[i].CreatedAt.Before(agents[j].CreatedAt)
	})

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d sub-agent(s):\n\n", len(agents)))
	for _, sa := range agents {
		snap, ok := t.Manager.Snapshot(sa.ID)
		if !ok {
			continue
		}
		sb.WriteString(formatSubAgentSnapshot(snap))
		sb.WriteString("\n")
	}

	return Result{Content: sb.String()}, nil
}

func formatSubAgentSnapshot(snap subagent.Snapshot) string {
	var sb strings.Builder
	duration := formatSubAgentDuration(snap)
	task := firstNonEmptyNonSpace(snap.DisplayTask, snap.Task)
	sb.WriteString(fmt.Sprintf("  %s [%s]%s\n", snap.ID, snap.Status, duration))
	if task != "" {
		sb.WriteString(fmt.Sprintf("    Task: %s\n", truncate(task, 80)))
	}
	if snap.ToolCallCount > 0 {
		sb.WriteString(fmt.Sprintf("    Tool calls: %d\n", snap.ToolCallCount))
	}
	if summary := strings.TrimSpace(snap.ProgressSummary); summary != "" {
		sb.WriteString(fmt.Sprintf("    Progress: %s\n", truncate(summary, 120)))
	} else if snap.CurrentTool != "" {
		sb.WriteString(fmt.Sprintf("    Current tool: %s\n", truncate(snap.CurrentTool, 80)))
	}
	if snap.CurrentPhase != "" {
		sb.WriteString(fmt.Sprintf("    Phase: %s\n", snap.CurrentPhase))
	}
	if snap.Status == subagent.StatusCompleted && strings.TrimSpace(snap.Result) != "" {
		sb.WriteString(fmt.Sprintf("    Result: %s\n", truncate(snap.Result, 120)))
	}
	if snap.Error != "" {
		sb.WriteString(fmt.Sprintf("    Error: %s\n", truncate(snap.Error, 120)))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func formatSubAgentDuration(snap subagent.Snapshot) string {
	if snap.StartedAt.IsZero() {
		return ""
	}
	end := snap.EndedAt
	if end.IsZero() {
		end = snap.StartedAt
	}
	if !snap.EndedAt.IsZero() {
		return fmt.Sprintf(" (%v)", snap.EndedAt.Sub(snap.StartedAt).Round(time.Second))
	}
	return fmt.Sprintf(" (%v)", time.Since(snap.StartedAt).Round(time.Second))
}

func firstNonEmptyNonSpace(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func truncate(s string, maxLen int) string {
	return util.Truncate(s, maxLen)
}

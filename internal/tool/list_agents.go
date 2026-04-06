package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

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
		duration := ""
		if !sa.EndedAt.IsZero() && !sa.StartedAt.IsZero() {
			duration = fmt.Sprintf(" (%v)", sa.EndedAt.Sub(sa.StartedAt).Round(1e9))
		}
		task := sa.DisplayTask
		if task == "" {
			task = sa.Task
		}
		sb.WriteString(fmt.Sprintf("  %s [%s]%s\n    Task: %s\n", sa.ID, sa.Status, duration, truncate(task, 80)))
		if summary := strings.TrimSpace(sa.ProgressSummary); summary != "" {
			sb.WriteString(fmt.Sprintf("    Progress: %s\n", truncate(summary, 120)))
		}
		if sa.Status == subagent.StatusCompleted {
			sb.WriteString(fmt.Sprintf("    Result: %s\n", truncate(sa.Result, 120)))
		}
		if sa.Error != nil {
			sb.WriteString(fmt.Sprintf("    Error: %v\n", sa.Error))
		}
		sb.WriteString("\n")
	}

	return Result{Content: sb.String()}, nil
}

func truncate(s string, maxLen int) string {
	return util.Truncate(s, maxLen)
}

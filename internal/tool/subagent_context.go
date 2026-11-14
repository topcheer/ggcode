package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/subagent"
)

// SubagentContextTool lets the MAIN agent read a running (or finished)
// sub-agent's live event stream (#5637): recent text output, tool calls
// and tool results, in-flight, without waiting for the run to complete.
// Combine with cancel_agent to stop a run the moment it goes off-track.
type SubagentContextTool struct {
	Manager *subagent.Manager
}

func (t SubagentContextTool) Name() string { return "subagent_context" }

func (t SubagentContextTool) Description() string {
	return "Read a running sub-agent's live context in-flight: header (status, current tool, progress) plus its most recent events verbatim (text output, tool calls, tool results) from the in-memory event ring. Use this to monitor what a sub-agent is doing WHILE it runs - you do not have to wait for wait_agent/completion to know what happened. Re-poll to follow progress; pair with cancel_agent to stop a run that went off-track (stop-loss)."
}

func (t SubagentContextTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"agent_id": {
			"type": "string",
			"description": "The sub-agent run ID (returned by spawn_agent or delegate)"
		},
		"tail": {
			"type": "integer",
			"description": "How many recent events to return (default 20, max 400)"
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI, in the user's language."
		}
	},
	"required": ["agent_id", "description"]
}`)
}

func (t SubagentContextTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		AgentID     string `json:"agent_id"`
		Tail        int    `json:"tail"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{Content: "invalid arguments: " + err.Error(), IsError: true}, nil
	}
	if strings.TrimSpace(args.AgentID) == "" {
		return Result{Content: "agent_id is required", IsError: true}, nil
	}
	if t.Manager == nil {
		return Result{Content: "sub-agent manager not available in this session", IsError: true}, nil
	}
	snap, ok := t.Manager.Snapshot(args.AgentID)
	if !ok {
		return Result{Content: fmt.Sprintf("unknown agent run %q (check list_agents for live IDs)", args.AgentID), IsError: true}, nil
	}
	events, _ := t.Manager.RecentEvents(args.AgentID, args.Tail)

	var b strings.Builder
	fmt.Fprintf(&b, "agent %s (%s) status=%s phase=%s\n", snap.ID, snap.Name, snap.Status, snap.CurrentPhase)
	if snap.CurrentTool != "" {
		fmt.Fprintf(&b, "current tool: %s %s\n", snap.CurrentTool, head(snap.CurrentArgs, 120))
	}
	fmt.Fprintf(&b, "tool calls=%d progress: %s\n", snap.ToolCallCount, snap.ProgressSummary)
	if snap.Status == subagent.StatusCompleted {
		fmt.Fprintf(&b, "result: %s\n", head(snap.Result, 400))
	}
	if snap.Error != "" {
		fmt.Fprintf(&b, "error: %s\n", snap.Error)
	}
	if len(events) == 0 {
		b.WriteString("no events recorded yet\n")
	} else {
		fmt.Fprintf(&b, "--- last %d events (oldest first) ---\n", len(events))
		for _, ev := range events {
			switch {
			case ev.Text != "":
				fmt.Fprintf(&b, "[text] %s\n", head(ev.Text, 400))
			case ev.ToolName != "" && ev.Result != "":
				tag := "result"
				if ev.IsError {
					tag = "result:ERROR"
				}
				fmt.Fprintf(&b, "[%s] %s: %s\n", tag, ev.ToolName, head(ev.Result, 300))
			case ev.ToolName != "":
				fmt.Fprintf(&b, "[tool] %s %s\n", ev.ToolName, head(ev.ToolArgs, 160))
			default:
				fmt.Fprintf(&b, "[system] %s\n", head(ev.ToolDetail, 200))
			}
		}
	}
	return Result{Content: b.String()}, nil
}

func head(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + " ..."
}

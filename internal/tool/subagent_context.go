package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/swarm"
)

// SubagentContextTool lets the MAIN agent read a running (or finished)
// sub-agent's live event stream (#5637): recent text output, tool calls
// and tool results, in-flight, without waiting for the run to complete.
// Combine with cancel_agent to stop a run the moment it goes off-track.
//
// Teammates (tm-*) are covered too: the swarm manager keeps the same kind
// of event ring per teammate, so the leader can watch a teammate's context
// live while it works ("看 docs-web 进度（是否卡住）"). Before this, tm-*
// resolved to "unknown agent run" because only the sub-agent manager was
// consulted.
type SubagentContextTool struct {
	Manager *subagent.Manager
	Swarm   *swarm.Manager
}

func (t SubagentContextTool) Name() string { return "subagent_context" }

func (t SubagentContextTool) Description() string {
	return "Read a running worker's live context in-flight: header (status, current task/tool, progress) plus its most recent events verbatim (text output, reasoning, tool calls, tool results) from the in-memory event ring. Works for BOTH sub-agent runs (agent-* from spawn_agent/delegate/use_namedagent) AND swarm teammates (tm-* from teammate_spawn/swarm_task_create) - the master can watch a teammate while it works to see whether it is stuck, looping or off-track. You do not have to wait for wait_agent/teammate_results to know what happened. Re-poll to follow progress; pair with cancel_agent (agent-*) or teammate_shutdown (tm-*) to stop a worker that went off-track (stop-loss)."
}

func (t SubagentContextTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"agent_id": {
			"type": "string",
			"description": "The worker ID: a sub-agent run ID (agent-*, from spawn_agent / delegate / use_namedagent) or a teammate ID (tm-*, from teammate_spawn / teammate_list / swarm_task_create)"
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
	if t.Manager == nil && t.Swarm == nil {
		return Result{Content: "sub-agent manager not available in this session", IsError: true}, nil
	}
	if t.Manager != nil {
		if snap, ok := t.Manager.Snapshot(args.AgentID); ok {
			return t.renderSubagent(snap, args.Tail), nil
		}
	}
	if t.Swarm != nil {
		if snap, ok := t.Swarm.TeammateSnapshot(args.AgentID); ok {
			return t.renderTeammate(snap, args.Tail), nil
		}
	}
	return Result{Content: fmt.Sprintf("unknown worker %q (check list_agents for live agent-* runs, teammate_list for tm-* teammates)", args.AgentID), IsError: true}, nil
}

// renderSubagent formats a sub-agent run's header and recent events.
func (t SubagentContextTool) renderSubagent(snap subagent.Snapshot, tail int) Result {
	events, _ := t.Manager.RecentEvents(snap.ID, tail)

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
	return Result{Content: b.String()}
}

// renderTeammate formats a swarm teammate's header and recent events from
// its ring (maxTeammateEvents entries, oldest first). The ring shape mirrors
// the sub-agent one, so the event lines read the same.
func (t SubagentContextTool) renderTeammate(snap swarm.TeammateSnapshot, tail int) Result {
	events := snap.Events
	if tail <= 0 {
		tail = 20
	}
	if tail > len(events) {
		tail = len(events)
	}
	events = events[len(events)-tail:]

	var b strings.Builder
	fmt.Fprintf(&b, "teammate %s (%s) status=%s", snap.ID, snap.Name, snap.Status)
	if snap.CurrentTask != "" {
		fmt.Fprintf(&b, " task=%s", head(snap.CurrentTask, 120))
	}
	fmt.Fprintf(&b, " last_active=%s", ago(snap.LastActiveAt))
	if snap.EventsDropped > 0 {
		fmt.Fprintf(&b, " (ring dropped %d older events)", snap.EventsDropped)
	}
	b.WriteString("\n")
	if snap.LastResult != "" && snap.Status != swarm.TeammateWorking {
		fmt.Fprintf(&b, "last result: %s\n", head(snap.LastResult, 400))
	}
	if len(events) == 0 {
		b.WriteString("no events recorded yet\n")
	} else {
		fmt.Fprintf(&b, "--- last %d events (oldest first) ---\n", len(events))
		for _, ev := range events {
			switch ev.Type {
			case swarm.TeammateEventToolCall:
				fmt.Fprintf(&b, "[tool] %s %s\n", ev.ToolName, head(ev.ToolArgs, 160))
			case swarm.TeammateEventToolResult:
				tag := "result"
				if ev.IsError {
					tag = "result:ERROR"
				}
				fmt.Fprintf(&b, "[%s] %s: %s\n", tag, ev.ToolName, head(ev.Result, 300))
			case swarm.TeammateEventReasoning:
				fmt.Fprintf(&b, "[reasoning] %s\n", head(ev.Text, 400))
			case swarm.TeammateEventError:
				fmt.Fprintf(&b, "[error] %s\n", head(ev.Text, 400))
			default:
				fmt.Fprintf(&b, "[text] %s\n", head(ev.Text, 400))
			}
		}
	}
	return Result{Content: b.String()}
}

func ago(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh%02dm ago", int(d.Hours()), int(d.Minutes())%60)
	}
}

func head(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + " ..."
}

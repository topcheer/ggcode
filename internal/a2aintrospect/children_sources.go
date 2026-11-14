package a2aintrospect

import (
	"strings"

	"github.com/topcheer/ggcode/internal/a2a"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/tool"
)

// #5605 P2: children/inflight face wiring. Adapters map live runtime
// sources (subagent manager, command job manager) into the v3 snapshot
// shapes - pure functions so pins can cover them without a live daemon.

// FromSubagents maps the sub-agent list to the children-face shape.
func FromSubagents(list []*subagent.SubAgent) []a2a.RuntimeSubagent {
	out := make([]a2a.RuntimeSubagent, 0, len(list))
	for _, sa := range list {
		if sa == nil {
			continue
		}
		task := sa.Task
		if sa.DisplayTask != "" {
			task = sa.DisplayTask
		}
		out = append(out, a2a.RuntimeSubagent{
			ID:          sa.ID,
			Name:        sa.Name,
			TaskHead:    head(task, 120),
			State:       string(sa.Status),
			Model:       sa.Model,
			Worktree:    sa.Worktree,
			CurrentTool: sa.CurrentTool,
			ToolCalls:   sa.ToolCallCount,
		})
	}
	return out
}

// EventSourcer is the sub-agent manager surface AttachSubagentEvents needs
// (implemented by *subagent.Manager). Interface keeps the adapter testable
// without a live daemon.
type EventSourcer interface {
	RecentEvents(id string, n int) ([]subagent.AgentEvent, bool)
}

// AttachSubagentEvents fills each child's Events tail from the live event
// ring (#5637): a peer reading the children face sees what a running
// sub-agent is doing in-flight, not just its status header. n is the per-
// child tail size (<=0 => 10).
func AttachSubagentEvents(subs []a2a.RuntimeSubagent, src EventSourcer, n int) {
	if src == nil {
		return
	}
	if n <= 0 {
		n = 10
	}
	for i := range subs {
		evs, ok := src.RecentEvents(subs[i].ID, n)
		if !ok || len(evs) == 0 {
			continue
		}
		out := make([]a2a.RuntimeSubagentEvent, 0, len(evs))
		for _, ev := range evs {
			rse := a2a.RuntimeSubagentEvent{}
			switch {
			case ev.Text != "":
				rse.Type, rse.Head = "text", head(ev.Text, 160)
			case ev.ToolName != "" && ev.Result != "":
				tag := "result"
				if ev.IsError {
					tag = "error"
				}
				rse.Type, rse.Head = tag, head(ev.ToolName+": "+ev.Result, 160)
			case ev.ToolName != "":
				rse.Type, rse.Head = "tool", head(ev.ToolName+" "+ev.ToolArgs, 120)
			default:
				rse.Type, rse.Head = "system", head(ev.ToolDetail, 120)
			}
			out = append(out, rse)
		}
		subs[i].Events = out
	}
}

// FromCommandJobs maps background command jobs to the children-face shape.
func FromCommandJobs(jobs []tool.CommandJobSnapshot) []a2a.RuntimeJob {
	out := make([]a2a.RuntimeJob, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, a2a.RuntimeJob{
			ID:      j.ID,
			CmdHead: head(j.Command, 120),
			State:   string(j.Status),
		})
	}
	return out
}

// InflightFromSubagents summarizes the ACTIVE sub-agent tasks into the
// inflight face (P2 scope: the active-task list of this seat; the P1
// "loop instrumentation" note stays - main-agent tool-call tracking is a
// later refinement). Idle/done seats are invisible here and show up in
// the children face instead.
func InflightFromSubagents(list []*subagent.SubAgent) *a2a.RuntimeInflight {
	var active []*subagent.SubAgent
	for _, sa := range list {
		if sa != nil && sa.Status == subagent.StatusRunning {
			active = append(active, sa)
		}
	}
	if len(active) == 0 {
		return nil
	}
	first := active[0]
	tool := first.CurrentTool
	if tool == "" {
		tool = "(working)"
	}
	return &a2a.RuntimeInflight{
		Tool:       tool,
		ParamsHead: head(first.Task, 200),
		StartedAt:  first.StartedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
}

// ActiveCount returns how many sub-agents are running (for status faces).
func ActiveCount(list []*subagent.SubAgent) int {
	n := 0
	for _, sa := range list {
		if sa != nil && sa.Status == subagent.StatusRunning {
			n++
		}
	}
	return n
}

func head(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

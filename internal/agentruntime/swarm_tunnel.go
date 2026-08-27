package agentruntime

import (
	"fmt"
	"hash/fnv"
	"strings"

	"github.com/topcheer/ggcode/internal/swarm"
	"github.com/topcheer/ggcode/internal/tunnel"
)

func PushTunnelSwarmEvent(
	currentBroker func() *tunnel.Broker,
	mgr *swarm.Manager,
	ev swarm.Event,
	displayName func(string, string) string,
	detail func(string, string) string,
) {
	if currentBroker == nil {
		return
	}
	broker := currentBroker()
	if broker == nil {
		return
	}

	switch ev.Type {
	case "teammate_tool_call":
		d := ""
		if detail != nil {
			d = detail(ev.CurrentTool, ev.ToolArgs)
		}
		name := ev.CurrentTool
		if displayName != nil {
			name = displayName(ev.CurrentTool, ev.ToolArgs)
		}
		broker.PushSubagentToolCall(ev.TeammateID, ev.ToolID, ev.CurrentTool, name, ev.ToolArgs, d)
		broker.PushSubagentStatus(ev.TeammateID, tunnel.StatusRunning, ev.CurrentTool)

	case "teammate_tool_result":
		broker.PushSubagentToolResult(ev.TeammateID, ev.ToolID, ev.CurrentTool, "", "", ev.Result, ev.IsError)

	case "teammate_text":
		msgID := fmt.Sprintf("tm-%s", ev.TeammateID)
		broker.PushSubagentText(ev.TeammateID, msgID, ev.Result, false)

	case "teammate_spawned":
		color := ""
		if mgr != nil {
			if snap, ok := mgr.TeammateSnapshot(ev.TeammateID); ok {
				color = snap.Color
			}
		}
		broker.PushSubagentSpawn(ev.TeammateID, ev.TeammateName, "teammate", color, ev.TeamID)

	case "teammate_working":
		broker.PushSubagentStatus(ev.TeammateID, tunnel.StatusRunning, ev.TeammateName)
		// Issue #1158: this branch used to replay the last buffered
		// teammate_text from the event history under the fixed msgID
		// "tm-<id>", but that text is the tail output of the PREVIOUS task
		// (already streamed live when it was produced). Mobile clients join
		// chunks by msgID without dedup, so every consecutive task re-pushed
		// the same leftover text under the old message ID and content
		// repeated linearly. Instead announce the new task explicitly with a
		// per-task message ID so clients create a fresh message per task;
		// no client-side change is needed.
		if mgr != nil {
			if snap, ok := mgr.TeammateSnapshot(ev.TeammateID); ok {
				if msgID, text, ok2 := workingTaskStartMessage(snap); ok2 {
					broker.PushSubagentText(ev.TeammateID, msgID, text, false)
				}
			}
		}

	case "teammate_idle":
		if ev.Result != "" {
			msgID := fmt.Sprintf("tm-%s", ev.TeammateID)
			broker.PushSubagentText(ev.TeammateID, msgID, ev.Result, true)
		}
		success := ev.Error == nil
		summary := ev.Result
		if ev.Error != nil {
			summary = ev.Error.Error()
		}
		broker.PushSubagentComplete(ev.TeammateID, ev.TeammateName, summary, success)

	case "teammate_shutdown":
		broker.PushSubagentComplete(ev.TeammateID, ev.TeammateName, "shutdown", true)

	case "teammate_error":
		errMsg := ""
		if ev.Error != nil {
			errMsg = ev.Error.Error()
		}
		broker.PushSubagentComplete(ev.TeammateID, ev.TeammateName, errMsg, false)
	}
}

// workingTaskStartMessage builds the explicit per-task start announcement
// emitted when a teammate begins working (issue #1158). It never reads the
// teammate's historical text buffer: that history belongs to the previous
// task and was already streamed live, so replaying it under a fixed msgID
// made mobile clients append stale tails onto old messages on every new
// task. The per-task msgID embeds the event-history length at task start
// (strictly monotonic across tasks within one teammate's lifetime) plus an
// FNV-32 hash of the task text for disambiguation when two consecutive
// tasks happen to start with equal history lengths.
func workingTaskStartMessage(snap swarm.TeammateSnapshot) (msgID string, text string, ok bool) {
	task := strings.TrimSpace(snap.CurrentTask)
	if task == "" {
		return "", "", false
	}
	h := fnv.New32a()
	h.Write([]byte(task))
	msgID = fmt.Sprintf("tm-%s-task-%d-%08x", snap.ID, len(snap.Events), h.Sum32())
	return msgID, "[task started] " + task, true
}

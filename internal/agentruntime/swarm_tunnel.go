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

	subagentTunnelReasoningMsgID := func(agentID string) string {
		return fmt.Sprintf("sa-%s-reasoning", agentID)
	}

	switch ev.Type {
	case "teammate_tool_call":
		broker.PushReasoningDone(subagentTunnelReasoningMsgID(ev.TeammateID))
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
		broker.PushReasoningDone(subagentTunnelReasoningMsgID(ev.TeammateID))
		broker.PushSubagentToolResult(ev.TeammateID, ev.ToolID, ev.CurrentTool, "", "", ev.Result, ev.IsError)

	case "teammate_reasoning":

		if chunk := tunnel.NormalizeReasoningChunk(ev.Result); chunk != "" {
			broker.PushSubagentReasoning(ev.TeammateID, subagentTunnelReasoningMsgID(ev.TeammateID), chunk, false)
		}

	case "teammate_text":
		broker.PushReasoningDone(subagentTunnelReasoningMsgID(ev.TeammateID))
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

		if mgr != nil {
			if snap, ok := mgr.TeammateSnapshot(ev.TeammateID); ok {
				if msgID, text, ok2 := workingTaskStartMessage(snap); ok2 {
					broker.PushSubagentText(ev.TeammateID, msgID, text, false)
				}
			}
		}

	case "teammate_idle":
		broker.PushReasoningDone(subagentTunnelReasoningMsgID(ev.TeammateID))
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

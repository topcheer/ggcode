package agentruntime

import (
	"fmt"

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
		broker.PushSubagentToolResult(ev.TeammateID, ev.ToolID, ev.CurrentTool, "", "", ev.ToolArgs, ev.IsError)

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
		if mgr != nil {
			if snap, ok := mgr.TeammateSnapshot(ev.TeammateID); ok && len(snap.Events) > 0 {
				last := snap.Events[len(snap.Events)-1]
				if last.Type == swarm.TeammateEventText && last.Text != "" {
					msgID := fmt.Sprintf("tm-%s", ev.TeammateID)
					broker.PushSubagentText(ev.TeammateID, msgID, last.Text, false)
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

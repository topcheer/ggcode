package agentruntime

import (
	"fmt"

	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/tunnel"
)

func TunnelSubagentTextID(agentID string) string {
	if agentID == "" {
		return ""
	}
	return fmt.Sprintf("sa-%s", agentID)
}

func TunnelSubagentReasoningID(agentID string) string {
	if agentID == "" {
		return ""
	}
	return fmt.Sprintf("sa-%s-reasoning", agentID)
}

func PushTunnelSubagentEvent(currentBroker func() *tunnel.Broker, markSpawned func(string) bool, sa *subagent.SubAgent) {
	if sa == nil || currentBroker == nil {
		return
	}
	broker := currentBroker()
	if broker == nil {
		return
	}

	pushSpawn := func() {
		if markSpawned == nil || markSpawned(sa.ID) {
			broker.PushSubagentSpawn(sa.ID, sa.Name, sa.Task, "", "")
		}
	}

	switch sa.Status {
	case subagent.StatusRunning:
		pushSpawn()
		broker.PushSubagentStatus(sa.ID, tunnel.StatusRunning, sa.CurrentTool)
	case subagent.StatusCompleted:
		pushSpawn()
		broker.PushReasoningDone(TunnelSubagentReasoningID(sa.ID))
		if sa.Result != "" {
			broker.PushSubagentText(sa.ID, TunnelSubagentTextID(sa.ID), sa.Result, true)
		}
		broker.PushSubagentComplete(sa.ID, sa.Name, sa.Result, true)
	case subagent.StatusFailed:
		pushSpawn()
		broker.PushReasoningDone(TunnelSubagentReasoningID(sa.ID))
		errMsg := ""
		if sa.Error != nil {
			errMsg = sa.Error.Error()
		}
		broker.PushSubagentComplete(sa.ID, sa.Name, errMsg, false)
	case subagent.StatusCancelled:
		pushSpawn()
		broker.PushReasoningDone(TunnelSubagentReasoningID(sa.ID))
		broker.PushSubagentComplete(sa.ID, sa.Name, "cancelled", false)
	}
}

func PushTunnelSubagentText(currentBroker func() *tunnel.Broker, agentID, text string) {
	if currentBroker == nil {
		return
	}
	if broker := currentBroker(); broker != nil {
		broker.PushReasoningDone(TunnelSubagentReasoningID(agentID))
		broker.PushSubagentText(agentID, TunnelSubagentTextID(agentID), text, false)
	}
}

func PushTunnelSubagentReasoning(currentBroker func() *tunnel.Broker, agentID, text string) {
	if currentBroker == nil {
		return
	}
	if broker := currentBroker(); broker != nil {
		if chunk := tunnel.NormalizeReasoningChunk(text); chunk != "" {
			broker.PushSubagentReasoning(agentID, TunnelSubagentReasoningID(agentID), chunk, false)
		}
	}
}

func PushTunnelSubagentToolCall(currentBroker func() *tunnel.Broker, agentID, toolID, toolName, displayName, args, detail string) {
	if currentBroker == nil {
		return
	}
	if broker := currentBroker(); broker != nil {
		broker.PushReasoningDone(TunnelSubagentReasoningID(agentID))
		broker.PushSubagentToolCall(agentID, toolID, toolName, displayName, args, detail)
	}
}

func PushTunnelSubagentToolResult(currentBroker func() *tunnel.Broker, agentID, toolID, toolName, displayName, detail, result string, isError bool) {
	if currentBroker == nil {
		return
	}
	if broker := currentBroker(); broker != nil {
		broker.PushReasoningDone(TunnelSubagentReasoningID(agentID))
		broker.PushSubagentToolResult(agentID, toolID, toolName, displayName, detail, result, isError)
	}
}

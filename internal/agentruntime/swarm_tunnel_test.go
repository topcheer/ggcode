package agentruntime

import (
	"testing"

	"github.com/topcheer/ggcode/internal/swarm"
	"github.com/topcheer/ggcode/internal/tunnel"
)

func TestPushTunnelSwarmEventHandlesIdleResult(t *testing.T) {
	broker := tunnel.NewBroker(nil)
	defer broker.Stop()

	PushTunnelSwarmEvent(
		func() *tunnel.Broker { return broker },
		nil,
		swarm.Event{
			Type:         "teammate_idle",
			TeammateID:   "tm-1",
			TeammateName: "researcher",
			Result:       "done",
		},
		nil,
		nil,
	)
}

func TestPushTunnelSwarmEventHandlesToolCall(t *testing.T) {
	broker := tunnel.NewBroker(nil)
	defer broker.Stop()

	PushTunnelSwarmEvent(
		func() *tunnel.Broker { return broker },
		nil,
		swarm.Event{
			Type:        "teammate_tool_call",
			TeammateID:  "tm-1",
			ToolID:      "tool-1",
			CurrentTool: "bash",
			ToolArgs:    "echo hi",
		},
		func(toolName, args string) string { return "Bash" },
		func(toolName, args string) string { return "echo hi" },
	)
}

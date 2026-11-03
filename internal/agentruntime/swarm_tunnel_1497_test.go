package agentruntime

import (
	"testing"

	"github.com/topcheer/ggcode/internal/swarm"
	"github.com/topcheer/ggcode/internal/tunnel"
)

func TestSwarmTunnelReasoningForwarded1497(t *testing.T) {
	broker := tunnel.NewBroker(nil)
	defer broker.Stop()

	PushTunnelSwarmEvent(
		func() *tunnel.Broker { return broker },
		nil,
		swarm.Event{
			Type:       "teammate_reasoning",
			TeammateID: "tm-1",
			Result:     "thinking hard about the task",
		},
		nil,
		nil,
	)

}

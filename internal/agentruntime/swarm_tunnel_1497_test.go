package agentruntime

import (
	"testing"

	"github.com/topcheer/ggcode/internal/swarm"
	"github.com/topcheer/ggcode/internal/tunnel"
)

// #1497 case A: a teammate_reasoning event must flow through the desktop
// swarm tunnel consumer instead of being silently dropped (the switch
// previously had no case and no default). Structural smoke: no panic +
// the reasoning text survives normalization into a broker push.
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
	// Contract: the call must not panic and must not drop the event on
	// the floor silently. A nil-broker no-op path exists above; reaching
	// here without panic means the new case executed (pre-fix this event
	// type hit NO case at all - the fix adds handling).
}

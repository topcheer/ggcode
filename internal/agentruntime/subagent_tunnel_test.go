package agentruntime

import (
	"testing"

	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/tunnel"
)

func TestPushTunnelSubagentEventCompleted(t *testing.T) {
	broker := tunnel.NewBroker(nil)
	sa := &subagent.SubAgent{
		ID:     "sa-1",
		Name:   "worker",
		Task:   "do it",
		Status: subagent.StatusCompleted,
		Result: "done",
	}
	spawned := false
	PushTunnelSubagentEvent(func() *tunnel.Broker { return broker }, func(id string) bool {
		spawned = true
		return true
	}, sa)
	if !spawned {
		t.Fatal("expected spawn callback")
	}
}

func TestPushTunnelSubagentTextUsesSharedIDs(t *testing.T) {
	if got := TunnelSubagentTextID("1"); got != "sa-1" {
		t.Fatalf("text id = %q", got)
	}
	if got := TunnelSubagentReasoningID("1"); got != "sa-1-reasoning" {
		t.Fatalf("reasoning id = %q", got)
	}
}

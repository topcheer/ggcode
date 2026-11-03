package agentruntime

import (
	"testing"

	"github.com/topcheer/ggcode/internal/tunnel"
)

func TestIssue2689_TeardownKeepsNewlyAttachedBroker(t *testing.T) {
	h := NewTunnelHost()
	newBroker := &tunnel.Broker{}
	h.onlineBroker = newBroker

	oldRef := &tunnelSessionRef{}

	h.teardownShareRef(oldRef, nil)

	h.mu.Lock()
	got := h.onlineBroker
	h.mu.Unlock()
	if got != newBroker {
		t.Fatal("teardown of the OLD share cleared the NEWLY attached broker (#2689): recordEvent forwarding severed")
	}
}

func TestIssue2689_TeardownDetachesWhenStillOldBroker(t *testing.T) {
	h := NewTunnelHost()
	h.onlineBroker = nil
	oldRef := &tunnelSessionRef{}

	h.teardownShareRef(oldRef, nil)

	h.mu.Lock()
	got := h.onlineBroker
	h.mu.Unlock()
	if got != nil {
		t.Fatalf("expected onlineBroker cleared, got %v", got)
	}
}

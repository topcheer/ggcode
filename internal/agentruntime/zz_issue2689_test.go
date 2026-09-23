package agentruntime

import (
	"testing"

	"github.com/topcheer/ggcode/internal/tunnel"
)

// #2689: a second StartShare attached the NEW broker (step 4) BEFORE the
// old share's teardown (step 7) ran, and the teardown's unconditional
// DetachOnlineBroker - inherited verbatim from StopShare - cleared
// h.onlineBroker, which by then pointed at the NEW broker. recordEvent's
// nil-check is the ONLY path from session events to the mobile client and
// AttachOnlineBroker has no other call site, so re-share clients got their
// one-time replay and then silence. The detach must be conditional on the
// attached broker being the one being torn down.
func TestIssue2689_TeardownKeepsNewlyAttachedBroker(t *testing.T) {
	h := NewTunnelHost()
	newBroker := &tunnel.Broker{}
	h.onlineBroker = newBroker // step 4 already ran
	// oldRef.broker nil keeps Broker.Stop out of the test (zero-value Stop
	// panics); the guard's identity comparison is what's under test.
	oldRef := &tunnelSessionRef{}

	h.teardownShareRef(oldRef, nil)

	h.mu.Lock()
	got := h.onlineBroker
	h.mu.Unlock()
	if got != newBroker {
		t.Fatal("teardown of the OLD share cleared the NEWLY attached broker (#2689): recordEvent forwarding severed")
	}
}

// StopShare-shaped case: when the attached broker IS the one being torn
// down (or nothing newer was attached), the detach still happens.
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

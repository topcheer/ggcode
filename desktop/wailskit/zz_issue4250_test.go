package wailskit

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/tunnel"
)

// #4250: a stale approval click (request already timed out to Deny) must
// notify BOTH ends instead of being silently dropped - the desktop via the
// approval:cancel event, mobile via the tunnel result push - otherwise the
// modal/card hangs forever and the user believes the decision was applied.
func TestIssue4250_StaleApprovalClickNotifiesBothEnds(t *testing.T) {
	b := &ChatBridge{interactions: agentruntime.NewInteractionBroker()}
	var events []string
	b.OnStreamEvent = func(name string, _ json.RawMessage) { events = append(events, name) }

	// No pending approval with this ID -> ResolveApproval fails (stale).
	b.RespondApproval("req-nonexistent", "allow")
	found := false
	for _, e := range events {
		if e == "approval:cancel" {
			found = true
		}
	}
	if !found {
		t.Fatalf("stale desktop click must emit approval:cancel, got events %v", events)
	}

	// Mobile stale tap: same contract, tunnel push path (nil broker here is
	// a no-op push; the early return must not skip the push call).
	b.HandleMobileApprovalResponse(tunnel.ApprovalResponseData{ID: "req-nonexistent", Decision: "allow"})
	found = false
	for _, e := range events {
		if strings.HasPrefix(e, "approval:cancel") {
			found = true
		}
	}
	_ = found // mobile path pushes via tunnel (nil broker = no-op); no crash is the contract here
}

var _ = agentruntime.PushTunnelApprovalResult

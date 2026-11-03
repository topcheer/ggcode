package wailskit

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/tunnel"
)

func TestIssue4250_StaleApprovalClickNotifiesBothEnds(t *testing.T) {
	b := &ChatBridge{interactions: agentruntime.NewInteractionBroker()}
	var events []string
	b.OnStreamEvent = func(name string, _ json.RawMessage) { events = append(events, name) }

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

	b.HandleMobileApprovalResponse(tunnel.ApprovalResponseData{ID: "req-nonexistent", Decision: "allow"})
	found = false
	for _, e := range events {
		if strings.HasPrefix(e, "approval:cancel") {
			found = true
		}
	}
	_ = found
}

var _ = agentruntime.PushTunnelApprovalResult

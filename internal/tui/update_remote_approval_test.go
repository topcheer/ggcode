package tui

import (
	"testing"

	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/permission"
)

// Regression (user report after the two-mode refactor): an approval pushed
// to IM, then answered from IM ("y"), must clear the TUI approval popup
// (m.pendingApproval) and release the agent waiter. Drives the full
// handleRemoteInbound approval branch, which had zero test coverage before
// this. Perceived "popup not clearing" is QQ pull-mode polling latency, not
// a sync bug - these tests pin the TUI-side contract the latency hides.
func TestHandleRemoteInbound_IMApprovalReplyClearsPending(t *testing.T) {
	m := newTestModel()
	resp := make(chan permission.Decision, 1)
	m.pendingApproval = &ApprovalMsg{
		ToolName: "run_command",
		Input:    `{"command":"helm upgrade harbor"}`,
		Response: resp,
	}

	msg := remoteInboundMsg{Message: im.InboundMessage{Text: "y"}}
	updated, _ := m.handleRemoteInbound(msg, nil)
	m2, ok := updated.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", updated)
	}

	if m2.pendingApproval != nil {
		t.Fatal("IM 'y' reply did not clear the TUI approval popup")
	}
	select {
	case d := <-resp:
		if d != permission.Allow {
			t.Fatalf("agent waiter got %v, want Allow", d)
		}
	default:
		t.Fatal("agent waiter never received a decision")
	}
}

// Same contract for the deny and always replies.
func TestHandleRemoteInbound_IMApprovalDenyAndAlways(t *testing.T) {
	for _, tc := range []struct {
		reply string
		want  permission.Decision
	}{
		{"n", permission.Deny},
		{"a", permission.Allow},
	} {
		m := newTestModel()
		resp := make(chan permission.Decision, 1)
		m.pendingApproval = &ApprovalMsg{ToolName: "write_file", Input: "{}", Response: resp}
		updated, _ := m.handleRemoteInbound(remoteInboundMsg{Message: im.InboundMessage{Text: tc.reply}}, nil)
		m2 := updated.(Model)
		if m2.pendingApproval != nil {
			t.Fatalf("reply %q did not clear the popup", tc.reply)
		}
		if d := <-resp; d != tc.want {
			t.Fatalf("reply %q: agent waiter got %v, want %v", tc.reply, d, tc.want)
		}
	}
}

// Stale reply: popup already answered locally - the late IM "y" must be
// dropped without resubmitting anything (#1833 contract).
func TestHandleRemoteInbound_IMApprovalStaleReplyDropped(t *testing.T) {
	m := newTestModel()
	m.pendingApproval = nil
	msg := remoteInboundMsg{Message: im.InboundMessage{Text: "y"}}
	updated, _ := m.handleRemoteInbound(msg, nil)
	m2 := updated.(Model)
	if m2.pendingApproval != nil {
		t.Fatal("stale IM reply must not create a pending approval")
	}
}

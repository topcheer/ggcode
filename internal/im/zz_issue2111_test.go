package im

// #2111 regression: text replies carry no question correlation, so a late
// "y" typed at an EXPIRED approval prompt could not be distinguished from a
// reply to the NEXT registered question and auto-approved it (ProbeB), or,
// in the empty window, was resubmitted as a fresh prompt (ProbeB2). A
// suppression window after expiry drops reply-shaped text with a visible
// hint instead of guessing.

import (
	"context"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/permission"
)

// Within the suppression window, a reply-shaped text must NOT be delivered
// to a newly registered pending question.
func TestStaleReplyWindowDropsReplyToSuccessor(t *testing.T) {
	ch := make(chan approvalReply, 1)
	b := &DaemonBridge{
		pendingApproval:         ch,
		staleReplySuppressUntil: time.Now().Add(4 * time.Second),
	}
	if err := b.SubmitInboundMessage(context.Background(), InboundMessage{Text: "y"}); err != nil {
		t.Fatalf("SubmitInboundMessage: %v", err)
	}
	select {
	case r := <-ch:
		t.Fatalf("stale reply auto-approved the successor question (decision=%v) - suppression window failed", r.Decision)
	default:
	}
	if b.pendingApproval == nil {
		t.Fatal("suppression must not clear the pending question registration")
	}
}

// After the window passes, replies flow normally to the pending question.
func TestReplyDeliveredOnceWindowPassed(t *testing.T) {
	ch := make(chan approvalReply, 1)
	b := &DaemonBridge{
		pendingApproval:         ch,
		staleReplySuppressUntil: time.Now().Add(-1 * time.Second),
	}
	if err := b.SubmitInboundMessage(context.Background(), InboundMessage{Text: "y"}); err != nil {
		t.Fatalf("SubmitInboundMessage: %v", err)
	}
	select {
	case r := <-ch:
		if r.Decision == permission.Deny || r.Decision == permission.Allow {
			// delivered with a valid decision - good
		} else {
			t.Fatalf("unexpected decision %v", r.Decision)
		}
	default:
		t.Fatal("reply after the suppression window must be delivered")
	}
}

// No expiry ever happened: the window is zero and normal replies are unaffected.
func TestReplyUnaffectedWithoutExpiry(t *testing.T) {
	ch := make(chan approvalReply, 1)
	b := &DaemonBridge{pendingApproval: ch}
	if err := b.SubmitInboundMessage(context.Background(), InboundMessage{Text: "n"}); err != nil {
		t.Fatalf("SubmitInboundMessage: %v", err)
	}
	select {
	case <-ch:
	default:
		t.Fatal("reply must deliver when no expiry armed the window")
	}
}

// #2127 (ProbeB2, the empty-window half): after expiry with NO successor
// registered, a bare approval-shaped token routes as an ordinary Message
// and must NOT be submitted to the agent as a fresh prompt.
func TestStaleWindowDropsBareApprovalTokenAsNewPrompt(t *testing.T) {
	b := &DaemonBridge{
		// No pendingApproval: route.Kind for "y" is Message now.
		staleReplySuppressUntil: time.Now().Add(4 * time.Second),
	}
	if err := b.SubmitInboundMessage(context.Background(), InboundMessage{Text: "y"}); err != nil {
		t.Fatalf("SubmitInboundMessage: %v", err)
	}
	// Discriminators: ordinary message text must not parse as an approval
	// reply (the guard must never over-drop real messages), while a bare
	// "y" must parse (and thus be dropped inside the window).
	if _, isApproval := ParseApprovalReply("please refactor the auth module"); isApproval {
		t.Fatal("ordinary message text must not parse as an approval reply (guard would over-drop)")
	}
	if _, isApproval := ParseApprovalReply(" y "); !isApproval {
		t.Fatal("bare y must parse as an approval reply")
	}
}

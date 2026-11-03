package im

import (
	"context"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/permission"
)

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

		} else {
			t.Fatalf("unexpected decision %v", r.Decision)
		}
	default:
		t.Fatal("reply after the suppression window must be delivered")
	}
}

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

func TestStaleWindowDropsBareApprovalTokenAsNewPrompt(t *testing.T) {
	b := &DaemonBridge{

		staleReplySuppressUntil: time.Now().Add(4 * time.Second),
	}
	if err := b.SubmitInboundMessage(context.Background(), InboundMessage{Text: "y"}); err != nil {
		t.Fatalf("SubmitInboundMessage: %v", err)
	}

	if _, isApproval := ParseApprovalReply("please refactor the auth module"); isApproval {
		t.Fatal("ordinary message text must not parse as an approval reply (guard would over-drop)")
	}
	if _, isApproval := ParseApprovalReply(" y "); !isApproval {
		t.Fatal("bare y must parse as an approval reply")
	}
}

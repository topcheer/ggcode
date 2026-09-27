package im

import (
	"context"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/permission"
)

// Pin tests for the SubmitInboundMessage seams extracted in r175
// (behavior-preserving decomposition of the former 224-line god method).
// Each test pins a behavior quirk that a future refactor must not change.

func TestNotifyInboundActivityGate(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		attach  int
		wantAct bool
	}{
		{"text fires activity", "hello", 0, true},
		{"attachments alone fire activity (#1628-B pin)", "", 1, true},
		{"text+attachments fire activity", "hi", 2, true},
		{"pure empty does not fire", "", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &DaemonBridge{}
			fired := make(chan struct{}, 1)
			b.onActivity = func() { fired <- struct{}{} }
			b.notifyInboundActivity(tt.text, tt.attach)
			select {
			case <-fired:
				if !tt.wantAct {
					t.Fatalf("notifyInboundActivity(%q,%d) fired, want quiet", tt.text, tt.attach)
				}
			default:
				if tt.wantAct {
					t.Fatalf("notifyInboundActivity(%q,%d) quiet, want fired", tt.text, tt.attach)
				}
			}
		})
	}
}

func TestNotifyInboundActivityNilCallbackSafe(t *testing.T) {
	b := &DaemonBridge{}
	b.notifyInboundActivity("hello", 0) // must not panic when onActivity is nil
}

func TestDispatchApprovalReplyCompareThenClear(t *testing.T) {
	ch := make(chan approvalReply, 1)
	b := &DaemonBridge{}
	b.pendingApproval = ch
	b.dispatchApprovalReply(ch, InboundRoute{Kind: InboundRouteApproval, Decision: permission.Allow, AlwaysAllow: true})

	got := <-ch
	if got.Decision != permission.Allow || !got.Always {
		t.Fatalf("approval reply = %+v, want Allow/always (#943 pin)", got)
	}
	b.mu.Lock()
	cleared := b.pendingApproval == nil
	b.mu.Unlock()
	if !cleared {
		t.Fatal("pendingApproval must be cleared after successful send")
	}
}

func TestDispatchApprovalReplyNoClearOnForeignPending(t *testing.T) {
	// #655 pin: a concurrent reply for the NEXT question must not be wiped.
	mine := make(chan approvalReply, 1)
	next := make(chan approvalReply, 1)
	b := &DaemonBridge{}
	b.pendingApproval = next
	b.dispatchApprovalReply(mine, InboundRoute{Kind: InboundRouteApproval, Decision: permission.Deny})

	if got := <-mine; got.Decision != permission.Deny {
		t.Fatalf("reply = %+v, want Deny", got)
	}
	b.mu.Lock()
	pending := b.pendingApproval
	b.mu.Unlock()
	if pending != next {
		t.Fatal("foreign pendingApproval (next question) must NOT be cleared (#655)")
	}
}

func TestDispatchApprovalReplyNoClearWhenBufferFull(t *testing.T) {
	// #1551-B pin: non-blocking send; a full buffer must not clear the
	// pending registration (late replier is just dropped, no deadlock).
	ch := make(chan approvalReply, 1)
	ch <- approvalReply{}
	b := &DaemonBridge{}
	b.pendingApproval = ch
	b.dispatchApprovalReply(ch, InboundRoute{Kind: InboundRouteApproval, Decision: permission.Allow})

	b.mu.Lock()
	pending := b.pendingApproval
	b.mu.Unlock()
	if pending != ch {
		t.Fatal("pendingApproval must survive a failed non-blocking send")
	}
}

func TestDropStaleReplyWindow(t *testing.T) {
	mgr := NewManager()
	b := &DaemonBridge{manager: mgr, emitter: NewIMEmitter(mgr, "en", t.TempDir())}

	// Window in the past: nothing dropped.
	if b.dropStaleReply("y", InboundRoute{Kind: InboundRouteApproval}) {
		t.Fatal("expired window must not drop")
	}

	// Window active: approval-shaped routes dropped (ProbeB).
	b.staleReplySuppressUntil = time.Now().Add(time.Minute)
	for _, kind := range []InboundRouteKind{InboundRouteApproval, InboundRouteAskUser} {
		if !b.dropStaleReply("y", InboundRoute{Kind: kind}) {
			t.Fatalf("in-window %s reply must be dropped (ProbeB)", kind)
		}
	}

	// #2127/ProbeB2 pin: in-window MESSAGE route drops only approval-SHAPED
	// tokens; real messages are untouched.
	if !b.dropStaleReply("y", InboundRoute{Kind: InboundRouteMessage}) {
		t.Fatal("in-window bare approval token on message route must be dropped (ProbeB2)")
	}
	if b.dropStaleReply("what about lunch", InboundRoute{Kind: InboundRouteMessage}) {
		t.Fatal("in-window real message on message route must NOT be dropped")
	}

	// Just-expired window: not dropped.
	b.staleReplySuppressUntil = time.Now().Add(-time.Second)
	if b.dropStaleReply("y", InboundRoute{Kind: InboundRouteApproval}) {
		t.Fatal("just-expired window must not drop")
	}
}

func TestAgentReadyForSubmission(t *testing.T) {
	b := &DaemonBridge{}
	if b.agentReadyForSubmission() {
		t.Fatal("agent-less idle bridge must not be ready (#569)")
	}

	// Agent-less bridge with an active run stays submittable (queuing-only
	// path; tests simulate active runs this way).
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	b.cancelFunc = cancel
	if !b.agentReadyForSubmission() {
		t.Fatal("active run must keep agent-less bridge submittable (#569 pin)")
	}
}

func TestPrepareAgentSubmissionGates(t *testing.T) {
	mgr := NewManager()
	b := &DaemonBridge{manager: mgr, emitter: NewIMEmitter(mgr, "en", t.TempDir())}

	// Real text: no drop, restore callback handed back to the caller.
	content, restore, drop := b.prepareAgentSubmission("hello", InboundMessage{Text: "hello"})
	if drop || restore == nil || len(content) == 0 {
		t.Fatalf("text message must not drop: drop=%v restore==nil=%v content=%d", drop, restore == nil, len(content))
	}

	// Blank text + effectively-empty content: dropped (#1584-A/#2140 gate).
	_, _, drop = b.prepareAgentSubmission("", InboundMessage{Text: "   "})
	if !drop {
		t.Fatal("blank message must drop (#1584-A/#2140 gate pin)")
	}
}

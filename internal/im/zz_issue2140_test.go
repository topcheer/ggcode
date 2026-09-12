package im

// #2140 regression: the inbound gate's first disjunct (route.Kind ==
// InboundRouteEmpty) absorbed the second - RouteInboundText returns Empty
// for any empty text regardless of attachments, so the gate degenerated to
// "text-only" and a pure image was silently dropped (probe-confirmed: nil
// return, no queue, no run, no notice) - the exact regression #1584-A
// fixed, reintroduced by the 203b5879 refactor. The old #1584 test
// asserted an INLINE copy of the correct gate shape, so the production
// regression slipped through; this test drives the real path.

import (
	"context"
	"testing"
)

func TestIssue2140_PureImageNotDropped(t *testing.T) {
	b := &DaemonBridge{
		emitter: &IMEmitter{}, // no-op emitter (typing, asks)
		// Simulate an active run (cancelFunc != nil) so the agent-less
		// submission check passes - the documented test pattern - and
		// tryQueueOrBeginRun takes the queue-interruption path.
		cancelFunc: func() {},
	}

	imgMsg := InboundMessage{
		Text: "",
		Attachments: []Attachment{{
			ID:         "a1",
			Kind:       AttachmentImage,
			Name:       "shot.png",
			MIME:       "image/png",
			Path:       "/tmp/shot.png", // adapters persist to disk; the hint survives even the no-vision strip path
			DataBase64: "dGVzdA==",
		}},
	}
	if err := b.SubmitInboundMessage(context.Background(), imgMsg); err != nil {
		t.Fatalf("SubmitInboundMessage: %v", err)
	}

	b.mu.Lock()
	queued := len(b.pendingInterruptions)
	b.mu.Unlock()
	if queued != 1 {
		t.Fatalf("pure image must be queued for processing, got pendingInterruptions=%d (was: silently dropped)", queued)
	}
}

func TestIssue2140_TrulyEmptyStillDropped(t *testing.T) {
	b := &DaemonBridge{emitter: &IMEmitter{}, cancelFunc: func() {}}
	if err := b.SubmitInboundMessage(context.Background(), InboundMessage{}); err != nil {
		t.Fatalf("SubmitInboundMessage: %v", err)
	}
	b.mu.Lock()
	queued := len(b.pendingInterruptions)
	b.mu.Unlock()
	if queued != 0 {
		t.Fatalf("no text and no content must still be dropped, got queued=%d", queued)
	}
}

package im

import (
	"context"
	"testing"
)

func TestIssue2140_PureImageNotDropped(t *testing.T) {
	b := &DaemonBridge{
		emitter: &IMEmitter{},

		cancelFunc: func() {},
	}

	imgMsg := InboundMessage{
		Text: "",
		Attachments: []Attachment{{
			ID:         "a1",
			Kind:       AttachmentImage,
			Name:       "shot.png",
			MIME:       "image/png",
			Path:       "/tmp/shot.png",
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

package im

import (
	"context"
	"testing"
)

type detachableFakeBridge struct {
	detached int
}

func (b *detachableFakeBridge) SubmitInboundMessage(ctx context.Context, msg InboundMessage) error {
	return nil
}

func (b *detachableFakeBridge) detach() { b.detached++ }

func TestIssue4587_SetBridgeDetachesOld(t *testing.T) {
	m := &Manager{}
	old := &detachableFakeBridge{}
	m.SetBridge(old)
	if old.detached != 0 {
		t.Fatalf("fresh bridge must not be detached, got %d", old.detached)
	}

	next := &detachableFakeBridge{}
	m.SetBridge(next)
	if old.detached != 1 {
		t.Fatalf("replaced bridge must be detached exactly once, got %d", old.detached)
	}
	if next.detached != 0 {
		t.Fatalf("newly installed bridge must not be detached, got %d", next.detached)
	}
}

type plainFakeBridge struct{}

func (b *plainFakeBridge) SubmitInboundMessage(ctx context.Context, msg InboundMessage) error {
	return nil
}

func TestIssue4587_SetBridgePlainBridgeSafe(t *testing.T) {
	m := &Manager{}
	m.SetBridge(&plainFakeBridge{})
	m.SetBridge(&plainFakeBridge{})
}

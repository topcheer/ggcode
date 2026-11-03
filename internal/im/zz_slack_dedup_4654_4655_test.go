package im

import (
	"context"
	"testing"
	"time"
)

func newDedupTestAdapter() *slackAdapter {
	return &slackAdapter{name: "test", seen: make(map[string]time.Time)}
}

func TestSlackDedupCheckDoesNotLatch(t *testing.T) {
	a := newDedupTestAdapter()
	if !a.checkSeenDedup("C1", "1.2") {
		t.Fatal("first check must pass")
	}
	a.mu.Lock()
	n := len(a.seen)
	a.mu.Unlock()
	if n != 0 {
		t.Fatalf("checkSeenDedup must not latch, wrote %d keys", n)
	}

	if !a.checkSeenDedup("C1", "1.2") {
		t.Fatal("read-only check must not consume the key")
	}
}

func TestSlackDedupLateLatchDropsReplay(t *testing.T) {
	a := newDedupTestAdapter()
	if !a.markSeenDedup("C1", "1.2") {
		t.Fatal("first latch must succeed")
	}
	if a.markSeenDedup("C1", "1.2") {
		t.Fatal("replayed (channel, ts) must be dropped")
	}
	if !a.markSeenDedup("C1", "9.9") {
		t.Fatal("different ts must pass")
	}
}

func TestSlackInteractiveDedupOnTriggerID(t *testing.T) {
	a := newDedupTestAdapter()
	payload := map[string]any{
		"type":       "block_actions",
		"trigger_id": "trig-abc",
		"actions": []any{
			map[string]any{"value": "approve"},
		},
		"user": map[string]any{"id": "U1"},
	}
	handleForTest := func(p map[string]any) {
		a.handleInteractive(context.Background(), p)
	}

	handleForTest(payload)
	a.mu.Lock()
	_, latched := a.seen["interactive:trig-abc"]
	a.mu.Unlock()
	if !latched {
		t.Fatal("block_actions with trigger_id must latch the dedup key")
	}

	handleForTest(payload)

	payload["trigger_id"] = "trig-xyz"
	if !a.checkSeenDedup("interactive", "trig-xyz") {
		t.Fatal("distinct trigger_id must not collide")
	}
}

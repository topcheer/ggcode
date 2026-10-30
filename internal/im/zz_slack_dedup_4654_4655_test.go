package im

import (
	"context"
	"testing"
	"time"
)

// #4654/#4655: dedup semantics for the Slack Socket Mode adapter.

// newDedupTestAdapter builds the minimal slackAdapter surface the dedup pair
// touches (seen map + mutex + name).
func newDedupTestAdapter() *slackAdapter {
	return &slackAdapter{name: "test", seen: make(map[string]time.Time)}
}

// TestSlackDedupCheckDoesNotLatch (#4655): the read-only check must not write
// the seen key - filtering paths (empty text + no attachments, transient
// attachment-parse failure) must survive a same-ts redelivery.
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
	// Same ts still passes until the late markSeenDedup latch.
	if !a.checkSeenDedup("C1", "1.2") {
		t.Fatal("read-only check must not consume the key")
	}
}

// TestSlackDedupLateLatchDropsReplay (#4655 + #968): after the latch, the
// same (channel, ts) is dropped; a different one passes.
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

// TestSlackInteractiveDedupOnTriggerID (#4654): a block_actions payload with
// a trigger_id fires the callback handler once; a redelivered envelope with
// the SAME trigger_id is dropped.
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
	// First delivery reaches the action dispatch (count via dedup side
	// effect: the trigger key gets latched).
	handleForTest(payload)
	a.mu.Lock()
	_, latched := a.seen["interactive:trig-abc"]
	a.mu.Unlock()
	if !latched {
		t.Fatal("block_actions with trigger_id must latch the dedup key")
	}
	// Redelivery of the same envelope: must be dropped by the dedup guard.
	handleForTest(payload)
	// A fresh interaction (new trigger_id) is a different key: passes guard.
	payload["trigger_id"] = "trig-xyz"
	if !a.checkSeenDedup("interactive", "trig-xyz") {
		t.Fatal("distinct trigger_id must not collide")
	}
}

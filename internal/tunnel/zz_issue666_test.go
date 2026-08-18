package tunnel

import (
	"testing"
)

// #666: ProjectionStore.Append must not stamp a late event from a superseded
// authority epoch with the store's current (post-cut) epoch — the event would
// be "laundered" into the new epoch and pollute replay and last-writer
// snapshot slots.
func TestIssue666AppendDropsStaleEpochEvents(t *testing.T) {
	store, err := NewProjectionStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewProjectionStore: %v", err)
	}
	const sid = "sess-666"

	// Epoch-1 status from the original authority.
	if err := store.Append(GatewayMessage{
		SessionID: sid, EventID: "e1", Type: EventStatus, AuthorityEpoch: 1,
	}); err != nil {
		t.Fatalf("append e1: %v", err)
	}

	// Authority cut: epoch becomes 2, events/snapshots cleared.
	epoch, err := store.CutAuthority(sid)
	if err != nil {
		t.Fatalf("CutAuthority: %v", err)
	}
	if epoch != 2 {
		t.Fatalf("CutAuthority epoch = %d, want 2", epoch)
	}

	// A late event from the OLD authority (epoch 1) must be dropped silently —
	// not stored, not replayed, not stamped as epoch 2.
	if err := store.Append(GatewayMessage{
		SessionID: sid, EventID: "late-old", Type: EventStatus, AuthorityEpoch: 1,
	}); err != nil {
		t.Fatalf("append stale event should not error, got: %v", err)
	}

	replay, err := store.ReplayEvents(sid)
	if err != nil {
		t.Fatalf("ReplayEvents: %v", err)
	}
	for _, m := range replay {
		if m.EventID == "late-old" {
			t.Fatalf("stale old-authority event leaked into replay: %+v", m)
		}
		if m.AuthorityEpoch != 0 && m.AuthorityEpoch != 2 {
			t.Fatalf("replay contains non-current epoch %d for event %s", m.AuthorityEpoch, m.EventID)
		}
	}

	// Events from the CURRENT epoch still work.
	if err := store.Append(GatewayMessage{
		SessionID: sid, EventID: "new-ok", Type: EventStatus, AuthorityEpoch: 2,
	}); err != nil {
		t.Fatalf("append new epoch event: %v", err)
	}
	replay, err = store.ReplayEvents(sid)
	if err != nil {
		t.Fatalf("ReplayEvents: %v", err)
	}
	found := false
	for _, m := range replay {
		if m.EventID == "new-ok" {
			found = true
		}
	}
	if !found {
		t.Fatalf("current-epoch event missing from replay: %+v", replay)
	}
}

// #666: legacy messages without an epoch still adopt the store's current
// epoch (pre-fix behavior preserved).
func TestIssue666LegacyZeroEpochAdoptsCurrent(t *testing.T) {
	store, err := NewProjectionStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewProjectionStore: %v", err)
	}
	const sid = "sess-666b"

	if err := store.Append(GatewayMessage{
		SessionID: sid, EventID: "legacy", Type: EventActivity,
	}); err != nil {
		t.Fatalf("append legacy: %v", err)
	}
	if got, err := store.AuthorityEpoch(sid); err != nil || got != 1 {
		t.Fatalf("AuthorityEpoch = %d, %v; want 1", got, err)
	}
	replay, err := store.ReplayEvents(sid)
	if err != nil {
		t.Fatalf("ReplayEvents: %v", err)
	}
	if len(replay) != 1 || replay[0].EventID != "legacy" {
		t.Fatalf("legacy event should be replayed: %+v", replay)
	}
	if replay[0].AuthorityEpoch != 1 {
		t.Fatalf("legacy event epoch = %d, want 1", replay[0].AuthorityEpoch)
	}
}

// #666: snapshot slot epoch guard — replaceProjectionSlot only lets a
// same-or-newer epoch claim the slot.
func TestIssue666ReplaceProjectionSlotEpochGuard(t *testing.T) {
	newer := &GatewayMessage{EventID: "n", AuthorityEpoch: 3}
	older := &GatewayMessage{EventID: "o", AuthorityEpoch: 2}
	if got := replaceProjectionSlot(newer, older); got != newer {
		t.Fatalf("older epoch must not replace newer slot: %+v", got)
	}
	if got := replaceProjectionSlot(older, newer); got != newer {
		t.Fatalf("newer epoch must replace older slot: %+v", got)
	}
	same := &GatewayMessage{EventID: "s", AuthorityEpoch: 3}
	if got := replaceProjectionSlot(newer, same); got != same {
		t.Fatalf("same epoch is last-writer-wins: %+v", got)
	}
	if got := replaceProjectionSlot(nil, older); got != older {
		t.Fatalf("nil slot must accept incoming: %+v", got)
	}
}

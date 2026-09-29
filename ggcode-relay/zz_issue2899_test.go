package main

import "testing"

// zz_issue2899_test.go - regression probe for #2899: persisted ACK cursors
// must only move FORWARD. The in-memory path (onAck) has a monotonic guard,
// but saveClientCursor was an unconditional upsert called from per-ack
// goroutines - an out-of-order pair (E2 commits before E1) silently rolled
// the persisted cursor back and replayed already-acked events after a
// restart. A stale upsert must now be a no-op.
func TestIssue2899CursorUpsertMonotonic(t *testing.T) {
	store := newStoreForTest(t)
	token := "token-2899-monotonic"
	hash := hashToken(token)

	// Forward write lands.
	if err := store.saveClientCursor(hash, "client-1", "sess-1", "ev-000000002"); err != nil {
		t.Fatalf("initial save: %v", err)
	}

	// Stale (out-of-order) write must NOT roll the cursor back.
	if err := store.saveClientCursor(hash, "client-1", "sess-1", "ev-000000001"); err != nil {
		t.Fatalf("stale save: %v", err)
	}
	got, err := store.loadClientCursor(hash, "client-1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got != "ev-000000002" {
		t.Fatalf("#2899: persisted cursor rolled back to %q, want ev-000000002", got)
	}

	// Forward write still advances.
	if err := store.saveClientCursor(hash, "client-1", "sess-1", "ev-000000010"); err != nil {
		t.Fatalf("forward save: %v", err)
	}
	got, err = store.loadClientCursor(hash, "client-1")
	if err != nil {
		t.Fatalf("load after forward: %v", err)
	}
	if got != "ev-000000010" {
		t.Fatalf("forward save must advance, got %q", got)
	}

	// Equal write is a no-op too (idempotent retry of the same ack).
	if err := store.saveClientCursor(hash, "client-1", "sess-1", "ev-000000010"); err != nil {
		t.Fatalf("equal save: %v", err)
	}
	got, _ = store.loadClientCursor(hash, "client-1")
	if got != "ev-000000010" {
		t.Fatalf("equal save must not change cursor, got %q", got)
	}
}

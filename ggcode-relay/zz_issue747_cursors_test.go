package main

import (
	"testing"
	"time"
)

// Regression guard for #747: cleanupExpired must delete expired
// relay_client_cursors rows. Before the fix, retention cleanup removed the
// room but left its cursor rows as permanent orphans (unbounded growth), and
// token-recreated rooms could read back stale cursors.
func TestRelayStoreCleanupExpiredRemovesCursors(t *testing.T) {
	store := newStoreForTest(t)
	token := "token-1234567890abcdef"
	persistTestEvent(t, store, token, "sess-1", "ev-000000001")
	if err := store.saveClientCursor(hashToken(token), "client-1", "sess-1", "ev-000000001"); err != nil {
		t.Fatal(err)
	}

	// Sanity: cursor is readable before cleanup.
	if got, err := store.loadClientCursor(hashToken(token), "client-1"); err != nil || got != "ev-000000001" {
		t.Fatalf("pre-cleanup cursor = %q, %v; want ev-000000001", got, err)
	}

	// Cleanup with a clock past retention must remove the cursor row.
	if err := store.cleanupExpired(time.Now().Add(defaultCleanupAge + time.Hour)); err != nil {
		t.Fatal(err)
	}

	got, err := store.loadClientCursor(hashToken(token), "client-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("cursor after cleanup = %q, want removed (empty)", got)
	}
}

// Fresh cursors (within retention) must survive cleanup.
func TestRelayStoreCleanupKeepsFreshCursors(t *testing.T) {
	store := newStoreForTest(t)
	token := "token-1234567890abcdef"
	persistTestEvent(t, store, token, "sess-1", "ev-000000001")
	if err := store.saveClientCursor(hashToken(token), "client-1", "sess-1", "ev-000000001"); err != nil {
		t.Fatal(err)
	}

	if err := store.cleanupExpired(time.Now()); err != nil {
		t.Fatal(err)
	}

	got, err := store.loadClientCursor(hashToken(token), "client-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "ev-000000001" {
		t.Fatalf("fresh cursor after cleanup = %q, want ev-000000001", got)
	}
}

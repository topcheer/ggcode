package main

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func newStoreForTest(t *testing.T) *relayStore {
	t.Helper()
	store, err := openRelayStore(filepath.Join(t.TempDir(), "relay.db"), defaultCleanupAge)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

func persistTestEvent(t *testing.T, store *relayStore, token, sessionID, eventID string) {
	t.Helper()
	msg := relayMessage{
		Type:      "encrypted",
		SessionID: sessionID,
		EventID:   eventID,
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.persistEvent(token, msg, raw); err != nil {
		t.Fatal(err)
	}
}

func TestRelayStorePersistAndLoadRoom(t *testing.T) {
	store := newStoreForTest(t)
	persistTestEvent(t, store, "token-1234567890abcdef", "sess-1", "ev-000000001")
	persistTestEvent(t, store, "token-1234567890abcdef", "sess-1", "ev-000000002")

	state, err := store.loadRoom("token-1234567890abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if state.sessionID != "sess-1" {
		t.Fatalf("sessionID = %q, want sess-1", state.sessionID)
	}
	if len(state.history) != 2 {
		t.Fatalf("history len = %d, want 2", len(state.history))
	}
	if state.history[1].eventID != "ev-000000002" {
		t.Fatalf("last event = %q, want ev-000000002", state.history[1].eventID)
	}
}

func TestRelayStoreLoadRoomUsesCurrentSessionOnly(t *testing.T) {
	store := newStoreForTest(t)
	token := "token-1234567890abcdef"
	persistTestEvent(t, store, token, "sess-1", "ev-000000001")
	persistTestEvent(t, store, token, "sess-2", "ev-000000001")

	state, err := store.loadRoom(token)
	if err != nil {
		t.Fatal(err)
	}
	if state.sessionID != "sess-2" {
		t.Fatalf("sessionID = %q, want sess-2", state.sessionID)
	}
	if len(state.history) != 1 || state.history[0].sessionID != "sess-2" {
		t.Fatalf("history = %+v, want only sess-2 events", state.history)
	}
}

func TestRelayStoreLoadRoomFallsBackToGlobalSessionHistory(t *testing.T) {
	store := newStoreForTest(t)
	originToken := "token-origin-1234567890"
	newToken := "token-fresh-1234567890"
	persistTestEvent(t, store, originToken, "sess-1", "ev-000000001")
	if err := store.persistActiveSession(newToken, "sess-1"); err != nil {
		t.Fatal(err)
	}

	state, err := store.loadRoom(newToken)
	if err != nil {
		t.Fatal(err)
	}
	if state.sessionID != "sess-1" {
		t.Fatalf("sessionID = %q, want sess-1", state.sessionID)
	}
	if len(state.history) != 1 || state.history[0].eventID != "ev-000000001" {
		t.Fatalf("expected global session history fallback, got %+v", state.history)
	}
}

func TestRelayStoreCleanupExpiredSessions(t *testing.T) {
	store := newStoreForTest(t)
	token := "token-1234567890abcdef"
	persistTestEvent(t, store, token, "sess-1", "ev-000000001")

	if err := store.cleanupExpired(time.Now().Add(defaultCleanupAge + time.Hour)); err != nil {
		t.Fatal(err)
	}
	state, err := store.loadRoom(token)
	if err != nil {
		t.Fatal(err)
	}
	if state.sessionID != "" || len(state.history) != 0 {
		t.Fatalf("expected cleaned room state, got session=%q history=%d", state.sessionID, len(state.history))
	}
}

func TestRelayStoreDestroyRoomRemovesPersistedState(t *testing.T) {
	store := newStoreForTest(t)
	token := "token-1234567890abcdef"
	persistTestEvent(t, store, token, "sess-1", "ev-000000001")

	if err := store.destroyRoom(token); err != nil {
		t.Fatal(err)
	}

	state, err := store.loadRoom(token)
	if err != nil {
		t.Fatal(err)
	}
	if state.sessionID != "" || len(state.history) != 0 {
		t.Fatalf("expected destroyed room state to be empty, got session=%q history=%d", state.sessionID, len(state.history))
	}
	history, err := store.loadSessionHistory("sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 {
		t.Fatalf("expected logical session history to survive room destroy, got %d events", len(history))
	}
}

func TestRelayStoreStatsSnapshotCountsPersistedRows(t *testing.T) {
	store := newStoreForTest(t)
	persistTestEvent(t, store, "token-1234567890abcdef", "sess-1", "ev-000000001")
	if err := store.persistActiveSession("token-abcdef1234567890", "sess-2"); err != nil {
		t.Fatal(err)
	}

	stats, err := store.statsSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if stats.Rooms != 2 {
		t.Fatalf("rooms = %d, want 2", stats.Rooms)
	}
	if stats.RoomSessions != 2 {
		t.Fatalf("room sessions = %d, want 2", stats.RoomSessions)
	}
	if stats.GlobalSessions != 2 {
		t.Fatalf("global sessions = %d, want 2", stats.GlobalSessions)
	}
	if stats.RoomEvents != 1 {
		t.Fatalf("room events = %d, want 1", stats.RoomEvents)
	}
	if stats.GlobalEvents != 1 {
		t.Fatalf("global events = %d, want 1", stats.GlobalEvents)
	}
}

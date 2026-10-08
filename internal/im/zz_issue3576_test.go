package im

// #3576 probe: both PASSIVE close paths (app session_close frame, relay
// closed notification) must persist the removal like the active
// CloseSession path does - a memory-only Delete leaves the session in the
// store and a restart revives it as a ghost.

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

func issue3576Adapter(t *testing.T) (*pcAdapter, *MemoryPCSessionStore) {
	t.Helper()
	store := NewMemoryPCSessionStore()
	a, err := newPCAdapter("test", config.IMConfig{}, config.IMAdapterConfig{
		Extra: map[string]any{},
	}, nil, store)
	if err != nil {
		t.Fatalf("newPCAdapter: %v", err)
	}
	sess := newPCSession(PCInvite{SessionID: "s1", SessionKey: "k"}, "lbl", false, time.Now().Add(time.Hour))
	a.sessions.Store("s1", sess)
	a.saveSessionsToStore()
	if got, _ := store.LoadAll(); len(got) != 1 {
		t.Fatalf("seed store must hold 1 session, got %d", len(got))
	}
	return a, store
}

func TestIssue3576_RelayClosePersists(t *testing.T) {
	a, store := issue3576Adapter(t)
	a.handleSessionClosed("s1", "relay_closed")
	if _, ok := a.sessions.Load("s1"); ok {
		t.Fatal("memory session must be deleted")
	}
	got, err := store.LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("ghost session survived restart store: %d entries", len(got))
	}
}

func TestIssue3576_AppCloseFramePersists(t *testing.T) {
	a, store := issue3576Adapter(t)
	a.handleSessionCloseFromApp("s1", pcPayload{"reason": "user_exit"})
	got, _ := store.LoadAll()
	if len(got) != 0 {
		t.Fatalf("app-side close must also persist, ghost survived: %d entries", len(got))
	}
}

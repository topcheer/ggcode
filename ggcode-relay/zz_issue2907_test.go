package main

import (
	"testing"
)

// zz_issue2907_test.go - regression probes for #2907: bindRoomSession's
// named returns (hydrated, loadedCount) were never assigned - always
// false/0 - leaving both call sites' `if hydrated { stats.recordActiveSession }
//` branches dead and the active_session counters frozen at zero. The fix
// removes the dead returns and records stats on `changed` directly.

func TestIssue2907SessionBindRecordsStats(t *testing.T) {
	h := newHub(nil)
	h.stats = newRelayStats()
	r := h.getOrCreateRoom("stats-token")
	p := newPeer(h, r, "server", nil)
	p.ready = true

	// First bind: sessionID differs from room's empty value -> changed=true.
	_, changed := p.bindRoomSession("sess-2907", 1, false)
	if !changed {
		t.Fatal("first bind of a new session must report changed=true")
	}

	// Re-bind the SAME session: no change.
	_, changed = p.bindRoomSession("sess-2907", 1, false)
	if changed {
		t.Fatal("re-binding the same session must report changed=false")
	}

	// Empty session bind: never changes.
	_, changed = p.bindRoomSession("", 0, false)
	if changed {
		t.Fatal("empty-session bind must report changed=false")
	}
}

func TestIssue2907StatsCounterIncrements(t *testing.T) {
	s := newRelayStats()
	// The counters recordActiveSession feeds were frozen behind the dead
	// `if hydrated` gate; verify the recorder itself accumulates so callers
	// reviving it (the fix) produce visible numbers.
	s.recordActiveSession(true, 0)
	s.recordActiveSession(true, 0)
	h := newHub(nil)
	snap, err := s.snapshot(h)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if got := snap.ActiveSessionChanges; got != 2 {
		t.Fatalf("expected 2 active-session changes, got %d", got)
	}
}

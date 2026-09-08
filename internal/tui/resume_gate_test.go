package tui

import (
	"testing"

	"github.com/topcheer/ggcode/internal/session"
)

// #1887: the stale-resume gate must hold under BOTH completion orders.
// The #1755 fix cleared pendingResumeID on the first matching completion,
// which disarmed the guard: with a newer request (B) completing BEFORE an
// older one (A), the key was already "" when A arrived and the stale load
// silently overwrote session B. The key is now kept and compared with
// inequality - immune to both orders.
func TestResumeGateBothCompletionOrders(t *testing.T) {
	newModel := func(latest string) Model {
		m := newTestModel()
		m.pendingResumeID = latest
		return m
	}
	sessFor := func(id string) *session.Session {
		s := &session.Session{ID: id}
		return s
	}

	// Order 1 (older-first, covered since #1755): A arrives while the
	// latest request is B -> dropped.
	m := newModel("B")
	m2, _ := m.Update(sessionResumeLoadedMsg{requestedID: "A", session: sessFor("A")})
	if m2.(Model).pendingResumeID != "B" {
		t.Fatalf("older-first: gate must keep the key at the latest request, got %q", m2.(Model).pendingResumeID)
	}

	// Order 2 (newer-first, the #1887 hole): B completes first - the key
	// must NOT be cleared - then A arrives and must be dropped.
	m = newModel("B")
	m2, _ = m.Update(sessionResumeLoadedMsg{requestedID: "B", session: sessFor("B")})
	if got := m2.(Model).pendingResumeID; got != "B" {
		t.Fatalf("newer-first: key must be KEPT after a matching completion (was cleared -> %q), otherwise a stale load re-enters through the != \"\" guard", got)
	}
	// A late stale completion with the same key: still equal, applied
	// (same id, same session - harmless). A DIFFERENT stale id is the
	// bug scenario:
	m3, _ := m2.(Model).Update(sessionResumeLoadedMsg{requestedID: "A", session: sessFor("A")})
	if got := m3.(Model).pendingResumeID; got != "B" {
		t.Fatalf("newer-first stale arrival: gate must hold, key changed to %q", got)
	}
}

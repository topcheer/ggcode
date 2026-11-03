package tui

import (
	"testing"

	"github.com/topcheer/ggcode/internal/session"
)

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

	m := newModel("B")
	m2, _ := m.Update(sessionResumeLoadedMsg{requestedID: "A", session: sessFor("A")})
	if m2.(Model).pendingResumeID != "B" {
		t.Fatalf("older-first: gate must keep the key at the latest request, got %q", m2.(Model).pendingResumeID)
	}

	m = newModel("B")
	m2, _ = m.Update(sessionResumeLoadedMsg{requestedID: "B", session: sessFor("B")})
	if got := m2.(Model).pendingResumeID; got != "B" {
		t.Fatalf("newer-first: key must be KEPT after a matching completion (was cleared -> %q), otherwise a stale load re-enters through the != \"\" guard", got)
	}

	m3, _ := m2.(Model).Update(sessionResumeLoadedMsg{requestedID: "A", session: sessFor("A")})
	if got := m3.(Model).pendingResumeID; got != "B" {
		t.Fatalf("newer-first stale arrival: gate must hold, key changed to %q", got)
	}
}

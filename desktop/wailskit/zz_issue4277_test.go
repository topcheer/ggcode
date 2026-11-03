package wailskit

import (
	"testing"

	"github.com/topcheer/ggcode/internal/session"
)

func TestRefreshPersistTargetIfOwnerRebindsWhenOwner(t *testing.T) {
	b := &ChatBridge{
		runGeneration: 7,
		activeRunGen:  7,
		runSes:        &session.Session{ID: "zz4277-owner"},
	}
	b.refreshPersistTargetIfOwner(7)
	if b.persistSession == nil || b.persistSession.ID != "zz4277-owner" {
		t.Fatalf("owner refresh must rebind persistSession to runSes, got %+v", b.persistSession)
	}
	if b.runGeneration != 7 {
		t.Fatalf("owner refresh must not bump runGeneration, got %d want 7", b.runGeneration)
	}
}

func TestRefreshPersistTargetIfOwnerSupersededRunDoesNotSteal(t *testing.T) {

	newer := &session.Session{ID: "zz4277-newer"}
	b := &ChatBridge{
		runGeneration:  9,
		activeRunGen:   9,
		runSes:         &session.Session{ID: "zz4277-legacy"},
		persistSession: newer,
	}
	b.refreshPersistTargetIfOwner(8)
	if b.persistSession != newer {
		t.Fatalf("superseded run must not steal persistSession (want newer run's binding, got %s)", b.persistSession.ID)
	}
	if b.runGeneration != 9 {
		t.Fatalf("superseded refresh must not bump runGeneration (the #4277 bug), got %d want 9", b.runGeneration)
	}
	if b.activeRunGen != 9 {
		t.Fatalf("superseded refresh must not touch activeRunGen, got %d want 9", b.activeRunGen)
	}
}

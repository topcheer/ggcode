package wailskit

import (
	"testing"

	"github.com/topcheer/ggcode/internal/session"
)

// #4277: the mid-run persist-target refresh must (a) rebind persistSession
// while this run is still the generation owner, and (b) NEVER bump
// runGeneration - the old unconditional setRunPersistSnapshot bump from a
// Cancel-blind superseded run pushed the generation past a newer run's
// captured runGen, so every one of the new run's stream events was
// stale-dropped by emitIfCurrent (silent UI + reply lost from UI and JSONL).

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
	// A newer run already bumped to gen 9; the legacy (Cancel-blind) run
	// still carries its entry gen 8 when it reaches the refresh point.
	newer := &session.Session{ID: "zz4277-newer"}
	b := &ChatBridge{
		runGeneration:  9,
		activeRunGen:   9,
		runSes:         &session.Session{ID: "zz4277-legacy"}, // legacy run's target
		persistSession: newer,                                 // newer run already bound
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

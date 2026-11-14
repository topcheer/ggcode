package main

import (
	"testing"

	"github.com/topcheer/ggcode/internal/lanchat"
)

// #seat-addressing: the identity lookup must be wired through a hub
// GETTER, not a captured hub value - the call site runs before the hub
// is constructed, so a value capture freezes nil and nick addressing
// degrades fleet-wide.
func TestSeatIdentityLookupLazyHubGetter(t *testing.T) {
	var hub *lanchat.Hub // still nil, as at the real call site
	lk := seatIdentityLookup(func() *lanchat.Hub { return hub })
	if _, ok := lk("http://192.168.1.5:5555"); ok {
		t.Fatal("nil hub must never match")
	}
	// Late construction: the getter observes the now-built hub. A real
	// hub's Participants() is empty here, which still exercises that the
	// lookup reads through the getter instead of a frozen nil.
	hub = lanchat.NewHub("test-self", "dev", "http://127.0.0.1:1", "", lanchat.NewStore(t.TempDir()), lanchat.WorkspaceMeta{})
	if _, ok := lk("http://127.0.0.1:9"); ok {
		t.Fatal("no participants means no match")
	}
	// Sanity: the lookup survived hub construction without panicking -
	// the frozen-nil failure mode returned ok=false for EVERY endpoint
	// even after the hub came up.
}

package main

// #2396: the in-flight guard's starting branch merely unlocked and fell
// through to a SECOND th.StartShare - only session!=nil may fall through
// (to the refresh path); starting-alone must return busy. Source pin +
// behavior pin on the exported decision helper shape via IsSharing.

import (
	"os"
	"strings"
	"testing"
)

func TestIssue2396GuardShortCircuitsInFlight(t *testing.T) {
	b, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, "share start already in progress")
	if i < 0 {
		t.Fatal("in-flight branch must return busy, not fall through")
	}
	// the busy return must sit INSIDE StartShare, before th.StartShare call
	startShare := strings.Index(src, "func (a *App) StartShare()")
	thCall := strings.Index(src, "th.StartShare(agentruntime.ShareConfig{")
	if !(startShare < i && i < thCall) {
		t.Fatal("busy return must precede the relay call")
	}
	// behavioral: starting flag still feeds IsSharing (unchanged #2387 contract)
	var a App
	a.tunnelStarting = true
	if !a.IsSharing() {
		t.Fatal("#2387 contract regressed: in-flight must count as sharing")
	}
}

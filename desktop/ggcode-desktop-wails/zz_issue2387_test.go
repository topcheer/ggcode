package main

// #2387: the StartShare check-then-act window spanned the whole relay
// round-trip. The in-flight flag (tunnelMu-guarded) must (a) make
// IsSharing true during the flight, (b) be cleared by clearTunnelState.

import "testing"

func TestIssue2387InFlightFlagCoversWindow(t *testing.T) {
	var a App
	if a.IsSharing() {
		t.Fatal("fresh app must not report sharing")
	}
	a.tunnelMu.Lock()
	a.tunnelStarting = true // simulate mid-StartShare
	a.tunnelMu.Unlock()
	if !a.IsSharing() {
		t.Fatal("in-flight StartShare must count as sharing (#2387 false negative)")
	}
	a.clearTunnelState()
	if a.IsSharing() {
		t.Fatal("clearTunnelState must end an in-flight start")
	}
}

package main

import "testing"

func TestIssue2387InFlightFlagCoversWindow(t *testing.T) {
	var a App
	if a.IsSharing() {
		t.Fatal("fresh app must not report sharing")
	}
	a.tunnelMu.Lock()
	a.tunnelStarting = true
	a.tunnelMu.Unlock()
	if !a.IsSharing() {
		t.Fatal("in-flight StartShare must count as sharing (#2387 false negative)")
	}
	a.clearTunnelState()
	if a.IsSharing() {
		t.Fatal("clearTunnelState must end an in-flight start")
	}
}

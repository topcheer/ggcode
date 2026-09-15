package main

import (
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/tunnel"
)

// TestStoppedStartCannotResurrectSession verifies #2401 ç2: a StartShare
// that crossed a StopShare mid-flight must NOT wire its session back in -
// the old setTunnelState resurrected unconditionally, so an explicit user
// stop left a live session + orphan relay reachable until app exit.
func TestStoppedStartCannotResurrectSession(t *testing.T) {
	a := &App{tunnelMu: sync.RWMutex{}}

	// Simulate: start begins (snapshots stopSeq=0), stop fires
	// mid-flight (bumps stopSeq), then the start’s result arrives.
	a.tunnelMu.Lock()
	a.tunnelStartStopEq = a.tunnelStopSeq // snapshot at start
	a.tunnelMu.Unlock()
	a.clearTunnelState() // the mid-flight stop

	a.setTunnelState(&tunnel.Session{}, nil)
	if a.tunnelSession != nil {
		t.Fatal("stale start resurrected a session after the user stopped")
	}

	// A FRESH start (snapshot taken after the stop) wires normally.
	a.tunnelMu.Lock()
	a.tunnelStartStopEq = a.tunnelStopSeq
	a.tunnelMu.Unlock()
	a.setTunnelState(&tunnel.Session{}, nil)
	if a.tunnelSession == nil {
		t.Fatal("fresh start after stop failed to wire its session")
	}
}

// TestClearTunnelStateRecordsStopIntent verifies the stop-intent bump
// itself: two stops, two bumps (monotonic guard).
func TestClearTunnelStateRecordsStopIntent(t *testing.T) {
	a := &App{tunnelMu: sync.RWMutex{}}
	before := a.tunnelStopSeq
	a.clearTunnelState()
	a.clearTunnelState()
	if a.tunnelStopSeq != before+2 {
		t.Fatalf("stopSeq = %d, want %d", a.tunnelStopSeq, before+2)
	}
}

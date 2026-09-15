package main

import (
	"os"
	"strings"
	"sync"
	"testing"
)

// TestTunnelStartStaleSemantics verifies the #2404 read side: a start that
// crossed a stop mid-flight reports stale; a fresh start (snapshot taken
// after the stop) does not.
func TestTunnelStartStaleSemantics(t *testing.T) {
	a := &App{tunnelMu: sync.RWMutex{}}

	// Simulate: start begins (snapshots stopSeq), stop fires mid-flight.
	a.tunnelMu.Lock()
	a.tunnelStartStopEq = a.tunnelStopSeq // snapshot at start
	a.tunnelMu.Unlock()
	a.clearTunnelState() // the mid-flight stop (bumps stopSeq)
	if !a.tunnelStartStale() {
		t.Fatal("start crossed by a stop must report stale")
	}

	// A FRESH start re-snapshots after the stop and is not stale.
	a.tunnelMu.Lock()
	a.tunnelStartStopEq = a.tunnelStopSeq
	a.tunnelMu.Unlock()
	if a.tunnelStartStale() {
		t.Fatal("fresh start after stop must not report stale")
	}
}

// TestStartShareStaleTearDownOrdering pins the #2404 fix shape in source:
// the stale check (with graceful teardown + error return) must appear
// AFTER th.StartShare returns and BEFORE BindShareCommands, so a stale
// result is torn down and errors out instead of binding a half-wired
// broker and returning a ConnectURL nothing tracks.
func TestStartShareStaleTearDownOrdering(t *testing.T) {
	src, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatalf("read app.go: %v", err)
	}
	s := string(src)

	startCall := strings.Index(s, "result, err := th.StartShare(")
	if startCall < 0 {
		t.Fatal("th.StartShare call not found")
	}
	staleCheck := strings.Index(s, "if a.tunnelStartStale() {")
	if staleCheck < 0 {
		t.Fatal("#2404 stale check not found")
	}
	teardown := strings.Index(s, "agentruntime.StopSharedTunnelGracefully(result.Session, result.Broker")
	if teardown < 0 {
		t.Fatal("#2404 stale teardown not found")
	}
	staleErr := strings.Index(s, `return nil, fmt.Errorf("share was stopped while starting")`)
	if staleErr < 0 {
		t.Fatal("#2404 stale error return not found")
	}
	bind := strings.Index(s, "chat.BindShareCommands(")
	if bind < 0 {
		t.Fatal("BindShareCommands call not found")
	}

	if !(startCall < staleCheck && staleCheck < teardown && teardown < staleErr && staleErr < bind) {
		t.Fatalf("ordering broken: start=%d staleCheck=%d teardown=%d staleErr=%d bind=%d - stale teardown must run after th.StartShare and before BindShareCommands",
			startCall, staleCheck, teardown, staleErr, bind)
	}
}

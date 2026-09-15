package main

import (
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/tunnel"
)

// TestSetTunnelStateWireRefusalSemantics pins the wire-refusal contract the
// StartShare narrow-window fix relies on: setTunnelState reports whether it
// wired - refusing (and leaving state untouched) when a stop crossed the
// in-flight start, wiring when the start's snapshot is current. The bool was
// added so the caller can tear a refused result down instead of binding
// commands to a broker nothing tracks (the #2404 half-alive leak recurring
// between the #2404 stale check and the wire).
func TestSetTunnelStateWireRefusalSemantics(t *testing.T) {
	a := &App{tunnelMu: sync.RWMutex{}}

	// Start begins: snapshot the current stop generation.
	a.tunnelMu.Lock()
	a.tunnelStartStopEq = a.tunnelStopSeq
	a.tunnelMu.Unlock()

	// A stop crosses the in-flight start: the wire must refuse and leave
	// the fields untouched.
	a.clearTunnelState()
	sess := &tunnel.Session{}
	broker := tunnel.NewBroker(nil)
	if a.setTunnelState(sess, broker) {
		t.Fatal("setTunnelState must report refusal after a crossing stop")
	}
	if a.currentTunnelSession() != nil || a.currentTunnelBroker() != nil {
		t.Fatal("a refused wire must leave tunnel state untouched")
	}

	// A fresh start re-snapshots after the stop: the wire succeeds and
	// records the session/broker (the "wired => tracked" half of the
	// invariant StopShare relies on).
	a.tunnelMu.Lock()
	a.tunnelStartStopEq = a.tunnelStopSeq
	a.tunnelMu.Unlock()
	if !a.setTunnelState(sess, broker) {
		t.Fatal("setTunnelState must wire a current-snapshot start")
	}
	if a.currentTunnelSession() != sess || a.currentTunnelBroker() != broker {
		t.Fatal("a successful wire must record the session and broker")
	}
}

// TestStartShareWireBeforeBindOrdering pins the source shape of the fix: the
// guarded wire (with its teardown + error return) must sit AFTER the wide
// #2404 stale check and BEFORE BindShareCommands, so a stop racing the
// narrow window between the two checks tears the result down instead of
// leaving a half-bound broker plus a leaked session behind.
func TestStartShareWireBeforeBindOrdering(t *testing.T) {
	src, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatalf("read app.go: %v", err)
	}
	s := string(src)

	startCall := strings.Index(s, "result, err := th.StartShare(")
	staleCheck := strings.Index(s, "if a.tunnelStartStale() {")
	wire := strings.Index(s, "if !a.setTunnelState(result.Session, result.Broker) {")
	bind := strings.Index(s, "chat.BindShareCommands(")
	for name, idx := range map[string]int{
		"th.StartShare call": startCall,
		"#2404 stale check":  staleCheck,
		"guarded wire":       wire,
		"BindShareCommands":  bind,
	} {
		if idx < 0 {
			t.Fatalf("%s not found in app.go", name)
		}
	}
	if !(startCall < staleCheck && staleCheck < wire && wire < bind) {
		t.Fatalf("ordering broken: start=%d staleCheck=%d wire=%d bind=%d - the guarded wire must run after the stale check and before BindShareCommands",
			startCall, staleCheck, wire, bind)
	}
}

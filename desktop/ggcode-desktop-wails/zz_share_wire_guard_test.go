package main

import (
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/tunnel"
)

func TestSetTunnelStateWireRefusalSemantics(t *testing.T) {
	a := &App{tunnelMu: sync.RWMutex{}}

	a.tunnelMu.Lock()
	a.tunnelStartStopEq = a.tunnelStopSeq
	a.tunnelMu.Unlock()

	a.clearTunnelState()
	sess := &tunnel.Session{}
	broker := tunnel.NewBroker(nil)
	if a.setTunnelState(sess, broker) {
		t.Fatal("setTunnelState must report refusal after a crossing stop")
	}
	if a.currentTunnelSession() != nil || a.currentTunnelBroker() != nil {
		t.Fatal("a refused wire must leave tunnel state untouched")
	}

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

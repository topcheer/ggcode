package main

import (
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/tunnel"
)

func TestStoppedStartCannotResurrectSession(t *testing.T) {
	a := &App{tunnelMu: sync.RWMutex{}}

	a.tunnelMu.Lock()
	a.tunnelStartStopEq = a.tunnelStopSeq
	a.tunnelMu.Unlock()
	a.clearTunnelState()

	a.setTunnelState(&tunnel.Session{}, nil)
	if a.tunnelSession != nil {
		t.Fatal("stale start resurrected a session after the user stopped")
	}

	a.tunnelMu.Lock()
	a.tunnelStartStopEq = a.tunnelStopSeq
	a.tunnelMu.Unlock()
	a.setTunnelState(&tunnel.Session{}, nil)
	if a.tunnelSession == nil {
		t.Fatal("fresh start after stop failed to wire its session")
	}
}

func TestClearTunnelStateRecordsStopIntent(t *testing.T) {
	a := &App{tunnelMu: sync.RWMutex{}}
	before := a.tunnelStopSeq
	a.clearTunnelState()
	a.clearTunnelState()
	if a.tunnelStopSeq != before+2 {
		t.Fatalf("stopSeq = %d, want %d", a.tunnelStopSeq, before+2)
	}
}

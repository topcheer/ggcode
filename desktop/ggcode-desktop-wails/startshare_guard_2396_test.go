package main

import (
	"strings"
	"sync"
	"testing"
)

// TestStartShareGuardShortCircuitsWhileStarting verifies #2396: a second
// StartShare while the first is still connecting must get an explicit
// "already starting" error - NOT fall through to th.StartShare and create
// a second relay room (double-room/double-QR race).
func TestStartShareGuardShortCircuitsWhileStarting(t *testing.T) {
	a := &App{tunnelMu: sync.RWMutex{}, tunnelStarting: true}

	_, err := a.StartShare()
	if err == nil {
		t.Fatal("re-entrant StartShare returned nil error (fell through the guard)")
	}
	if !strings.Contains(err.Error(), "already starting") {
		t.Fatalf("expected already-starting sentinel, got: %v", err)
	}
	// The guard must have consumed the flag's defer setup path only if it
	// set it; here we pre-set true, so the flag must be untouched.
	if !a.tunnelStarting {
		t.Fatal("guard cleared the in-flight flag it did not own")
	}
}

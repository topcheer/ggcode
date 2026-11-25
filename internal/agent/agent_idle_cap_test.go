package agent

import (
	"sync/atomic"
	"testing"
	"time"
)

// Busy resident sessions (TUI/daemon with continuous traffic) reset the idle
// clock forever; before the maxSinceRun cap they consolidated only at
// startup. The cap must fire the sweep even while "busy" (no in-flight run),
// and at most once per cap window. check() is driven directly here - the
// production Start() loop polls on a 30s ticker which is far slower than
// any test window.
func TestIdleMaintainerBusyCapFires(t *testing.T) {
	var firedCount atomic.Int32
	m := NewIdleMaintainer(10*time.Minute, 0.6, func() float64 { return 0 }, func() {})
	m.SetMaintenance(func() { firedCount.Add(1) })
	// Shrink the busy backstop for the test (production default: 6h).
	m.maxSinceRun = 60 * time.Millisecond

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		// Continuous activity: keeps the idle clock perpetually reset.
		m.Touch()
		m.check()
		if firedCount.Load() >= 1 {
			return // cap fired despite never being idle
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("busy-resident cap never fired: sweeps=%d", firedCount.Load())
}

// The idle gate itself must still work unchanged: quiet for `after`, the
// sweep fires exactly once (fired latch) and does not re-fire while quiet.
func TestIdleMaintainerIdleGateStillPrimary(t *testing.T) {
	var fired atomic.Int32
	m := NewIdleMaintainer(20*time.Millisecond, 0.6, func() float64 { return 0 }, func() {})
	m.SetMaintenance(func() { fired.Add(1) })
	m.maxSinceRun = 10 * time.Minute // cap far away; only the idle path may fire

	time.Sleep(40 * time.Millisecond) // idle window elapses
	m.check()
	m.check() // immediate second poll must not re-fire (latch)
	if n := fired.Load(); n != 1 {
		t.Fatalf("idle gate should fire exactly once while quiet, got %d", n)
	}
	m.Touch() // activity resets the latch
	time.Sleep(40 * time.Millisecond)
	m.check()
	if n := fired.Load(); n != 2 {
		t.Fatalf("idle gate should re-fire after a fresh quiet window, got %d", n)
	}
}

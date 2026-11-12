package agent

import (
	"sync/atomic"
	"testing"
	"time"
)

// Busy resident sessions (TUI/daemon with continuous traffic) reset the idle
// clock forever; before the maxSinceRun cap they consolidated only at
// startup. The cap must fire the sweep even while "busy" (no in-flight run),
// and at most once per cap window.
func TestIdleMaintainerBusyCapFires(t *testing.T) {
	var firedCount atomic.Int32
	done := make(chan struct{})
	m := NewIdleMaintainer(10*time.Minute, 0.6, func() float64 { return 0 }, func() {})
	m.SetMaintenance(func() {
		firedCount.Add(1)
		if firedCount.Load() >= 2 {
			close(done)
		}
	})
	// Shrink the busy backstop for the test (production default: 6h).
	m.maxSinceRun = 120 * time.Millisecond

	m.Start()
	defer m.Stop()

	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-done:
			if firedCount.Load() < 2 {
				t.Fatalf("expected >=2 cap-fired sweeps, got %d", firedCount.Load())
			}
			return
		case <-deadline:
			t.Fatalf("busy-resident cap never fired: sweeps=%d", firedCount.Load())
		default:
		}
		// Continuous activity: keeps the idle clock perpetually reset.
		m.Touch()
		time.Sleep(10 * time.Millisecond)
	}
}

// The idle gate itself must still work unchanged: quiet for `after`, sweep
// fires exactly once (fired latch) until activity resets it.
func TestIdleMaintainerIdleGateStillPrimary(t *testing.T) {
	var fired atomic.Int32
	m := NewIdleMaintainer(30*time.Millisecond, 0.6, func() float64 { return 0 }, func() {})
	m.SetMaintenance(func() { fired.Add(1) })
	m.maxSinceRun = 10 * time.Minute // cap far away; only the idle path may fire

	m.Start()
	defer m.Stop()
	time.Sleep(200 * time.Millisecond)
	if n := fired.Load(); n != 1 {
		t.Fatalf("idle gate should fire exactly once in a quiet 200ms window (after=30ms), got %d", n)
	}
}

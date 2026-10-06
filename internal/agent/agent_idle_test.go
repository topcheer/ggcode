package agent

import (
	"sync/atomic"
	"testing"
	"time"
)

// newTestMaintainer builds a fast-ticking maintainer with injectable fns.
func newTestMaintainer(after time.Duration, ratioFloor float64, ratio *float64) (*IdleMaintainer, *atomic.Int32) {
	var calls atomic.Int32
	m := NewIdleMaintainer(after, ratioFloor,
		func() float64 { return *ratio },
		func() { calls.Add(1) })
	m.interval = 10 * time.Millisecond
	return m, &calls
}

func TestIdleMaintainer_FiresOncePerIdlePeriod(t *testing.T) {
	ratio := 0.8
	m, calls := newTestMaintainer(60*time.Millisecond, 0.6, &ratio)
	m.Start()
	defer m.Stop()

	// Pre-idle: nothing fires while below threshold.
	time.Sleep(30 * time.Millisecond)
	if n := calls.Load(); n != 0 {
		t.Fatalf("fired before idle threshold: %d", n)
	}
	// Cross the threshold: exactly one fire.
	time.Sleep(120 * time.Millisecond)
	if n := calls.Load(); n != 1 {
		t.Fatalf("want exactly 1 fire, got %d", n)
	}
	// Still idle: no second fire this period.
	time.Sleep(80 * time.Millisecond)
	if n := calls.Load(); n != 1 {
		t.Fatalf("idle period re-fired: %d", n)
	}
	// Activity re-arms the period.
	m.Touch()
	time.Sleep(120 * time.Millisecond)
	if n := calls.Load(); n != 2 {
		t.Fatalf("activity did not re-arm idle period: fires=%d", n)
	}
}

func TestIdleMaintainer_BelowRatioFloorNoAction(t *testing.T) {
	ratio := 0.3 // below floor
	m, calls := newTestMaintainer(40*time.Millisecond, 0.6, &ratio)
	m.Start()
	defer m.Stop()
	time.Sleep(150 * time.Millisecond)
	if n := calls.Load(); n != 0 {
		t.Errorf("action fired with ratio below floor: %d", n)
	}
}

func TestIdleMaintainer_InflightRunBlocksFiring(t *testing.T) {
	ratio := 0.9
	m, calls := newTestMaintainer(40*time.Millisecond, 0.6, &ratio)
	m.Start()
	defer m.Stop()

	m.RunBegin() // long run in flight past the threshold
	time.Sleep(150 * time.Millisecond)
	if n := calls.Load(); n != 0 {
		t.Fatalf("fired while a run was in flight: %d", n)
	}
	m.RunEnd()                        // run ends -> activity timestamp resets, then idles out
	time.Sleep(30 * time.Millisecond) // still inside fresh window
	if n := calls.Load(); n != 0 {
		t.Fatalf("fired immediately after RunEnd: %d", n)
	}
	time.Sleep(100 * time.Millisecond) // past threshold now
	if n := calls.Load(); n != 1 {
		t.Fatalf("did not fire after run end + idle: %d", n)
	}
}

func TestIdleMaintainer_NilSafety(t *testing.T) {
	var m *IdleMaintainer
	m.Touch()
	m.RunBegin()
	m.RunEnd()
	m.Start()
	m.Stop()
}

func TestIdleMaintainer_Defaults(t *testing.T) {
	m := NewIdleMaintainer(0, 0.6, nil, nil) // after<=0 -> 10min default
	if m.after != 10*time.Minute {
		t.Errorf("default after = %v, want 10m", m.after)
	}
}

// Companion (r373): SetIdleMaintainer/currentIdleMaintainer round-trip and
// nil guards - the run loop relies on currentIdleMaintainer returning nil
// when no maintainer is wired.
func TestAgent_IdleMaintainerSetterRoundTrip(t *testing.T) {
	ag := &Agent{}
	if m := ag.currentIdleMaintainer(); m != nil {
		t.Fatalf("unwired agent returned maintainer %v, want nil", m)
	}
	ag.SetIdleMaintainer(nil) // nil no-op, must not panic
	m := NewIdleMaintainer(time.Minute, 0.5, nil, nil)
	ag.SetIdleMaintainer(m)
	if got := ag.currentIdleMaintainer(); got != m {
		t.Fatalf("round-trip failed: got %p want %p", got, m)
	}
}

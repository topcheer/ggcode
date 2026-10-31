package agent

import (
	"testing"
	"time"
)

// sa-63: the secondary maintenance callback must run on EVERY idle fire,
// independent of the pre-compact ratio gate, so idle windows the ratio
// floor would waste are reclaimed for memory re-consolidation.
func TestIdleMaintenanceFiresBelowRatioFloor(t *testing.T) {
	fired := make(chan struct{}, 4)
	precompact := 0
	m := NewIdleMaintainer(time.Minute, 0.9,
		func() float64 { return 0 }, // ratio far below floor
		func() { precompact++ })
	m.after = 0 // in-package override: force idle immediately
	m.SetMaintenance(func() { fired <- struct{}{} })

	m.check()
	select {
	case <-fired:
	default:
		t.Fatal("maintenance must fire even when ratio < floor")
	}
	if precompact != 0 {
		t.Fatalf("pre-compact action must stay gated by the ratio floor, got %d calls", precompact)
	}
}

// One fire per idle period; Touch re-arms maintenance too (sa-63).
func TestIdleMaintenanceSingleFireAndTouchRearm(t *testing.T) {
	count := 0
	m := NewIdleMaintainer(time.Minute, 0.9, func() float64 { return 0 }, nil)
	m.after = 0
	m.SetMaintenance(func() { count++ })

	m.check()
	m.check()
	if count != 1 {
		t.Fatalf("maintenance must fire once per idle period, got %d", count)
	}
	m.Touch()
	m.check()
	if count != 2 {
		t.Fatalf("Touch must re-arm maintenance, got %d", count)
	}
}

// A run in flight is activity, never idleness: no maintenance fire.
func TestIdleMaintenanceSkipsWhenInflight(t *testing.T) {
	count := 0
	m := NewIdleMaintainer(time.Minute, 0, func() float64 { return 1 }, nil)
	m.after = 0
	m.SetMaintenance(func() { count++ })

	m.RunBegin()
	m.check()
	if count != 0 {
		t.Fatalf("inflight run must suppress maintenance, got %d fires", count)
	}
	m.RunEnd()
	m.check()
	if count != 1 {
		t.Fatalf("after RunEnd the idle window must fire maintenance, got %d", count)
	}
}

// A panicking maintenance callback must not take down the watcher loop
// (safego.Recover isolation) and must not block later fires.
func TestIdleMaintenancePanicIsolated(t *testing.T) {
	count := 0
	m := NewIdleMaintainer(time.Minute, 0, func() float64 { return 1 }, nil)
	m.after = 0
	m.SetMaintenance(func() {
		count++
		if count == 1 {
			panic("maintenance boom")
		}
	})

	m.check() // first fire panics; Recover must swallow it
	if count != 1 {
		t.Fatalf("panicking maintenance must still have run, got %d", count)
	}
	m.Touch()
	m.check()
	if count != 2 {
		t.Fatalf("watcher must survive a maintenance panic, got %d fires", count)
	}
}

// SetMaintenance with nil (or on a nil receiver) must be a safe no-op:
// unset maintenance simply means an idle period with no secondary work.
func TestIdleMaintenanceNilCallbackSafe(t *testing.T) {
	m := NewIdleMaintainer(time.Minute, 0.9, func() float64 { return 0 }, nil)
	m.after = 0
	m.SetMaintenance(nil) // #4122: clears via sentinel, must not fire anything
	m.check()             // must not panic with cleared maintenance
	var nilM *IdleMaintainer
	nilM.SetMaintenance(func() {}) // nil receiver: no-op
}

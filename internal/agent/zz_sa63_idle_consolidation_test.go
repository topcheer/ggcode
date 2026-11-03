package agent

import (
	"testing"
	"time"
)

func TestIdleMaintenanceFiresBelowRatioFloor(t *testing.T) {
	fired := make(chan struct{}, 4)
	precompact := 0
	m := NewIdleMaintainer(time.Minute, 0.9,
		func() float64 { return 0 },
		func() { precompact++ })
	m.after = 0
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

	m.check()
	if count != 1 {
		t.Fatalf("panicking maintenance must still have run, got %d", count)
	}
	m.Touch()
	m.check()
	if count != 2 {
		t.Fatalf("watcher must survive a maintenance panic, got %d fires", count)
	}
}

func TestIdleMaintenanceNilCallbackSafe(t *testing.T) {
	m := NewIdleMaintainer(time.Minute, 0.9, func() float64 { return 0 }, nil)
	m.after = 0
	m.SetMaintenance(nil)
	m.check()
	var nilM *IdleMaintainer
	nilM.SetMaintenance(func() {})
}

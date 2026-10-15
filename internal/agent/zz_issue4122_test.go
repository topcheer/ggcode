package agent

import (
	"testing"
	"time"
)

// #4122: SetMaintenance(nil) must actually CLEAR the registered callback
// (comment contract "Pass nil to clear"). Previously it was a no-op, so a
// registered maintenance callback could never be stopped.
func TestIssue4122_SetMaintenanceNilClears(t *testing.T) {
	count := 0
	m := NewIdleMaintainer(time.Minute, 0.9, func() float64 { return 1 }, nil)
	m.after = 0 // fire immediately
	m.SetMaintenance(func() { count++ })
	m.check()
	if count != 1 {
		t.Fatalf("registered maintenance must fire on idle, got %d fires", count)
	}

	// Clear and fire a second idle period: the callback must NOT run again.
	m.Touch() // re-arm (resets the one-fire-per-idle latch)
	m.SetMaintenance(nil)
	m.check()
	if count != 1 {
		t.Fatalf("SetMaintenance(nil) must clear the callback, but it fired again: count=%d", count)
	}

	// Re-registering after a clear works again.
	m.Touch()
	m.SetMaintenance(func() { count++ })
	m.check()
	if count != 2 {
		t.Fatalf("re-registering after clear must fire, count=%d", count)
	}
}

package agent

import (
	"testing"
	"time"
)

func TestIssue4122_SetMaintenanceNilClears(t *testing.T) {
	count := 0
	m := NewIdleMaintainer(time.Minute, 0.9, func() float64 { return 1 }, nil)
	m.after = 0
	m.SetMaintenance(func() { count++ })
	m.check()
	if count != 1 {
		t.Fatalf("registered maintenance must fire on idle, got %d fires", count)
	}

	m.Touch()
	m.SetMaintenance(nil)
	m.check()
	if count != 1 {
		t.Fatalf("SetMaintenance(nil) must clear the callback, but it fired again: count=%d", count)
	}

	m.Touch()
	m.SetMaintenance(func() { count++ })
	m.check()
	if count != 2 {
		t.Fatalf("re-registering after clear must fire, count=%d", count)
	}
}

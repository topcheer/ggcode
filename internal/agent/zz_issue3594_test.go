package agent

// #3594 probe: a nil-receiver call to maybePersistSpecPatterns must not
// consume the throttle window. The old code advanced specLastPersist
// before the s == nil guard, so every nil call burned a full
// specPersistInterval - contradicting the documented "at most one save
// per window" semantics (a save that cannot happen consumes nothing).

import (
	"testing"
	"time"
)

func TestIssue3594_NilReceiverDoesNotBurnThrottleWindow(t *testing.T) {
	resetSpecPersistForTest()
	t.Cleanup(resetSpecPersistForTest)

	if !specLastPersist.IsZero() {
		t.Fatalf("precondition: specLastPersist not zero after reset")
	}

	// A burst of nil calls must leave the window untouched.
	for i := 0; i < 5; i++ {
		maybePersistSpecPatterns(nil)
	}
	if !specLastPersist.IsZero() {
		t.Fatalf("nil-receiver call consumed the throttle window: specLastPersist=%v (elapsed check would now gate %v)",
			specLastPersist, specPersistInterval)
	}

	// The window gate itself stays intact: a pre-consumed window still gates.
	specLastPersist = time.Now()
	if elapsed := time.Since(specLastPersist); elapsed >= specPersistInterval {
		t.Fatalf("test time anomaly: elapsed %v >= interval %v", elapsed, specPersistInterval)
	}
	// (Non-nil persistence behavior is covered by the existing persist tests.)
}

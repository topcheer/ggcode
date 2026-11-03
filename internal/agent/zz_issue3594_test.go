package agent

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

	for i := 0; i < 5; i++ {
		maybePersistSpecPatterns(nil)
	}
	if !specLastPersist.IsZero() {
		t.Fatalf("nil-receiver call consumed the throttle window: specLastPersist=%v (elapsed check would now gate %v)",
			specLastPersist, specPersistInterval)
	}

	specLastPersist = time.Now()
	if elapsed := time.Since(specLastPersist); elapsed >= specPersistInterval {
		t.Fatalf("test time anomaly: elapsed %v >= interval %v", elapsed, specPersistInterval)
	}

}

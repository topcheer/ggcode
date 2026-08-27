package tunnel

import (
	"testing"
	"time"
)

// #1182: a producer stuck in waitProjectionSync must eventually wake up and
// proceed degraded instead of blocking forever when the sync holder never
// calls endProjectionSync (e.g. wedged behind a cross-package lock during
// mobile reconnect).
func TestWaitProjectionSyncTimesOutInsteadOfBlockingForever(t *testing.T) {
	orig := projectionSyncWaitTimeout
	projectionSyncWaitTimeout = 150 * time.Millisecond
	defer func() { projectionSyncWaitTimeout = orig }()

	b := NewBroker(NewSession("wss://test.local"))
	b.beginProjectionSync()
	// Nobody calls endProjectionSync.

	start := time.Now()
	b.waitProjectionSync()
	elapsed := time.Since(start)
	if elapsed > 5*time.Second {
		t.Fatalf("waitProjectionSync blocked %v, want ~timeout (150ms): permanent producer stall (#1182)", elapsed)
	}
}

// #1182: the normal notify path must keep working (sync ends, waiter wakes
// immediately, well before the timeout fires).
func TestWaitProjectionSyncStillWakesOnEndProjectionSync(t *testing.T) {
	orig := projectionSyncWaitTimeout
	projectionSyncWaitTimeout = 30 * time.Second
	defer func() { projectionSyncWaitTimeout = orig }()

	b := NewBroker(NewSession("wss://test.local"))
	b.beginProjectionSync()
	go func() {
		time.Sleep(50 * time.Millisecond)
		b.endProjectionSync()
	}()

	start := time.Now()
	b.waitProjectionSync()
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("waitProjectionSync blocked %v despite endProjectionSync", elapsed)
	}
}

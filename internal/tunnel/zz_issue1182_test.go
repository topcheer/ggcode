package tunnel

import (
	"testing"
	"time"
)

func TestWaitProjectionSyncTimesOutInsteadOfBlockingForever(t *testing.T) {
	orig := projectionSyncWaitTimeout.Load()
	projectionSyncWaitTimeout.Store(int64(150 * time.Millisecond))
	defer func() { projectionSyncWaitTimeout.Store(orig) }()

	b := NewBroker(NewSession("wss://test.local"))

	t.Cleanup(func() { b.Stop() })
	b.beginProjectionSync()

	start := time.Now()
	b.waitProjectionSync()
	elapsed := time.Since(start)
	if elapsed > 5*time.Second {
		t.Fatalf("waitProjectionSync blocked %v, want ~timeout (150ms): permanent producer stall (#1182)", elapsed)
	}
}

func TestWaitProjectionSyncStillWakesOnEndProjectionSync(t *testing.T) {
	orig := projectionSyncWaitTimeout.Load()
	projectionSyncWaitTimeout.Store(int64(30 * time.Second))
	defer func() { projectionSyncWaitTimeout.Store(orig) }()

	b := NewBroker(NewSession("wss://test.local"))

	t.Cleanup(func() { b.Stop() })
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

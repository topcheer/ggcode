package memreclaim

import (
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestIntervalEnv(t *testing.T) {
	t.Setenv("GGCODE_MEMRECLAIM_INTERVAL", "120")
	if got := Interval(); got != 120*time.Second {
		t.Fatalf("Interval env=120 -> %v, want 120s", got)
	}
	t.Setenv("GGCODE_MEMRECLAIM_INTERVAL", "0")
	if got := Interval(); got != 0 {
		t.Fatalf("Interval env=0 -> %v, want 0 (disabled)", got)
	}
	t.Setenv("GGCODE_MEMRECLAIM_INTERVAL", "garbage")
	if got := Interval(); got != DefaultInterval {
		t.Fatalf("Interval invalid -> %v, want default %v", got, DefaultInterval)
	}
}

func TestThresholdEnv(t *testing.T) {
	t.Setenv("GGCODE_MEMRECLAIM_THRESHOLD", "1073741824")
	if got := Threshold(); got != 1<<30 {
		t.Fatalf("Threshold env -> %d, want 1GiB", got)
	}
	// below the minimum floor: falls back to default
	t.Setenv("GGCODE_MEMRECLAIM_THRESHOLD", "1000")
	if got := Threshold(); got != DefaultThreshold {
		t.Fatalf("Threshold below floor -> %d, want default %d", got, DefaultThreshold)
	}
}

// Conditional reclaim: FreeOSMemory must fire only when idle spans exceed
// the threshold; a below-threshold tick is a no-op. Uses a 1s interval.
func TestLoopConditionalReclaim(t *testing.T) {
	var calls int32
	savedForce, savedStats := *forceFn.Load(), *statsFn.Load()
	t.Cleanup(func() { forceFn.Store(&savedForce); statsFn.Store(&savedStats) })

	f := func() { atomic.AddInt32(&calls, 1) }
	forceFn.Store(&f)
	high := func(ms *runtime.MemStats) { // 1GiB idle: above 512MB threshold
		ms.HeapIdle = 1 << 30
		ms.HeapInuse = 1 << 20
	}
	statsFn.Store(&high)

	t.Setenv("GGCODE_MEMRECLAIM_INTERVAL", "1")
	stop := Start()
	if stop == nil {
		t.Fatal("Start returned nil stop")
	}

	time.Sleep(1500 * time.Millisecond)
	if atomic.LoadInt32(&calls) == 0 {
		t.Fatal("no reclaim fired despite idle spans above threshold")
	}

	low := func(ms *runtime.MemStats) { // 100MB idle: below threshold
		ms.HeapIdle = 100 << 20
		ms.HeapInuse = 1 << 20
	}
	statsFn.Store(&low)
	before := atomic.LoadInt32(&calls)
	time.Sleep(1500 * time.Millisecond)
	if after := atomic.LoadInt32(&calls); after != before {
		t.Fatalf("reclaim fired below threshold: %d -> %d", before, after)
	}
	stop()

	// Explicit disable: Start is a no-op with a safe stop func.
	t.Setenv("GGCODE_MEMRECLAIM_INTERVAL", "0")
	noStop := Start()
	noStop() // must not panic on immediate stop
}

func TestIdleSpanBytes(t *testing.T) {
	savedStats := *statsFn.Load()
	t.Cleanup(func() { statsFn.Store(&savedStats) })
	s := func(ms *runtime.MemStats) {
		ms.HeapIdle = 900
		ms.HeapInuse = 400
	}
	statsFn.Store(&s)
	if got := idleSpanBytes(); got != 500 {
		t.Fatalf("idleSpanBytes = %d, want 500", got)
	}
	s2 := func(ms *runtime.MemStats) { // inuse > idle: clamp to 0
		ms.HeapIdle = 100
		ms.HeapInuse = 900
	}
	statsFn.Store(&s2)
	if got := idleSpanBytes(); got != 0 {
		t.Fatalf("idleSpanBytes clamp = %d, want 0", got)
	}
}

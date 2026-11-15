// Package memreclaim periodically returns freed heap to the OS (#5682).
//
// Go frees heap spans with MADV_FREE on darwin: the pages stay counted in
// RSS until the system needs them. After #5679/#5680 capped the resident
// objects, live instances still showed RSS far above heap in-use (1.03GB
// vs 52MB) — the gap is idle-but-not-returned spans. A conditional
// debug.FreeOSMemory() (only when idle spans exceed a threshold) shrinks
// RSS back near in-use at the cost of one short STW GC per interval.
package memreclaim

import (
	"os"
	"runtime"
	stddebug "runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

// Defaults. Override via env:
//
//	GGCODE_MEMRECLAIM_INTERVAL   (time.Duration-ish seconds, 0 disables)
//	GGCODE_MEMRECLAIM_THRESHOLD  (bytes of idle spans before a reclaim)
const (
	DefaultInterval  = 30 * time.Minute
	DefaultThreshold = 512 << 20 // 512MB idle spans
	minimumThreshold = 64 << 20  // never scavenge below this - not worth a STW
)

var (
	forceFn atomic.Pointer[func()]                  // test seam
	statsFn atomic.Pointer[func(*runtime.MemStats)] // test seam
)

func init() {
	f := stddebug.FreeOSMemory
	forceFn.Store(&f)
	s := runtime.ReadMemStats
	statsFn.Store(&s)
}

// Interval resolves GGCODE_MEMRECLAIM_INTERVAL (seconds; 0 disables).
func Interval() time.Duration {
	if v := os.Getenv("GGCODE_MEMRECLAIM_INTERVAL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return time.Duration(n) * time.Second
		}
	}
	return DefaultInterval
}

// Threshold resolves GGCODE_MEMRECLAIM_THRESHOLD (bytes; floors at 64MB).
func Threshold() uint64 {
	if v := os.Getenv("GGCODE_MEMRECLAIM_THRESHOLD"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil && n > minimumThreshold {
			return n
		}
	}
	return DefaultThreshold
}

// Start launches the reclaim loop. Zero interval (explicit disable) is a
// no-op returning a nil-safe stop func. Safe to call once per process.
func Start() (stop func()) {
	interval := Interval()
	if interval <= 0 {
		debug.Log("memreclaim", "disabled (interval=0)")
		return func() {}
	}
	threshold := Threshold()

	var once sync.Once
	done := make(chan struct{})
	safego.Go("memreclaim.loop", func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				var ms runtime.MemStats
				(*statsFn.Load())(&ms)
				writeStatsLine(&ms, false) // #5682 stats: no-op unless GGCODE_MEM_SAMPLE=1
				if idleOf(&ms) >= threshold {
					debug.Log("memreclaim", "reclaim: idle spans over threshold, forcing FreeOSMemory")
					(*forceFn.Load())()
				}
			}
		}
	})
	return func() { once.Do(func() { close(done) }) }
}

// idleSpanBytes reports HeapIdle-HeapInuse: spans the runtime has freed
// (or reserved) but not yet returned to the OS.
func idleSpanBytes() uint64 {
	var ms runtime.MemStats
	(*statsFn.Load())(&ms)
	return idleOf(&ms)
}

func idleOf(ms *runtime.MemStats) uint64 {
	if ms.HeapIdle > ms.HeapInuse {
		return ms.HeapIdle - ms.HeapInuse
	}
	return 0
}

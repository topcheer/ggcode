//go:build linux

package tool

import (
	"sync"
	"testing"
)

// TestWaylandSessionIssue4611ConcurrentSafe pins the #4611 fix: the
// package-level lazy-init cache behind isWaylandSession must be safe under
// concurrent first-call pressure from multiple sessions (sub-agents, swarm
// teammates, IM adapters share the process). Before the sync.Once fix two
// goroutines could concurrently read linuxBackendResolved and write both
// package vars - a data race under the Go memory model (-race would flag
// it). After the fix the once gate serializes the single resolution and all
// callers observe the same stable value.
func TestWaylandSessionIssue4611ConcurrentSafe(t *testing.T) {
	const goroutines = 32
	results := make([]bool, goroutines)
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	for i := 0; i < goroutines; i++ {
		done.Add(1)
		go func(idx int) {
			defer done.Done()
			start.Wait() // maximize simultaneous first-call pressure
			results[idx] = isWaylandSession()
		}(i)
	}
	start.Done()
	done.Wait()

	// Every goroutine must observe the SAME resolved session type - a torn
	// init (one caller seeing resolved=true while the value write is not
	// yet visible) would surface as mixed results.
	for i := 1; i < goroutines; i++ {
		if results[i] != results[0] {
			t.Fatalf("mixed results across concurrent callers: results[0]=%v results[%d]=%v", results[0], i, results[i])
		}
	}
	// The resolution must have run exactly once.
	if !linuxBackendResolved {
		t.Fatal("resolution flag not set after concurrent calls")
	}
	if linuxBackendWayland != results[0] {
		t.Fatalf("cached value %v disagrees with returned %v", linuxBackendWayland, results[0])
	}
}

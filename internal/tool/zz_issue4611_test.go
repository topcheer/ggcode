//go:build linux

package tool

import (
	"sync"
	"testing"
)

func TestIssue4611IsWaylandSessionConcurrentSafe(t *testing.T) {
	const goroutines = 32
	results := make([]bool, goroutines)
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	for i := 0; i < goroutines; i++ {
		done.Add(1)
		go func(idx int) {
			defer done.Done()
			start.Wait()
			results[idx] = isWaylandSession()
		}(i)
	}
	start.Done()
	done.Wait()

	for i := 1; i < goroutines; i++ {
		if results[i] != results[0] {
			t.Fatalf("mixed results across concurrent callers: results[0]=%v results[%d]=%v", results[0], i, results[i])
		}
	}

	if !linuxBackendResolved {
		t.Fatal("resolution flag not set after concurrent calls")
	}
	if linuxBackendWayland != results[0] {
		t.Fatalf("cached value %v disagrees with returned %v", linuxBackendWayland, results[0])
	}
}

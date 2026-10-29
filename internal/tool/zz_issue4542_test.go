//go:build goolm

package tool

// Regression test for GitHub issue #4542: concurrent first Stop() calls
// panicked with "close of closed channel" because the select-default
// check-then-close was not atomic. sync.Once makes Stop idempotent and
// race-free - hammer it from many goroutines under -race.

import (
	"sync"
	"testing"
)

func TestIssue4542_ConcurrentStopNoDoubleClose(t *testing.T) {
	m := &CodeIndexManager{
		stopCh: make(chan struct{}),
	}
	var wg sync.WaitGroup
	const n = 16
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.Stop() // must never panic, no matter the interleaving
		}()
	}
	wg.Wait()
	// Channel must be closed exactly once: a receive succeeds.
	select {
	case <-m.stopCh:
	default:
		t.Fatalf("stopCh must be closed after Stop")
	}
	// Stop stays idempotent on later sequential calls.
	m.Stop()
}

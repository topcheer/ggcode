//go:build goolm

package tool

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
			m.Stop()
		}()
	}
	wg.Wait()

	select {
	case <-m.stopCh:
	default:
		t.Fatalf("stopCh must be closed after Stop")
	}

	m.Stop()
}

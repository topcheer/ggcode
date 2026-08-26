package acp

import (
	"sync"
	"testing"
)

// TestSessionTryBeginRun covers the prompt busy guard added in #1033:
// the first TryBeginRun wins, concurrent attempts fail until EndRun.
func TestSessionTryBeginRun(t *testing.T) {
	s := NewSession("/tmp", nil)

	if !s.TryBeginRun() {
		t.Fatal("first TryBeginRun should succeed")
	}
	if s.TryBeginRun() {
		t.Fatal("second TryBeginRun while run active should fail")
	}

	s.EndRun()
	if !s.TryBeginRun() {
		t.Fatal("TryBeginRun after EndRun should succeed")
	}
}

// TestSessionTryBeginRunConcurrent ensures exactly one winner across many
// concurrent attempts - the property handleSessionPrompt relies on to keep a
// single goroutine driving an AgentLoop (#1033).
func TestSessionTryBeginRunConcurrent(t *testing.T) {
	s := NewSession("/tmp", nil)

	const n = 32
	var wg sync.WaitGroup
	wins := make(chan bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			wins <- s.TryBeginRun()
		}()
	}
	wg.Wait()
	close(wins)

	winCount := 0
	for w := range wins {
		if w {
			winCount++
		}
	}
	if winCount != 1 {
		t.Fatalf("expected exactly 1 winner among concurrent TryBeginRun, got %d", winCount)
	}
}

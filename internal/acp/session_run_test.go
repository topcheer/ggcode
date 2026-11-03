package acp

import (
	"sync"
	"testing"
)

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

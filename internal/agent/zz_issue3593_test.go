package agent

import (
	"sync"
	"testing"
)

func TestIssue3593_ReserveSlotEnforcesCapAtomically(t *testing.T) {
	s := &speculator{}
	var mu sync.Mutex
	granted := 0

	var wg sync.WaitGroup
	for i := 0; i < specMaxConcurrent; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s.reserveSpecSlot() {
				mu.Lock()
				granted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if granted != specMaxConcurrent {
		t.Fatalf("expected %d grants with zero held, got %d", specMaxConcurrent, granted)
	}
	if got := s.activeSpeculations; got != specMaxConcurrent {
		t.Fatalf("activeSpeculations=%d, want %d", got, specMaxConcurrent)
	}

	if s.reserveSpecSlot() {
		t.Fatalf("reservation succeeded past the %d cap", specMaxConcurrent)
	}
	if got := s.activeSpeculations; got != specMaxConcurrent {
		t.Fatalf("failed reservation mutated the counter: activeSpeculations=%d", got)
	}

	s.releaseSpecSlot()
	if got := s.activeSpeculations; got != specMaxConcurrent-1 {
		t.Fatalf("after release activeSpeculations=%d, want %d", got, specMaxConcurrent-1)
	}
	if !s.reserveSpecSlot() {
		t.Fatal("reservation failed after a release freed a slot")
	}
}

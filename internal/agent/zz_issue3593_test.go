package agent

// #3593 probe: the concurrency slot must be reserved atomically
// (check+increment in one critical section). The old code checked under
// the first lock and incremented only under the second lock, after the
// unlocked predictArgs window - two loop iterations in flight could both
// pass the check and both increment, pushing the effective limit past
// specMaxConcurrent. The reserve/release pair pins the invariant.

import (
	"sync"
	"testing"
)

func TestIssue3593_ReserveSlotEnforcesCapAtomically(t *testing.T) {
	s := &speculator{}
	var mu sync.Mutex
	granted := 0

	// specMaxConcurrent vying goroutines each try to reserve; the cap must
	// hold exactly regardless of interleaving (each success holds its slot).
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

	// Cap saturated: further reservations must fail, never overshoot.
	if s.reserveSpecSlot() {
		t.Fatalf("reservation succeeded past the %d cap", specMaxConcurrent)
	}
	if got := s.activeSpeculations; got != specMaxConcurrent {
		t.Fatalf("failed reservation mutated the counter: activeSpeculations=%d", got)
	}

	// Release restores exactly one slot.
	s.releaseSpecSlot()
	if got := s.activeSpeculations; got != specMaxConcurrent-1 {
		t.Fatalf("after release activeSpeculations=%d, want %d", got, specMaxConcurrent-1)
	}
	if !s.reserveSpecSlot() {
		t.Fatal("reservation failed after a release freed a slot")
	}
}

package agentruntime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/memory"
)

// #5764: failStreak/breakerUntil are shared by the sweep and loop goroutines
// (both run curateBatchLoop). All access must be guarded - the old
// unsynchronized read-modify-write was a -race-detected data race.

func TestIssue5764_BreakerOpensAfterThreeFailedPasses(t *testing.T) {
	withTestHome(t)
	fake := &fakeCuratorProvider{err: errors.New("llm down")}
	cur := NewMemoryCurator(nil, nil, fake, MemoryCuratorOptions{DisableSweep: true})
	batch := []memory.ChangeEvent{{Scope: "project", Op: "save", Key: "k"}}

	cur.curateBatchLoop(context.Background(), batch)
	cur.curateBatchLoop(context.Background(), batch)
	if d := cur.breakerWait(); d != 0 {
		t.Fatalf("breaker should stay closed before %d consecutive failures, open for %v", memoryCuratorBreakerAfter, d)
	}

	cur.curateBatchLoop(context.Background(), batch)
	d := cur.breakerWait()
	if d <= 0 || d > memoryCuratorBreakerCooloff {
		t.Fatalf("breaker should be OPEN for ~%s after %d consecutive failures, got %v",
			memoryCuratorBreakerCooloff, memoryCuratorBreakerAfter, d)
	}
}

func TestIssue5764_SuccessfulPassResetsStreak(t *testing.T) {
	withTestHome(t)
	cur := NewMemoryCurator(nil, nil, nil, MemoryCuratorOptions{DisableSweep: true})

	if tripped, _ := cur.noteFailedPass(); tripped {
		t.Fatal("breaker must not trip at streak 1")
	}
	if tripped, _ := cur.noteFailedPass(); tripped {
		t.Fatal("breaker must not trip at streak 2")
	}
	if d := cur.breakerWait(); d != 0 {
		t.Fatalf("breaker should be closed at streak 2, open for %v", d)
	}

	cur.resetFailStreak()
	if tripped, _ := cur.noteFailedPass(); tripped {
		t.Fatal("breaker must not trip at streak 1 after reset")
	}
	if d := cur.breakerWait(); d != 0 {
		t.Fatalf("breaker should be closed after reset+1 failure, open for %v", d)
	}
}

// TestIssue5764_ConcurrentPassesGuardBreakerState mirrors the production
// overlap (sweep goroutine running a chunk while the event loop runs a
// debounced batch): concurrent curateBatchLoop calls plus a concurrent
// breaker reader. Run under -race - the pre-fix code fails this test's
// read/write on failStreak; functionally, 4 failed passes must open the
// breaker with no lost updates.
func TestIssue5764_ConcurrentPassesGuardBreakerState(t *testing.T) {
	withTestHome(t)
	fake := &fakeCuratorProvider{err: errors.New("llm down")}
	cur := NewMemoryCurator(nil, nil, fake, MemoryCuratorOptions{DisableSweep: true})
	batch := []memory.ChangeEvent{{Scope: "project", Op: "save", Key: "k"}}

	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cur.curateBatchLoop(context.Background(), batch)
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if cur.breakerWait() > memoryCuratorBreakerCooloff {
				t.Error("breakerWait must never exceed the cooloff window")
				return
			}
		}
	}()
	wg.Wait()

	if d := cur.breakerWait(); d <= 0 {
		t.Fatal("breaker should be open after 4 concurrent failed passes (no lost streak updates)")
	}
}

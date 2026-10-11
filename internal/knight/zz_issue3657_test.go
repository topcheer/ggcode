package knight

// #3657 probes: (1) Start writes k.lock only under k.mu - concurrent
// Start/Stop must be race-clean under -race (the detector is the
// assertion). The old Start assigned k.lock and released it on error
// paths entirely outside k.mu while Stop and Status read it under k.mu.
// (2) Stop releases the cross-process instance lock only AFTER runLoop
// fully exits - a Start immediately following Stop must reacquire it.

import (
	"context"
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestIssue3657_StartStopConcurrentRaceClean(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	k := New(config.DefaultKnightConfig(), home, proj, nil)

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = k.Start(context.Background()) // ErrLockConflict ok mid-race
		}()
		go func() {
			defer wg.Done()
			k.Stop()
		}()
	}
	wg.Wait()
	k.Stop() // drain: lock must end up free

	k2 := New(config.DefaultKnightConfig(), home, proj, nil)
	if err := k2.Start(context.Background()); err != nil {
		t.Fatalf("after concurrent drain the instance lock must be acquirable: %v", err)
	}
	k2.Stop()
}

func TestIssue3657_StopReleasesLockForRestart(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	k := New(config.DefaultKnightConfig(), home, proj, nil)

	if err := k.Start(context.Background()); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	k.Stop()
	// Lock must be free after Stop returns: a fresh Knight on the same
	// projDir must reacquire it (a leaked/double-held lock fails here).
	k2 := New(config.DefaultKnightConfig(), home, proj, nil)
	if err := k2.Start(context.Background()); err != nil {
		t.Fatalf("Start after Stop must reacquire the instance lock: %v", err)
	}
	k2.Stop()
}

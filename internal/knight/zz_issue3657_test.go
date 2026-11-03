package knight

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
			_ = k.Start(context.Background())
		}()
		go func() {
			defer wg.Done()
			k.Stop()
		}()
	}
	wg.Wait()
	k.Stop()

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

	k2 := New(config.DefaultKnightConfig(), home, proj, nil)
	if err := k2.Start(context.Background()); err != nil {
		t.Fatalf("Start after Stop must reacquire the instance lock: %v", err)
	}
	k2.Stop()
}

package subagent

// #2119 regression: never-Run Pending entries had no reclamation path -
// they held a concurrency slot (Spawn counts Pending) forever and Wait
// blocked indefinitely; Spawn after Shutdown registered entries that
// could never execute. Probes A/B/C verified all three holes. The
// watchdog now fails stale Pending entries, and Spawn rejects calls made
// after rootCtx cancellation.

import (
	"context"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

func TestIssue2119_SpawnAfterShutdownRejected(t *testing.T) {
	m := NewManager(config.SubAgentConfig{})
	m.Shutdown()

	id := m.Spawn("late", "task", "", nil, context.Background())
	sa, ok := m.Get(id)
	if !ok {
		t.Fatal("spawn entry missing")
	}
	if sa.Status != StatusFailed {
		t.Fatalf("post-shutdown Spawn status = %v, want StatusFailed (was Pending forever, probe C)", sa.Status)
	}
	if sa.Error == nil {
		t.Fatal("post-shutdown Spawn must carry an error")
	}
	// Wait semantics must NOT block indefinitely on the rejected entry -
	// the done channel is closed so any waiter unblocks.
	sa.mu.Lock()
	doneCh := sa.done
	sa.mu.Unlock()
	select {
	case <-doneCh:
	default:
		t.Fatal("rejected entry's done channel must be closed (waiters would block forever, probe C)")
	}
}

func TestIssue2119_StalePendingReclaimed(t *testing.T) {
	m := NewManager(config.SubAgentConfig{})
	defer m.Shutdown()
	// Shrink the watchdog window so the test does not wait 5 minutes.
	m.inactivityTimeout = 10 * time.Millisecond

	id := m.Spawn("ghost", "never run", "", nil, context.Background())
	if _, ok := m.Get(id); !ok {
		t.Fatal("spawn failed")
	}
	time.Sleep(60 * time.Millisecond)
	m.reapInactiveAgents()

	sa, ok := m.Get(id)
	if !ok {
		t.Fatal("entry vanished entirely (should fail visibly, not disappear)")
	}
	if sa.Status != StatusFailed {
		t.Fatalf("stale pending entry status = %v, want StatusFailed (slot leak, probe A/B)", sa.Status)
	}
	if sa.Error == nil || sa.Error.Error() == "" {
		t.Fatal("reclaimed entry must carry a diagnostic error")
	}
	// The slot must be free again: spawn up to maxConcurrent must succeed.
	for i := 0; i < m.maxConcurrent; i++ {
		nid := m.Spawn("filler", "x", "", nil, context.Background())
		if s, ok := m.Get(nid); !ok || s.Status == StatusFailed {
			t.Fatalf("slot not reclaimed: filler spawn %d rejected", i)
		}
	}
}

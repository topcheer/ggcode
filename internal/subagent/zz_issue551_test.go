package subagent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

func TestIssue551A_SetStatusTerminalGuard(t *testing.T) {
	for _, terminal := range []Status{StatusCancelled, StatusCompleted, StatusFailed} {
		sa := &SubAgent{
			ID:     "sa-issue551-a",
			Status: terminal,
			done:   make(chan struct{}),
		}
		sa.setStatus(StatusRunning)
		if got := sa.getStatus(); got != terminal {
			t.Fatalf("setStatus overwrote terminal status %s -> %s", terminal, got)
		}

		sa2 := &SubAgent{Status: StatusPending, done: make(chan struct{})}
		sa2.setStatus(StatusRunning)
		if sa2.getStatus() != StatusRunning {
			t.Fatalf("pending -> running transition blocked, got %s", sa2.getStatus())
		}
	}
}

func TestIssue551B_CompleteBackfillsResultOnTerminalRace(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{})
	id := mgr.Spawn("probe", "task", "task", nil, context.Background())

	if sa, ok := mgr.Get(id); !ok {
		t.Fatal("agent not found after Spawn")
	} else {
		sa.mu.Lock()
		sa.Status = StatusCancelled
		sa.Error = context.Canceled
		sa.mu.Unlock()
	}

	partial := strings.Repeat("partial output line\n", 50)
	mgr.Complete(id, partial, context.Canceled)

	snap, ok := mgr.Snapshot(id)
	if !ok {
		t.Fatal("snapshot not found")
	}
	if snap.Status != StatusCancelled {
		t.Fatalf("terminal status overwritten: %s", snap.Status)
	}
	if snap.Result != partial {
		t.Fatalf("partial result lost on terminal race: got %d bytes, want %d", len(snap.Result), len(partial))
	}

	mgr.Complete(id, "second-result", nil)
	snap2, _ := mgr.Snapshot(id)
	if snap2.Result != partial {
		t.Fatalf("existing result overwritten by later Complete: got %q", snap2.Result[:min(40, len(snap2.Result))])
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := Wait(ctx, mgr, id)
	if res != partial {
		t.Fatalf("Wait returned lost result: %d bytes", len(res))
	}
	if err == nil {
		t.Fatal("expected cancellation error from Wait")
	}
}

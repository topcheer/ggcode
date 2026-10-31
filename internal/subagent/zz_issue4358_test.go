package subagent

import (
	"context"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// TestIssue4358WaitPostSignalGraceCoversCancelThenComplete reproduces the
// Cancel-then-Complete race from #4358: Cancel sets the terminal status (and
// closes done for never-started agents), while the runner's Complete backfill
// lands shortly after. Wait must apply a real post-signal grace and re-read
// the result instead of returning the stale empty snapshot.
func TestIssue4358WaitPostSignalGraceCoversCancelThenComplete(t *testing.T) {
	m := NewManager(config.SubAgentConfig{})
	ctx := context.Background()
	id := m.Spawn("grace-probe", "task", "display", nil, ctx)

	if !m.Cancel(id) {
		t.Fatal("Cancel returned false")
	}

	// In-flight Complete backfill, landing after Wait has already been able
	// to observe the cancelled status (30ms is well inside the 200ms grace).
	go func() {
		time.Sleep(30 * time.Millisecond)
		m.Complete(id, "salvaged result", nil)
	}()

	// A cancelled agent legitimately surfaces context.Canceled as its error
	// (Cancel sets sa.Error and the Complete backfill intentionally leaves the
	// terminal status/error untouched per #2783). The regression under test is
	// the RESULT: the post-signal grace must let the backfill land.
	res, _ := Wait(ctx, m, id)
	if res != "salvaged result" {
		t.Fatalf("Wait result = %q, want %q (post-signal grace dropped the backfill)", res, "salvaged result")
	}
}

// TestIssue4358WaitGraceBoundedByContext ensures the grace wait honors ctx
// cancellation instead of blocking the full grace window.
func TestIssue4358WaitGraceBoundedByContext(t *testing.T) {
	m := NewManager(config.SubAgentConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	id := m.Spawn("ctx-probe", "task", "display", nil, ctx)

	if !m.Cancel(id) {
		t.Fatal("Cancel returned false")
	}
	// No Complete will ever land; ctx dies first — Wait must surface the
	// context error promptly, not block out the grace.
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	if _, err := Wait(ctx, m, id); err == nil {
		t.Fatal("expected context error from Wait")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("Wait blocked %v, expected ctx-bounded return well under 500ms", elapsed)
	}
}

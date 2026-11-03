package subagent

import (
	"context"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

func TestIssue4358WaitPostSignalGraceCoversCancelThenComplete(t *testing.T) {
	m := NewManager(config.SubAgentConfig{})
	ctx := context.Background()
	id := m.Spawn("grace-probe", "task", "display", nil, ctx)

	if !m.Cancel(id) {
		t.Fatal("Cancel returned false")
	}

	go func() {
		time.Sleep(30 * time.Millisecond)
		m.Complete(id, "salvaged result", nil)
	}()

	res, _ := Wait(ctx, m, id)
	if res != "salvaged result" {
		t.Fatalf("Wait result = %q, want %q (post-signal grace dropped the backfill)", res, "salvaged result")
	}
}

func TestIssue4358WaitGraceBoundedByContext(t *testing.T) {
	m := NewManager(config.SubAgentConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	id := m.Spawn("ctx-probe", "task", "display", nil, ctx)

	if !m.Cancel(id) {
		t.Fatal("Cancel returned false")
	}

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

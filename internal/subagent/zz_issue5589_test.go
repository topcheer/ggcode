package subagent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

// #5589: the snapshot the main chain polls must carry the runtime's
// lastActivity/toolExecuting state so stuck-vs-progressing is decidable.
func TestIssue5589_SnapshotCarriesActivityState(t *testing.T) {
	mgr := NewManager(config.SubAgentConfig{})
	id := mgr.Spawn("probe", "task", "", nil, context.Background())
	sa, _ := mgr.Get(id)

	stale := time.Now().Add(-5 * time.Minute)
	sa.mu.Lock()
	sa.Status = StatusRunning
	sa.lastActivity = stale
	sa.toolExecuting = true
	sa.mu.Unlock()

	snap := sa.snapshot()
	if !snap.LastActivity.Equal(stale) {
		t.Fatalf("snapshot LastActivity = %v, want %v", snap.LastActivity, stale)
	}
	if !snap.ToolExecuting {
		t.Fatal("snapshot ToolExecuting must mirror toolExecuting")
	}
}

// The mid-wait progress summary (SnapshotProgressFunc) is the second face of
// #5589: it must surface tool-running vs idle-age while the parent waits.
func TestIssue5589_ProgressSummaryShowsToolRunning(t *testing.T) {
	snap := Snapshot{
		Status:        StatusRunning,
		ToolCallCount: 4,
		CurrentTool:   "read_file",
		ToolExecuting: true,
	}
	out := formatProgressSummary(snap)
	if !strings.Contains(out, "(running)") {
		t.Fatalf("progress summary for executing tool must carry (running), got: %s", out)
	}
	if !strings.Contains(out, "4 tools") || !strings.Contains(out, "read_file") {
		t.Fatalf("progress summary must keep tool count/name, got: %s", out)
	}
}

func TestIssue5589_ProgressSummaryShowsIdleAge(t *testing.T) {
	snap := Snapshot{
		Status:       StatusRunning,
		CurrentPhase: "thinking",
		LastActivity: time.Now().Add(-90 * time.Second),
	}
	out := formatProgressSummary(snap)
	if !strings.Contains(out, "(idle 1m30s)") {
		t.Fatalf("idle progress summary must carry age, got: %s", out)
	}
	if strings.Contains(out, "(running)") {
		t.Fatalf("idle agent must not be marked (running), got: %s", out)
	}
}

func TestIssue5589_ProgressSummaryZeroActivityBare(t *testing.T) {
	out := formatProgressSummary(Snapshot{Status: StatusRunning})
	if strings.Contains(out, "(running)") || strings.Contains(out, "(idle") {
		t.Fatalf("zero-activity snapshot must stay bare, got: %s", out)
	}
}

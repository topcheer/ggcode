package tool

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/subagent"
)

// Pins issue #5589 (stuck-vs-progressing): a registered agent's live snapshot
// must carry a non-zero LastActivity after activity updates reach snapshot().
func TestLivenessManagerSnapshotLastActivity(t *testing.T) {
	mgr := subagent.NewManager(config.SubAgentConfig{})
	id := mgr.Spawn("liveness", "check liveness", "check liveness", nil, context.Background())
	if id == "" {
		t.Fatal("Spawn returned empty id")
	}
	mgr.UpdateActivity(id, "tool", "read_file", "{}")
	snap, ok := mgr.Snapshot(id)
	if !ok {
		t.Fatalf("Snapshot(%q) not found", id)
	}
	if snap.LastActivity.IsZero() {
		t.Fatal("live snapshot LastActivity is zero after UpdateActivity, want non-zero")
	}
	if snap.CurrentTool != "read_file" {
		t.Fatalf("live snapshot CurrentTool = %q, want read_file", snap.CurrentTool)
	}
}

func TestLivenessFormatSubAgentSnapshotExecuting(t *testing.T) {
	snap := subagent.Snapshot{
		ID:            "sa-1",
		Name:          "worker",
		Status:        subagent.StatusRunning,
		CurrentTool:   "read_file",
		ToolExecuting: true,
		LastActivity:  time.Now().Add(-3 * time.Second),
	}
	out := formatSubAgentSnapshot(snap)
	if !strings.Contains(out, "Executing") {
		t.Fatalf("executing snapshot must render Executing line, got %q", out)
	}
	if !strings.Contains(out, "read_file") {
		t.Fatalf("executing snapshot must name the tool, got %q", out)
	}
	if !strings.Contains(out, "for 3s") {
		t.Fatalf("executing line must include rounded duration, got %q", out)
	}
}

func TestLivenessFormatSubAgentSnapshotIdle(t *testing.T) {
	snap := subagent.Snapshot{
		ID:           "sa-2",
		Name:         "idle-worker",
		Status:       subagent.StatusRunning,
		LastActivity: time.Now().Add(-90 * time.Second),
	}
	out := formatSubAgentSnapshot(snap)
	if !strings.Contains(out, "Last activity") {
		t.Fatalf("idle snapshot must render Last activity line, got %q", out)
	}
	if !strings.Contains(out, "ago") {
		t.Fatalf("Last activity line must include age, got %q", out)
	}
}

func TestLivenessFormatSubAgentSnapshotZeroLastActivity(t *testing.T) {
	snap := subagent.Snapshot{ID: "sa-3", Name: "fresh", Status: subagent.StatusPending}
	out := formatSubAgentSnapshot(snap)
	if strings.Contains(out, "Executing") {
		t.Fatalf("zero LastActivity snapshot must not render Executing line, got %q", out)
	}
	if strings.Contains(out, "Last activity") {
		t.Fatalf("zero LastActivity snapshot must not render Last activity line, got %q", out)
	}
}

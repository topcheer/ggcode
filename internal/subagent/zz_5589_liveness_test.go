package subagent

import (
	"strings"
	"testing"
	"time"
)

// TestLivenessSnapshotRunningFields pins issue #5589: snapshot() must surface
// LastActivity and ToolExecuting so callers can distinguish a stuck agent from
// one that is actively progressing through tool execution.
func TestLivenessSnapshotRunningFields(t *testing.T) {
	sa := &SubAgent{
		ID:     "sa-liveness",
		Name:   "liveness",
		Status: StatusRunning,
		done:   make(chan struct{}),
	}

	snap := sa.snapshot()
	if !snap.LastActivity.IsZero() {
		t.Fatalf("fresh agent snapshot LastActivity = %v, want zero", snap.LastActivity)
	}
	if snap.ToolExecuting {
		t.Fatal("fresh agent snapshot ToolExecuting = true, want false")
	}

	started := time.Now()
	sa.setToolExecuting(true)
	snap = sa.snapshot()
	if !snap.ToolExecuting {
		t.Fatal("executing snapshot ToolExecuting = false, want true")
	}
	if snap.LastActivity.IsZero() {
		t.Fatal("executing snapshot LastActivity is zero, want set at tool start")
	}
	if snap.LastActivity.Before(started.Add(-time.Second)) || snap.LastActivity.After(time.Now().Add(time.Second)) {
		t.Fatalf("executing snapshot LastActivity = %v, want ~%v", snap.LastActivity, started)
	}

	sa.setToolExecuting(false)
	snap = sa.snapshot()
	if snap.ToolExecuting {
		t.Fatal("post-execution snapshot ToolExecuting = true, want false")
	}
	if snap.LastActivity.IsZero() {
		t.Fatal("post-execution snapshot LastActivity is zero, want preserved")
	}
}

// TestLivenessFormatProgressSummarySuffixes pins issue #5589: the wait/progress
// summary must mark an actively executing tool as (running) and otherwise show
// the idle age since last activity, with no suffix when LastActivity is zero.
func TestLivenessFormatProgressSummarySuffixes(t *testing.T) {
	running := Snapshot{
		Status:        StatusRunning,
		CurrentTool:   "edit_file",
		ToolExecuting: true,
		LastActivity:  time.Now(),
	}
	if got := formatProgressSummary(running); !strings.Contains(got, "(running)") {
		t.Fatalf("executing summary must contain (running), got %q", got)
	}

	idle := Snapshot{
		Status:       StatusRunning,
		LastActivity: time.Now().Add(-5 * time.Minute),
	}
	if got := formatProgressSummary(idle); !strings.Contains(got, "(idle 5m") {
		t.Fatalf("idle summary must contain (idle ...), got %q", got)
	}

	fresh := Snapshot{Status: StatusPending}
	got := formatProgressSummary(fresh)
	if strings.Contains(got, "(running)") || strings.Contains(got, "(idle") {
		t.Fatalf("zero LastActivity summary must not carry liveness suffixes, got %q", got)
	}
}

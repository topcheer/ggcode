package tool

import (
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/subagent"
)

// #5589: wait_agent running-state output must let the main chain tell a
// progressing sub-agent from a stuck one (lastTool timestamp face).
func TestIssue5589_RunningSnapshotShowsActivityAge(t *testing.T) {
	stale := time.Now().Add(-7 * time.Minute)

	cases := []struct {
		name string
		snap subagent.Snapshot
		want []string
		ban  []string
	}{
		{
			name: "tool executing for minutes",
			snap: subagent.Snapshot{
				Status:        subagent.StatusRunning,
				ToolCallCount: 9,
				CurrentTool:   "run_command",
				ToolExecuting: true,
				LastActivity:  stale,
			},
			want: []string{"Tool calls: 9", "Current tool: run_command", "Executing tool for 7m"},
			ban:  []string{"Last activity"},
		},
		{
			name: "streaming, recent activity",
			snap: subagent.Snapshot{
				Status:       subagent.StatusRunning,
				CurrentPhase: "writing",
				LastActivity: time.Now().Add(-2 * time.Second),
			},
			want: []string{"Phase: writing", "Last activity 2s ago"},
			ban:  []string{"Executing tool"},
		},
		{
			name: "idle long = stuck candidate",
			snap: subagent.Snapshot{
				Status:       subagent.StatusRunning,
				LastActivity: stale,
			},
			want: []string{"Last activity 7m0s ago"},
		},
	}
	for _, tc := range cases {
		out := formatSubAgentSnapshot(tc.snap)
		for _, w := range tc.want {
			if !strings.Contains(out, w) {
				t.Errorf("%s: output missing %q:\n%s", tc.name, w, out)
			}
		}
		for _, b := range tc.ban {
			if strings.Contains(out, b) {
				t.Errorf("%s: output must not contain %q:\n%s", tc.name, b, out)
			}
		}
	}
}

func TestIssue5589_NonRunningOmitsActivity(t *testing.T) {
	snap := subagent.Snapshot{
		Status:       subagent.StatusCompleted,
		Result:       "done",
		LastActivity: time.Now().Add(-time.Hour),
	}
	out := formatSubAgentSnapshot(snap)
	if strings.Contains(out, "Last activity") || strings.Contains(out, "Executing tool") {
		t.Fatalf("completed snapshot must not carry activity lines:\n%s", out)
	}
	if !strings.Contains(out, "Result: done") {
		t.Fatalf("completed snapshot must keep Result line:\n%s", out)
	}
}

func TestIssue5589_ZeroActivityOmitsLine(t *testing.T) {
	snap := subagent.Snapshot{Status: subagent.StatusRunning}
	out := formatSubAgentSnapshot(snap)
	if strings.Contains(out, "Last activity") || strings.Contains(out, "Executing tool") {
		t.Fatalf("zero-activity running snapshot must omit activity lines:\n%s", out)
	}
}

func TestIssue5589_SnapshotExposesRuntimeActivityFields(t *testing.T) {
	last := time.Now().Add(-3 * time.Minute)
	snap := subagent.Snapshot{LastActivity: last, ToolExecuting: true}
	if !snap.ToolExecuting {
		t.Fatal("Snapshot.ToolExecuting must round-trip")
	}
	if !snap.LastActivity.Equal(last) {
		t.Fatal("Snapshot.LastActivity must round-trip")
	}
}

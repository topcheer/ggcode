package a2a

import (
	"strings"
	"testing"
)

// G1 (#5672 followup): FormatSnapshotV3 must render the health/budget/
// memory/team fields the server already assembles - raw JSON without a
// render branch silently drops them on the CLI/agent display path.
func TestFormatSnapshotV3RendersNewFields(t *testing.T) {
	s := &RuntimeSnapshotV3{Depth: DepthFull}
	s.Status = &RuntimeStatus{
		Model: "m", Iteration: 2,
		GoalProgress: &RuntimeGoalProgress{HasGoal: true, StrategistRun: 3, NoProgressRun: 1},
		BudgetPct:    41.5,
	}
	s.Env = &RuntimeEnv{Workspace: "/w", MemoryKeys: []string{"build-process", "api-gotcha"}}
	s.Plan = &RuntimePlan{
		Teams: []RuntimeTeamView{{
			TeamID: "t1", Name: "squad", LeaderID: "lead", TaskCount: 2,
			Members: []RuntimeTeamMate{{Nick: "alice_dev", State: "idle"}},
		}},
	}
	out := FormatSnapshotV3("peer", "/ws", s)
	for _, want := range []string{
		"goal_progress: has_goal=true strategist_calls=3 no_progress_streak=1",
		"budget_pct: 41.5%",
		"memory_keys: build-process, api-gotcha",
		"1 teams",
		"team squad (leader=lead tasks=2 members=1)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q in:\n%s", want, out)
		}
	}
}

// Zero-value faces render nothing new (nil GoalProgress, zero BudgetPct,
// empty MemoryKeys stay silent - omitempty parity on the display side).
func TestFormatSnapshotV3NewFieldsOmittedWhenEmpty(t *testing.T) {
	s := &RuntimeSnapshotV3{Depth: DepthFull}
	s.Status = &RuntimeStatus{Model: "m"}
	s.Env = &RuntimeEnv{Workspace: "/w"}
	out := FormatSnapshotV3("peer", "/ws", s)
	for _, ban := range []string{"goal_progress:", "budget_pct:", "memory_keys:"} {
		if strings.Contains(out, ban) {
			t.Errorf("empty face must omit %q, got:\n%s", ban, out)
		}
	}
}

package a2a

import "strings"

import "testing"

func TestFormatSnapshotV3PlanFace(t *testing.T) {
	s := &RuntimeSnapshotV3{Depth: DepthFull}
	s.Plan = &RuntimePlan{
		Todos:     []RuntimePlanTodo{{ID: "t1", Content: "wire plan face", Status: "done"}},
		GoalNodes: []RuntimeGoalNode{{ID: "g1", Level: "goal", Title: "introspection P2", Status: "in_progress", NextStep: "verify"}},
		Tasks:     []RuntimePlanTask{{ID: "k1", Subject: "render plan", Status: "pending", Owner: "sa"}},
	}
	out := FormatSnapshotV3("peer", "/ws", s)
	for _, want := range []string{
		"plan: 1 todos, 1 goal nodes, 1 board tasks",
		"todo [done] wire plan face",
		"goal[in_progress] goal introspection P2 -> verify",
		"task [pending] render plan (owner=sa)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q in:\n%s", want, out)
		}
	}
}

func TestFormatSnapshotV3PlanFaceOmitted(t *testing.T) {
	out := FormatSnapshotV3("peer", "/ws", &RuntimeSnapshotV3{Depth: DepthBrief})
	if strings.Contains(out, "plan:") {
		t.Errorf("plan face must be omitted when nil, got:\n%s", out)
	}
}

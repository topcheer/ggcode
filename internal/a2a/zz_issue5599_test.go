package a2a

import (
	"strings"
	"testing"
)

// #5599 (rebased onto v3 #5600): FormatSnapshotV3 must not panic when any
// face is nil (unwired source / stub introspector / nil-snap branches).
// The v2 guard semantics migrated: nil faces are skipped, never deref'd.
func TestFormatSnapshotV3NilFacesNoPanic(t *testing.T) {
	out := FormatSnapshotV3("peer", "ws", &RuntimeSnapshotV3{Schema: "v3", Depth: "full"})
	if strings.Contains(out, "panic") {
		t.Fatalf("panic leaked into output: %q", out)
	}
	if !strings.Contains(out, "depth=full") {
		t.Fatalf("header row lost: %q", out)
	}
	// every face individually nil while others render
	out = FormatSnapshotV3("peer", "ws", &RuntimeSnapshotV3{
		Schema: "v3", Depth: "brief",
		Status:   &RuntimeStatus{Busy: true, Model: "m"},
		Schedule: &RuntimeSchedule{Todos: []RuntimeTodo{{ID: "1", Subject: "s", Status: "in_progress"}}},
	})
	if !strings.Contains(out, "BUSY") || !strings.Contains(out, "todo") {
		t.Fatalf("rendered faces lost while nil faces skipped: %q", out)
	}
}

func TestFormatSnapshotV3BusyFaceRendered(t *testing.T) {
	out := FormatSnapshotV3("peer", "ws", &RuntimeSnapshotV3{
		Schema: "v3", Depth: "full",
		Status:   &RuntimeStatus{Busy: true, BusySince: "2026-11-14T00:00:00Z", Spinner: "working"},
		Env:      &RuntimeEnv{Workspace: "/w", Branch: "main", Head: "abc1234", DirtyFiles: 2, Provider: "glm"},
		Children: &RuntimeChildren{Subagents: []RuntimeSubagent{{ID: "agent-1", TaskHead: "research", State: "running"}}},
	})
	for _, want := range []string{"BUSY", "working", "branch=main", "head=abc1234", "dirty=2", "subagent agent-1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in: %q", want, out)
		}
	}
}

package a2aintrospect

import (
	"testing"

	"github.com/topcheer/ggcode/internal/a2a"
	"github.com/topcheer/ggcode/internal/subagent"
)

// #5605 P2: children/inflight adapters - mapping, active filter, and
// aggregation through the Sources pipeline.
func TestIssue5605_InflightChildrenFaces(t *testing.T) {
	list := []*subagent.SubAgent{
		{ID: "a-1", Name: "research", Status: subagent.StatusRunning, Task: "explore repo", CurrentTool: "grep", ToolCallCount: 4, Model: "glm-5.3"},
		{ID: "a-2", Name: "pending-one", Status: subagent.StatusPending, Task: "done thing"},
	}
	// inflight: only the running seat summarized
	inf := InflightFromSubagents(list)
	if inf == nil || inf.Tool != "grep" || inf.ParamsHead != "explore repo" {
		t.Fatalf("inflight: %+v", inf)
	}
	if InflightFromSubagents([]*subagent.SubAgent{{ID: "x", Status: subagent.StatusPending}}) != nil {
		t.Fatal("idle-only list must yield nil inflight")
	}
	// children: both seats visible with detail fields
	src := &Sources{
		Inflight: func() *a2a.RuntimeInflight { return InflightFromSubagents(list) },
		Children: func() *a2a.RuntimeChildren { return &a2a.RuntimeChildren{Subagents: FromSubagents(list)} },
	}
	snap, err := src.RuntimeSnapshotV3(a2a.SnapshotRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Inflight == nil || snap.Inflight.Tool != "grep" {
		t.Fatalf("snapshot inflight: %+v", snap.Inflight)
	}
	if len(snap.Children.Subagents) != 2 {
		t.Fatalf("children count: %d", len(snap.Children.Subagents))
	}
	sa := snap.Children.Subagents[0]
	if sa.Name != "research" || sa.CurrentTool != "grep" || sa.ToolCalls != 4 {
		t.Fatalf("children detail: %+v", sa)
	}
	// faces filter still works
	snap2, _ := src.RuntimeSnapshotV3(a2a.SnapshotRequest{Faces: []string{"inflight"}})
	if snap2.Children != nil {
		t.Fatal("faces filter broken for children")
	}
}

package a2a

import (
	"testing"

	"github.com/topcheer/ggcode/internal/agent"
)

func TestPropagateWorkingDir_InheritsParent(t *testing.T) {
	parent := agent.NewAgent(nil, nil, "", 0)
	parent.SetWorkingDir("/ws/parent")
	h := NewTaskHandler("/ws/bound", parent, nil)

	child := agent.NewAgent(nil, nil, "", 0)
	h.propagateWorkingDir(child)

	if got := child.WorkingDir(); got != "/ws/parent" {
		t.Fatalf("child working dir = %q, want parent /ws/parent", got)
	}
}

func TestPropagateWorkingDir_FallsBackToHandlerWorkspace(t *testing.T) {
	parent := agent.NewAgent(nil, nil, "", 0)
	h := NewTaskHandler("/ws/bound", parent, nil)

	child := agent.NewAgent(nil, nil, "", 0)
	h.propagateWorkingDir(child)

	if got := child.WorkingDir(); got != "/ws/bound" {
		t.Fatalf("child working dir = %q, want handler workspace /ws/bound", got)
	}
}

func TestPropagateWorkingDir_NoDirLeavesChildUnset(t *testing.T) {
	parent := agent.NewAgent(nil, nil, "", 0)
	h := NewTaskHandler(t.TempDir(), parent, nil)
	h.workspace = ""

	child := agent.NewAgent(nil, nil, "", 0)
	h.propagateWorkingDir(child)

	if got := child.WorkingDir(); got != "" {
		t.Fatalf("child working dir = %q, want empty (no false anchoring)", got)
	}
}

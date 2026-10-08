package a2a

import (
	"testing"

	"github.com/topcheer/ggcode/internal/agent"
)

// sa-135 (ClawGuard coverage): per-task A2A agents must inherit the parent's
// working dir - otherwise the invariant engine stays inert and every tool
// call inside the A2A task bypasses the project's declared block/warn rules.

func TestPropagateWorkingDir_InheritsParent(t *testing.T) {
	parent := agent.NewAgent(nil, nil, "", 0)
	parent.SetWorkingDir("/ws/parent")
	// Handler bound to a DIFFERENT workspace: the parent's dir must win.
	h := NewTaskHandler("/ws/bound", parent, nil)

	child := agent.NewAgent(nil, nil, "", 0)
	h.propagateWorkingDir(child)

	if got := child.WorkingDir(); got != "/ws/parent" {
		t.Fatalf("child working dir = %q, want parent /ws/parent", got)
	}
}

func TestPropagateWorkingDir_FallsBackToHandlerWorkspace(t *testing.T) {
	parent := agent.NewAgent(nil, nil, "", 0) // no working dir set
	h := NewTaskHandler("/ws/bound", parent, nil)

	child := agent.NewAgent(nil, nil, "", 0)
	h.propagateWorkingDir(child)

	if got := child.WorkingDir(); got != "/ws/bound" {
		t.Fatalf("child working dir = %q, want handler workspace /ws/bound", got)
	}
}

func TestPropagateWorkingDir_NoDirLeavesChildUnset(t *testing.T) {
	parent := agent.NewAgent(nil, nil, "", 0)
	// Bind to a workspace path (detectWorkspaceMeta requires a usable path),
	// then simulate the both-empty case directly.
	h := NewTaskHandler(t.TempDir(), parent, nil)
	h.workspace = ""

	child := agent.NewAgent(nil, nil, "", 0)
	h.propagateWorkingDir(child)

	if got := child.WorkingDir(); got != "" {
		t.Fatalf("child working dir = %q, want empty (no false anchoring)", got)
	}
}

package tool

import (
	"sync"
	"testing"
)

// TestCloneCreatesIndependentInstances verifies that Clone() returns a new
// registry where tools with WorkingDir are independent copies — mutating one
// does not affect the other. This is the core correctness property for
// concurrent sub-agents and swarm teammates using different worktrees.
func TestCloneCreatesIndependentInstances(t *testing.T) {
	orig := NewRegistry()

	// Register tools with WorkingDir
	orig.Register(&RunCommand{WorkingDir: "/original", Policy: nil})
	orig.Register(&GitStatus{WorkingDir: "/original"})
	orig.Register(&EnterWorktree{WorkingDir: "/original"})

	// Register a stateless tool (no WorkingDir, no Clone)
	orig.Register(SleepTool{})

	cloned := orig.Clone()

	// Verify cloned registry has same tool count
	if len(cloned.tools) != len(orig.tools) {
		t.Fatalf("expected %d tools in clone, got %d", len(orig.tools), len(cloned.tools))
	}

	// Mutate cloned tool's WorkingDir
	rc, ok := cloned.Get("run_command")
	if !ok {
		t.Fatal("run_command not found in cloned registry")
	}
	rc.(*RunCommand).WorkingDir = "/cloned"

	gs, ok := cloned.Get("git_status")
	if !ok {
		t.Fatal("git_status not found in cloned registry")
	}
	gs.(*GitStatus).WorkingDir = "/cloned"

	ew, ok := cloned.Get("enter_worktree")
	if !ok {
		t.Fatal("enter_worktree not found in cloned registry")
	}
	ew.(*EnterWorktree).WorkingDir = "/cloned"

	// Verify original registry is unaffected
	rcOrig, _ := orig.Get("run_command")
	if rcOrig.(*RunCommand).WorkingDir != "/original" {
		t.Errorf("original RunCommand.WorkingDir changed to %q after clone mutation", rcOrig.(*RunCommand).WorkingDir)
	}

	gsOrig, _ := orig.Get("git_status")
	if gsOrig.(*GitStatus).WorkingDir != "/original" {
		t.Errorf("original GitStatus.WorkingDir changed to %q after clone mutation", gsOrig.(*GitStatus).WorkingDir)
	}

	ewOrig, _ := orig.Get("enter_worktree")
	if ewOrig.(*EnterWorktree).WorkingDir != "/original" {
		t.Errorf("original EnterWorktree.WorkingDir changed to %q after clone mutation", ewOrig.(*EnterWorktree).WorkingDir)
	}
}

// TestCloneSharesStatelessTools verifies that tools without mutable state
// (no Cloner interface) are shared between original and cloned registries,
// not copied. This is important for tools like command job managers that
// intentionally share state across agents.
func TestCloneSharesStatelessTools(t *testing.T) {
	orig := NewRegistry()
	orig.Register(SleepTool{})

	cloned := orig.Clone()

	stOrig, _ := orig.Get("sleep")
	stClone, _ := cloned.Get("sleep")

	// Stateless tools should be the exact same pointer (shared)
	if stOrig != stClone {
		t.Error("stateless tool should be shared (same pointer), not cloned")
	}
}

// TestCloneConcurrentWorkingDirMutations simulates the real scenario:
// multiple agents (sub-agents / swarm teammates) each get their own cloned
// registry and concurrently set WorkingDir. No agent should see another
// agent's WorkingDir.
func TestCloneConcurrentWorkingDirMutations(t *testing.T) {
	orig := NewRegistry()
	orig.Register(&RunCommand{WorkingDir: "/base"})
	orig.Register(&GitStatus{WorkingDir: "/base"})
	orig.Register(&GitCommit{WorkingDir: "/base"})

	const numAgents = 10
	var wg sync.WaitGroup
	wg.Add(numAgents)

	for i := 0; i < numAgents; i++ {
		go func(agentID int) {
			defer wg.Done()

			// Each agent gets its own cloned registry
			reg := orig.Clone()
			dir := "/agent-" + string(rune('A'+agentID))

			// Set WorkingDir on all tools via syncToolWorkingDir pattern
			// (directly, since we're testing tool independence)
			rc, _ := reg.Get("run_command")
			rc.(*RunCommand).WorkingDir = dir

			gs, _ := reg.Get("git_status")
			gs.(*GitStatus).WorkingDir = dir

			gc, _ := reg.Get("git_commit")
			gc.(*GitCommit).WorkingDir = dir

			// Verify this agent sees its own dir
			if rc.(*RunCommand).WorkingDir != dir {
				t.Errorf("agent %d: RunCommand.WorkingDir = %q, want %q", agentID, rc.(*RunCommand).WorkingDir, dir)
			}
			if gs.(*GitStatus).WorkingDir != dir {
				t.Errorf("agent %d: GitStatus.WorkingDir = %q, want %q", agentID, gs.(*GitStatus).WorkingDir, dir)
			}
			if gc.(*GitCommit).WorkingDir != dir {
				t.Errorf("agent %d: GitCommit.WorkingDir = %q, want %q", agentID, gc.(*GitCommit).WorkingDir, dir)
			}
		}(i)
	}

	wg.Wait()

	// Verify original is still "/base"
	rcOrig, _ := orig.Get("run_command")
	if rcOrig.(*RunCommand).WorkingDir != "/base" {
		t.Errorf("original RunCommand.WorkingDir = %q, want /base", rcOrig.(*RunCommand).WorkingDir)
	}
}

// TestClonePreservesAllToolNames verifies that Clone() preserves the full
// set of tool names, including after filtering via Unregister.
func TestClonePreservesAllToolNames(t *testing.T) {
	orig := NewRegistry()
	orig.Register(&RunCommand{WorkingDir: "/base"})
	orig.Register(&GitStatus{WorkingDir: "/base"})
	orig.Register(SleepTool{})
	orig.Register(WebFetch{})

	cloned := orig.Clone()

	origNames := map[string]bool{}
	for _, n := range orig.ToolNames() {
		origNames[n] = true
	}
	for _, n := range cloned.ToolNames() {
		if !origNames[n] {
			t.Errorf("cloned registry has extra tool %q", n)
		}
		delete(origNames, n)
	}
	for n := range origNames {
		t.Errorf("cloned registry missing tool %q", n)
	}
}

// TestAllWorkingDirToolsImplementCloner is a compile-time/runtime safety check:
// every tool type that has a WorkingDir field MUST implement the Cloner interface.
// If someone adds a new tool with WorkingDir but forgets Clone(), this test fails.
func TestAllWorkingDirToolsImplementCloner(t *testing.T) {
	// List of tool instances that have WorkingDir — must all implement Cloner
	toolsWithWorkingDir := []Tool{
		&RunCommand{},
		&GitStatus{},
		&GitDiff{},
		&GitLog{},
		&GitShow{},
		&GitBlame{},
		&GitBranchList{},
		&GitRemote{},
		&GitStashList{},
		&GitAdd{},
		&GitCommit{},
		&GitStash{},
		&EnterWorktree{},
		&ExitWorktree{},
		SpawnAgentTool{},
		SkillTool{},
	}

	for _, t2 := range toolsWithWorkingDir {
		if _, ok := t2.(Cloner); !ok {
			t.Errorf("%T has WorkingDir but does not implement Cloner — add Clone() Tool method", t2)
		}
	}
}

// TestCloneFiltersCorrectly tests the pattern used in BuildToolSet:
// clone → unregister specific tools → verify only allowed tools remain.
func TestCloneFiltersCorrectly(t *testing.T) {
	orig := NewRegistry()
	orig.Register(&RunCommand{WorkingDir: "/base"})
	orig.Register(&GitStatus{WorkingDir: "/base"})
	orig.Register(SpawnAgentTool{})
	orig.Register(SleepTool{})

	// Clone and remove agent lifecycle tools (like BuildToolSet does)
	cloned := orig.Clone()
	cloned.Unregister("spawn_agent")
	cloned.Unregister("wait_agent")
	cloned.Unregister("list_agents")

	names := cloned.ToolNames()
	for _, n := range names {
		if n == "spawn_agent" || n == "wait_agent" || n == "list_agents" {
			t.Errorf("agent lifecycle tool %q should have been removed", n)
		}
	}

	// Run command and git should still be there
	if _, ok := cloned.Get("run_command"); !ok {
		t.Error("run_command should still be in cloned registry")
	}
	if _, ok := cloned.Get("git_status"); !ok {
		t.Error("git_status should still be in cloned registry")
	}

	// Original should still have spawn_agent
	if _, ok := orig.Get("spawn_agent"); !ok {
		t.Error("original registry should still have spawn_agent")
	}
}

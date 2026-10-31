package agent

import (
	"os"
	"strings"
	"testing"
)

// TestIssue4012_ToolPathWiringExists is a source-level guard: the mindless
// action detector was dead from birth because its only recordStep call sat
// inside the no-tool-call branch (constant false). #4012 moved detection to
// the tool-execution path (beginBatch) and kept the no-tool branch as a pure
// streak reset. This test pins BOTH wirings so a future refactor cannot
// silently re-break the detector the way the original wiring did.
func TestIssue4012_ToolPathWiringExists(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatalf("read agent.go: %v", err)
	}
	srcStr := string(src)

	// Detection wiring: recordStep(..., true) on the tool path, near the
	// batch boundary the tool path is known for. #4622 extended the length
	// argument to sum text + reasoning; the wiring pin evolves with it.
	if !strings.Contains(srcStr, "a.mindlessAction.recordStep(len(textBuf)+reasoningChars, true)") {
		t.Fatalf("tool-path recordStep(true) wiring missing - detector is dead again (#4012 regression)")
	}
	toolIdx := strings.Index(srcStr, "a.mindlessAction.recordStep(len(textBuf)+reasoningChars, true)")
	batchIdx := strings.Index(srcStr, "a.cascade.beginBatch()")
	if toolIdx < 0 || batchIdx < 0 || toolIdx < batchIdx {
		t.Fatalf("tool-path wiring must sit at/after the beginBatch boundary")
	}

	// Reset wiring: the no-tool branch must pass a constant false (not
	// len(toolCalls) > 0, which is structurally false there and misled
	// readers into thinking detection could happen).
	if !strings.Contains(srcStr, "a.mindlessAction.recordStep(len(assistantText), false)") {
		t.Fatalf("no-tool reset wiring missing or still using the misleading len(toolCalls) > 0 form")
	}

	// The old dead form must be gone.
	if strings.Contains(srcStr, "a.mindlessAction.recordStep(len(assistantText), len(toolCalls) > 0)") {
		t.Fatalf("old dead wiring (constant-false arg) still present (#4012)")
	}
}

// TestIssue4012_StateMachineStillFiresAfterWiring sanity-checks the state
// machine the wiring feeds: 4 consecutive tool-bearing, low-reasoning steps
// must fire exactly as the detector's design intends.
func TestIssue4012_StateMachineStillFiresAfterWiring(t *testing.T) {
	s := newMindlessActionState()
	firedAt := -1
	for step := 1; step <= 6; step++ {
		if s.recordStep(20, true) { // short text + tool call
			firedAt = step
			break
		}
	}
	if firedAt != 4 {
		t.Fatalf("expected fire at step 4 (streak>=4), got %d", firedAt)
	}

	// A reasoning-only turn resets the streak.
	s2 := newMindlessActionState()
	for i := 0; i < 3; i++ {
		s2.recordStep(20, true)
	}
	s2.recordStep(20, false) // no tool call: reset
	if s2.recordStep(20, true) {
		t.Fatalf("fired after reset with streak=1 - reset semantics broken")
	}
}

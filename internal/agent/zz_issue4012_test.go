package agent

import (
	"strings"
	"testing"
)

func TestIssue4012_ToolPathWiringExists(t *testing.T) {
	// #5543: the tool path and no-tool path split into run_tool_calls.go
	// and run_no_tool_calls.go; scan the set. Concatenation order puts
	// the tool path first, so the beginBatch ordering pin still reads
	// left-to-right (shape-agnostic file targeting).
	srcStr, err := readStreamLoopSource("run_tool_calls.go", "run_tool_call_idx.go", "run_no_tool_calls.go")
	if err != nil {
		t.Fatalf("read stream-loop source: %v", err)
	}

	if !strings.Contains(srcStr, "a.mindlessAction.recordStep(len(textBuf)+reasoningChars, true)") {
		t.Fatalf("tool-path recordStep(true) wiring missing - detector is dead again (#4012 regression)")
	}
	toolIdx := strings.Index(srcStr, "a.mindlessAction.recordStep(len(textBuf)+reasoningChars, true)")
	batchIdx := strings.Index(srcStr, "a.cascade.beginBatch()")
	if toolIdx < 0 || batchIdx < 0 || toolIdx < batchIdx {
		t.Fatalf("tool-path wiring must sit at/after the beginBatch boundary")
	}

	if !strings.Contains(srcStr, "a.mindlessAction.recordStep(len(assistantText), false)") {
		t.Fatalf("no-tool reset wiring missing or still using the misleading len(toolCalls) > 0 form")
	}

	if strings.Contains(srcStr, "a.mindlessAction.recordStep(len(assistantText), len(toolCalls) > 0)") {
		t.Fatalf("old dead wiring (constant-false arg) still present (#4012)")
	}
}

func TestIssue4012_StateMachineStillFiresAfterWiring(t *testing.T) {
	s := newMindlessActionState()
	firedAt := -1
	for step := 1; step <= 6; step++ {
		if s.recordStep(20, true) {
			firedAt = step
			break
		}
	}
	if firedAt != 4 {
		t.Fatalf("expected fire at step 4 (streak>=4), got %d", firedAt)
	}

	s2 := newMindlessActionState()
	for i := 0; i < 3; i++ {
		s2.recordStep(20, true)
	}
	s2.recordStep(20, false)
	if s2.recordStep(20, true) {
		t.Fatalf("fired after reset with streak=1 - reset semantics broken")
	}
}

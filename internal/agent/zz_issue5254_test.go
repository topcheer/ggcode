package agent

import (
	"encoding/json"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// #5254: the four cross-file tracker gates (batch_conflict /
// edit_propagation / repetition_tracker / todo_check) still consumed the
// raw #3006-pinned map after #5250 unified the agent.go gates, so
// Responses-native apply_patch edits were invisible to them. Each gate now
// routes through mutatingSourceEdit, and batch_conflict's extractor grew
// the apply_patch case (gate conversion alone would be a silent no-op
// there - its switch is name-keyed).

func TestIssue5254_BatchConflictDetectsApplyPatchSameFile(t *testing.T) {
	calls := []provider.ToolCallDelta{
		{ID: "1", Name: "apply_patch", Arguments: json.RawMessage(`{"path":"internal/a.go","diff":"x"}`)},
		{ID: "2", Name: "apply_patch", Arguments: json.RawMessage(`{"path":"internal/a.go","diff":"y"}`)},
	}
	warns := detectBatchEditConflicts(calls)
	if len(warns) != 2 {
		t.Fatalf("two same-file apply_patch calls must both warn, got %v", warns)
	}
	// mixed: apply_patch vs edit_file on the same file also conflicts
	calls[1] = provider.ToolCallDelta{ID: "2", Name: "edit_file", Arguments: json.RawMessage(`{"path":"internal/a.go","old_text":"x","new_text":"y"}`)}
	warns = detectBatchEditConflicts(calls)
	if len(warns) != 2 {
		t.Fatalf("apply_patch vs edit_file same-file must warn, got %v", warns)
	}
}

func TestIssue5254_BatchConflictExtractorPullsApplyPatchPath(t *testing.T) {
	got := extractFilePathsForConflict(provider.ToolCallDelta{
		Name:      "apply_patch",
		Arguments: json.RawMessage(`{"path":"internal/b.go"}`),
	})
	if len(got) != 1 || got[0] != "internal/b.go" {
		t.Fatalf("want [internal/b.go], got %v", got)
	}
}

func TestIssue5254_EditPropagationRecordsApplyPatch(t *testing.T) {
	s := newEditPropagationState()
	if !s.recordEdit("apply_patch", `{"path":"internal/c.go","diff":"x"}`) {
		t.Fatal("apply_patch edit must be recorded by edit propagation (#5254)")
	}
	// second distinct file: still records; same file: no new distinct entry
	if !s.recordEdit("apply_patch", `{"path":"internal/d.go"}`) {
		t.Fatal("distinct apply_patch target must record")
	}
	if s.recordEdit("browser", `{"url":"https://x"}`) {
		t.Fatal("non-edit tools must not record")
	}
}

func TestIssue5254_RepetitionTrackerCountsApplyPatch(t *testing.T) {
	rt := newRepetitionTracker()
	// failures on the same apply_patch target accumulate through the gate
	for i := 0; i < 3; i++ {
		rt.recordEditAttempt("apply_patch", json.RawMessage(`{"path":"internal/e.go","diff":"x"}`), true)
	}
	rt.mu.Lock()
	n := rt.failedEditsByFile[repetitionFileKey("internal/e.go")]
	rt.mu.Unlock()
	if n != 3 {
		t.Fatalf("3 failed apply_patch attempts on one file must count, got %d", n)
	}
}

func TestIssue5254_TodoCheckCountsApplyPatchAsCodeChange(t *testing.T) {
	rs := &RunStats{ToolCalls: map[string]int{"apply_patch": 1}}
	if !codeChangedInRun(rs) {
		t.Fatal("an apply_patch-only run counts as a code change for todo discipline (#5254)")
	}
	if codeChangedInRun(&RunStats{ToolCalls: map[string]int{"grep": 2}}) {
		t.Fatal("read-only runs must not count as code changes")
	}
}

func TestIssue5254_CanonicalMapRatchetIntact(t *testing.T) {
	if len(sourceMutatingTools) != 9 {
		t.Fatalf("#3006: canonical map must stay at 9, got %d", len(sourceMutatingTools))
	}
	if !mutatingSourceEdit("apply_patch") {
		t.Fatal("helper must keep unioning apply_patch")
	}
	if mutatingSourceEdit("read_file") {
		t.Fatal("helper must not admit read-only tools")
	}
}

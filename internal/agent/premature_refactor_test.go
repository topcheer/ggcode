package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPrematureRefactor_StateReset(t *testing.T) {
	s := newPrematureRefactorState()
	s.refactorEdits = 5
	s.hasVerified = true
	s.warned = true

	s.reset()

	if s.refactorEdits != 0 || s.hasVerified || s.warned {
		t.Fatalf("reset did not clear state: %+v", s)
	}
}

func TestIsRefactoringContent(t *testing.T) {
	tests := []struct {
		name    string
		oldText string
		newText string
		want    bool
	}{
		{
			name:    "refactor keyword",
			oldText: strings.Repeat("a", 200),
			newText: "// refactor this function\n" + strings.Repeat("b", 180),
			want:    true,
		},
		{
			name:    "rename keyword",
			oldText: strings.Repeat("x", 200),
			newText: "// rename variable\n" + strings.Repeat("y", 190),
			want:    true,
		},
		{
			name:    "similar size restructuring",
			oldText: strings.Repeat("a", 500),
			newText: strings.Repeat("b", 480), // delta=20, ratio=0.04 < 0.3
			want:    true,
		},
		{
			name:    "adding new code (big size difference)",
			oldText: strings.Repeat("a", 100),
			newText: strings.Repeat("b", 500), // delta=400, ratio=0.8 > 0.3
			want:    false,
		},
		{
			name:    "too small old_text",
			oldText: strings.Repeat("a", 50),
			newText: strings.Repeat("b", 50),
			want:    false,
		},
		{
			name:    "empty",
			oldText: "",
			newText: "",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isRefactoringContent(tt.oldText, tt.newText)
			if got != tt.want {
				t.Errorf("isRefactoringContent() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClassifyRefactorEdit(t *testing.T) {
	// edit_file with similar sizes -> refactoring
	editArgs, _ := json.Marshal(map[string]string{
		"old_text": strings.Repeat("a", 300),
		"new_text": strings.Repeat("b", 290),
	})
	if !classifyRefactorEdit("edit_file", editArgs) {
		t.Error("edit_file with similar sizes should be classified as refactoring")
	}

	// edit_file with big size diff -> not refactoring
	editArgsBig, _ := json.Marshal(map[string]string{
		"old_text": strings.Repeat("a", 300),
		"new_text": strings.Repeat("b", 800),
	})
	if classifyRefactorEdit("edit_file", editArgsBig) {
		t.Error("edit_file with big size diff should NOT be classified as refactoring")
	}

	// batch_replace is always refactoring
	batchArgs, _ := json.Marshal(map[string]interface{}{
		"files": []map[string]string{{"pattern": "foo", "replacement": "bar"}},
	})
	if !classifyRefactorEdit("batch_replace", batchArgs) {
		t.Error("batch_replace should always be classified as refactoring")
	}

	// non-edit tool -> false
	if classifyRefactorEdit("read_file", editArgs) {
		t.Error("read_file should not be classified as refactoring")
	}

	// invalid JSON -> false
	if classifyRefactorEdit("edit_file", json.RawMessage(`{invalid`)) {
		t.Error("invalid JSON should return false")
	}

	// empty args -> false
	if classifyRefactorEdit("edit_file", nil) {
		t.Error("empty args should return false")
	}
}

func TestClassifyRefactorEdit_MultiEdit(t *testing.T) {
	// multi_edit_file with one refactoring sub-edit
	multiArgs, _ := json.Marshal(map[string]interface{}{
		"edits": []map[string]string{
			{
				"old_text": strings.Repeat("a", 300),
				"new_text": strings.Repeat("b", 295),
			},
		},
	})
	if !classifyRefactorEdit("multi_edit_file", multiArgs) {
		t.Error("multi_edit_file with similar-sized sub-edit should be refactoring")
	}
}

func TestPrematureRefactorCheck(t *testing.T) {
	a := &Agent{prematureRefactor: newPrematureRefactorState()}

	// No edits yet -> no warning
	if g := a.prematureRefactorCheck(); g != "" {
		t.Error("expected no guidance with zero edits")
	}

	// One refactoring edit -> not enough
	a.prematureRefactor.refactorEdits = 1
	if g := a.prematureRefactorCheck(); g != "" {
		t.Error("expected no guidance with only 1 refactoring edit")
	}

	// Two refactoring edits without verification -> warning
	a.prematureRefactor.refactorEdits = 2
	g := a.prematureRefactorCheck()
	if g == "" {
		t.Fatal("expected guidance with 2 refactoring edits and no verification")
	}
	if !strings.Contains(g, "Premature refactoring") {
		t.Errorf("guidance should mention 'Premature refactoring': %s", g)
	}
	if !strings.Contains(g, "build") {
		t.Errorf("guidance should mention build/test: %s", g)
	}

	// Already warned -> no more warnings
	if g2 := a.prematureRefactorCheck(); g2 != "" {
		t.Error("should not warn again after already warned")
	}

	// After verification -> no warning even if not yet warned
	a2 := &Agent{prematureRefactor: newPrematureRefactorState()}
	a2.prematureRefactor.refactorEdits = 5
	a2.prematureRefactor.hasVerified = true
	if g := a2.prematureRefactorCheck(); g != "" {
		t.Error("should not warn after verification has been done")
	}
}

func TestPrematureRefactorRecordEdit(t *testing.T) {
	a := &Agent{prematureRefactor: newPrematureRefactorState()}

	// Record a refactoring edit
	editArgs, _ := json.Marshal(map[string]string{
		"old_text": strings.Repeat("a", 300),
		"new_text": strings.Repeat("b", 295),
	})
	a.prematureRefactorRecordEdit("edit_file", editArgs)

	if a.prematureRefactor.refactorEdits != 1 {
		t.Errorf("expected 1 refactoring edit, got %d", a.prematureRefactor.refactorEdits)
	}

	// Record a non-refactoring edit (big size diff) -> should not increment
	bigArgs, _ := json.Marshal(map[string]string{
		"old_text": strings.Repeat("a", 100),
		"new_text": strings.Repeat("b", 800),
	})
	a.prematureRefactorRecordEdit("edit_file", bigArgs)

	if a.prematureRefactor.refactorEdits != 1 {
		t.Errorf("non-refactoring edit should not increment: got %d", a.prematureRefactor.refactorEdits)
	}

	// Record another refactoring edit -> now 2
	a.prematureRefactorRecordEdit("edit_file", editArgs)

	if a.prematureRefactor.refactorEdits != 2 {
		t.Errorf("expected 2 refactoring edits, got %d", a.prematureRefactor.refactorEdits)
	}
}

func TestPrematureRefactorRecordEdit_AfterVerify(t *testing.T) {
	a := &Agent{prematureRefactor: newPrematureRefactorState()}
	a.prematureRefactor.hasVerified = true

	editArgs, _ := json.Marshal(map[string]string{
		"old_text": strings.Repeat("a", 300),
		"new_text": strings.Repeat("b", 295),
	})
	a.prematureRefactorRecordEdit("edit_file", editArgs)

	if a.prematureRefactor.refactorEdits != 0 {
		t.Errorf("should not record refactoring edits after verification: got %d", a.prematureRefactor.refactorEdits)
	}
}

func TestPrematureRefactorRecordVerify(t *testing.T) {
	a := &Agent{prematureRefactor: newPrematureRefactorState()}

	if a.prematureRefactor.hasVerified {
		t.Error("should start unverified")
	}

	a.prematureRefactorRecordVerify()

	if !a.prematureRefactor.hasVerified {
		t.Error("should be verified after recordVerify call")
	}
}

func TestPrematureRefactorReset(t *testing.T) {
	a := &Agent{prematureRefactor: newPrematureRefactorState()}
	a.prematureRefactor.refactorEdits = 5
	a.prematureRefactor.hasVerified = true
	a.prematureRefactor.warned = true

	a.resetPrematureRefactor()

	if a.prematureRefactor.refactorEdits != 0 || a.prematureRefactor.hasVerified || a.prematureRefactor.warned {
		t.Errorf("reset failed: %+v", a.prematureRefactor)
	}
}

func TestPrematureRefactor_NilSafe(t *testing.T) {
	a := &Agent{prematureRefactor: nil}

	// All methods should be nil-safe
	a.prematureRefactorRecordEdit("edit_file", nil)
	a.prematureRefactorRecordVerify()
	if g := a.prematureRefactorCheck(); g != "" {
		t.Error("nil state should return empty guidance")
	}
	a.resetPrematureRefactor()
}

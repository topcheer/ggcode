package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// Regression guard for #744 defect 1: undo_edit with action=list is a pure
// read (checkpoint listing) and must never fire an annihilation warning,
// even when edits precede it in the window.
func TestActionAnnihilate_UndoEditListIsPureRead(t *testing.T) {
	s := newActionAnnihilateState()
	editArgs := rawJSON(t, map[string]interface{}{"file_path": "/tmp/x.go"})

	s.recordToolCall("edit_file", editArgs, 1)
	listArgs := rawJSON(t, map[string]interface{}{"action": "list"})
	warn := s.recordToolCall("undo_edit", listArgs, 2)

	if warn != "" {
		t.Fatalf("undo_edit action=list must not warn, got: %q", warn)
	}
	if s.cancelCount != 0 {
		t.Errorf("cancelCount = %d, want 0 for pure-read list", s.cancelCount)
	}
}

// Regression guard for #744 defect 2: when mixed edit tools precede an
// undo, the warning must cite the most recent prior edit (write_file), not
// whichever pair is declared first in annihilationPairs (edit_file).
func TestActionAnnihilate_UndoAttributionMostRecentPrior(t *testing.T) {
	s := newActionAnnihilateState()
	editA := rawJSON(t, map[string]interface{}{"file_path": "/tmp/a.go"})
	editAFix := rawJSON(t, map[string]interface{}{"file_path": "/tmp/a.go", "new": "fix"})
	writeB := rawJSON(t, map[string]interface{}{"path": "/tmp/b.go"})
	undoArgs := rawJSON(t, map[string]interface{}{"action": "undo"})

	s.recordToolCall("edit_file", editA, 1)
	s.recordToolCall("run_command", rawJSON(t, map[string]interface{}{"command": "ls"}), 2)
	s.recordToolCall("edit_file", editAFix, 3)
	s.recordToolCall("write_file", writeB, 4)
	warn := s.recordToolCall("undo_edit", undoArgs, 5)

	if warn == "" {
		t.Fatal("expected annihilation warning for write_file→undo_edit")
	}
	if !strings.Contains(warn, "write_file then undo_edit") {
		t.Errorf("warning must cite the most recent prior (write_file), got: %q", warn)
	}
	if !strings.Contains(warn, "Iterations 4-5") {
		t.Errorf("warning must span prior iteration 4 to current 5, got: %q", warn)
	}
	if strings.Contains(warn, "edit_file then undo_edit") {
		t.Errorf("warning must not attribute to the older edit_file, got: %q", warn)
	}
}

// undo_edit with missing/unparsable args must stay conservative and be
// treated as a state-changing undo (existing detection behavior preserved).
func TestActionAnnihilate_UndoEditMalformedArgsStillWarns(t *testing.T) {
	s := newActionAnnihilateState()
	editArgs := rawJSON(t, map[string]interface{}{"file_path": "/tmp/x.go"})

	s.recordToolCall("edit_file", editArgs, 1)
	warn := s.recordToolCall("undo_edit", json.RawMessage(`{malformed`), 2)

	if warn == "" {
		t.Fatal("malformed undo_edit args must still warn (conservative default)")
	}
}

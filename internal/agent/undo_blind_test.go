package agent

import (
	"encoding/json"
	"testing"
)

func TestUndoBlind_Reset(t *testing.T) {
	s := newUndoBlindState()
	s.pendingUndoFiles["foo.go"] = true
	s.warnCount = 1
	s.reset()
	if len(s.pendingUndoFiles) != 0 {
		t.Errorf("expected empty pendingUndoFiles after reset, got %d", len(s.pendingUndoFiles))
	}
	if s.warnCount != 0 {
		t.Errorf("expected warnCount=0 after reset, got %d", s.warnCount)
	}
}

func TestUndoBlind_UndoOpMarksFile(t *testing.T) {
	s := newUndoBlindState()
	args, _ := json.Marshal(map[string]string{"file_path": "main.go"})
	msg := s.recordToolCall("undo_edit", args)
	if msg != "" {
		t.Errorf("undo op should not produce warning, got %q", msg)
	}
	if !s.pendingUndoFiles["main.go"] {
		t.Error("expected main.go in pendingUndoFiles")
	}
}

func TestUndoBlind_BlindMutationAfterUndo(t *testing.T) {
	s := newUndoBlindState()

	// Step 1: undo_edit on main.go
	args, _ := json.Marshal(map[string]string{"file_path": "main.go"})
	s.recordToolCall("undo_edit", args)

	// Step 2: edit main.go WITHOUT re-reading -> should warn
	editArgs, _ := json.Marshal(map[string]string{"file_path": "main.go"})
	msg := s.recordToolCall("edit_file", editArgs)
	if msg == "" {
		t.Error("expected warning for blind edit after undo")
	}
}

func TestUndoBlind_ReadClearsPending(t *testing.T) {
	s := newUndoBlindState()

	// undo_edit on main.go
	args, _ := json.Marshal(map[string]string{"file_path": "main.go"})
	s.recordToolCall("undo_edit", args)

	// read_file clears the pending state
	readArgs, _ := json.Marshal(map[string]string{"path": "main.go"})
	s.recordToolCall("read_file", readArgs)

	// edit_file now should NOT warn
	editArgs, _ := json.Marshal(map[string]string{"file_path": "main.go"})
	msg := s.recordToolCall("edit_file", editArgs)
	if msg != "" {
		t.Errorf("expected no warning after read clears pending, got %q", msg)
	}
}

func TestUndoBlind_MaxWarnings(t *testing.T) {
	s := newUndoBlindState()

	// First undo + blind edit -> warning
	args1, _ := json.Marshal(map[string]string{"file_path": "a.go"})
	s.recordToolCall("undo_edit", args1)
	editArgs1, _ := json.Marshal(map[string]string{"file_path": "a.go"})
	msg1 := s.recordToolCall("edit_file", editArgs1)
	if msg1 == "" {
		t.Error("expected first warning")
	}

	// Second undo + blind edit -> warning
	args2, _ := json.Marshal(map[string]string{"file_path": "b.go"})
	s.recordToolCall("undo_edit", args2)
	editArgs2, _ := json.Marshal(map[string]string{"file_path": "b.go"})
	msg2 := s.recordToolCall("edit_file", editArgs2)
	if msg2 == "" {
		t.Error("expected second warning")
	}

	// Third undo + blind edit -> no warning (capped)
	args3, _ := json.Marshal(map[string]string{"file_path": "c.go"})
	s.recordToolCall("undo_edit", args3)
	editArgs3, _ := json.Marshal(map[string]string{"file_path": "c.go"})
	msg3 := s.recordToolCall("edit_file", editArgs3)
	if msg3 != "" {
		t.Errorf("expected no third warning (max reached), got %q", msg3)
	}
}

func TestUndoBlind_GitResetWildcard(t *testing.T) {
	s := newUndoBlindState()

	// git_reset without file path -> wildcard
	args, _ := json.Marshal(map[string]string{"mode": "hard"})
	s.recordToolCall("git_reset", args)

	if !s.pendingUndoFiles["*"] {
		t.Error("expected wildcard pending after git_reset without file")
	}

	// Any edit should trigger warning
	editArgs, _ := json.Marshal(map[string]string{"file_path": "any.go"})
	msg := s.recordToolCall("edit_file", editArgs)
	if msg == "" {
		t.Error("expected warning for blind edit after wildcard undo")
	}
}

func TestUndoBlind_DifferentFileNoWarn(t *testing.T) {
	s := newUndoBlindState()

	// undo_edit on main.go
	args, _ := json.Marshal(map[string]string{"file_path": "main.go"})
	s.recordToolCall("undo_edit", args)

	// edit on DIFFERENT file -> should NOT warn
	editArgs, _ := json.Marshal(map[string]string{"file_path": "other.go"})
	msg := s.recordToolCall("edit_file", editArgs)
	if msg != "" {
		t.Errorf("expected no warning for different file, got %q", msg)
	}
}

func TestUndoBlind_GitCheckoutNoFile(t *testing.T) {
	s := newUndoBlindState()

	// git_checkout with branch arg (no file)
	args, _ := json.Marshal(map[string]string{"branch": "feature-x"})
	s.recordToolCall("git_checkout", args)

	if !s.pendingUndoFiles["*"] {
		t.Error("expected wildcard pending after git_checkout without file")
	}
}

func TestUndoBlind_GitRevertWithMessage(t *testing.T) {
	s := newUndoBlindState()

	args, _ := json.Marshal(map[string]string{"commit": "abc123"})
	s.recordToolCall("git_revert", args)

	// git_revert doesn't have file_path, so no specific tracking
	if len(s.pendingUndoFiles) > 0 {
		// Only a wildcard or empty is expected
		if len(s.pendingUndoFiles) != 1 || !s.pendingUndoFiles["*"] {
			t.Errorf("expected at most wildcard, got %v", s.pendingUndoFiles)
		}
	}
}

func TestUndoBlind_IsUndoOp(t *testing.T) {
	tests := []struct {
		tool string
		want bool
	}{
		{"undo_edit", true},
		{"git_revert", true},
		{"git_reset", true},
		{"git_checkout", true},
		{"git_stash", true},
		{"edit_file", false},
		{"read_file", false},
		{"run_command", false},
	}
	for _, tt := range tests {
		if got := undoBlindIsUndoOp(tt.tool); got != tt.want {
			t.Errorf("undoBlindIsUndoOp(%q) = %v, want %v", tt.tool, got, tt.want)
		}
	}
}

func TestUndoBlind_IsMutation(t *testing.T) {
	tests := []struct {
		tool string
		want bool
	}{
		{"edit_file", true},
		{"multi_edit_file", true},
		{"write_file", true},
		{"multi_file_edit", true},
		{"batch_replace", true},
		{"lsp_rename", true},
		{"read_file", false},
		{"grep", false},
		{"run_command", false},
	}
	for _, tt := range tests {
		if got := undoBlindIsMutation(tt.tool); got != tt.want {
			t.Errorf("undoBlindIsMutation(%q) = %v, want %v", tt.tool, got, tt.want)
		}
	}
}

func TestUndoBlind_IsRead(t *testing.T) {
	tests := []struct {
		tool string
		want bool
	}{
		{"read_file", true},
		{"multi_file_read", true},
		{"git_show", true},
		{"git_diff", true},
		{"edit_file", false},
		{"run_command", false},
	}
	for _, tt := range tests {
		if got := undoBlindIsRead(tt.tool); got != tt.want {
			t.Errorf("undoBlindIsRead(%q) = %v, want %v", tt.tool, got, tt.want)
		}
	}
}

func TestUndoBlind_WriteFileAfterUndo(t *testing.T) {
	s := newUndoBlindState()

	// undo_edit on main.go
	args, _ := json.Marshal(map[string]string{"file_path": "main.go"})
	s.recordToolCall("undo_edit", args)

	// write_file on main.go WITHOUT re-reading -> should warn
	writeArgs, _ := json.Marshal(map[string]string{"path": "main.go"})
	msg := s.recordToolCall("write_file", writeArgs)
	if msg == "" {
		t.Error("expected warning for blind write after undo")
	}
}

func TestUndoBlind_NonUndoOpNoTracking(t *testing.T) {
	s := newUndoBlindState()

	// Regular tool call should not set any pending
	args, _ := json.Marshal(map[string]string{"file_path": "main.go"})
	s.recordToolCall("grep", args)

	if len(s.pendingUndoFiles) > 0 {
		t.Errorf("expected no pending files after grep, got %v", s.pendingUndoFiles)
	}
}

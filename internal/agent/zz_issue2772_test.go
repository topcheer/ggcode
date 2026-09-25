package agent

import (
	"encoding/json"
	"testing"
)

// TestIssue2772PathReadDoesNotClearWildcard pins #2772: after a tree-wide
// revert (pendingUndoFiles["*"]), any single path-carrying read - even of a
// file unrelated to the revert - must NOT disarm the whole-tree blind-edit
// guard. Reading one file is not re-grounding the tree; only the mutation
// branch consuming the wildcard (its warning flow) may clear it.
func TestIssue2772PathReadDoesNotClearWildcard(t *testing.T) {
	s := newUndoBlindState()
	s.pendingUndoFiles["*"] = true

	// A path-carrying read of an UNRELATED file (README.md).
	args, _ := json.Marshal(map[string]string{"path": "/repo/README.md"})
	if msg := s.recordToolCall("read_file", args); msg != "" {
		t.Fatalf("unexpected warning on read: %q", msg)
	}

	// The whole-tree guard must survive.
	if _, ok := s.pendingUndoFiles["*"]; !ok {
		t.Fatal("path-carrying read of an unrelated file cleared the tree-wide wildcard - blind-edit guard silently disarmed (#2772)")
	}

	// The very next mutation must still trigger the undo-blind warning.
	mArgs, _ := json.Marshal(map[string]string{"file_path": "/repo/internal/foo.go", "old_text": "x", "new_text": "y"})
	msg := s.recordToolCall("edit_file", mArgs)
	if msg == "" {
		t.Fatal("mutation after unrelated read did not warn - tree-wide guard was disarmed")
	}
}

// TestIssue2772SpecificFileReadStillGrounds pins that the per-file
// grounding semantics are unchanged: reading a file that has its own
// pending entry still clears that specific entry.
func TestIssue2772SpecificFileReadStillGrounds(t *testing.T) {
	s := newUndoBlindState()
	s.pendingUndoFiles["/repo/a.go"] = true
	s.pendingUndoFiles["*"] = true

	args, _ := json.Marshal(map[string]string{"path": "/repo/a.go"})
	if msg := s.recordToolCall("read_file", args); msg != "" {
		t.Fatalf("unexpected warning on read: %q", msg)
	}
	if _, ok := s.pendingUndoFiles["/repo/a.go"]; ok {
		t.Fatal("per-file grounding broken: specific entry not cleared by its own read")
	}
	// Wildcard survives per-file grounding (tree not re-grounded).
	if _, ok := s.pendingUndoFiles["*"]; !ok {
		t.Fatal("per-file read also cleared the wildcard - expected wildcard to survive")
	}
}

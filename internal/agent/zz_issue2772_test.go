package agent

import (
	"encoding/json"
	"testing"
)

func TestIssue2772PathReadDoesNotClearWildcard(t *testing.T) {
	s := newUndoBlindState()
	s.pendingUndoFiles["*"] = true

	args, _ := json.Marshal(map[string]string{"path": "/repo/README.md"})
	if msg := s.recordToolCall("read_file", args); msg != "" {
		t.Fatalf("unexpected warning on read: %q", msg)
	}

	if _, ok := s.pendingUndoFiles["*"]; !ok {
		t.Fatal("path-carrying read of an unrelated file cleared the tree-wide wildcard - blind-edit guard silently disarmed (#2772)")
	}

	mArgs, _ := json.Marshal(map[string]string{"file_path": "/repo/internal/foo.go", "old_text": "x", "new_text": "y"})
	msg := s.recordToolCall("edit_file", mArgs)
	if msg == "" {
		t.Fatal("mutation after unrelated read did not warn - tree-wide guard was disarmed")
	}
}

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

	if _, ok := s.pendingUndoFiles["*"]; !ok {
		t.Fatal("per-file read also cleared the wildcard - expected wildcard to survive")
	}
}

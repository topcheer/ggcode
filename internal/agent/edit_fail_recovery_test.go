package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEditFailRecovery_BelowThreshold(t *testing.T) {
	s := newEditFailState()
	// First failure — no guidance yet.
	hint := s.recordEditFailure("/src/foo.go")
	if hint != "" {
		t.Fatalf("expected no guidance on first failure, got: %s", hint)
	}
}

func TestEditFailRecovery_AtThreshold(t *testing.T) {
	s := newEditFailState()
	s.recordEditFailure("/src/foo.go")
	hint := s.recordEditFailure("/src/foo.go")
	if hint == "" {
		t.Fatal("expected guidance on 2nd consecutive failure")
	}
	if !strings.Contains(hint, "consecutive failed edit") {
		t.Errorf("hint should mention consecutive failures, got: %s", hint)
	}
	if !strings.Contains(hint, "read_file") {
		t.Errorf("hint should suggest read_file, got: %s", hint)
	}
}

func TestEditFailRecovery_NoDuplicateGuidance(t *testing.T) {
	s := newEditFailState()
	s.recordEditFailure("/src/foo.go")
	hint1 := s.recordEditFailure("/src/foo.go")
	if hint1 == "" {
		t.Fatal("expected guidance on 2nd failure")
	}
	// Third failure — guidance already fired, should not repeat.
	hint2 := s.recordEditFailure("/src/foo.go")
	if hint2 != "" {
		t.Fatalf("expected no duplicate guidance, got: %s", hint2)
	}
}

func TestEditFailRecovery_SuccessResets(t *testing.T) {
	s := newEditFailState()
	s.recordEditFailure("/src/foo.go")
	s.recordEditSuccess("/src/foo.go")
	// After success, next failure should be "first" again.
	hint := s.recordEditFailure("/src/foo.go")
	if hint != "" {
		t.Fatalf("expected no guidance after success reset, got: %s", hint)
	}
}

func TestEditFailRecovery_ReadResets(t *testing.T) {
	s := newEditFailState()
	s.recordEditFailure("/src/foo.go")
	s.recordRead("/src/foo.go")
	// After read, next failure should be "first" again.
	hint := s.recordEditFailure("/src/foo.go")
	if hint != "" {
		t.Fatalf("expected no guidance after read reset, got: %s", hint)
	}
}

func TestEditFailRecovery_PerFileTracking(t *testing.T) {
	s := newEditFailState()
	// Failures on different files are tracked independently.
	s.recordEditFailure("/src/a.go")
	s.recordEditFailure("/src/b.go")
	// Second failure on a.go triggers guidance.
	hintA := s.recordEditFailure("/src/a.go")
	if hintA == "" {
		t.Fatal("expected guidance for a.go on its 2nd failure")
	}
	// b.go only has 1 failure — no guidance.
	hintB := s.recordEditFailure("/src/b.go")
	if hintB == "" {
		t.Fatal("expected guidance for b.go on its 2nd failure")
	}
}

func TestEditFailRecovery_Reset(t *testing.T) {
	s := newEditFailState()
	s.recordEditFailure("/src/foo.go")
	s.recordEditFailure("/src/foo.go") // guidance fires
	s.reset()
	// After reset, same file starts fresh.
	hint := s.recordEditFailure("/src/foo.go")
	if hint != "" {
		t.Fatalf("expected no guidance after reset, got: %s", hint)
	}
}

func TestEditFailRecovery_EmptyPath(t *testing.T) {
	s := newEditFailState()
	if hint := s.recordEditFailure(""); hint != "" {
		t.Fatalf("expected no guidance for empty path, got: %s", hint)
	}
	s.recordEditSuccess("")
	s.recordRead("")
	// Should not panic
}

func TestEditFailRecovery_Summarize(t *testing.T) {
	s := newEditFailState()
	if summary := s.summarizeEditFailures(); summary != "" {
		t.Fatalf("expected empty summary with no failures, got: %s", summary)
	}
	s.recordEditFailure("/src/very/long/path/foo.go")
	s.recordEditFailure("/src/very/long/path/foo.go")
	s.recordEditFailure("/src/bar.go")
	summary := s.summarizeEditFailures()
	if !strings.Contains(summary, "foo.go") {
		t.Errorf("summary should contain foo.go, got: %s", summary)
	}
	if !strings.Contains(summary, "bar.go") {
		t.Errorf("summary should contain bar.go, got: %s", summary)
	}
}

func TestExtractFileForEdit(t *testing.T) {
	args := json.RawMessage(`{"file_path": "/src/test.go", "old_text": "a", "new_text": "b"}`)
	path := extractFileForEdit("edit_file", args)
	if path != "/src/test.go" {
		t.Errorf("expected /src/test.go, got %s", path)
	}
	// Unknown tool
	if path := extractFileForEdit("unknown", args); path != "" {
		t.Errorf("expected empty for unknown tool, got %s", path)
	}
}

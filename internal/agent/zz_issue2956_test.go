package agent

import (
	"strings"
	"testing"
)

func TestIssue2956FileOpsDeleteFailuresTriggerFixation(t *testing.T) {
	s := newSolutionFixationState()

	args := `{"operations":[{"action":"delete","source":"/src/locked.go"}]}`
	s.recordToolCall("file_ops", args, true)
	s.recordToolCall("file_ops", args, true)
	s.recordToolCall("file_ops", args, true)
	msg := s.checkAndWarn()
	if msg == "" {
		t.Fatal("#2956: expected Solution Fixation warning after 3 failed file_ops deletes on same file")
	}
	if !strings.Contains(msg, "locked.go") {
		t.Errorf("warning should mention the file_ops target, got: %s", msg)
	}
}

func TestIssue2956FileOpsMoveAttribution(t *testing.T) {

	s := newSolutionFixationState()
	args := `{"operations":[{"action":"move","source":"/src/a.go","destination":"/src/b.go"}]}`
	s.recordToolCall("file_ops", args, true)
	s.recordToolCall("file_ops", args, true)
	s.recordToolCall("file_ops", args, true)

	if s.failedByFile["/src/a.go"] != 3 || s.failedByFile["/src/b.go"] != 3 {
		t.Fatalf("move must attribute BOTH endpoints (3 failures each), got: %v", s.failedByFile)
	}
	msg := s.checkAndWarn()
	if msg == "" {
		t.Fatal("#2956: expected warning after 3 failed file_ops moves on same target pair")
	}
	if !strings.Contains(msg, "a.go") && !strings.Contains(msg, "b.go") {
		t.Errorf("warning should mention a move endpoint, got: %s", msg)
	}
}

func TestIssue2956FileOpsSuccessDoesNotTrigger(t *testing.T) {
	s := newSolutionFixationState()
	args := `{"operations":[{"action":"delete","source":"/src/ok.go"}]}`
	s.recordToolCall("file_ops", args, false)
	s.recordToolCall("file_ops", args, false)
	s.recordToolCall("file_ops", args, false)
	if msg := s.checkAndWarn(); msg != "" {
		t.Fatalf("successful file_ops must not count as failures, got: %s", msg)
	}
}

func TestIssue2956ExtractFilePathsFileOpsSchema(t *testing.T) {

	paths := extractFilePathsFromEditArgs(`{"operations":[{"action":"mkdir","source":"newdir"}]}`)
	if len(paths) != 1 || !strings.Contains(paths[0], "newdir") {
		t.Errorf("mkdir source not extracted: %v", paths)
	}
	paths = extractFilePathsFromEditArgs(`{"operations":[{"action":"move","source":"/a/x.go","destination":"/b/y.go"}]}`)
	if len(paths) != 2 {
		t.Errorf("move should yield source+destination, got: %v", paths)
	}

	paths = extractFilePathsFromEditArgs(`{"operations":[{"action":"delete","source":"/a.go"},{"action":"delete","source":"/b.go"}]}`)
	if len(paths) != 2 {
		t.Errorf("multi-op batch should yield both sources, got: %v", paths)
	}
}

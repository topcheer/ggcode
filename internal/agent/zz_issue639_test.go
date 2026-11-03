package agent

import (
	"strings"
	"testing"
)

func TestIssue639_WindowSlidesOnAllToolCalls(t *testing.T) {
	s := newSolutionFixationState()

	record := func(tool, args string, isErr bool) {
		s.recordToolCall(tool, args, isErr)
		_ = s.checkAndWarn()
	}

	record("edit_file", `{"file_path":"/src/x.go"}`, true)

	for i := 0; i < 11; i++ {
		record("read_file", `{"path":"/src/other.go"}`, false)
	}

	record("edit_file", `{"file_path":"/src/x.go"}`, true)
	record("edit_file", `{"file_path":"/src/x.go"}`, true)

	if msg := s.checkAndWarn(); msg != "" {
		t.Fatalf("#639 regression: window slid on edit calls only; 3 failures spanning 14 tool calls fired: %s", msg)
	}
	if got := s.failedByFile["/src/x.go"]; got != 2 {
		t.Fatalf("expected first failure evicted from window, got %d in-window failures", got)
	}
}

func TestIssue639_TightFailureClusterStillFires(t *testing.T) {
	s := newSolutionFixationState()
	for i := 0; i < 3; i++ {
		s.recordToolCall("edit_file", `{"file_path":"/src/x.go"}`, true)
	}
	if msg := s.checkAndWarn(); msg == "" {
		t.Fatal("expected fire on 3 consecutive failed edits")
	}
}

func TestIssue639_MultiFileEditAttributedToAllFiles(t *testing.T) {
	s := newSolutionFixationState()
	s.recordToolCall("multi_file_edit", `{"files":[{"path":"/src/b.go"},{"path":"/src/c.go"},{"path":"/src/a.go"}]}`, true)
	s.recordToolCall("multi_file_edit", `{"files":[{"path":"/src/c.go"},{"path":"/src/a.go"},{"path":"/src/b.go"}]}`, true)
	s.recordToolCall("multi_file_edit", `{"files":[{"path":"/src/a.go"},{"path":"/src/b.go"},{"path":"/src/c.go"}]}`, true)

	for _, f := range []string{"/src/a.go", "/src/b.go", "/src/c.go"} {
		if got := s.failedByFile[f]; got != 3 {
			t.Fatalf("file %s: expected 3 attributed failures (Files[0]-only attribution bug), got %d", f, got)
		}
	}
	msg := s.checkAndWarn()
	if msg == "" {
		t.Fatal("expected fire when every batched file reaches the threshold")
	}
	if !strings.Contains(msg, "/src/") {
		t.Fatalf("warning should mention a file, got: %s", msg)
	}
}

func TestIssue639_MultiFileWriteCounted(t *testing.T) {
	s := newSolutionFixationState()
	for i := 0; i < 3; i++ {
		s.recordToolCall("multi_file_write", `{"files":[{"path":"/src/a.go"},{"path":"/src/b.go"}]}`, true)
	}
	if msg := s.checkAndWarn(); msg == "" {
		t.Fatal("multi_file_write failures must feed the fixation detector")
	}
}

func TestIssue639_CanonicalMutationToolListConsistency(t *testing.T) {
	want := []string{
		"edit_file", "write_file", "multi_edit_file", "multi_file_edit",
		"multi_file_write", "notebook_edit", "batch_replace", "lsp_rename",
	}
	for _, tool := range want {
		if !isAgentMutationEditTool(tool) {
			t.Errorf("canonical list missing %s", tool)
		}
		if !errorRushIsMutation(tool) {
			t.Errorf("error_rush mutation list missing %s (list drift)", tool)
		}
	}
	if isAgentMutationEditTool("run_command") || isAgentMutationEditTool("read_file") {
		t.Error("non-edit tools must not be in the canonical mutation list")
	}
}

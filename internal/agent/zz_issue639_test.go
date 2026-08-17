package agent

// #639: two defects in solution_fixation.
//  1. The sliding window advanced only on edit calls, so failures separated
//     by many healthy non-edit calls still stacked toward the threshold
//     (documented unit: "a sliding window of 12 tool calls").
//  2. multi_file_edit failures were attributed only to Files[0], and
//     multi_file_write/batch_replace/lsp_rename were absent from the edit
//     tool list while error_rush and momentum_loss counted them.

import (
	"strings"
	"testing"
)

// Defect 1: failures interleaved with enough non-edit tool calls must fall
// out of the 12-call window before reaching the threshold. With the old
// edit-only window the three X failures stacked regardless of the reads.
func TestIssue639_WindowSlidesOnAllToolCalls(t *testing.T) {
	s := newSolutionFixationState()

	record := func(tool, args string, isErr bool) {
		s.recordToolCall(tool, args, isErr)
		_ = s.checkAndWarn() // production wiring checks after every call
	}

	// One failure on X.
	record("edit_file", `{"file_path":"/src/x.go"}`, true)
	// 11 healthy non-edit calls (reads) — with the documented window unit
	// these advance the window past the first failure.
	for i := 0; i < 11; i++ {
		record("read_file", `{"path":"/src/other.go"}`, false)
	}
	// Two more sporadic failures on X, far apart in tool-call time.
	record("edit_file", `{"file_path":"/src/x.go"}`, true)
	record("edit_file", `{"file_path":"/src/x.go"}`, true)

	if msg := s.checkAndWarn(); msg != "" {
		t.Fatalf("#639 regression: window slid on edit calls only; 3 failures spanning 14 tool calls fired: %s", msg)
	}
	if got := s.failedByFile["/src/x.go"]; got != 2 {
		t.Fatalf("expected first failure evicted from window, got %d in-window failures", got)
	}
}

// Sanity: a genuine tight cluster (3 failed edits with no interleaved calls)
// must still fire.
func TestIssue639_TightFailureClusterStillFires(t *testing.T) {
	s := newSolutionFixationState()
	for i := 0; i < 3; i++ {
		s.recordToolCall("edit_file", `{"file_path":"/src/x.go"}`, true)
	}
	if msg := s.checkAndWarn(); msg == "" {
		t.Fatal("expected fire on 3 consecutive failed edits")
	}
}

// Defect 2a: a failed multi_file_edit must be attributed to EVERY file it
// touched, not just Files[0]. Three failed batches with reordered entries
// give each file 3 failures under full attribution.
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

// Defect 2b: multi_file_write failures count, with every files[] entry
// attributed (multi_file_write was missing from editToolsFixation entirely).
func TestIssue639_MultiFileWriteCounted(t *testing.T) {
	s := newSolutionFixationState()
	for i := 0; i < 3; i++ {
		s.recordToolCall("multi_file_write", `{"files":[{"path":"/src/a.go"},{"path":"/src/b.go"}]}`, true)
	}
	if msg := s.checkAndWarn(); msg == "" {
		t.Fatal("multi_file_write failures must feed the fixation detector")
	}
}

// Defect 2c: single canonical mutation-tool list — error_rush, momentum_loss,
// and solution_fixation must agree on which tools are file mutations.
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
		if !momentumProductiveTools[tool] {
			t.Errorf("momentum_loss productive list missing %s (list drift)", tool)
		}
	}
	if isAgentMutationEditTool("run_command") || isAgentMutationEditTool("read_file") {
		t.Error("non-edit tools must not be in the canonical mutation list")
	}
}

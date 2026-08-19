package agent

// zz_issue737_test.go -- pins issue #737: strategy_fixation mutation set must
// not drift from the canonical sourceMutatingTools superset (#153/#154), and
// sfExtractMutationPaths must parse batch_replace's files []string form and
// file_ops operations[].source/destination -- previously silently empty, so
// batch codemod retries (the most typical fixation scenario) were untracked.

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestIssue737_MutationSetMatchesCanonicalSuperset pins the drift fix: every
// tool in the canonical 9-tool superset must be classified as a mutation by
// strategyFixationIsMutation. Before #737, multi_file_write/batch_replace/
// lsp_rename/file_ops were missing.
func TestIssue737_MutationSetMatchesCanonicalSuperset(t *testing.T) {
	required := []string{
		"edit_file", "write_file", "multi_edit_file", "multi_file_edit",
		"multi_file_write", "batch_replace", "lsp_rename", "file_ops", "notebook_edit",
	}
	for _, tool := range required {
		if !strategyFixationIsMutation(tool) {
			t.Errorf("strategyFixationIsMutation(%q) = false, want true (canonical superset member)", tool)
		}
	}
	if len(required) != len(sourceMutatingTools) {
		t.Errorf("required list (%d) out of sync with sourceMutatingTools (%d)", len(required), len(sourceMutatingTools))
	}
	// Non-mutating tools must stay excluded.
	for _, tool := range []string{"read_file", "grep", "run_command", "lsp_diagnostics"} {
		if strategyFixationIsMutation(tool) {
			t.Errorf("strategyFixationIsMutation(%q) = true, want false", tool)
		}
	}
}

// TestIssue737_ExtractMutationPaths_BatchReplaceStringFiles: batch_replace
// carries files as []string (schema), which the []map-only branch silently
// dropped -- codemod retries extracted zero paths.
func TestIssue737_ExtractMutationPaths_BatchReplaceStringFiles(t *testing.T) {
	args := json.RawMessage(`{"pattern":"foo","replacement":"bar","files":["/x/a.go","/x/b.go","/x/c.go"]}`)
	got := sfExtractMutationPaths(args)
	want := []string{"/x/a.go", "/x/b.go", "/x/c.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sfExtractMutationPaths(batch_replace) = %v, want %v", got, want)
	}
}

// TestIssue737_ExtractMutationPaths_FileOpsOperations: file_ops carries its
// touched paths in operations[].source (always) and destination (move/rename).
func TestIssue737_ExtractMutationPaths_FileOpsOperations(t *testing.T) {
	args := json.RawMessage(`{"operations":[` +
		`{"action":"move","source":"/x/a.go","destination":"/x/b.go"},` +
		`{"action":"delete","source":"/x/c.go"},` +
		`{"action":"mkdir","source":"/x/d"}]}`)
	got := sfExtractMutationPaths(args)
	want := []string{"/x/a.go", "/x/b.go", "/x/c.go", "/x/d"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sfExtractMutationPaths(file_ops) = %v, want %v", got, want)
	}
}

// TestIssue737_ExtractMutationPaths_MapFilesNoRegression: the pre-existing
// []map form (multi_file_edit / multi_file_write) must keep working.
func TestIssue737_ExtractMutationPaths_MapFilesNoRegression(t *testing.T) {
	args := json.RawMessage(`{"files":[{"path":"a.go","content":"x"},{"path":"sub/b.go","content":"y"}]}`)
	got := sfExtractMutationPaths(args)
	want := []string{"a.go", "sub/b.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sfExtractMutationPaths(map files) = %v, want %v", got, want)
	}
}

// TestIssue737_BatchReplaceRetryTriggersFixationWarning: end-to-end pipeline --
// three failed batch_replace retries on the same file, with two failed
// verifications naming that file, must fire the strategy-fixation warning.
// Before #737, batch_replace mutations were neither classified nor extracted,
// so this exact scenario produced zero tracking.
func TestIssue737_BatchReplaceRetryTriggersFixationWarning(t *testing.T) {
	s := newStrategyFixationState()
	args := json.RawMessage(`{"pattern":"foo","replacement":"bar","files":["/proj/internal/agent/a.go"]}`)

	for i := 0; i < 3; i++ {
		if !strategyFixationIsMutation("batch_replace") {
			t.Fatal("precondition: batch_replace must be a mutation tool")
		}
		for _, p := range sfExtractMutationPaths(args) {
			s.recordEdit(p)
		}
		s.recordVerification("run_command", "FAIL: /proj/internal/agent/a.go:10: undefined: foo", true)
	}

	msg := s.check()
	if msg == "" {
		t.Fatal("expected strategy-fixation warning for batch_replace retries on a.go, got none")
	}
	if want := "[strategy-fixation]"; len(msg) < len(want) || msg[:len(want)] != want {
		t.Fatalf("warning prefix = %q, want prefix %q", msg, want)
	}
}

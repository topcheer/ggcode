package agent

import "testing"

// zz_issue2991_test.go - regression probes for #2991: multi_file_edit
// partial_success carries IsError=true while its written_paths files ARE on
// disk. The post-exec invalidation block (speculator/toolMemo
// TTL/commandCache) was gated on !IsError alone, so partial writes skipped
// invalidation entirely - cached grep/LSP/git results and (worst case) a
// pre-edit PASS commandCache entry kept serving stale state with no
// self-healing. #1028 principle: side effects already happened.

func TestIssue2991PartialSuccessWroteDespiteError(t *testing.T) {
	// Mirror the real result shape: JSON payload first (extractWrittenPaths
	// stops at the first decode error, so the object must lead), trailing
	// prose summarizing the partial outcome - same shape as the #1762
	// fixture in overseer_test.go.
	content := `{"written_files":1,"written_paths":["/a.go"],"failed_files":1,"failed_paths":["/b.go"]}` + "\npartial_success: 1 succeeded, 1 failed."
	if !partialEditWroteDespiteError("multi_file_edit", content, true) {
		t.Fatalf("#2991: partial_success with written_paths must count as wrote-despite-error (invalidation must run)")
	}
}

func TestIssue2991AtomicAllFailedNoInvalidation(t *testing.T) {
	// Atomic mode (default) writes NOTHING when any file fails: no
	// written_paths, no side effects - the IsError gate must keep skipping
	// invalidation for this shape.
	content := `multi_file_edit failed: no files were written (atomic mode).`
	if partialEditWroteDespiteError("multi_file_edit", content, true) {
		t.Fatalf("#2991: all-failed atomic result must NOT trigger invalidation")
	}
}

func TestIssue2991OnlyMultiFileEditShape(t *testing.T) {
	// Other errored tools with written_paths-shaped prose in output must
	// not trip the exemption - the shape is specific to multi_file_edit's
	// partial_success contract.
	content := `{"written_paths": ["a.go"]}`
	if partialEditWroteDespiteError("run_command", content, true) {
		t.Fatalf("#2991: exemption is multi_file_edit-specific")
	}
	if partialEditWroteDespiteError("multi_file_edit", content, false) {
		t.Fatalf("#2991: non-error results take the normal !IsError path, helper must stay false")
	}
}

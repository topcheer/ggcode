package agent

import "testing"

func TestIssue2991PartialSuccessWroteDespiteError(t *testing.T) {

	content := `{"written_files":1,"written_paths":["/a.go"],"failed_files":1,"failed_paths":["/b.go"]}` + "\npartial_success: 1 succeeded, 1 failed."
	if !partialEditWroteDespiteError("multi_file_edit", content, true) {
		t.Fatalf("#2991: partial_success with written_paths must count as wrote-despite-error (invalidation must run)")
	}
}

func TestIssue2991AtomicAllFailedNoInvalidation(t *testing.T) {

	content := `multi_file_edit failed: no files were written (atomic mode).`
	if partialEditWroteDespiteError("multi_file_edit", content, true) {
		t.Fatalf("#2991: all-failed atomic result must NOT trigger invalidation")
	}
}

func TestIssue2991OnlyMultiFileEditShape(t *testing.T) {

	content := `{"written_paths": ["a.go"]}`
	if partialEditWroteDespiteError("run_command", content, true) {
		t.Fatalf("#2991: exemption is multi_file_edit-specific")
	}
	if partialEditWroteDespiteError("multi_file_edit", content, false) {
		t.Fatalf("#2991: non-error results take the normal !IsError path, helper must stay false")
	}
}

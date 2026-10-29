package agent

// #3547 probe: multi_file_write and notebook_edit are write-class tools and
// must expose their target paths to path-predicated invariants. The old
// invariantTargetPath had no case for either (and opOf didn't even classify
// notebook_edit), so PathGlob-style rules silently never fired.

import "testing"

func TestIssue3547_MultiFileWriteTargetExtracted(t *testing.T) {
	args := []byte(`{"mode":"atomic","files":[{"path":"/repo/a.go","content":"x"},{"path":"/repo/b.go","content":"y"}]}`)
	if got := invariantOpOf("multi_file_write", args); got != "write" {
		t.Fatalf("multi_file_write op = %q, want write", got)
	}
	if got := invariantTargetPath("multi_file_write", args); got != "/repo/a.go" {
		t.Fatalf("multi_file_write target = %q, want /repo/a.go (first file)", got)
	}
	// Degenerate shapes fall back to empty (best-effort, like batch_replace).
	if got := invariantTargetPath("multi_file_write", []byte(`{"files":[]}`)); got != "" {
		t.Fatalf("empty files list should yield empty target, got %q", got)
	}
	if got := invariantTargetPath("multi_file_write", []byte(`not json`)); got != "" {
		t.Fatalf("garbage args should yield empty target, got %q", got)
	}
}

func TestIssue3547_NotebookEditClassifiedAndExtracted(t *testing.T) {
	args := []byte(`{"notebook_path":"/nb/analysis.ipynb","operation":"replace"}`)
	if got := invariantOpOf("notebook_edit", args); got != "write" {
		t.Fatalf("notebook_edit op = %q, want write", got)
	}
	if got := invariantTargetPath("notebook_edit", args); got != "/nb/analysis.ipynb" {
		t.Fatalf("notebook_edit target = %q, want /nb/analysis.ipynb", got)
	}
}

func TestIssue3547_ExistingTargetsUnchanged(t *testing.T) {
	// Regression guard: the pre-existing write-family extractions are intact.
	if got := invariantTargetPath("write_file", []byte(`{"file_path":"/w/f.go"}`)); got != "/w/f.go" {
		t.Fatalf("write_file target regressed: %q", got)
	}
	if got := invariantTargetPath("batch_replace", []byte(`{"files":["/r/1.go","/r/2.go"]}`)); got != "/r/1.go" {
		t.Fatalf("batch_replace target regressed: %q", got)
	}
}

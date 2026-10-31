//go:build goolm

package tool

// #4448 regression: file_ops move's pre-rename RemoveTracking dropped the
// source's read-state, and a FAILED rename (EPERM/EACCES/EINVAL) left the
// file physically in place but untracked - a later write_file false-
// positived the blind-write warning for a file the agent HAD read.

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestIssue4448_FailedMoveKeepsTrackerReadState(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based EACCES trigger is unix-only")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "a.go")
	dst := filepath.Join(dir, "sub", "b.go")
	if err := os.WriteFile(src, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Simulate the agent having read the source.
	defaultFileTracker.RecordRead(src)
	t.Cleanup(func() { defaultFileTracker.RemoveTracking(src) })
	if !defaultFileTracker.HasBeenSeen(src) {
		t.Fatal("precondition: source must be tracked after RecordRead")
	}

	// Make the rename fail with a non-EXDEV error: remove write permission
	// from the directory so rename(2) returns EACCES.
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	tool := FileOps{WorkingDir: dir}
	input := mustJSON(t, map[string]any{
		"operations": []map[string]any{
			{"action": "move", "source": src, "destination": dst},
		},
		"description": "test failed move keeps read state",
	})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("move under a read-only directory must fail: %s", result.Content)
	}
	// The physical file is untouched.
	if b, _ := os.ReadFile(src); string(b) != "data" {
		t.Fatalf("source damaged: %q", b)
	}
	// #4448: the read-state must survive the failed move.
	if !defaultFileTracker.HasBeenSeen(src) {
		t.Fatal("failed move dropped the source's read-state (#4448): a later write_file would false-positive the blind-write warning")
	}
}

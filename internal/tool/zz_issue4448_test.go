//go:build goolm

package tool

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

	defaultFileTracker.RecordRead(src)
	t.Cleanup(func() { defaultFileTracker.RemoveTracking(src) })
	if !defaultFileTracker.HasBeenSeen(src) {
		t.Fatal("precondition: source must be tracked after RecordRead")
	}

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

	if b, _ := os.ReadFile(src); string(b) != "data" {
		t.Fatalf("source damaged: %q", b)
	}

	if !defaultFileTracker.HasBeenSeen(src) {
		t.Fatal("failed move dropped the source's read-state (#4448): a later write_file would false-positive the blind-write warning")
	}
}

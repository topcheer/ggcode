package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// zz_issue4903_test.go pins the fix for #4903: multi_file_edit's apply paths
// (atomic and partial_success) wrote plan-stage snapshots blindly - no
// LockWritePath, no CheckStale. The #2327/#2315 stale discipline only ever
// covered write_file/multi_file_write/edit_file-single. The fix mirrors
// multi_file_write.go's guard via guardStaleWrite in BOTH apply paths.

func runMultiFileEdit(t *testing.T, dir string, files []map[string]string, mode string) Result {
	t.Helper()
	tool := MultiFileEdit{}
	type fileEntry struct {
		Path  string              `json:"path"`
		Edits []map[string]string `json:"edits"`
	}
	var entries []fileEntry
	for _, f := range files {
		entries = append(entries, fileEntry{Path: f["path"], Edits: []map[string]string{
			{"old_text": f["old"], "new_text": f["new"]},
		}})
	}
	input, _ := json.Marshal(map[string]any{"files": entries, "mode": mode})
	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	return res
}

// TestAtomicPathRejectsStaleFile: mutate a target file externally AFTER the
// plan is built (simulated by poisoning the tracker's baseline) - the atomic
// apply must refuse to overwrite and leave the external content intact.
func TestAtomicPathRejectsStaleFile(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")
	os.WriteFile(a, []byte("original-a\n"), 0644)
	os.WriteFile(b, []byte("original-b\n"), 0644)

	// Register reads (the agent's read baseline).
	defaultFileTracker.RecordRead(a)
	defaultFileTracker.RecordRead(b)
	// External modification lands between plan and apply.
	time.Sleep(2 * time.Millisecond)
	os.WriteFile(a, []byte("original-a\nexternal-tail\n"), 0644)

	res := runMultiFileEdit(t, dir, []map[string]string{
		{"path": a, "old": "original-a", "new": "agent-a"},
		{"path": b, "old": "original-b", "new": "agent-b"},
	}, "atomic")
	if !res.IsError {
		t.Fatalf("#4903 regression: atomic apply overwrote a stale file:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "modified externally") && !strings.Contains(res.Content, "a.txt") {
		t.Fatalf("expected stale rejection naming the file, got:\n%s", res.Content)
	}
	got, _ := os.ReadFile(a)
	if !strings.Contains(string(got), "external-tail") {
		t.Fatalf("external change must survive the refusal, got %q", got)
	}
}

// TestPartialPathRejectsStaleFile: same scenario through partial_success -
// only the stale file fails, the healthy sibling still applies.
func TestPartialPathRejectsStaleFile(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")
	os.WriteFile(a, []byte("original-a\n"), 0644)
	os.WriteFile(b, []byte("original-b\n"), 0644)

	defaultFileTracker.RecordRead(a)
	defaultFileTracker.RecordRead(b)
	time.Sleep(2 * time.Millisecond)
	os.WriteFile(b, []byte("original-b\nexternal-tail\n"), 0644)

	res := runMultiFileEdit(t, dir, []map[string]string{
		{"path": a, "old": "original-a", "new": "agent-a"},
		{"path": b, "old": "original-b", "new": "agent-b"},
	}, "partial_success")
	if !strings.Contains(res.Content, "modified externally") {
		t.Fatalf("stale file must be reported as an error entry:\n%s", res.Content)
	}
	// Healthy sibling applied.
	gotA, _ := os.ReadFile(a)
	if !strings.Contains(string(gotA), "agent-a") {
		t.Fatalf("healthy file should still apply, got %q", gotA)
	}
	// Stale file untouched.
	gotB, _ := os.ReadFile(b)
	if !strings.Contains(string(gotB), "external-tail") {
		t.Fatalf("stale file must keep external content, got %q", gotB)
	}
}

// TestFreshEditStillApplies: the guard must not reject normal flows - a file
// read then edited in the same breath applies cleanly in atomic mode.
func TestFreshEditStillApplies(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	os.WriteFile(a, []byte("hello\n"), 0644)
	defaultFileTracker.RecordRead(a)

	res := runMultiFileEdit(t, dir, []map[string]string{
		{"path": a, "old": "hello", "new": "world"},
	}, "atomic")
	if res.IsError {
		t.Fatalf("fresh edit must apply, got error:\n%s", res.Content)
	}
	got, _ := os.ReadFile(a)
	if !strings.Contains(string(got), "world") {
		t.Fatalf("edit not applied: %q", got)
	}
}

package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadHashTracker_BasicFlow(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "test.go")
	os.WriteFile(f, []byte("package main\n"), 0644)

	tr := newReadHashTracker()

	// Record hash at read time.
	tr.recordReadHash(f)
	if len(tr.hashes) != 1 {
		t.Fatalf("expected 1 hash stored, got %d", len(tr.hashes))
	}

	// Edit with unchanged content -> no warning.
	hint := tr.validateContentAtEdit(f, 100)
	if hint != "" {
		t.Errorf("expected no warning for unchanged content, got: %s", hint)
	}
}

func TestReadHashTracker_ContentChanged(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "main.go")
	os.WriteFile(f, []byte("package main\n\nfunc A() {}\n"), 0644)

	tr := newReadHashTracker()
	tr.recordReadHash(f)

	// Simulate external modification (same second -- mtime won't change on HFS+).
	os.WriteFile(f, []byte("package main\n\nfunc B() {}\n"), 0644)

	hint := tr.validateContentAtEdit(f, 100)
	if hint == "" {
		t.Error("expected content mismatch warning, got empty")
	}
}

func TestReadHashTracker_FalsePositiveSuppression(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "util.go")
	os.WriteFile(f, []byte("package util\n"), 0644)

	tr := newReadHashTracker()
	tr.recordReadHash(f)

	// Simulate touch: mtime changes but content stays the same.
	os.Chtimes(f, time.Now().Add(10*time.Second), time.Now().Add(10*time.Second))

	hint := tr.validateContentAtEdit(f, 100)
	if hint != "" {
		t.Errorf("expected no warning when content unchanged (false positive suppression), got: %s", hint)
	}
}

func TestReadHashTracker_RecordWriteClears(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "app.go")
	os.WriteFile(f, []byte("package app\n"), 0644)

	tr := newReadHashTracker()
	tr.recordReadHash(f)

	tr.recordWriteHash(f)
	if len(tr.hashes) != 0 {
		t.Errorf("expected hash cleared after write, got %d remaining", len(tr.hashes))
	}

	hint := tr.validateContentAtEdit(f, 100)
	if hint != "" {
		t.Errorf("expected no warning after write cleared hash, got: %s", hint)
	}
}

func TestReadHashTracker_WarnedOncePerFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "svc.go")
	os.WriteFile(f, []byte("old content\n"), 0644)

	tr := newReadHashTracker()
	tr.recordReadHash(f)

	os.WriteFile(f, []byte("new content\n"), 0644)

	hint1 := tr.validateContentAtEdit(f, 100)
	if hint1 == "" {
		t.Error("expected first warning")
	}

	hint2 := tr.validateContentAtEdit(f, 100)
	if hint2 != "" {
		t.Errorf("expected no duplicate warning, got: %s", hint2)
	}
}

func TestReadHashTracker_NoHashRecorded(t *testing.T) {
	tr := newReadHashTracker()
	hint := tr.validateContentAtEdit("/nonexistent/file.go", 100)
	if hint != "" {
		t.Errorf("expected empty for file with no stored hash, got: %s", hint)
	}
}

func TestReadHashTracker_EmptyPath(t *testing.T) {
	tr := newReadHashTracker()
	tr.recordReadHash("")
	tr.recordWriteHash("")
	hint := tr.validateContentAtEdit("", 100)
	if hint != "" {
		t.Errorf("expected empty for empty path, got: %s", hint)
	}
}

func TestReadHashTracker_Reset(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "reset.go")
	os.WriteFile(f, []byte("data\n"), 0644)

	tr := newReadHashTracker()
	tr.recordReadHash(f)
	tr.reset()
	if len(tr.hashes) != 0 || len(tr.warned) != 0 {
		t.Error("reset should clear all maps")
	}
}

func TestHashFilePrefix_NonExistentFile(t *testing.T) {
	h := hashFilePrefix("/nonexistent/path/file.go")
	if h != 0 {
		t.Errorf("expected 0 hash for non-existent file, got %d", h)
	}
}

func TestReadHashTracker_LargeFilePartialHash(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "large.go")

	// Create a file larger than maxHashBytes.
	large := make([]byte, maxHashBytes+4096)
	for i := range large {
		large[i] = byte(i % 256)
	}
	os.WriteFile(f, large, 0644)

	tr := newReadHashTracker()
	tr.recordReadHash(f)

	// Modify beyond the hashed prefix -- should NOT be detected.
	large[maxHashBytes+100] = 255
	os.WriteFile(f, large, 0644)

	hint := tr.validateContentAtEdit(f, 100)
	if hint != "" {
		t.Error("changes beyond hashed prefix should not trigger warning (by design)")
	}

	// Modify within the hashed prefix -- SHOULD be detected.
	large[100] = 255
	os.WriteFile(f, large, 0644)

	hint2 := tr.validateContentAtEdit(f, 100)
	if hint2 == "" {
		t.Error("changes within hashed prefix should trigger warning")
	}
}

func TestReadHashTracker_SmallEditThreshold(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "small.go")
	os.WriteFile(f, []byte("original\n"), 0644)

	tr := newReadHashTracker()
	tr.recordReadHash(f)
	os.WriteFile(f, []byte("modified\n"), 0644)

	// Small old_text -> softer wording but still warns.
	hint := tr.validateContentAtEdit(f, 10)
	if hint == "" {
		t.Error("expected warning even for small edit")
	}
}

// --- batch_replace path extraction tests ---

func TestExtractEditFilePaths_BatchReplace(t *testing.T) {
	args := json.RawMessage(`{"files": ["/a/foo.go", "/b/bar.go"], "pattern": "old", "replacement": "new"}`)
	paths := extractEditFilePaths("batch_replace", args)
	if len(paths) != 2 {
		t.Fatalf("expected 2 paths, got %d: %v", len(paths), paths)
	}
	if paths[0] != "/a/foo.go" || paths[1] != "/b/bar.go" {
		t.Errorf("unexpected paths: %v", paths)
	}
}

func TestExtractCreateFilePaths_BatchReplace(t *testing.T) {
	args := json.RawMessage(`{"files": ["/x/y.go"], "pattern": "a", "replacement": "b"}`)
	paths := extractCreateFilePaths("batch_replace", args)
	if len(paths) != 1 || paths[0] != "/x/y.go" {
		t.Errorf("unexpected paths: %v", paths)
	}
}

func TestExtractEditFilePaths_BatchReplaceEmpty(t *testing.T) {
	paths := extractEditFilePaths("batch_replace", nil)
	if len(paths) != 0 {
		t.Errorf("expected 0 paths for nil args, got %d", len(paths))
	}
}

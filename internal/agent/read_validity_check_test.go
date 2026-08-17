package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestReadHashTracker_AssemblyOrder_ValidateBeforeRecordWrite is a regression
// test for #283: in the agent loop (agent.go), validateContentAtEdit must run
// BEFORE recordWriteHash within the same edit iteration. recordWriteHash
// deletes the stored hash, so validating after it always misses and the
// detector is dead in production. Unit tests calling tracker methods directly
// bypassed this wiring and stayed green.
func TestReadHashTracker_AssemblyOrder_ValidateBeforeRecordWrite(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "wire.go")
	if err := os.WriteFile(f, []byte("package main\n\nfunc A() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tr := newReadHashTracker()

	// 1. Agent reads the file.
	tr.recordReadHash(f)

	// 2. External modification in the same second (mtime-blind change).
	if err := os.WriteFile(f, []byte("package main\n\nfunc B() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// 3. Correct agent-loop order: validate FIRST, then record the write.
	// This mirrors the wiring in agent.go post-#283.
	hint := tr.validateContentAtEdit(f, 100)
	if hint == "" {
		t.Fatal("expected content mismatch warning when validate runs before recordWriteHash — detector is mis-wired again")
	}
	tr.recordWriteHash(f)
	if len(tr.hashes) != 0 {
		t.Fatalf("expected hash cleared after recordWriteHash, got %d remaining", len(tr.hashes))
	}
}

// TestReadHashTracker_AssemblyOrder_WrongOrderDocumentsBug documents the
// pre-#283 bug: validating after recordWriteHash silently suppresses the
// warning. If this test ever fails, it means the wiring got "fixed" in a way
// that makes this order detectable — re-check the agent loop ordering.
func TestReadHashTracker_AssemblyOrder_WrongOrderDocumentsBug(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "wire2.go")
	if err := os.WriteFile(f, []byte("package main\n\nfunc A() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tr := newReadHashTracker()
	tr.recordReadHash(f)
	if err := os.WriteFile(f, []byte("package main\n\nfunc B() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Buggy order: record write first (hash deleted), then validate.
	tr.recordWriteHash(f)
	hint := tr.validateContentAtEdit(f, 100)
	if hint != "" {
		t.Fatalf("recordWriteHash no longer clears hashes — validateContentAtEdit after it produced %q", hint)
	}
}

// TestExtractOldTextLen verifies old_text length extraction from edit tool
// arguments (#283): previously hardcoded 0, making the small-edit soft-hint
// branch (0 < oldTextLen < 50) unreachable in production.
func TestExtractOldTextLen(t *testing.T) {
	cases := []struct {
		name    string
		tool    string
		args    string
		wantLen int
	}{
		{
			name:    "edit_file with short old_text",
			tool:    "edit_file",
			args:    `{"file_path":"/x.go","old_text":"abc","new_text":"abd"}`,
			wantLen: 3,
		},
		{
			name:    "edit_file with long old_text",
			tool:    "edit_file",
			args:    `{"file_path":"/x.go","old_text":"` + strings.Repeat("x", 80) + `","new_text":"y"}`,
			wantLen: 80,
		},
		{
			name:    "edit_file missing old_text",
			tool:    "edit_file",
			args:    `{"file_path":"/x.go"}`,
			wantLen: 0,
		},
		{
			name:    "multi_edit_file sums edits",
			tool:    "multi_edit_file",
			args:    `{"file_path":"/x.go","edits":[{"old_text":"aa","new_text":"bb"},{"old_text":"cccc","new_text":"dd"}]}`,
			wantLen: 6,
		},
		{
			name:    "invalid json",
			tool:    "edit_file",
			args:    `{not-json`,
			wantLen: 0,
		},
		{
			name:    "unknown tool",
			tool:    "write_file",
			args:    `{"path":"/x.go","old_text":"abc"}`,
			wantLen: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractOldTextLen(tc.tool, json.RawMessage(tc.args))
			if got != tc.wantLen {
				t.Fatalf("extractOldTextLen(%s) = %d, want %d", tc.tool, got, tc.wantLen)
			}
		})
	}
}

// TestReadHashTracker_SmallEditSoftHint verifies the soft-hint branch that
// was unreachable before #283 (oldTextLen between 1 and staleHashThreshold-1).
func TestReadHashTracker_SmallEditSoftHint(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "small.go")
	if err := os.WriteFile(f, []byte("package main\n\nfunc A() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tr := newReadHashTracker()
	tr.recordReadHash(f)
	if err := os.WriteFile(f, []byte("package main\n\nfunc B() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	small := tr.validateContentAtEdit(f, 10)
	if small == "" {
		t.Fatal("expected soft hint for small edit")
	}
	if !strings.Contains(small, "For this small edit") {
		t.Fatalf("expected small-edit soft hint, got: %s", small)
	}
}

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

	// Create a file larger than maxHashBytes (1MB since #627).
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

// #627 defect 2: a tail edit in a file that fits the (now 1MB) hash window
// must be detected even when mtime is restored to the original value, which
// simulates a sub-second edit the mtime check cannot see.
func TestReadHashTracker_TailEditWithinWindowDetected(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "big.go")

	// 32KB file: fully hashed now, tail beyond the old 16KB window.
	content := make([]byte, 32*1024)
	for i := range content {
		content[i] = byte(i % 251)
	}
	if err := os.WriteFile(f, content, 0o644); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(f)
	if err != nil {
		t.Fatal(err)
	}
	origMtime := fi.ModTime()

	tr := newReadHashTracker()
	tr.recordReadHash(f)

	// Edit deep in the tail (byte 31KB) and restore the original mtime.
	content[31*1024] = 99
	if err := os.WriteFile(f, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(f, origMtime, origMtime); err != nil {
		t.Fatal(err)
	}

	if got := tr.validateContentAtEdit(f, 100); got == "" {
		t.Error("tail edit within hash window not detected despite restored mtime (#627)")
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

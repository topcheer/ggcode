package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestMultiFileWriteAtomicRollback verifies that when a mid-batch write fails
// in atomic mode, previously written files are restored to their original content.
// Regression test for issue #103.
func TestMultiFileWriteAtomicRollback(t *testing.T) {
	tmpDir := t.TempDir()

	// File A already exists — should be restored on rollback.
	fileA := filepath.Join(tmpDir, "existing.txt")
	originalContent := []byte("original content")
	if err := os.WriteFile(fileA, originalContent, 0o644); err != nil {
		t.Fatal(err)
	}
	// Mark file A as read so stale-read guard doesn't fire.
	defaultFileTracker.RecordWrite(fileA)

	// File B is in a path that will fail sandbox check.
	fileB := filepath.Join(tmpDir, "blocked.txt")

	// File C would be written if not for atomic rollback.
	fileC := filepath.Join(tmpDir, "new.txt")

	tool := MultiFileWrite{
		SandboxCheck: func(path string) bool {
			// Block fileB to simulate sandbox violation mid-batch.
			return filepath.Base(path) != "blocked.txt"
		},
		WorkingDir: tmpDir,
	}

	type fileArg struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	type writeArgs struct {
		Files []fileArg `json:"files"`
		Mode  string    `json:"mode"`
	}

	input, err := json.Marshal(writeArgs{
		Files: []fileArg{
			{Path: fileA, Content: "modified content"},
			{Path: fileB, Content: "should fail"},
			{Path: fileC, Content: "should be rolled back"},
		},
		Mode: "atomic",
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.IsError {
		t.Error("expected IsError=true for atomic mode with failure")
	}

	// File A should be restored to original content.
	data, err := os.ReadFile(fileA)
	if err != nil {
		t.Fatalf("failed to read fileA: %v", err)
	}
	if string(data) != string(originalContent) {
		t.Errorf("fileA not rolled back: got %q, want %q", string(data), string(originalContent))
	}

	// File C should not exist (was new, should have been removed on rollback).
	if _, err := os.Stat(fileC); err == nil {
		t.Error("fileC should have been removed by rollback (it was newly created)")
	}

	// File B should not exist.
	if _, err := os.Stat(fileB); err == nil {
		t.Error("fileB should not exist (sandbox blocked it)")
	}
}

// TestMultiFileWriteAtomicAllSucceed verifies normal atomic mode still works
// when all writes succeed.
func TestMultiFileWriteAtomicAllSucceed(t *testing.T) {
	tmpDir := t.TempDir()

	fileA := filepath.Join(tmpDir, "a.txt")
	fileB := filepath.Join(tmpDir, "b.txt")

	tool := MultiFileWrite{
		SandboxCheck: func(path string) bool { return true },
		WorkingDir:   tmpDir,
	}

	type fileArg struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	type writeArgs struct {
		Files []fileArg `json:"files"`
		Mode  string    `json:"mode"`
	}

	input, err := json.Marshal(writeArgs{
		Files: []fileArg{
			{Path: fileA, Content: "hello A"},
			{Path: fileB, Content: "hello B"},
		},
		Mode: "atomic",
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Errorf("expected no error, got: %s", result.Content)
	}

	// Both files should exist with new content.
	dataA, _ := os.ReadFile(fileA)
	if string(dataA) != "hello A" {
		t.Errorf("fileA: got %q, want %q", string(dataA), "hello A")
	}
	dataB, _ := os.ReadFile(fileB)
	if string(dataB) != "hello B" {
		t.Errorf("fileB: got %q, want %q", string(dataB), "hello B")
	}
}

package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMultiFileWrite_CreateMultiple(t *testing.T) {
	dir := t.TempDir()

	paths := []string{
		filepath.Join(dir, "a.txt"),
		filepath.Join(dir, "sub", "b.txt"),
		filepath.Join(dir, "deep", "nested", "c.txt"),
	}
	contents := []string{"content A", "content B", "content C"}

	input, _ := json.Marshal(map[string]any{
		"files": []map[string]string{
			{"path": paths[0], "content": contents[0]},
			{"path": paths[1], "content": contents[1]},
			{"path": paths[2], "content": contents[2]},
		},
	})

	tool := MultiFileWrite{}
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}

	// Verify all files exist with correct content.
	for i, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("file %s not created: %v", p, err)
			continue
		}
		if string(data) != contents[i] {
			t.Errorf("file %s: got %q, want %q", p, string(data), contents[i])
		}
	}
}

func TestMultiFileWrite_OverwriteExisting(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "existing.txt")
	original := "old content"
	if err := os.WriteFile(p, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	newContent := "new content"
	input, _ := json.Marshal(map[string]any{
		"files": []map[string]string{
			{"path": p, "content": newContent},
		},
	})

	tool := MultiFileWrite{}
	result, _ := tool.Execute(context.Background(), input)
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	data, _ := os.ReadFile(p)
	if string(data) != newContent {
		t.Errorf("got %q, want %q", string(data), newContent)
	}
}

func TestMultiFileWrite_DuplicatePath(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "dup.txt")

	input, _ := json.Marshal(map[string]any{
		"files": []map[string]string{
			{"path": p, "content": "A"},
			{"path": p, "content": "B"},
		},
	})

	tool := MultiFileWrite{}
	result, _ := tool.Execute(context.Background(), input)
	if result.IsError {
		t.Fatalf("expected success with last-write-wins, got error: %s", result.Content)
	}
	// Last write should win.
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "B" {
		t.Errorf("expected content 'B' (last write wins), got %q", string(got))
	}
}

func TestMultiFileWrite_EmptyFiles(t *testing.T) {
	tool := MultiFileWrite{}
	input, _ := json.Marshal(map[string]any{
		"files": []map[string]string{},
	})
	result, _ := tool.Execute(context.Background(), input)
	if !result.IsError {
		t.Error("expected error for empty files array")
	}
}

func TestMultiFileWrite_SandboxViolation(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sandboxed.txt")

	input, _ := json.Marshal(map[string]any{
		"files": []map[string]string{
			{"path": p, "content": "test"},
		},
	})

	// Sandbox always denies.
	tool := MultiFileWrite{SandboxCheck: func(path string) bool { return false }}
	result, _ := tool.Execute(context.Background(), input)
	if !result.IsError {
		t.Error("expected error for sandbox violation")
	}

	// File should not have been written.
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("file should not exist after sandbox violation in atomic mode")
	}
}

func TestMultiFileWrite_PartialSuccess(t *testing.T) {
	dir := t.TempDir()
	goodPath := filepath.Join(dir, "good.txt")
	sandboxedPath := filepath.Join(dir, "bad.txt")

	input, _ := json.Marshal(map[string]any{
		"mode": "partial_success",
		"files": []map[string]string{
			{"path": goodPath, "content": "ok"},
			{"path": sandboxedPath, "content": "denied"},
		},
	})

	// Sandbox denies only sandboxedPath.
	tool := MultiFileWrite{SandboxCheck: func(path string) bool {
		return path != sandboxedPath
	}}
	result, _ := tool.Execute(context.Background(), input)
	if result.IsError {
		t.Fatalf("partial_success should not set IsError: %s", result.Content)
	}

	// Good file should exist.
	data, err := os.ReadFile(goodPath)
	if err != nil {
		t.Fatalf("good file not written: %v", err)
	}
	if string(data) != "ok" {
		t.Errorf("good file: got %q, want %q", string(data), "ok")
	}

	// Bad file should not exist.
	if _, err := os.Stat(sandboxedPath); !os.IsNotExist(err) {
		t.Error("sandboxed file should not exist")
	}
}

func TestMultiFileWrite_AtomicRollback(t *testing.T) {
	dir := t.TempDir()
	goodPath := filepath.Join(dir, "good.txt")
	blockedPath := filepath.Join(dir, "blocked.txt")
	// A directory at the target path makes the WRITE phase fail (after
	// good.txt is already written), which is what actually exercises the
	// rollback path - a sandbox denial happens at planning time, before
	// anything is on disk (#1331 follow-up).
	if err := os.Mkdir(blockedPath, 0o755); err != nil {
		t.Fatal(err)
	}

	input, _ := json.Marshal(map[string]any{
		"mode": "atomic",
		"files": []map[string]string{
			{"path": goodPath, "content": "ok"},
			{"path": blockedPath, "content": "denied"},
		},
	})

	tool := MultiFileWrite{SandboxCheck: func(path string) bool { return true }}
	result, _ := tool.Execute(context.Background(), input)
	if !result.IsError {
		t.Error("atomic mode with failures should set IsError")
	}

	// good.txt was written then rolled back.
	if _, err := os.Stat(goodPath); !os.IsNotExist(err) {
		t.Error("good file should not exist after atomic failure")
	}

	// #1331: summary and per-file detail must not contradict each other -
	// rolled-back files render as "rolled_back" lines under written=0,
	// never as "✓ ... written" lines.
	if !strings.Contains(result.Content, "written=0") {
		t.Errorf("summary should report written=0, got: %s", result.Content)
	}
	if strings.Contains(result.Content, "✓") {
		t.Errorf("rolled-back detail must not render as written: %s", result.Content)
	}
	if !strings.Contains(result.Content, "rolled back") {
		t.Errorf("rolled-back file should be marked in detail: %s", result.Content)
	}
	// Successful rollback must NOT claim INCOMPLETE.
	if strings.Contains(result.Content, "INCOMPLETE") {
		t.Errorf("successful rollback wrongly reported as incomplete: %s", result.Content)
	}
}

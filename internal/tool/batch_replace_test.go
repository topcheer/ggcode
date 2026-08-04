package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestBatchReplace_Literal(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "a.go")
	f2 := filepath.Join(dir, "b.go")
	os.WriteFile(f1, []byte("oldName := 1\noldName++\n"), 0644)
	os.WriteFile(f2, []byte("var oldName string\n"), 0644)

	tool := BatchReplace{WorkingDir: dir}
	input := mustJSON(t, map[string]any{
		"pattern":     "oldName",
		"replacement": "newName",
		"files":       []string{f1, f2},
	})

	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success: %s", res.Content)
	}

	// Verify content changed.
	data, _ := os.ReadFile(f1)
	if string(data) != "newName := 1\nnewName++\n" {
		t.Fatalf("unexpected content in f1: %q", string(data))
	}
	data, _ = os.ReadFile(f2)
	if string(data) != "var newName string\n" {
		t.Fatalf("unexpected content in f2: %q", string(data))
	}
}

func TestBatchReplace_DryRun(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "a.go")
	original := "fooBar fooBar"
	os.WriteFile(f1, []byte(original), 0644)

	tool := BatchReplace{WorkingDir: dir}
	input := mustJSON(t, map[string]any{
		"pattern":     "fooBar",
		"replacement": "bazQux",
		"files":       []string{f1},
		"dry_run":     true,
	})

	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success: %s", res.Content)
	}

	// File should NOT be changed in dry_run mode.
	data, _ := os.ReadFile(f1)
	if string(data) != original {
		t.Fatalf("file should not change in dry_run mode: %q", string(data))
	}

	// Output should mention dry_run.
	var content batchReplaceContent
	if err := json.Unmarshal([]byte(res.Content), &content); err != nil {
		t.Fatal(err)
	}
	if !content.DryRun {
		t.Fatal("expected dry_run=true in output")
	}
	if content.TotalMatches != 2 {
		t.Fatalf("expected 2 matches, got %d", content.TotalMatches)
	}
}

func TestBatchReplace_Regex(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "a.go")
	os.WriteFile(f1, []byte("fmt.Errorf(\"err: %s\", err)\n"), 0644)

	tool := BatchReplace{WorkingDir: dir}
	input := mustJSON(t, map[string]any{
		"pattern":     `fmt\.Errorf\("err: %s", err\)`,
		"replacement": `fmt.Errorf("err: %w", err)`,
		"files":       []string{f1},
		"is_regex":    true,
	})

	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success: %s", res.Content)
	}

	data, _ := os.ReadFile(f1)
	if string(data) != "fmt.Errorf(\"err: %w\", err)\n" {
		t.Fatalf("unexpected content: %q", string(data))
	}
}

func TestBatchReplace_NoMatch_Skipped(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "a.go")
	os.WriteFile(f1, []byte("hello world"), 0644)

	tool := BatchReplace{WorkingDir: dir}
	input := mustJSON(t, map[string]any{
		"pattern":     "nonexistent",
		"replacement": "whatever",
		"files":       []string{f1},
	})

	res, _ := tool.Execute(context.Background(), input)
	if res.IsError {
		t.Fatalf("expected success even with no matches")
	}

	var content batchReplaceContent
	json.Unmarshal([]byte(res.Content), &content)
	if content.FilesChanged != 0 {
		t.Fatalf("expected 0 files changed, got %d", content.FilesChanged)
	}
	if content.FilesSkipped != 1 {
		t.Fatalf("expected 1 skipped, got %d", content.FilesSkipped)
	}
}

func TestBatchReplace_EmptyPattern(t *testing.T) {
	tool := BatchReplace{WorkingDir: t.TempDir()}
	input := mustJSON(t, map[string]any{
		"pattern":     "",
		"replacement": "x",
		"files":       []string{"/tmp/a"},
	})

	res, _ := tool.Execute(context.Background(), input)
	if !res.IsError {
		t.Fatal("expected error for empty pattern")
	}
}

func TestBatchReplace_TooManyFiles(t *testing.T) {
	dir := t.TempDir()
	files := make([]string, maxBatchReplaceFiles+1)
	for i := range files {
		files[i] = filepath.Join(dir, "f.go")
	}

	tool := BatchReplace{WorkingDir: dir}
	input := mustJSON(t, map[string]any{
		"pattern":     "x",
		"replacement": "y",
		"files":       files,
	})

	res, _ := tool.Execute(context.Background(), input)
	if !res.IsError {
		t.Fatal("expected error for too many files")
	}
}

func TestBatchReplace_SandboxBlocked(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "a.go")
	os.WriteFile(f1, []byte("test"), 0644)

	tool := BatchReplace{
		WorkingDir:   dir,
		SandboxCheck: func(path string) bool { return false },
	}
	input := mustJSON(t, map[string]any{
		"pattern":     "test",
		"replacement": "x",
		"files":       []string{f1},
	})

	res, _ := tool.Execute(context.Background(), input)
	// Sandbox blocked files get "error" status per-file, which sets IsError.
	var content batchReplaceContent
	json.Unmarshal([]byte(res.Content), &content)
	if content.FilesError == 0 {
		t.Fatal("expected at least 1 error from sandbox block")
	}
}

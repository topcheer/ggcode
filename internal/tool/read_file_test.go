package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileBasic(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.txt")
	os.WriteFile(fp, []byte("hello world\nline 2\n"), 0o644)

	r := ReadFile{}
	input, _ := json.Marshal(map[string]string{"path": fp})
	result, err := r.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !containsStr(result.Content, "hello world") {
		t.Errorf("expected file content: %s", result.Content)
	}
}

func TestReadFileWithOffsetLimit(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "lines.txt")
	var content string
	for i := 0; i < 100; i++ {
		content += "line content\n"
	}
	os.WriteFile(fp, []byte(content), 0o644)

	r := ReadFile{}
	input, _ := json.Marshal(map[string]interface{}{"path": fp, "offset": 10, "limit": 5})
	result, err := r.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	// Should contain 5 lines
	t.Logf("result length: %d", len(result.Content))
}

func TestReadFileNotFound(t *testing.T) {
	r := ReadFile{}
	input, _ := json.Marshal(map[string]string{"path": "/nonexistent/file.txt"})
	result, err := r.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for missing file")
	}
}

func TestReadFileInvalidInput(t *testing.T) {
	r := ReadFile{}
	result, err := r.Execute(context.Background(), json.RawMessage(`not json`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error result")
	}
}

func TestReadFileEmptyPath(t *testing.T) {
	r := ReadFile{}
	input, _ := json.Marshal(map[string]string{"path": ""})
	result, err := r.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for empty path")
	}
}

func TestReadFileDirectory(t *testing.T) {
	dir := t.TempDir()
	r := ReadFile{}
	input, _ := json.Marshal(map[string]string{"path": dir})
	result, err := r.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for directory path")
	}
}

func TestReadFileSandboxCheck(t *testing.T) {
	r := ReadFile{SandboxCheck: func(path string) bool { return false }}
	input, _ := json.Marshal(map[string]string{"path": "/forbidden/secret.txt"})
	result, err := r.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected sandbox error")
	}
}

func TestReadFileLargeFileWithOffsetLimit(t *testing.T) {
	// Create a file larger than maxFileSize (>10MB) and verify that
	// offset/limit reads work via the streaming path.
	dir := t.TempDir()
	fp := filepath.Join(dir, "large.txt")
	var sb strings.Builder
	for i := 0; i < 200000; i++ {
		sb.WriteString("this is a line of content for testing\n")
	}
	// Each line is ~37 bytes, 200000 lines = ~7.4MB. Add more to exceed 10MB.
	for i := 0; i < 100000; i++ {
		sb.WriteString("extra padding line to make this file very large indeed\n")
	}
	os.WriteFile(fp, []byte(sb.String()), 0o644)

	info, _ := os.Stat(fp)
	if info.Size() <= maxFileSize {
		t.Fatalf("test file too small: %d bytes (need > %d)", info.Size(), maxFileSize)
	}

	// Without offset/limit: should error
	r := ReadFile{}
	input, _ := json.Marshal(map[string]string{"path": fp})
	result, _ := r.Execute(context.Background(), input)
	if !result.IsError {
		t.Fatal("expected error for large file without offset/limit")
	}

	// With offset/limit: should succeed via streaming reader
	input, _ = json.Marshal(map[string]interface{}{"path": fp, "offset": 100, "limit": 10})
	result, _ = r.Execute(context.Background(), input)
	if result.IsError {
		t.Fatalf("streaming read should succeed, got error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "line of content") {
		t.Errorf("expected line content in result: %s", result.Content[:min(200, len(result.Content))])
	}
}

func TestReadFileStreamingRangeDirect(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.txt")
	os.WriteFile(fp, []byte("alpha\nbeta\ngamma\ndelta\nepsilon\n"), 0o644)

	// Read lines 2-3 (beta, gamma)
	text, err := readFileRangeStreaming(fp, 2, 2, readFileRangeOptions{
		defaultLimit: maxOutputLines,
		moreHint:     "test hint",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "beta") || !strings.Contains(text, "gamma") {
		t.Errorf("expected beta and gamma: %s", text)
	}
	if strings.Contains(text, "alpha") || strings.Contains(text, "delta") {
		t.Errorf("should not contain alpha or delta: %s", text)
	}
}

func TestReadFileStreamingRangeBeyondEnd(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "small.txt")
	os.WriteFile(fp, []byte("only\nthree\nlines\n"), 0o644)

	text, err := readFileRangeStreaming(fp, 100, 5, readFileRangeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "beyond end") {
		t.Errorf("expected beyond-end message: %s", text)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

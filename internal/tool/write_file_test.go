package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFileSchemaWarnsOverwriteAndAbsolutePath(t *testing.T) {
	params := string(WriteFile{}.Parameters())
	for _, want := range []string{"Prefer an absolute path", "fully replaced", "use edit_file for targeted changes"} {
		if !containsAny(params, want) {
			t.Fatalf("write_file schema should mention %q, got %s", want, params)
		}
	}
}

func TestWriteFileBasic(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "output.txt")

	w := WriteFile{}
	input, _ := json.Marshal(map[string]string{"path": fp, "content": "hello world"})
	result, err := w.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	data, _ := os.ReadFile(fp)
	if string(data) != "hello world" {
		t.Errorf("content mismatch: %q", string(data))
	}
}

func TestWriteFileOverwrite(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "overwrite.txt")
	os.WriteFile(fp, []byte("old content"), 0o644)

	w := WriteFile{}
	input, _ := json.Marshal(map[string]string{"path": fp, "content": "new content"})
	result, err := w.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	data, _ := os.ReadFile(fp)
	if string(data) != "new content" {
		t.Errorf("content mismatch: %q", string(data))
	}
}

func TestWriteFileEmptyContent(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "empty.txt")

	w := WriteFile{}
	input, _ := json.Marshal(map[string]string{"path": fp, "content": ""})
	result, err := w.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	data, _ := os.ReadFile(fp)
	if len(data) != 0 {
		t.Errorf("expected empty file, got %q", string(data))
	}
}

func TestWriteFileInvalidInput(t *testing.T) {
	w := WriteFile{}
	result, err := w.Execute(context.Background(), json.RawMessage(`not json`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error result")
	}
}

func TestWriteFileSandboxCheck(t *testing.T) {
	w := WriteFile{SandboxCheck: func(path string) bool { return false }}
	input, _ := json.Marshal(map[string]string{"path": "/forbidden/file.txt", "content": "data"})
	result, err := w.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected sandbox error")
	}
}

func TestWriteFileSpecialCharacters(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "special.txt")

	w := WriteFile{}
	content := "hello\nworld\ttab\reol\r\nunicode: 你好世界 🌍"
	input, _ := json.Marshal(map[string]string{"path": fp, "content": content})
	result, err := w.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	data, _ := os.ReadFile(fp)
	if string(data) != content {
		t.Errorf("content mismatch")
	}
}

func TestWriteFileSandboxRecheckAfterMkdirAll(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()

	checks := 0

	inSandbox := func(path string) bool {
		base := filepath.Base(path)
		resolved, err := filepath.EvalSymlinks(filepath.Dir(path))
		if err != nil {
			return true
		}
		return strings.HasPrefix(filepath.Join(resolved, base), dir) && !strings.HasPrefix(filepath.Join(resolved, base), outside)
	}
	w := WriteFile{WorkingDir: dir, SandboxCheck: func(path string) bool {
		checks++
		if checks == 1 {

			if err := os.Symlink(outside, filepath.Join(dir, "linked")); err != nil && !os.IsExist(err) {
				t.Fatalf("symlink setup: %v", err)
			}
		}
		return inSandbox(path)
	}}

	input, _ := json.Marshal(map[string]string{
		"path":    filepath.Join(dir, "linked", "escape.txt"),
		"content": "should not land outside",
	})
	result, err := w.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected sandbox re-check to refuse the swapped parent")
	}
	if _, statErr := os.Stat(filepath.Join(outside, "escape.txt")); statErr == nil {
		t.Error("bytes landed outside the sandbox")
	}
}

func TestWriteFile_SecondWriteNotFlaggedStale(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "seq.txt")
	os.WriteFile(path, []byte("v0"), 0o644)

	defaultFileTracker.RecordRead(path)
	wf := WriteFile{WorkingDir: tmp}
	mk := func(c string) json.RawMessage {
		b, _ := json.Marshal(map[string]string{"path": path, "content": c})
		return b
	}
	if res, err := wf.Execute(context.Background(), mk("v1")); err != nil || res.IsError {
		t.Fatalf("write #1 failed: %v %s", err, res.Content)
	}
	res, err := wf.Execute(context.Background(), mk("v2"))
	if err != nil || res.IsError {
		t.Fatalf("write #2 must succeed without stale misreport (#1358): %v %s", err, res.Content)
	}
	if strings.Contains(res.Content, "modified externally") {
		t.Fatalf("second write flagged as externally modified: %s", res.Content)
	}
}

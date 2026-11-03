package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func issue604Write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestIssue604_T1_MultiEditResolvesRelativePathAgainstWorkingDir(t *testing.T) {
	workDir := t.TempDir()
	cwdDir := t.TempDir()

	workContent := "alpha\nbeta\ngamma\n"
	cwdContent := "ALPHA\nBETA\nGAMMA\n"
	issue604Write(t, filepath.Join(workDir, "rel.txt"), workContent)
	issue604Write(t, filepath.Join(cwdDir, "rel.txt"), cwdContent)

	restore := issue604Chdir(t, cwdDir)

	tool := MultiEditFile{WorkingDir: workDir}
	input, err := json.Marshal(map[string]any{
		"file_path": "rel.txt",
		"edits": []map[string]string{
			{"old_text": "beta", "new_text": "BETA-EDITED"},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Content)
	}

	got, rerr := os.ReadFile(filepath.Join(workDir, "rel.txt"))
	if rerr != nil {
		t.Fatalf("read workdir file: %v", rerr)
	}
	if !strings.Contains(string(got), "BETA-EDITED") {
		t.Errorf("WorkingDir copy was not edited; content: %q", string(got))
	}

	cwdGot, rerr := os.ReadFile(filepath.Join(cwdDir, "rel.txt"))
	if rerr != nil {
		t.Fatalf("read cwd file: %v", rerr)
	}
	if string(cwdGot) != cwdContent {
		t.Errorf("CWD file was modified (wrong-file edit): %q", string(cwdGot))
	}
	restore()
}

func TestIssue604_T1_MultiEditSandboxCheckReceivesAbsolutePath(t *testing.T) {
	workDir := t.TempDir()
	issue604Write(t, filepath.Join(workDir, "rel.txt"), "alpha\nbeta\n")

	var checked []string
	tool := MultiEditFile{
		WorkingDir: workDir,
		SandboxCheck: func(p string) bool {
			checked = append(checked, p)
			return true
		},
	}
	input, err := json.Marshal(map[string]any{
		"file_path": "rel.txt",
		"edits": []map[string]string{
			{"old_text": "beta", "new_text": "gamma"},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Content)
	}
	if len(checked) != 1 {
		t.Fatalf("SandboxCheck called %d times, want 1", len(checked))
	}
	if !filepath.IsAbs(checked[0]) {
		t.Errorf("SandboxCheck received non-absolute path %q (edit_file basis mismatch)", checked[0])
	}
	want := filepath.Join(workDir, "rel.txt")
	if checked[0] != want {
		t.Errorf("SandboxCheck received %q, want %q", checked[0], want)
	}
}

func issue604Chdir(t *testing.T, dir string) (restore func()) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	return func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore chdir: %v", err)
		}
	}
}

func TestIssue604_T2_TrailingWhitespaceFallbackUniqueness(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dup.txt")

	content := "header\ndup   \nvalue   \nmiddle\ndup \nvalue \nfooter\n"
	issue604Write(t, path, content)

	tool := EditFile{WorkingDir: dir}
	input, _ := json.Marshal(map[string]any{
		"file_path": path,
		"old_text":  "dup\nvalue",
		"new_text":  "REPLACED",
	})
	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected disambiguation error, got success: %s", res.Content)
	}
	if !strings.Contains(res.Content, "whitespace-tolerant") || !strings.Contains(res.Content, "2 times") {
		t.Errorf("error should report the loose match count and semantics, got: %s", res.Content)
	}

	got, _ := os.ReadFile(path)
	if string(got) != content {
		t.Errorf("file must be unchanged after the uniqueness error; got: %q", string(got))
	}
}

func TestIssue604_T2_MultiEditTrailingWhitespaceFallbackUniqueness(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dup.txt")
	content := "header\ndup   \nvalue   \nmiddle\ndup \nvalue \nfooter\n"
	issue604Write(t, path, content)

	tool := MultiEditFile{WorkingDir: dir}
	input, _ := json.Marshal(map[string]any{
		"file_path": path,
		"edits": []map[string]string{
			{"old_text": "dup\nvalue", "new_text": "REPLACED"},
		},
	})
	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected disambiguation error, got success: %s", res.Content)
	}
	if !strings.Contains(res.Content, "whitespace-tolerant") || !strings.Contains(res.Content, "2 times") {
		t.Errorf("error should report the loose match count and semantics, got: %s", res.Content)
	}
	got, _ := os.ReadFile(path)
	if string(got) != content {
		t.Errorf("file must be unchanged (atomic failure); got: %q", string(got))
	}
}

func TestIssue604_T2_IndentShiftFallbackUniqueness(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dup.txt")

	content := "start\n\t\tline one\n\t\tline two\nmid\n\t\t\t\tline one\n\t\t\t\tline two\nend\n"
	issue604Write(t, path, content)

	tool := EditFile{WorkingDir: dir}
	input, _ := json.Marshal(map[string]any{
		"file_path": path,
		"old_text":  "line one\nline two",
		"new_text":  "replaced",
	})
	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected disambiguation error, got success: %s", res.Content)
	}
	if !strings.Contains(res.Content, "whitespace-tolerant") || !strings.Contains(res.Content, "2 times") {
		t.Errorf("error should report the loose match count and semantics, got: %s", res.Content)
	}
	got, _ := os.ReadFile(path)
	if string(got) != content {
		t.Errorf("file must be unchanged after the uniqueness error; got: %q", string(got))
	}
}

func TestIssue604_T2_UniqueFallbackStillSucceeds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "single.txt")
	issue604Write(t, path, "header\ntarget  \nend   \n")

	tool := EditFile{WorkingDir: dir}
	input, _ := json.Marshal(map[string]any{
		"file_path": path,
		"old_text":  "target\nend",
		"new_text":  "REPLACED",
	})
	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unique fallback match must succeed, got error: %s", res.Content)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "REPLACED") {
		t.Errorf("edit not applied; content: %q", string(got))
	}
}

func TestIssue604_T2_ByteExactDuplicateStillRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dup.txt")
	content := "header\ndup\nmiddle\ndup\nfooter\n"
	issue604Write(t, path, content)

	tool := EditFile{WorkingDir: dir}
	input, _ := json.Marshal(map[string]any{
		"file_path": path,
		"old_text":  "dup",
		"new_text":  "REPLACED",
	})
	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected uniqueness error, got success: %s", res.Content)
	}
	if !strings.Contains(res.Content, "found 2 times") {
		t.Errorf("expected byte-exact duplicate message, got: %s", res.Content)
	}
}

func TestIssue604_T3_ClonePreservesJobManager(t *testing.T) {
	jm := NewCommandJobManager("issue604-test")
	orig := &RunCommand{
		WorkingDir: "/some/dir",
		JobManager: jm,
		OutputTee:  nil,
		OnPreExec:  func(string, string) {},
		OnPostExec: func(int, error) {},
	}
	clone, ok := orig.Clone().(*RunCommand)
	if !ok {
		t.Fatalf("Clone() returned %T, want *RunCommand", orig.Clone())
	}
	if clone.JobManager != jm {
		t.Errorf("Clone() dropped JobManager: got %v, want the shared manager (pointer identity)", clone.JobManager)
	}
	if clone.WorkingDir != orig.WorkingDir {
		t.Errorf("Clone() WorkingDir = %q, want %q", clone.WorkingDir, orig.WorkingDir)
	}
	if clone.OnPreExec == nil || clone.OnPostExec == nil {
		t.Error("Clone() must keep OnPreExec/OnPostExec callbacks")
	}
}

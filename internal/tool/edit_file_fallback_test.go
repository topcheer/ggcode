package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditFile_LineNumberPrefixStripped(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.go")
	content := "package main\n\nfunc hello() {\n\tprintln(\"hi\")\n}\n"
	if err := os.WriteFile(fp, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// LLM pasted back read_file's `cat -n` output verbatim.
	input, _ := json.Marshal(map[string]any{
		"file_path": fp,
		"old_text":  "     3\tfunc hello() {\n     4\t\tprintln(\"hi\")\n     5\t}",
		"new_text":  "func hello() {\n\tprintln(\"hello\")\n}",
	})
	res, err := EditFile{}.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("expected line-number prefix to be auto-stripped; got error: %s", res.Content)
	}
	got, _ := os.ReadFile(fp)
	if !strings.Contains(string(got), `println("hello")`) {
		t.Errorf("file not updated: %q", got)
	}
}

func TestEditFile_CRLFFallback(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(fp, []byte("alpha\r\nbeta\r\ngamma\r\n"), 0644); err != nil {
		t.Fatal(err)
	}

	input, _ := json.Marshal(map[string]any{
		"file_path": fp,
		"old_text":  "alpha\nbeta", // LF instead of file's CRLF
		"new_text":  "ALPHA\nBETA",
	})
	res, err := EditFile{}.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("expected CRLF fallback; got error: %s", res.Content)
	}
	got, _ := os.ReadFile(fp)
	if string(got) != "ALPHA\r\nBETA\r\ngamma\r\n" {
		t.Errorf("CRLF preservation failed: %q", got)
	}
}

func TestEditFile_TrailingWhitespaceTolerant(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.txt")
	// File has a trailing space on the second line.
	if err := os.WriteFile(fp, []byte("first\nsecond  \nthird\n"), 0644); err != nil {
		t.Fatal(err)
	}

	input, _ := json.Marshal(map[string]any{
		"file_path": fp,
		"old_text":  "first\nsecond\nthird", // no trailing spaces
		"new_text":  "ONE\nTWO\nTHREE",
	})
	res, err := EditFile{}.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("expected trailing-whitespace fallback; got error: %s", res.Content)
	}
	got, _ := os.ReadFile(fp)
	if string(got) != "ONE\nTWO\nTHREE\n" {
		t.Errorf("unexpected content: %q", got)
	}
}

func TestEditFile_NonUniqueIncludesLineNumbers(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(fp, []byte("dup\nA\ndup\nB\ndup\n"), 0644); err != nil {
		t.Fatal(err)
	}

	input, _ := json.Marshal(map[string]any{
		"file_path": fp,
		"old_text":  "dup",
		"new_text":  "X",
	})
	res, err := EditFile{}.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected non-unique error")
	}
	if !strings.Contains(res.Content, "line(s)") {
		t.Errorf("error should list match line numbers, got: %s", res.Content)
	}
	for _, ln := range []string{"1", "3", "5"} {
		if !strings.Contains(res.Content, ln) {
			t.Errorf("expected line %s in error; got: %s", ln, res.Content)
		}
	}
	if !strings.Contains(res.Content, "replace_all") {
		t.Errorf("expected replace_all hint; got: %s", res.Content)
	}
}

func TestEditFile_DescriptionNotRequired(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.txt")
	os.WriteFile(fp, []byte("hello\n"), 0644)

	// No "description" field — must succeed under the new schema.
	input, _ := json.Marshal(map[string]any{
		"file_path": fp,
		"old_text":  "hello",
		"new_text":  "world",
	})
	res, err := EditFile{}.Execute(context.Background(), input)
	if err != nil || res.IsError {
		t.Fatalf("description should be optional; got err=%v res=%v", err, res)
	}
}

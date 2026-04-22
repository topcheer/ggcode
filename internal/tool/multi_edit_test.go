package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMultiEdit_Basic(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.go")
	os.WriteFile(fp, []byte("package main\n\nfunc a() { }\nfunc b() { }\n"), 0644)

	me := MultiEditFile{}
	input, _ := json.Marshal(map[string]interface{}{
		"file_path": fp,
		"edits": []map[string]string{
			{"old_text": "func a() { }", "new_text": "func a() int { return 1 }"},
			{"old_text": "func b() { }", "new_text": "func b() int { return 2 }"},
		},
	})
	result, err := me.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	data, _ := os.ReadFile(fp)
	if !containsAny(string(data), "return 1") || !containsAny(string(data), "return 2") {
		t.Errorf("unexpected content: %s", string(data))
	}
}

func TestMultiEdit_Overlapping(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.txt")
	os.WriteFile(fp, []byte("aaa bbb ccc"), 0644)

	me := MultiEditFile{}
	input, _ := json.Marshal(map[string]interface{}{
		"file_path": fp,
		"edits": []map[string]string{
			{"old_text": "aaa bbb", "new_text": "xxx"},
			{"old_text": "bbb ccc", "new_text": "yyy"},
		},
	})
	result, err := me.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for overlapping edits")
	}
}

func TestMultiEdit_DuplicateOldText(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.txt")
	os.WriteFile(fp, []byte("aaa aaa bbb"), 0644)

	me := MultiEditFile{}
	input, _ := json.Marshal(map[string]interface{}{
		"file_path": fp,
		"edits": []map[string]string{
			{"old_text": "aaa", "new_text": "xxx"},
		},
	})
	result, err := me.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for duplicate old_text")
	}
}

func TestMultiEdit_NotFound(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.txt")
	os.WriteFile(fp, []byte("hello world\n"), 0644)

	me := MultiEditFile{}
	input, _ := json.Marshal(map[string]interface{}{
		"file_path": fp,
		"edits": []map[string]string{
			{"old_text": "nonexistent", "new_text": "xxx"},
		},
	})
	result, err := me.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for not found old_text")
	}
}

func TestMultiEdit_EmptyEdits(t *testing.T) {
	me := MultiEditFile{}
	input, _ := json.Marshal(map[string]interface{}{
		"file_path": "/tmp/x",
		"edits":     []map[string]string{},
	})
	result, err := me.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for empty edits")
	}
}

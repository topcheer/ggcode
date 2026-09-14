package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #1652 case 2 probes: write_file and multi_edit_file must flag conflict
// markers in the content they land (edit_file and read_file already do).

func TestWriteFileFlagsConflictMarkers(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	p := filepath.Join(dir, "merged.txt")
	tf := &WriteFile{WorkingDir: dir}
	input, _ := json.Marshal(map[string]interface{}{
		"path":    p,
		"content": "line1\n<<<<<<< HEAD\nours\n=======\ntheirs\n>>>>>>> b\nline2\n",
	})
	res, err := tf.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("write failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "conflict") {
		t.Fatalf("#1652: conflict markers in written content not flagged:\n%s", res.Content)
	}
}

func TestMultiEditFileFlagsConflictMarkers(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	p := filepath.Join(dir, "me.txt")
	if err := os.WriteFile(p, []byte("alpha\n<<<<<<< HEAD\nours\n=======\ntheirs\n>>>>>>> b\nomega\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mt := &MultiEditFile{WorkingDir: dir}
	input, _ := json.Marshal(map[string]interface{}{
		"file_path": p,
		"edits": []interface{}{
			map[string]interface{}{"old_text": "alpha", "new_text": "ALPHA"},
		},
	})
	res, err := mt.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("edit failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "conflict") {
		t.Fatalf("#1652: untouched conflict markers in edited file not flagged:\n%s", res.Content)
	}
}

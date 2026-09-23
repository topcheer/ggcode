package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/checkpoint"
)

// writeUndoStore writes a one-record undo store for session id, recording a
// modification of filePath from oldContent to newContent.
func writeUndoStore(t *testing.T, dir, id, filePath, oldContent, newContent string) {
	t.Helper()
	rec := checkpoint.Checkpoint{
		ID: "c1", FilePath: filePath,
		OldContent: oldContent, NewContent: newContent,
		Existed: true, ToolCall: "edit_file",
	}
	line, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, checkpoint.SessionFileName(id)), line, 0o600); err != nil {
		t.Fatal(err)
	}
}

func runUndo(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	cmd := newUndoCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("undo %v: %v", args, err)
	}
	return out.String()
}

func TestUndoCmdList(t *testing.T) {
	store := t.TempDir()
	writeUndoStore(t, store, "t1", "/nowhere/f.txt", "a", "b")
	out := runUndo(t, "--list", "--dir", store)
	if !strings.Contains(out, "t1") || !strings.Contains(out, "ggcode undo --session") {
		t.Fatalf("list output missing session id or hint:\n%s", out)
	}
}

func TestUndoCmdRollbackRestoresFile(t *testing.T) {
	root := t.TempDir()
	store := filepath.Join(root, "store")
	if err := os.MkdirAll(store, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "f.txt")
	if err := os.WriteFile(target, []byte("agent wrecked this"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeUndoStore(t, store, "t1", target, "original content", "agent wrecked this")

	out := runUndo(t, "--yes", "--dir", store)
	if !strings.Contains(out, "restored") {
		t.Fatalf("expected restored action, got:\n%s", out)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "original content" {
		t.Fatalf("file not restored: %q err=%v", got, err)
	}

	// Second rollback is a no-op ("unchanged").
	out = runUndo(t, "--yes", "--dir", store)
	if !strings.Contains(out, "unchanged") {
		t.Fatalf("expected idempotent unchanged, got:\n%s", out)
	}
}

func TestUndoCmdDryRunLeavesFile(t *testing.T) {
	root := t.TempDir()
	store := filepath.Join(root, "store")
	if err := os.MkdirAll(store, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "f.txt")
	if err := os.WriteFile(target, []byte("current"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeUndoStore(t, store, "t1", target, "baseline", "current")

	out := runUndo(t, "--dry-run", "--dir", store)
	if !strings.Contains(out, "Dry run: no files changed") {
		t.Fatalf("expected dry-run notice, got:\n%s", out)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "current" {
		t.Fatalf("dry run modified the file: %q", got)
	}
}

func TestUndoCmdAbortsOnNonConfirm(t *testing.T) {
	root := t.TempDir()
	store := filepath.Join(root, "store")
	if err := os.MkdirAll(store, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "f.txt")
	if err := os.WriteFile(target, []byte("current"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeUndoStore(t, store, "t1", target, "baseline", "current")

	// stdin is not a TTY in tests; ReadString hits EOF -> treated as "no".
	out := runUndo(t, "--dir", store)
	if !strings.Contains(out, "Aborted") {
		t.Fatalf("expected abort on EOF confirmation, got:\n%s", out)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "current" {
		t.Fatalf("file changed despite abort: %q", got)
	}
}

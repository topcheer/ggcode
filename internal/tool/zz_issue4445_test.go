//go:build goolm

package tool

// #4445 regression: file_ops move must refuse to silently overwrite an
// existing destination (Unix rename replaces regular files atomically and
// the EXDEV fallback clobbers via os.WriteFile), and must not RecordRead a
// destination it never read.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestIssue4445_MoveRefusesExistingDestination(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.yaml")
	dst := filepath.Join(dir, "config.yaml")
	os.WriteFile(src, []byte("source"), 0o644)
	os.WriteFile(dst, []byte("precious"), 0o644)

	tool := FileOps{WorkingDir: dir}
	input := mustJSON(t, map[string]any{
		"operations": []map[string]any{
			{"action": "move", "source": src, "destination": dst},
		},
		"description": "test move overwrite refused",
	})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("move onto existing destination must fail (#4445): %s", result.Content)
	}
	// The precious content must be intact and the source untouched.
	if b, _ := os.ReadFile(dst); string(b) != "precious" {
		t.Fatalf("destination was overwritten: %q (#4445)", b)
	}
	if b, _ := os.ReadFile(src); string(b) != "source" {
		t.Fatalf("source was consumed: %q (#4445)", b)
	}
}

func TestIssue4445_MoveToFreshDestinationStillWorks(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.yaml")
	dst := filepath.Join(dir, "sub", "dst.yaml")
	os.WriteFile(src, []byte("payload"), 0o644)

	tool := FileOps{WorkingDir: dir}
	input := mustJSON(t, map[string]any{
		"operations": []map[string]any{
			{"action": "move", "source": src, "destination": dst},
		},
		"description": "test move fresh dest",
	})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("fresh-destination move failed: %s (#4445)", result.Content)
	}
	if b, _ := os.ReadFile(dst); string(b) != "payload" {
		t.Fatalf("move content lost: %q (#4445)", b)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("source still present after move (#4445)")
	}
}

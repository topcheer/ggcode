package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/checkpoint"
)

func TestAtomicWriteFileCreatesCheckpoint(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.txt")

	// Write initial content
	os.WriteFile(fp, []byte("original"), 0644)

	// Set up checkpoint saver
	mgr := checkpoint.NewManager(50)
	SetPreWriteHook(CheckpointSaver(mgr))

	// Atomic write should trigger checkpoint
	err := atomicWriteFile(fp, []byte("modified"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	// Verify file was written
	data, _ := os.ReadFile(fp)
	if string(data) != "modified" {
		t.Errorf("file content = %q, want %q", string(data), "modified")
	}

	// Verify checkpoint was saved
	cps := mgr.List()
	if len(cps) != 1 {
		t.Fatalf("expected 1 checkpoint, got %d", len(cps))
	}
	if cps[0].OldContent != "original" {
		t.Errorf("old content = %q", cps[0].OldContent)
	}
	if cps[0].NewContent != "modified" {
		t.Errorf("new content = %q", cps[0].NewContent)
	}
	if cps[0].FilePath != fp {
		t.Errorf("file path = %q", cps[0].FilePath)
	}

	// Clean up
	SetPreWriteHook(nil)
}

func TestAtomicWriteFileNoCheckpointForNewFile(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "new.txt")

	mgr := checkpoint.NewManager(50)
	SetPreWriteHook(CheckpointSaver(mgr))

	err := atomicWriteFile(fp, []byte("content"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	// New file should not create a checkpoint (no old content)
	cps := mgr.List()
	if len(cps) != 0 {
		t.Errorf("expected 0 checkpoints for new file, got %d", len(cps))
	}

	SetPreWriteHook(nil)
}

func TestAtomicWriteFileNoHook(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.txt")
	os.WriteFile(fp, []byte("old"), 0644)

	SetPreWriteHook(nil)
	err := atomicWriteFile(fp, []byte("new"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(fp)
	if string(data) != "new" {
		t.Errorf("file content = %q", string(data))
	}
}

func TestCheckpointSaverUndo(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "code.go")

	// Write initial content
	os.WriteFile(fp, []byte("package main\n\nfunc main() {}\n"), 0644)

	mgr := checkpoint.NewManager(50)
	SetPreWriteHook(CheckpointSaver(mgr))

	// Edit file
	atomicWriteFile(fp, []byte("package main\n\nfunc main() { panic(\"oops\") }\n"), 0644)

	// Undo
	cp, err := mgr.Undo()
	if err != nil {
		t.Fatal(err)
	}
	if cp.OldContent != "package main\n\nfunc main() {}\n" {
		t.Errorf("restored content = %q", cp.OldContent)
	}

	// Verify file was restored
	data, _ := os.ReadFile(fp)
	if string(data) != "package main\n\nfunc main() {}\n" {
		t.Errorf("file after undo = %q", string(data))
	}

	SetPreWriteHook(nil)
}

func TestPreWriteHookAbortsOnError(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "abort.txt")
	os.WriteFile(fp, []byte("original"), 0644)

	// Set a hook that returns an error
	SetPreWriteHook(func(_, _, _, _ string) error {
		return fmt.Errorf("hook denied write")
	})
	defer SetPreWriteHook(nil)

	err := atomicWriteFile(fp, []byte("new content"), 0644)
	if err == nil {
		t.Fatal("expected error from hook-aborted write")
	}
	if !strings.Contains(err.Error(), "hook denied write") {
		t.Errorf("error = %q, want hook denied write", err.Error())
	}

	// File should not have changed
	data, _ := os.ReadFile(fp)
	if string(data) != "original" {
		t.Errorf("file should be unchanged, got %q", string(data))
	}
}

func TestPreWriteHookAllowsOnNilError(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "allow.txt")
	os.WriteFile(fp, []byte("old"), 0644)

	var capturedPath string
	SetPreWriteHook(func(p, _, _, _ string) error {
		capturedPath = p
		return nil
	})
	defer SetPreWriteHook(nil)

	err := atomicWriteFile(fp, []byte("new"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	if capturedPath != fp {
		t.Errorf("hook received path = %q, want %q", capturedPath, fp)
	}

	data, _ := os.ReadFile(fp)
	if string(data) != "new" {
		t.Errorf("file = %q, want %q", string(data), "new")
	}
}

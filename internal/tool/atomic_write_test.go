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

	os.WriteFile(fp, []byte("original"), 0644)

	mgr := checkpoint.NewManager(50)
	SetPreWriteHook(CheckpointSaver(mgr))

	err := atomicWriteFile(fp, []byte("modified"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(fp)
	if string(data) != "modified" {
		t.Errorf("file content = %q, want %q", string(data), "modified")
	}

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

	os.WriteFile(fp, []byte("package main\n\nfunc main() {}\n"), 0644)

	mgr := checkpoint.NewManager(50)
	SetPreWriteHook(CheckpointSaver(mgr))

	atomicWriteFile(fp, []byte("package main\n\nfunc main() { panic(\"oops\") }\n"), 0644)

	cp, err := mgr.Undo("user")
	if err != nil {
		t.Fatal(err)
	}
	if cp.OldContent != "package main\n\nfunc main() {}\n" {
		t.Errorf("restored content = %q", cp.OldContent)
	}

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

package util

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAtomicWriteFile_NewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	err := AtomicWriteFile(path, []byte("hello"), 0644)
	if err != nil {
		t.Fatalf("AtomicWriteFile error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("expected 'hello', got %q", string(data))
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat error: %v", err)
	}
	if info.Mode().Perm() != 0644 {
		t.Errorf("expected mode 0644, got %o", info.Mode().Perm())
	}
}

func TestAtomicWriteFile_Overwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	// Create with 0600
	if err := os.WriteFile(path, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}

	// Overwrite — should preserve 0600
	err := AtomicWriteFile(path, []byte("second"), 0644)
	if err != nil {
		t.Fatalf("AtomicWriteFile error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	if string(data) != "second" {
		t.Errorf("expected 'second', got %q", string(data))
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat error: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("expected mode 0600 preserved, got %o", info.Mode().Perm())
	}
}

func TestAtomicWriteFile_BadDir(t *testing.T) {
	err := AtomicWriteFile("/nonexistent/dir/file.txt", []byte("data"), 0644)
	if err == nil {
		t.Fatal("expected error for nonexistent directory")
	}
}

func TestNewShellCommand(t *testing.T) {
	cmd, spec, err := NewShellCommand("echo hello")
	if err != nil {
		t.Fatalf("NewShellCommand error: %v", err)
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}
	if runtime.GOOS != "windows" {
		if spec.Path != "sh" {
			t.Errorf("expected sh, got %s", spec.Path)
		}
	}
}

func TestNewShellCommandContext(t *testing.T) {
	ctx := context.Background()
	cmd, spec, err := NewShellCommandContext(ctx, "echo hello")
	if err != nil {
		t.Fatalf("NewShellCommandContext error: %v", err)
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}
	if spec.Path == "" {
		t.Error("expected non-empty shell path")
	}
}

func TestDetectShell(t *testing.T) {
	spec, err := DetectShell()
	if err != nil {
		t.Fatalf("DetectShell error: %v", err)
	}
	if spec.Path == "" {
		t.Error("expected non-empty shell path")
	}
	if len(spec.Args) == 0 {
		t.Error("expected non-empty shell args")
	}
}

func TestDetectShell_Windows_Fallback(t *testing.T) {
	// Test Windows fallback path (no shell found)
	_, err := detectShell("windows",
		func(name string) (string, error) { return "", os.ErrNotExist },
		func(path string) (os.FileInfo, error) { return nil, os.ErrNotExist },
		func(key string) string { return "" },
	)
	if err == nil {
		t.Fatal("expected error when no shell found on Windows")
	}
}

func TestDetectShell_Windows_GitBash(t *testing.T) {
	spec, err := detectShell("windows",
		func(name string) (string, error) { return "", os.ErrNotExist },
		func(path string) (os.FileInfo, error) { return nil, nil }, // all paths exist
		func(key string) string { return "/fake" },
	)
	_ = spec // ignore unused
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAtomicWriteFile_EmptyData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")

	err := AtomicWriteFile(path, []byte{}, 0600)
	if err != nil {
		t.Fatalf("AtomicWriteFile error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("expected empty file, got %d bytes", len(data))
	}
}

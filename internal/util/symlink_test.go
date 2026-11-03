package util

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSafeSymlink(t *testing.T) {
	tmpDir := t.TempDir()

	srcFile := filepath.Join(tmpDir, "source.txt")
	if err := os.WriteFile(srcFile, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	linkFile := filepath.Join(tmpDir, "link.txt")
	if err := SafeSymlink(srcFile, linkFile); err != nil {
		t.Fatalf("SafeSymlink file: %v", err)
	}

	data, err := os.ReadFile(linkFile)
	if err != nil {
		t.Fatalf("read through link: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("expected 'hello', got %q", data)
	}

	srcDir := filepath.Join(tmpDir, "sourcedir")
	if err := os.MkdirAll(filepath.Join(srcDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "sub", "file.txt"), []byte("dir"), 0o644); err != nil {
		t.Fatal(err)
	}

	linkDir := filepath.Join(tmpDir, "linkdir")
	if err := SafeSymlink(srcDir, linkDir); err != nil {
		t.Fatalf("SafeSymlink dir: %v", err)
	}

	data, err = os.ReadFile(filepath.Join(linkDir, "sub", "file.txt"))
	if err != nil {
		t.Fatalf("read through dir link: %v", err)
	}
	if string(data) != "dir" {
		t.Fatalf("expected 'dir', got %q", data)
	}

	if err := SafeSymlink(srcFile, linkFile); err == nil {

	}
}

func TestSafeSymlink_DanglingLink(t *testing.T) {
	tmpDir := t.TempDir()

	err := SafeSymlink("/nonexistent/file.txt", filepath.Join(tmpDir, "link.txt"))
	if err != nil {
		t.Fatalf("dangling symlink should succeed on Unix, got: %v", err)
	}

	target, err := os.Readlink(filepath.Join(tmpDir, "link.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if target != "/nonexistent/file.txt" {
		t.Errorf("expected /nonexistent/file.txt, got %q", target)
	}
}

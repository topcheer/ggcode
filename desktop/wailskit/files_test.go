//go:build goolm

package wailskit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListDirectory_NonRecursive(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0644)
	os.Mkdir(filepath.Join(dir, "sub"), 0755)

	entries, err := ListDirectory(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	var foundFile, foundDir bool
	for _, e := range entries {
		if e.Name == "a.txt" && !e.IsDir {
			foundFile = true
			if e.Size != 5 {
				t.Fatalf("expected size 5, got %d", e.Size)
			}
		}
		if e.Name == "sub" && e.IsDir {
			foundDir = true
		}
	}
	if !foundFile || !foundDir {
		t.Fatalf("missing entries: file=%v dir=%v", foundFile, foundDir)
	}
}

func TestListDirectory_Recursive(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sub", "deep"), 0755)
	os.WriteFile(filepath.Join(dir, "sub", "deep", "c.txt"), []byte("x"), 0644)

	entries, err := ListDirectory(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 { // "sub" dir + "sub/deep/c.txt" (deep is a dir entry, c.txt is a file)
		// Actually: sub (dir), sub/deep (dir), sub/deep/c.txt (file) = 3
		if len(entries) != 3 {
			t.Fatalf("expected 3 entries (sub, deep, c.txt), got %d: %+v", len(entries), entries)
		}
	}

	var foundNested bool
	for _, e := range entries {
		if e.Name == "c.txt" {
			foundNested = true
			if e.Path != filepath.Join("sub", "deep", "c.txt") {
				t.Fatalf("expected relative path, got %q", e.Path)
			}
		}
	}
	if !foundNested {
		t.Fatal("expected to find nested file c.txt")
	}
}

func TestListDirectory_NonExistent(t *testing.T) {
	_, err := ListDirectory("/nonexistent/path/that/does/not/exist", false)
	if err == nil {
		t.Fatal("expected error for non-existent directory")
	}
}

func TestListDirectory_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	entries, err := ListDirectory(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected empty result, got %d", len(entries))
	}
}

func TestListDirectory_NotADirectory(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file.txt")
	os.WriteFile(f, []byte("x"), 0644)
	_, err := ListDirectory(f, false)
	if err == nil {
		t.Fatal("expected error when path is not a directory")
	}
}

func TestReadFileContent_Normal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("hello world"), 0644)

	content, err := ReadFileContent(path)
	if err != nil {
		t.Fatal(err)
	}
	if content != "hello world" {
		t.Fatalf("expected 'hello world', got %q", content)
	}
}

func TestReadFileContent_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "secret.txt")
	os.WriteFile(target, []byte("password"), 0644)

	// After Clean, the traversal stays within dir/../secret.txt which is parent
	// The check rejects paths containing ".."
	path := filepath.Join(dir, "..", "secret.txt")
	_, err := ReadFileContent(path)
	if err == nil {
		t.Fatal("expected path traversal error")
	}
}

func TestReadFileContent_Directory(t *testing.T) {
	dir := t.TempDir()
	_, err := ReadFileContent(dir)
	if err == nil {
		t.Fatal("expected error when reading a directory")
	}
}

func TestGetWorkingDir(t *testing.T) {
	wd := GetWorkingDir()
	if wd == "" {
		t.Fatal("expected non-empty working directory")
	}
}

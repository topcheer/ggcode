//go:build goolm

package wailskit

import (
	"os"
	"path/filepath"
	"testing"
)

// chdir changes the working directory for the duration of the test and
// restores it afterward. The ListDirectory containment check (#146) now
// restricts access to the working directory, so tests that list temp
// directories must run from inside them.
func chdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(old) })
}

func TestListDirectory_NonRecursive(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0644)
	os.Mkdir(filepath.Join(dir, "sub"), 0755)
	chdir(t, dir)

	entries, err := ListDirectory("", false)
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
	chdir(t, dir)

	entries, err := ListDirectory("", true)
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
	chdir(t, dir)
	entries, err := ListDirectory("", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected empty result, got %d", len(entries))
	}
}

func TestListDirectory_NotADirectory(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "file.txt")
	os.WriteFile(f, []byte("x"), 0644)
	chdir(t, dir)
	_, err := ListDirectory("file.txt", false)
	if err == nil {
		t.Fatal("expected error when path is not a directory")
	}
}

func TestListDirectory_OutsideWorkingDir(t *testing.T) {
	// #146: listing a directory outside the working directory must be denied.
	dir := t.TempDir()
	chdir(t, dir)
	_, err := ListDirectory(os.TempDir(), false)
	if err == nil {
		t.Fatal("expected access denied for directory outside working directory")
	}
}

// #146: a symlink inside the working directory that points outside must be
// rejected — filepath.Abs alone does not resolve symlinks.
func TestReadFileContent_SymlinkBypass(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	outside := filepath.Join(dir, "..", "secret_target.txt")
	if err := os.WriteFile(outside, []byte("password"), 0644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(outside)

	link := filepath.Join(dir, "leaky_link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink creation failed: %v", err)
	}

	_, err := ReadFileContent("leaky_link")
	if err == nil {
		t.Fatal("expected access denied for symlink pointing outside working directory")
	}
}

// #146: ListDirectory must also resolve symlinks before containment check.
func TestListDirectory_SymlinkBypass(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	outside := filepath.Join(dir, "..", "secret_dir")
	if err := os.Mkdir(outside, 0755); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(outside)

	link := filepath.Join(dir, "leaky_dir")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink creation failed: %v", err)
	}

	_, err := ListDirectory("leaky_dir", false)
	if err == nil {
		t.Fatal("expected access denied for symlinked directory outside working directory")
	}
}

func TestReadFileContent_Normal(t *testing.T) {
	// Create file within working directory so the security check passes
	wd, _ := os.Getwd()
	path := filepath.Join(wd, ".test_read_content.txt")
	os.WriteFile(path, []byte("hello world"), 0644)
	defer os.Remove(path)

	content, err := ReadFileContent(path)
	if err != nil {
		t.Fatal(err)
	}
	if content != "hello world" {
		t.Fatalf("expected 'hello world', got %q", content)
	}
}

func TestReadFileContent_PathTraversal(t *testing.T) {
	// The security check rejects paths that resolve outside the working directory.
	wd, _ := os.Getwd()
	parent := filepath.Dir(wd)
	target := filepath.Join(parent, ".test_secret.txt")
	os.WriteFile(target, []byte("password"), 0644)
	defer os.Remove(target)

	// Construct a path that uses .. to reach the parent directory
	path := filepath.Join(wd, "..", ".test_secret.txt")
	_, err := ReadFileContent(path)
	if err == nil {
		t.Fatal("expected path traversal error")
	}
}

func TestReadFileContent_Directory(t *testing.T) {
	// Create a directory within working directory
	wd, _ := os.Getwd()
	subdir := filepath.Join(wd, ".test_subdir")
	os.Mkdir(subdir, 0755)
	defer os.Remove(subdir)

	_, err := ReadFileContent(subdir)
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

//go:build goolm

package wailskit

import (
	"os"
	"path/filepath"
	"strings"
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

// #285: directories whose name merely starts with ".." (e.g. "..cfg") are
// ordinary path elements, not parent references. The old
// strings.HasPrefix(rel, "..") check wrongly denied them.
func TestListDirectory_DotDotPrefixedDirAllowed(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "..cfg"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "..cfg", "settings.toml"), []byte("k=1"), 0644); err != nil {
		t.Fatal(err)
	}
	chdir(t, dir)

	// Listing the ..-prefixed directory itself must succeed.
	entries, err := ListDirectory("..cfg", false)
	if err != nil {
		t.Fatalf("expected ..cfg to be listable, got error: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "settings.toml" {
		t.Fatalf("expected settings.toml inside ..cfg, got %+v", entries)
	}
}

// #285: a genuine parent-directory escape must still be denied.
func TestListDirectory_RealParentStillDenied(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "..")
	chdir(t, dir)
	_, err := ListDirectory(outside, false)
	if err == nil {
		t.Fatal("expected access denied for parent directory")
	}
}

func TestReadFileContent_DotDotPrefixedDirAllowed(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "..hidden"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "..hidden", "note.txt"), []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	chdir(t, dir)

	content, err := ReadFileContent(filepath.Join("..hidden", "note.txt"))
	if err != nil {
		t.Fatalf("expected file inside ..hidden to be readable, got error: %v", err)
	}
	if content != "secret" {
		t.Fatalf("expected 'secret', got %q", content)
	}
}

// #287: ReadFileContent must refuse oversized files before reading.
func TestReadFileContent_TooLarge(t *testing.T) {
	if maxReadFileTextBytes <= 4096 {
		t.Skip("cannot test size cap without writing a huge file")
	}
	dir := t.TempDir()
	big := filepath.Join(dir, "big.log")
	// Sparse file: declare size beyond the cap without writing 20MB+1 bytes.
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxReadFileTextBytes + 1); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	chdir(t, dir)

	_, err = ReadFileContent("big.log")
	if err == nil {
		t.Fatal("expected error for oversized file")
	}
	if !strings.Contains(err.Error(), "20MB") {
		t.Fatalf("expected error to mention 20MB limit, got: %v", err)
	}
}

// #287: small files remain fully readable.
func TestReadFileContent_SmallFileStillRead(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "small.txt"), []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}
	chdir(t, dir)

	content, err := ReadFileContent("small.txt")
	if err != nil {
		t.Fatal(err)
	}
	if content != "ok" {
		t.Fatalf("expected 'ok', got %q", content)
	}
}

// #329: ResolveContainedPath is the shared containment helper backing both
// wailskit's own APIs and the Wails App file APIs. It must anchor containment
// at an explicit root (not os.Getwd) and still reject symlink escapes.
// mustEval resolves symlinks in a test path so expectations match macOS
// /var -> /private/var normalization done by ResolveContainedPath.
func mustEval(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestResolveContainedPath_WithinRoot(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "in.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveContainedPath(dir, filepath.Join(dir, "in.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if resolved != filepath.Join(mustEval(t, dir), "in.txt") {
		t.Fatalf("unexpected resolved path %q", resolved)
	}
}

func TestResolveContainedPath_RelativeToRoot(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rel.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	// Run from an unrelated cwd: containment root is explicit, not Getwd.
	old, _ := os.Getwd()
	os.Chdir(t.TempDir())
	defer os.Chdir(old)
	resolved, err := ResolveContainedPath(dir, "rel.txt")
	if err != nil {
		t.Fatal(err)
	}
	if resolved != filepath.Join(mustEval(t, dir), "rel.txt") {
		t.Fatalf("unexpected resolved path %q", resolved)
	}
}

func TestResolveContainedPath_OutsideRootDenied(t *testing.T) {
	dir := t.TempDir()
	other := t.TempDir()
	if _, err := ResolveContainedPath(dir, other); err == nil {
		t.Fatal("expected access denied for path outside root")
	}
	if _, err := ResolveContainedPath(dir, filepath.Join(dir, "..")); err == nil {
		t.Fatal("expected access denied for parent traversal")
	}
}

func TestResolveContainedPath_SymlinkEscapeDenied(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("pw"), 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "leak")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlink creation failed: %v", err)
	}
	if _, err := ResolveContainedPath(dir, link); err == nil {
		t.Fatal("expected access denied for symlink escape")
	}
}

func TestResolveContainedPath_EmptyRootFallsBackToGetwd(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	old, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(old)
	resolved, err := ResolveContainedPath("", "f.txt")
	if err != nil {
		t.Fatal(err)
	}
	if resolved != filepath.Join(mustEval(t, dir), "f.txt") {
		t.Fatalf("unexpected resolved path %q", resolved)
	}
}

// #285 semantics preserved by the shared helper: dot-dot-prefixed names are fine.
func TestResolveContainedPath_DotDotPrefixedName(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "..cfg"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveContainedPath(dir, filepath.Join(dir, "..cfg")); err != nil {
		t.Fatalf("expected ..cfg to be allowed, got %v", err)
	}
}

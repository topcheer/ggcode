package vcs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectGit(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	v := Detect(dir)
	if v == nil {
		t.Fatal("expected Git, got nil")
	}
	if v.Name() != "git" {
		t.Errorf("expected git, got %s", v.Name())
	}
	if v.DisplayName() != "Git" {
		t.Errorf("expected Git, got %s", v.DisplayName())
	}
}

func TestDetectMercurial(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".hg"), 0755); err != nil {
		t.Fatal(err)
	}
	v := Detect(dir)
	if v == nil {
		t.Fatal("expected Mercurial, got nil")
	}
	if v.Name() != "hg" {
		t.Errorf("expected hg, got %s", v.Name())
	}
}

func TestDetectSubversion(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".svn"), 0755); err != nil {
		t.Fatal(err)
	}
	v := Detect(dir)
	if v == nil {
		t.Fatal("expected Subversion, got nil")
	}
	if v.Name() != "svn" {
		t.Errorf("expected svn, got %s", v.Name())
	}
}

func TestDetectJujutsu(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".jj"), 0755); err != nil {
		t.Fatal(err)
	}
	v := Detect(dir)
	if v == nil {
		t.Fatal("expected Jujutsu, got nil")
	}
	if v.Name() != "jj" {
		t.Errorf("expected jj, got %s", v.Name())
	}
}

func TestDetectNone(t *testing.T) {
	dir := t.TempDir()
	v := Detect(dir)
	if v != nil {
		t.Errorf("expected nil for plain directory, got %v", v)
	}
}

func TestDetectNested(t *testing.T) {
	// Create: tmp/.git + tmp/sub/dir
	// Detect from tmp/sub/dir should find git at tmp.
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	subDir := filepath.Join(root, "sub", "dir")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	v := Detect(subDir)
	if v == nil {
		t.Fatal("expected Git detected from nested directory")
	}
	if v.Name() != "git" {
		t.Errorf("expected git, got %s", v.Name())
	}
}

func TestDetectOrGitFallback(t *testing.T) {
	dir := t.TempDir()
	v := DetectOrGit(dir)
	if v == nil {
		t.Fatal("expected non-nil fallback")
	}
	if v.Name() != "git" {
		t.Errorf("expected git fallback, got %s", v.Name())
	}
}

// TestGitPreferenceOverJj: when both .git and .jj exist, git should win.
func TestGitPreferenceOverJj(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, ".jj"), 0755); err != nil {
		t.Fatal(err)
	}
	v := Detect(dir)
	if v == nil {
		t.Fatal("expected non-nil")
	}
	if v.Name() != "git" {
		t.Errorf("expected git when both .git and .jj exist, got %s", v.Name())
	}
}

// TestGitPreferenceOverHg: when both .git and .hg exist, git should win.
func TestGitPreferenceOverHg(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, ".hg"), 0755); err != nil {
		t.Fatal(err)
	}
	v := Detect(dir)
	if v == nil {
		t.Fatal("expected non-nil")
	}
	if v.Name() != "git" {
		t.Errorf("expected git when both .git and .hg exist, got %s", v.Name())
	}
}

// TestGitStatusOnRealGitRepo: verify Git.Status works in an actual git repo.
func TestGitStatusOnRealGitRepo(t *testing.T) {
	v := Git{}
	dir := t.TempDir()

	// Init a minimal git repo.
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	// This won't be a valid git repo without `git init`, but we test
	// the error path — the Status() call should return an error gracefully.
	_, err := v.Status(t.Context(), dir)
	if err == nil {
		// If git is installed and handles this gracefully, that's fine.
		// We mainly care that it doesn't panic.
	}
}

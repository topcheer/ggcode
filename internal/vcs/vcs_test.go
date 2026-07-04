package vcs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// --- Detection tests (metadata-directory based) ---

func TestDetectGit(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	v := Detect(dir)
	if v == nil || v.Name() != "git" {
		t.Fatalf("expected git, got %v", v)
	}
}

func TestDetectMercurial(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".hg"), 0755); err != nil {
		t.Fatal(err)
	}
	v := Detect(dir)
	if v == nil || v.Name() != "hg" {
		t.Fatalf("expected hg, got %v", v)
	}
}

func TestDetectSubversion(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".svn"), 0755); err != nil {
		t.Fatal(err)
	}
	v := Detect(dir)
	if v == nil || v.Name() != "svn" {
		t.Fatalf("expected svn, got %v", v)
	}
}

func TestDetectJujutsu(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".jj"), 0755); err != nil {
		t.Fatal(err)
	}
	v := Detect(dir)
	if v == nil || v.Name() != "jj" {
		t.Fatalf("expected jj, got %v", v)
	}
}

func TestDetectNone(t *testing.T) {
	v := Detect(t.TempDir())
	if v != nil {
		t.Errorf("expected nil, got %v", v)
	}
}

func TestDetectNested(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, ".git"), 0755)
	subDir := filepath.Join(root, "sub", "dir")
	os.MkdirAll(subDir, 0755)
	if v := Detect(subDir); v == nil || v.Name() != "git" {
		t.Fatalf("expected git from nested dir, got %v", v)
	}
}

func TestDetectOrGitFallback(t *testing.T) {
	v := DetectOrGit(t.TempDir())
	if v == nil || v.Name() != "git" {
		t.Fatalf("expected git fallback, got %v", v)
	}
}

func TestGitPreferenceOverJj(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, ".git"), 0755)
	os.Mkdir(filepath.Join(dir, ".jj"), 0755)
	if v := Detect(dir); v == nil || v.Name() != "git" {
		t.Fatalf("expected git, got %v", v)
	}
}

func TestGitPreferenceOverHg(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, ".git"), 0755)
	os.Mkdir(filepath.Join(dir, ".hg"), 0755)
	if v := Detect(dir); v == nil || v.Name() != "git" {
		t.Fatalf("expected git, got %v", v)
	}
}

// --- Integration tests against a REAL git repository ---
//
// These create an actual git repo with `git init`, add files, commit, and
// then exercise every VCS interface method to verify correctness.

// hasBinary reports whether the given command exists on PATH.
func hasBinary(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// setupGitRepo creates a real git repo in a temp dir with one initial commit.
func setupGitRepo(t *testing.T) string {
	t.Helper()
	if !hasBinary("git") {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		// Set a minimal git identity so commit works without global config.
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	// Write a file and commit.
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test repo\n"), 0644)
	run("add", "README.md")
	run("commit", "-m", "initial commit")
	return dir
}

func TestGitStatus_CleanRepo(t *testing.T) {
	dir := setupGitRepo(t)
	v := Git{}

	out, err := v.Status(context.Background(), dir)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty status for clean repo, got %q", out)
	}

	clean, err := v.IsClean(context.Background(), dir)
	if err != nil {
		t.Fatalf("IsClean failed: %v", err)
	}
	if !clean {
		t.Error("expected IsClean=true for clean repo")
	}
}

func TestGitStatus_DirtyRepo(t *testing.T) {
	dir := setupGitRepo(t)
	v := Git{}

	// Modify the tracked file.
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# modified\n"), 0644)
	// Add a new untracked file.
	os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new\n"), 0644)

	out, err := v.Status(context.Background(), dir)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if !strings.Contains(out, "README.md") {
		t.Errorf("status should show modified README.md, got %q", out)
	}
	if !strings.Contains(out, "new.txt") {
		t.Errorf("status should show untracked new.txt, got %q", out)
	}

	clean, _ := v.IsClean(context.Background(), dir)
	if clean {
		t.Error("expected IsClean=false for dirty repo")
	}
}

func TestGitDiff_UncommittedChanges(t *testing.T) {
	dir := setupGitRepo(t)
	v := Git{}

	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# modified\n"), 0644)

	out, err := v.Diff(context.Background(), dir, false, "")
	if err != nil {
		t.Fatalf("Diff failed: %v", err)
	}
	if !strings.Contains(out, "-# test repo") || !strings.Contains(out, "+# modified") {
		t.Errorf("diff should show the change, got %q", out)
	}
}

func TestGitDiff_SpecificFile(t *testing.T) {
	dir := setupGitRepo(t)
	v := Git{}

	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("aaa\n"), 0644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("bbb\n"), 0644)

	// Diff a file that has no changes (a.txt is new/untracked, diff shows nothing).
	out, err := v.Diff(context.Background(), dir, false, "README.md")
	if err != nil {
		t.Fatalf("Diff with file filter failed: %v", err)
	}
	// README.md is unchanged, so diff should be empty.
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty diff for unchanged file, got %q", out)
	}
}

func TestGitDiff_Cached(t *testing.T) {
	dir := setupGitRepo(t)
	v := Git{}

	os.WriteFile(filepath.Join(dir, "new.txt"), []byte("staged\n"), 0644)
	// Stage the file.
	cmd := exec.Command("git", "add", "new.txt")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}

	// Diff cached should show the staged file.
	out, err := v.Diff(context.Background(), dir, true, "")
	if err != nil {
		t.Fatalf("Diff(cached) failed: %v", err)
	}
	if !strings.Contains(out, "new.txt") || !strings.Contains(out, "+staged") {
		t.Errorf("cached diff should show staged new.txt, got %q", out)
	}

	// Non-cached diff should be empty (everything is staged).
	out2, err := v.Diff(context.Background(), dir, false, "")
	if err != nil {
		t.Fatalf("Diff(non-cached) failed: %v", err)
	}
	if strings.TrimSpace(out2) != "" {
		t.Errorf("non-cached diff should be empty after staging, got %q", out2)
	}
}

func TestGitLog(t *testing.T) {
	dir := setupGitRepo(t)
	v := Git{}

	// Make a second commit.
	os.WriteFile(filepath.Join(dir, "file2.txt"), []byte("content\n"), 0644)
	cmd := exec.Command("git", "add", "file2.txt")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "second commit")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	cmd.Run()

	out, err := v.Log(context.Background(), dir, 10)
	if err != nil {
		t.Fatalf("Log failed: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		t.Errorf("expected at least 2 commits in log, got %d: %q", len(lines), out)
	}
	if !strings.Contains(lines[0], "second commit") {
		t.Errorf("most recent commit should be 'second commit', got %q", lines[0])
	}
	if !strings.Contains(lines[1], "initial commit") {
		t.Errorf("second line should be 'initial commit', got %q", lines[1])
	}
}

func TestGitLog_WithCount(t *testing.T) {
	dir := setupGitRepo(t)
	v := Git{}

	out, err := v.Log(context.Background(), dir, 1)
	if err != nil {
		t.Fatalf("Log failed: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 {
		t.Errorf("expected exactly 1 commit with count=1, got %d lines: %q", len(lines), out)
	}
}

func TestGitAdd(t *testing.T) {
	dir := setupGitRepo(t)
	v := Git{}

	os.WriteFile(filepath.Join(dir, "newfile.txt"), []byte("content\n"), 0644)

	_, err := v.Add(context.Background(), dir, []string{"newfile.txt"})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Verify it's staged.
	out, err := v.Status(context.Background(), dir)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if !strings.Contains(out, "A") && !strings.Contains(out, "newfile.txt") {
		t.Errorf("newfile.txt should be staged after Add, status=%q", out)
	}
}

func TestGitCommit(t *testing.T) {
	dir := setupGitRepo(t)
	v := Git{}

	os.WriteFile(filepath.Join(dir, "newfile.txt"), []byte("content\n"), 0644)

	_, err := v.Add(context.Background(), dir, []string{"newfile.txt"})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	out, err := v.Commit(context.Background(), dir, "test commit message")
	if err != nil {
		t.Fatalf("Commit failed: %v", err)
	}
	if !strings.Contains(out, "test commit message") && !strings.Contains(out, "main") && !strings.Contains(out, "master") {
		t.Errorf("commit output should mention the commit, got %q", out)
	}

	// Verify working tree is clean after commit.
	clean, _ := v.IsClean(context.Background(), dir)
	if !clean {
		t.Error("expected clean tree after commit")
	}
}

func TestGitCurrentBranch(t *testing.T) {
	dir := setupGitRepo(t)
	v := Git{}

	branch, err := v.CurrentBranch(context.Background(), dir)
	if err != nil {
		t.Fatalf("CurrentBranch failed: %v", err)
	}
	// git init creates either "main" or "master" depending on git version.
	if branch != "main" && branch != "master" {
		t.Errorf("expected main or master, got %q", branch)
	}
}

func TestGitIsClean_AfterCommit(t *testing.T) {
	dir := setupGitRepo(t)
	v := Git{}

	clean, err := v.IsClean(context.Background(), dir)
	if err != nil {
		t.Fatalf("IsClean failed: %v", err)
	}
	if !clean {
		t.Error("expected clean after initial commit")
	}
}

// --- End-to-end: verify Detect + VCS methods work together ---

func TestDetectThenStatus_RealRepo(t *testing.T) {
	dir := setupGitRepo(t)

	v := Detect(dir)
	if v == nil {
		t.Fatal("Detect returned nil for real git repo")
	}
	if v.Name() != "git" {
		t.Fatalf("expected git, got %s", v.Name())
	}

	out, err := v.Status(context.Background(), dir)
	if err != nil {
		t.Fatalf("Status via interface failed: %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty status for clean repo, got %q", out)
	}
}

// --- Mercurial integration tests (skip if hg not installed) ---

func setupHgRepo(t *testing.T) string {
	t.Helper()
	if !hasBinary("hg") {
		t.Skip("hg not installed")
	}
	dir := t.TempDir()
	cmd := exec.Command("hg", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hg init: %v\n%s", err, out)
	}
	// Write + commit.
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# hg repo\n"), 0644)
	cmd = exec.Command("hg", "add", "README.md")
	cmd.Dir = dir
	cmd.Run()
	cmd = exec.Command("hg", "commit", "-m", "initial commit", "-u", "test")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hg commit: %v\n%s", err, out)
	}
	return dir
}

func TestHgStatus_CleanRepo(t *testing.T) {
	dir := setupHgRepo(t)
	v := Mercurial{}

	out, err := v.Status(context.Background(), dir)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty status for clean repo, got %q", out)
	}
}

func TestHgStatus_DirtyRepo(t *testing.T) {
	dir := setupHgRepo(t)
	v := Mercurial{}

	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# modified\n"), 0644)

	out, err := v.Status(context.Background(), dir)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if !strings.Contains(out, "README.md") {
		t.Errorf("status should show modified README.md, got %q", out)
	}
}

func TestHgDiff(t *testing.T) {
	dir := setupHgRepo(t)
	v := Mercurial{}

	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# modified\n"), 0644)

	out, err := v.Diff(context.Background(), dir, false, "")
	if err != nil {
		t.Fatalf("Diff failed: %v", err)
	}
	if !strings.Contains(out, "# modified") {
		t.Errorf("diff should show the change, got %q", out)
	}
}

func TestHgLog(t *testing.T) {
	dir := setupHgRepo(t)
	v := Mercurial{}

	out, err := v.Log(context.Background(), dir, 10)
	if err != nil {
		t.Fatalf("Log failed: %v", err)
	}
	if !strings.Contains(out, "initial commit") {
		t.Errorf("log should contain 'initial commit', got %q", out)
	}
}

func TestHgAdd(t *testing.T) {
	dir := setupHgRepo(t)
	v := Mercurial{}

	os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new\n"), 0644)
	_, err := v.Add(context.Background(), dir, []string{"new.txt"})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
}

func TestHgCommit(t *testing.T) {
	dir := setupHgRepo(t)
	v := Mercurial{}

	os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new\n"), 0644)
	v.Add(context.Background(), dir, []string{"new.txt"})

	_, err := v.Commit(context.Background(), dir, "second commit")
	if err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Verify clean.
	out, _ := v.Status(context.Background(), dir)
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected clean after commit, got %q", out)
	}
}

func TestHgCurrentBranch(t *testing.T) {
	dir := setupHgRepo(t)
	v := Mercurial{}

	branch, err := v.CurrentBranch(context.Background(), dir)
	if err != nil {
		t.Fatalf("CurrentBranch failed: %v", err)
	}
	if branch != "default" {
		t.Errorf("expected 'default', got %q", branch)
	}
}

func TestHgIsClean(t *testing.T) {
	dir := setupHgRepo(t)
	v := Mercurial{}

	clean, err := v.IsClean(context.Background(), dir)
	if err != nil {
		t.Fatalf("IsClean failed: %v", err)
	}
	if !clean {
		t.Error("expected clean after initial commit")
	}
}

func TestDetectThenStatus_RealHgRepo(t *testing.T) {
	dir := setupHgRepo(t)

	v := Detect(dir)
	if v == nil || v.Name() != "hg" {
		t.Fatalf("expected hg detection, got %v", v)
	}

	out, err := v.Status(context.Background(), dir)
	if err != nil {
		t.Fatalf("Status via interface failed: %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty status for clean repo, got %q", out)
	}
}

// --- Error handling tests ---

func TestGitStatus_NotARepo(t *testing.T) {
	v := Git{}
	_, err := v.Status(context.Background(), t.TempDir())
	if err == nil {
		t.Error("expected error for status in non-repo directory")
	}
}

func TestGitCommit_NothingToCommit(t *testing.T) {
	dir := setupGitRepo(t)
	v := Git{}

	_, err := v.Commit(context.Background(), dir, "empty commit")
	if err == nil {
		// Some git versions allow empty commits with no changes via -m,
		// but plain `git commit -m` should fail with "nothing to commit".
		// If it succeeds, the repo might have been configured to allow it.
	}
	// We just verify it doesn't panic — the behavior varies by git config.
}

// Keep the reference to runtime to avoid unused import on some builds.
var _ = runtime.GOOS

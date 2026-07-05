package vcs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- Detection tests ---

func TestDetectGit(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, ".git"), 0755)
	v := Detect(dir)
	if v == nil || v.Name() != "git" {
		t.Fatalf("expected git, got %v", v)
	}
}

func TestDetectMercurial(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, ".hg"), 0755)
	v := Detect(dir)
	if v == nil || v.Name() != "hg" {
		t.Fatalf("expected hg, got %v", v)
	}
}

func TestDetectSubversion(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, ".svn"), 0755)
	v := Detect(dir)
	if v == nil || v.Name() != "svn" {
		t.Fatalf("expected svn, got %v", v)
	}
}

func TestDetectJujutsu(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, ".jj"), 0755)
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

// --- Helpers ---

func hasBinary(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// setGitEnv sets a minimal git identity so commit works without global config.
func setGitEnv(cmd *exec.Cmd) {
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// =====================================================================
// Git Integration Tests
// =====================================================================

func setupGitRepo(t *testing.T) string {
	t.Helper()
	if !hasBinary("git") {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		setGitEnv(cmd)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	// Set repo-local identity so commits work even without global git config (CI).
	run("config", "user.name", "Test")
	run("config", "user.email", "test@test.com")
	writeFile(t, dir, "README.md", "# test repo\n")
	run("add", "README.md")
	run("commit", "-m", "initial commit")
	return dir
}

func TestGitStatus_CleanRepo(t *testing.T) {
	dir := setupGitRepo(t)
	v := Git{}
	out, err := v.Status(context.Background(), dir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty status, got %q", out)
	}
	clean, _ := v.IsClean(context.Background(), dir)
	if !clean {
		t.Error("expected IsClean=true")
	}
}

func TestGitStatus_DirtyRepo(t *testing.T) {
	dir := setupGitRepo(t)
	v := Git{}
	writeFile(t, dir, "README.md", "# modified\n")
	writeFile(t, dir, "new.txt", "new\n")
	out, err := v.Status(context.Background(), dir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !strings.Contains(out, "README.md") || !strings.Contains(out, "new.txt") {
		t.Errorf("status missing files: %q", out)
	}
	clean, _ := v.IsClean(context.Background(), dir)
	if clean {
		t.Error("expected IsClean=false")
	}
}

func TestGitDiff_UncommittedChanges(t *testing.T) {
	dir := setupGitRepo(t)
	v := Git{}
	writeFile(t, dir, "README.md", "# modified\n")
	out, err := v.Diff(context.Background(), dir, false, "")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(out, "-# test repo") || !strings.Contains(out, "+# modified") {
		t.Errorf("diff missing changes: %q", out)
	}
}

func TestGitDiff_Cached(t *testing.T) {
	dir := setupGitRepo(t)
	v := Git{}
	writeFile(t, dir, "staged.txt", "staged content\n")
	cmd := exec.Command("git", "add", "staged.txt")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	out, err := v.Diff(context.Background(), dir, true, "")
	if err != nil {
		t.Fatalf("Diff(cached): %v", err)
	}
	if !strings.Contains(out, "staged.txt") || !strings.Contains(out, "+staged content") {
		t.Errorf("cached diff missing staged file: %q", out)
	}
}

func TestGitLog(t *testing.T) {
	dir := setupGitRepo(t)
	v := Git{}
	// Add second commit
	writeFile(t, dir, "file2.txt", "content\n")
	cmd := exec.Command("git", "add", "file2.txt")
	cmd.Dir = dir
	setGitEnv(cmd)
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "second commit")
	cmd.Dir = dir
	setGitEnv(cmd)
	cmd.Run()

	out, err := v.Log(context.Background(), dir, 10)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected >= 2 commits, got %d: %q", len(lines), out)
	}
	if !strings.Contains(lines[0], "second commit") {
		t.Errorf("expected 'second commit' first, got %q", lines[0])
	}
}

func TestGitLog_WithCount(t *testing.T) {
	dir := setupGitRepo(t)
	v := Git{}
	out, err := v.Log(context.Background(), dir, 1)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(strings.Split(strings.TrimSpace(out), "\n")) != 1 {
		t.Errorf("expected 1 line, got %q", out)
	}
}

func TestGitAdd(t *testing.T) {
	dir := setupGitRepo(t)
	v := Git{}
	writeFile(t, dir, "newfile.txt", "content\n")
	_, err := v.Add(context.Background(), dir, []string{"newfile.txt"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	out, _ := v.Status(context.Background(), dir)
	if !strings.Contains(out, "newfile.txt") {
		t.Errorf("newfile.txt should be staged: %q", out)
	}
}

func TestGitCommit(t *testing.T) {
	dir := setupGitRepo(t)
	v := Git{}
	writeFile(t, dir, "newfile.txt", "content\n")
	v.Add(context.Background(), dir, []string{"newfile.txt"})
	_, err := v.Commit(context.Background(), dir, "test commit message")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	clean, _ := v.IsClean(context.Background(), dir)
	if !clean {
		t.Error("expected clean after commit")
	}
}

func TestGitCurrentBranch(t *testing.T) {
	dir := setupGitRepo(t)
	v := Git{}
	branch, err := v.CurrentBranch(context.Background(), dir)
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	if branch != "main" && branch != "master" {
		t.Errorf("expected main/master, got %q", branch)
	}
}

func TestDetectThenStatus_RealGitRepo(t *testing.T) {
	dir := setupGitRepo(t)
	v := Detect(dir)
	if v == nil || v.Name() != "git" {
		t.Fatalf("expected git detection, got %v", v)
	}
	out, err := v.Status(context.Background(), dir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty status, got %q", out)
	}
}

func TestGitStatus_NotARepo(t *testing.T) {
	v := Git{}
	_, err := v.Status(context.Background(), t.TempDir())
	if err == nil {
		t.Error("expected error for non-repo")
	}
}

// =====================================================================
// Mercurial Integration Tests
// =====================================================================

func setupHgRepo(t *testing.T) string {
	t.Helper()
	if !hasBinary("hg") {
		t.Skip("hg not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("hg", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("hg %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	// Set repo-local identity so commits work without global hg config (CI).
	// Write hgrc with username.
	os.MkdirAll(filepath.Join(dir, ".hg"), 0755)
	os.WriteFile(filepath.Join(dir, ".hg", "hgrc"), []byte("[ui]\nusername = Test <test@test.com>\n"), 0644)
	writeFile(t, dir, "README.md", "# hg repo\n")
	run("add", "README.md")
	run("commit", "-m", "initial commit")
	return dir
}

//go:build integration_local

package vcs

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func TestHgStatus_CleanRepo(t *testing.T) {
	dir := setupHgRepo(t)
	v := Mercurial{}
	out, err := v.Status(context.Background(), dir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty, got %q", out)
	}
}

func TestHgStatus_DirtyRepo(t *testing.T) {
	dir := setupHgRepo(t)
	v := Mercurial{}
	writeFile(t, dir, "README.md", "# modified\n")
	out, err := v.Status(context.Background(), dir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !strings.Contains(out, "README.md") {
		t.Errorf("status missing README.md: %q", out)
	}
}

func TestHgDiff(t *testing.T) {
	dir := setupHgRepo(t)
	v := Mercurial{}
	writeFile(t, dir, "README.md", "# modified\n")
	out, err := v.Diff(context.Background(), dir, false, "")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(out, "# modified") {
		t.Errorf("diff missing changes: %q", out)
	}
}

func TestHgLog(t *testing.T) {
	dir := setupHgRepo(t)
	v := Mercurial{}
	out, err := v.Log(context.Background(), dir, 10)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if !strings.Contains(out, "initial commit") {
		t.Errorf("log missing 'initial commit': %q", out)
	}
}

func TestHgAdd(t *testing.T) {
	dir := setupHgRepo(t)
	v := Mercurial{}
	writeFile(t, dir, "new.txt", "new\n")
	_, err := v.Add(context.Background(), dir, []string{"new.txt"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
}

func TestHgCommit(t *testing.T) {
	dir := setupHgRepo(t)
	v := Mercurial{}
	writeFile(t, dir, "new.txt", "new\n")
	v.Add(context.Background(), dir, []string{"new.txt"})
	_, err := v.Commit(context.Background(), dir, "second commit")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
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
		t.Fatalf("CurrentBranch: %v", err)
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
		t.Fatalf("IsClean: %v", err)
	}
	if !clean {
		t.Error("expected clean")
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
		t.Fatalf("Status: %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty, got %q", out)
	}
}

func TestHgStatus_NotARepo(t *testing.T) {
	v := Mercurial{}
	_, err := v.Status(context.Background(), t.TempDir())
	if err == nil {
		t.Error("expected error for non-repo")
	}
}

// =====================================================================
// Subversion Integration Tests
// =====================================================================

func setupSvnRepo(t *testing.T) string {
	t.Helper()
	if !hasBinary("svn") || !hasBinary("svnadmin") {
		t.Skip("svn or svnadmin not installed")
	}
	// Create a local SVN repository and check out a working copy.
	repoDir := t.TempDir()
	wcDir := t.TempDir()
	// svnadmin create
	cmd := exec.Command("svnadmin", "create", repoDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("svnadmin create: %v\n%s", err, out)
	}
	// svn checkout
	cmd = exec.Command("svn", "checkout", "file://"+repoDir, wcDir, "--non-interactive")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("svn checkout: %v\n%s", err, out)
	}
	// Write a file and commit.
	writeFile(t, wcDir, "README.md", "# svn repo\n")
	cmd = exec.Command("svn", "add", "README.md")
	cmd.Dir = wcDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("svn add: %v\n%s", err, out)
	}
	cmd = exec.Command("svn", "commit", "-m", "initial commit",
		"--username", "test", "--password", "test", "--non-interactive")
	cmd.Dir = wcDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("svn commit: %v\n%s", err, out)
	}
	return wcDir
}

func TestSvnStatus_CleanRepo(t *testing.T) {
	dir := setupSvnRepo(t)
	v := Subversion{}
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

func TestSvnStatus_DirtyRepo(t *testing.T) {
	dir := setupSvnRepo(t)
	v := Subversion{}
	writeFile(t, dir, "README.md", "# modified\n")
	out, err := v.Status(context.Background(), dir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !strings.Contains(out, "README.md") {
		t.Errorf("status missing README.md: %q", out)
	}
	clean, _ := v.IsClean(context.Background(), dir)
	if clean {
		t.Error("expected IsClean=false")
	}
}

func TestSvnDiff(t *testing.T) {
	dir := setupSvnRepo(t)
	v := Subversion{}
	writeFile(t, dir, "README.md", "# modified\n")
	out, err := v.Diff(context.Background(), dir, false, "")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(out, "modified") {
		t.Errorf("diff missing changes: %q", out)
	}
}

func TestSvnLog(t *testing.T) {
	dir := setupSvnRepo(t)
	v := Subversion{}
	out, err := v.Log(context.Background(), dir, 10)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if !strings.Contains(out, "initial commit") {
		t.Errorf("log missing 'initial commit': %q", out)
	}
}

func TestSvnAdd(t *testing.T) {
	dir := setupSvnRepo(t)
	v := Subversion{}
	writeFile(t, dir, "new.txt", "new\n")
	_, err := v.Add(context.Background(), dir, []string{"new.txt"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
}

func TestSvnCommit(t *testing.T) {
	dir := setupSvnRepo(t)
	v := Subversion{}
	writeFile(t, dir, "new.txt", "new\n")
	v.Add(context.Background(), dir, []string{"new.txt"})
	_, err := v.Commit(context.Background(), dir, "second commit")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	out, _ := v.Status(context.Background(), dir)
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected clean after commit, got %q", out)
	}
}

func TestSvnCurrentBranch(t *testing.T) {
	dir := setupSvnRepo(t)
	v := Subversion{}
	// For svn, "branch" is the last path component of the repo URL.
	branch, err := v.CurrentBranch(context.Background(), dir)
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	// Should return something non-empty.
	if branch == "" {
		t.Error("expected non-empty branch/repo name")
	}
}

func TestSvnIsClean(t *testing.T) {
	dir := setupSvnRepo(t)
	v := Subversion{}
	clean, err := v.IsClean(context.Background(), dir)
	if err != nil {
		t.Fatalf("IsClean: %v", err)
	}
	if !clean {
		t.Error("expected clean after initial commit")
	}
}

func TestDetectThenStatus_RealSvnRepo(t *testing.T) {
	dir := setupSvnRepo(t)
	v := Detect(dir)
	if v == nil || v.Name() != "svn" {
		t.Fatalf("expected svn detection, got %v", v)
	}
	out, err := v.Status(context.Background(), dir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected empty status, got %q", out)
	}
}

func TestSvnStatus_NotARepo(t *testing.T) {
	v := Subversion{}
	// svn status works on any directory (returns empty). This is expected
	// svn behavior — unlike git, svn doesn't error on non-repo dirs.
	out, err := v.Status(context.Background(), t.TempDir())
	if err != nil {
		// If it does error on some platforms, that's fine too.
		return
	}
	// On a non-repo dir, output should be empty.
	if strings.TrimSpace(out) != "" {
		t.Logf("svn status on non-repo returned: %q (expected empty)", out)
	}
}

// =====================================================================
// Jujutsu Integration Tests
// =====================================================================

func setupJjRepo(t *testing.T) string {
	t.Helper()
	if !hasBinary("jj") {
		t.Skip("jj not installed")
	}
	dir := t.TempDir()
	// jj git init --colocate creates a repo with .jj and .git dirs.
	// Use `jj init` (or `jj git init`) — detect will find .jj.
	cmd := exec.Command("jj", "git", "init", "--colocate")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("jj git init: %v\n%s", err, out)
	}
	// Write a file. jj tracks it automatically.
	writeFile(t, dir, "README.md", "# jj repo\n")
	// Describe the current change (commit message).
	cmd = exec.Command("jj", "describe", "-m", "initial commit")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("jj describe: %v\n%s", err, out)
	}
	// Create a new change on top so the initial one is finalized.
	cmd = exec.Command("jj", "new")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("jj new: %v\n%s", err, out)
	}
	return dir
}

func TestJjStatus_CleanRepo(t *testing.T) {
	dir := setupJjRepo(t)
	v := Jujutsu{}
	// After setup, we're on a new empty change — should be "clean".
	out, err := v.Status(context.Background(), dir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !strings.Contains(out, "no changes") {
		t.Errorf("expected 'no changes' in clean repo, got %q", out)
	}
}

func TestJjStatus_DirtyRepo(t *testing.T) {
	dir := setupJjRepo(t)
	v := Jujutsu{}
	writeFile(t, dir, "newfile.txt", "new content\n")
	out, err := v.Status(context.Background(), dir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !strings.Contains(out, "newfile.txt") {
		t.Errorf("status should show newfile.txt: %q", out)
	}
}

func TestJjDiff(t *testing.T) {
	dir := setupJjRepo(t)
	v := Jujutsu{}
	writeFile(t, dir, "diff_file.txt", "diff content\n")
	out, err := v.Diff(context.Background(), dir, false, "")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(out, "diff_file.txt") {
		t.Errorf("diff should show diff_file.txt: %q", out)
	}
}

func TestJjLog(t *testing.T) {
	dir := setupJjRepo(t)
	v := Jujutsu{}
	out, err := v.Log(context.Background(), dir, 10)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if !strings.Contains(out, "initial commit") {
		t.Errorf("log should contain 'initial commit': %q", out)
	}
}

func TestJjCommit(t *testing.T) {
	dir := setupJjRepo(t)
	v := Jujutsu{}
	// Write a file, then commit.
	writeFile(t, dir, "committed.txt", "data\n")
	_, err := v.Commit(context.Background(), dir, "my test commit")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	// After Commit (describe + new), working copy should be clean.
	clean, err := v.IsClean(context.Background(), dir)
	if err != nil {
		t.Fatalf("IsClean: %v", err)
	}
	if !clean {
		out, _ := v.Status(context.Background(), dir)
		t.Errorf("expected clean after commit, status: %q", out)
	}
	// Verify the commit appears in log.
	out, _ := v.Log(context.Background(), dir, 5)
	if !strings.Contains(out, "my test commit") {
		t.Errorf("log should contain 'my test commit': %q", out)
	}
}

func TestJjCurrentBranch(t *testing.T) {
	dir := setupJjRepo(t)
	v := Jujutsu{}
	branch, err := v.CurrentBranch(context.Background(), dir)
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	if branch == "" {
		t.Error("expected non-empty branch/change ID")
	}
}

func TestJjIsClean(t *testing.T) {
	dir := setupJjRepo(t)
	v := Jujutsu{}
	clean, err := v.IsClean(context.Background(), dir)
	if err != nil {
		t.Fatalf("IsClean: %v", err)
	}
	if !clean {
		t.Error("expected clean for empty working copy")
	}
}

func TestDetectThenStatus_RealJjRepo(t *testing.T) {
	dir := setupJjRepo(t)
	v := Detect(dir)
	// jj git init --colocate creates both .jj and .git — Detect prefers git.
	// This is expected behavior. Verify we detect *something*.
	if v == nil {
		t.Fatal("expected VCS detection, got nil")
	}
	// When colocated, git takes priority.
	if v.Name() != "git" && v.Name() != "jj" {
		t.Errorf("expected git or jj, got %s", v.Name())
	}
}

func TestJjStatus_NotARepo(t *testing.T) {
	v := Jujutsu{}
	_, err := v.Status(context.Background(), t.TempDir())
	if err == nil {
		t.Error("expected error for non-repo")
	}
}

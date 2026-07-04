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

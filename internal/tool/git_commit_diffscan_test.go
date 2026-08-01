package tool

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGetWorkingTreeDiff verifies that getWorkingTreeDiff returns the correct
// diff for unstaged changes relative to HEAD. This is the function used by
// git_commit when all:true to scan for quality issues before auto-staging.
func TestGetWorkingTreeDiff(t *testing.T) {
	dir := t.TempDir()
	gitInitTest(t, dir)

	// Create initial committed file.
	original := "package main\n\nfunc main() {}\n"
	writeFileTest(t, filepath.Join(dir, "main.go"), original)
	gitAddAndCommitTest(t, dir, "initial")

	// Modify the file without staging.
	modified := "package main\n\nfunc main() {\n	fmt.Println(\"debug\")\n}\n"
	writeFileTest(t, filepath.Join(dir, "main.go"), modified)

	diff := getWorkingTreeDiff(context.Background(), dir)
	if diff == "" {
		t.Fatal("expected non-empty diff from getWorkingTreeDiff")
	}

	// The diff should contain the added debug line.
	if !strings.Contains(diff, "fmt.Println") {
		t.Errorf("diff should contain added line, got:\n%s", diff)
	}

	// ScanStagedDiffForIssues should detect the debug statement.
	issues := ScanStagedDiffForIssues(diff)
	if len(issues) == 0 {
		t.Errorf("expected issues from scan of working tree diff, got 0")
	}

	foundDebug := false
	for _, iss := range issues {
		if iss.Category == "debug-stmt" {
			foundDebug = true
		}
	}
	if !foundDebug {
		t.Errorf("expected debug-stmt issue, got: %+v", issues)
	}
}

// TestGetWorkingTreeDiff_NoHead verifies graceful handling when there's no
// HEAD commit (empty repo).
func TestGetWorkingTreeDiff_NoHead(t *testing.T) {
	dir := t.TempDir()
	gitInitTest(t, dir)

	writeFileTest(t, filepath.Join(dir, "file.txt"), "content")

	diff := getWorkingTreeDiff(context.Background(), dir)
	// "git diff HEAD" fails on repos with no commits — should return "".
	if diff != "" {
		t.Errorf("expected empty diff when no HEAD exists, got: %s", diff)
	}
}

// TestGetWorkingTreeDiff_NoChanges verifies empty diff when working tree is clean.
func TestGetWorkingTreeDiff_NoChanges(t *testing.T) {
	dir := t.TempDir()
	gitInitTest(t, dir)

	writeFileTest(t, filepath.Join(dir, "file.txt"), "content")
	gitAddAndCommitTest(t, dir, "initial")

	diff := getWorkingTreeDiff(context.Background(), dir)
	if diff != "" {
		t.Errorf("expected empty diff for clean working tree, got: %s", diff)
	}
}

// --- helpers (test-scoped, suffixed to avoid collisions) ---

func gitInitTest(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v\n%s", err, out)
	}
	for _, kv := range [][2]string{
		{"user.name", "Test"},
		{"user.email", "test@test.com"},
	} {
		c := exec.Command("git", "config", kv[0], kv[1])
		c.Dir = dir
		if err := c.Run(); err != nil {
			t.Fatalf("git config %s failed: %v", kv[0], err)
		}
	}
	c := exec.Command("git", "symbolic-ref", "HEAD", "refs/heads/main")
	c.Dir = dir
	_ = c.Run()
}

func gitAddAndCommitTest(t *testing.T, dir, msg string) {
	t.Helper()
	add := exec.Command("git", "add", "-A")
	add.Dir = dir
	if err := add.Run(); err != nil {
		t.Fatalf("git add failed: %v", err)
	}
	commit := exec.Command("git", "commit", "-m", msg)
	commit.Dir = dir
	if out, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("git commit failed: %v\n%s", err, out)
	}
}

func writeFileTest(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writeFile %s failed: %v", path, err)
	}
}

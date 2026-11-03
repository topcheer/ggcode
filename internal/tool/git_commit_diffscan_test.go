package tool

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetWorkingTreeDiff(t *testing.T) {
	dir := t.TempDir()
	gitInitTest(t, dir)

	original := "package main\n\nfunc main() {}\n"
	writeFileTest(t, filepath.Join(dir, "main.go"), original)
	gitAddAndCommitTest(t, dir, "initial")

	modified := "package main\n\nfunc main() {\n	fmt.Println(\"debug\")\n}\n"
	writeFileTest(t, filepath.Join(dir, "main.go"), modified)

	diff := getWorkingTreeDiff(context.Background(), dir)
	if diff == "" {
		t.Fatal("expected non-empty diff from getWorkingTreeDiff")
	}

	if !strings.Contains(diff, "fmt.Println") {
		t.Errorf("diff should contain added line, got:\n%s", diff)
	}

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

func TestGetWorkingTreeDiff_NoHead(t *testing.T) {
	dir := t.TempDir()
	gitInitTest(t, dir)

	writeFileTest(t, filepath.Join(dir, "file.txt"), "content")

	diff := getWorkingTreeDiff(context.Background(), dir)

	if diff != "" {
		t.Errorf("expected empty diff when no HEAD exists, got: %s", diff)
	}
}

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

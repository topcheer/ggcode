package tool

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// setupGitRepo creates a temporary git repo with an initial commit and returns its path.
func setupGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "Test")
	// Create a file and commit it
	writeTestFile(t, dir+"/hello.txt", "hello\n")
	run("add", ".")
	run("commit", "-m", "initial commit")
	return dir
}

func TestGitRevert_Execute(t *testing.T) {
	dir := setupGitRepo(t)
	// Make a second commit to revert
	writeTestFile(t, dir+"/world.txt", "world\n")
	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("add", ".")
	runGit("commit", "-m", "add world")

	// Get the commit hash
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	hashOut, _ := cmd.Output()
	hash := strings.TrimSpace(string(hashOut))

	tool := GitRevert{WorkingDir: dir}
	input, _ := json.Marshal(map[string]interface{}{
		"commit":      hash,
		"description": "test revert",
	})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Reverted") && !strings.Contains(strings.ToLower(result.Content), "revert") {
		t.Fatalf("expected revert mention in output, got: %s", result.Content)
	}

	// Verify the file was removed by the revert
	if fileExists(dir + "/world.txt") {
		t.Fatal("world.txt should have been removed by revert")
	}
}

func TestGitRevert_NoCommitRequired(t *testing.T) {
	tool := GitRevert{WorkingDir: "/tmp"}
	input, _ := json.Marshal(map[string]interface{}{
		"description": "test",
	})
	result, _ := tool.Execute(context.Background(), input)
	if !result.IsError {
		t.Fatal("expected error when commit is missing")
	}
}

func TestGitReset_SoftUnstage(t *testing.T) {
	dir := setupGitRepo(t)
	// Stage a new file
	writeTestFile(t, dir+"/new.txt", "new\n")
	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("add", "new.txt")

	tool := GitReset{WorkingDir: dir}
	input, _ := json.Marshal(map[string]interface{}{
		"mode":        "mixed",
		"description": "test reset",
	})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	// File should still exist (mixed doesn't delete)
	if !fileExists(dir + "/new.txt") {
		t.Fatal("file should still exist after mixed reset")
	}
}

func TestGitReset_InvalidMode(t *testing.T) {
	tool := GitReset{}
	input, _ := json.Marshal(map[string]interface{}{
		"mode":        "invalid",
		"description": "test",
	})
	result, _ := tool.Execute(context.Background(), input)
	if !result.IsError {
		t.Fatal("expected error for invalid mode")
	}
}

func TestGitReset_FilesUnstage(t *testing.T) {
	dir := setupGitRepo(t)
	writeTestFile(t, dir+"/a.txt", "a\n")
	writeTestFile(t, dir+"/b.txt", "b\n")
	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("add", "a.txt", "b.txt")

	tool := GitReset{WorkingDir: dir}
	input, _ := json.Marshal(map[string]interface{}{
		"files":       []string{"a.txt"},
		"description": "test",
	})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
}

func TestGitTag_ListCreateDelete(t *testing.T) {
	dir := setupGitRepo(t)
	tool := GitTag{WorkingDir: dir}

	// List (empty)
	input, _ := json.Marshal(map[string]interface{}{
		"action":      "list",
		"description": "test list",
	})
	result, _ := tool.Execute(context.Background(), input)
	if result.IsError {
		t.Fatalf("list failed: %s", result.Content)
	}
	if result.Content != "No tags found." {
		t.Fatalf("expected no tags, got: %s", result.Content)
	}

	// Create
	input, _ = json.Marshal(map[string]interface{}{
		"action":      "create",
		"name":        "v1.0.0",
		"message":     "first release",
		"description": "test create",
	})
	result, _ = tool.Execute(context.Background(), input)
	if result.IsError {
		t.Fatalf("create failed: %s", result.Content)
	}
	if !strings.Contains(result.Content, "v1.0.0") {
		t.Fatalf("expected tag name in output, got: %s", result.Content)
	}

	// List (should show tag)
	input, _ = json.Marshal(map[string]interface{}{
		"action":      "list",
		"description": "test list",
	})
	result, _ = tool.Execute(context.Background(), input)
	if !strings.Contains(result.Content, "v1.0.0") {
		t.Fatalf("expected v1.0.0 in list, got: %s", result.Content)
	}

	// Delete
	input, _ = json.Marshal(map[string]interface{}{
		"action":      "delete",
		"name":        "v1.0.0",
		"description": "test delete",
	})
	result, _ = tool.Execute(context.Background(), input)
	if result.IsError {
		t.Fatalf("delete failed: %s", result.Content)
	}

	// List (should be empty again)
	input, _ = json.Marshal(map[string]interface{}{
		"action":      "list",
		"description": "test list",
	})
	result, _ = tool.Execute(context.Background(), input)
	if result.Content != "No tags found." {
		t.Fatalf("expected no tags after delete, got: %s", result.Content)
	}
}

func TestGitTag_CreateRequiresName(t *testing.T) {
	tool := GitTag{}
	input, _ := json.Marshal(map[string]interface{}{
		"action":      "create",
		"description": "test",
	})
	result, _ := tool.Execute(context.Background(), input)
	if !result.IsError {
		t.Fatal("expected error when name is missing")
	}
}

// Helper functions
func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

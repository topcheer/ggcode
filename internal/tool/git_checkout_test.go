package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitCheckoutDescriptionMentionsSafety(t *testing.T) {
	desc := GitCheckout{}.Description()
	for _, want := range []string{"Switch", "create", "git_status"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("git_checkout description should mention %q, got %q", want, desc)
		}
	}
}

func TestGitCheckoutInvalidInput(t *testing.T) {
	gc := GitCheckout{}
	result, err := gc.Execute(context.Background(), json.RawMessage(`bad json`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for invalid input")
	}
}

func TestGitCheckoutEmptyBranch(t *testing.T) {
	gc := GitCheckout{}
	input, _ := json.Marshal(map[string]string{"branch": ""})
	result, err := gc.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for empty branch name")
	}
}

func TestValidateBranchName(t *testing.T) {
	valid := []string{
		"main", "feature/add-login", "fix-123", "release/v2.0",
		"user/feature/branch", "v1.0.0", "hotfix_urgent",
	}
	for _, name := range valid {
		if err := validateBranchName(name); err != nil {
			t.Errorf("validateBranchName(%q) = %v, want nil", name, err)
		}
	}

	invalid := map[string]string{
		"":             "empty",
		"my branch":    "space",
		"branch~1":     "tilde",
		"branch^":      "caret",
		"bra:ch":       "colon",
		"-feature":     "starts with dash",
		"branch..name": "double dot",
		"branch@{now}": "reflog",
		"trailing/":    "trailing slash",
		"repo.lock":    "lock suffix",
	}
	for name, reason := range invalid {
		if err := validateBranchName(name); err == nil {
			t.Errorf("validateBranchName(%q) = nil, want error (%s)", name, reason)
		}
	}
}

func TestValidateRefName(t *testing.T) {
	valid := []string{"HEAD", "main", "abc1234", "v1.0.0", "origin/main"}
	for _, ref := range valid {
		if err := validateRefName(ref); err != nil {
			t.Errorf("validateRefName(%q) = %v, want nil", ref, err)
		}
	}

	invalid := map[string]string{
		"$(echo)":    "shell substitution",
		"a;b":        "semicolon",
		"a | b":      "pipe",
		"-flag":      "starts with dash",
		`quote"name`: "double quote",
	}
	for ref, reason := range invalid {
		if err := validateRefName(ref); err == nil {
			t.Errorf("validateRefName(%q) = nil, want error (%s)", ref, reason)
		}
	}
}

func TestGitCheckoutCreateAndSwitch(t *testing.T) {
	// Create a temp git repo for this test.
	tmpDir := t.TempDir()
	if err := initTestRepo(tmpDir); err != nil {
		t.Fatalf("failed to init test repo: %v", err)
	}

	gc := GitCheckout{WorkingDir: tmpDir}

	// Create a new branch.
	input, _ := json.Marshal(map[string]any{
		"branch":      "feature/test-branch",
		"create":      true,
		"description": "test",
	})
	result, err := gc.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content)
	}

	// Verify we're on the new branch.
	current := currentBranchName(context.Background(), tmpDir)
	if current != "feature/test-branch" {
		t.Fatalf("expected branch 'feature/test-branch', got %q", current)
	}

	// Switch back to main.
	input, _ = json.Marshal(map[string]any{
		"branch":      "main",
		"description": "test",
	})
	result, err = gc.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("expected success switching back, got error: %s", result.Content)
	}

	current = currentBranchName(context.Background(), tmpDir)
	if current != "main" {
		t.Fatalf("expected branch 'main', got %q", current)
	}
}

func TestGitCheckoutDirtyWarning(t *testing.T) {
	tmpDir := t.TempDir()
	if err := initTestRepo(tmpDir); err != nil {
		t.Fatalf("failed to init test repo: %v", err)
	}

	// Create a dirty file.
	if err := os.WriteFile(filepath.Join(tmpDir, "uncommitted.txt"), []byte("dirty"), 0644); err != nil {
		t.Fatal(err)
	}

	gc := GitCheckout{WorkingDir: tmpDir}

	// Create a new branch — should succeed with warning.
	input, _ := json.Marshal(map[string]any{
		"branch":      "feature/dirty-switch",
		"create":      true,
		"description": "test",
	})
	result, err := gc.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("expected success despite dirty tree, got error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "uncommitted changes") {
		t.Errorf("expected dirty warning in result, got: %s", result.Content)
	}
}

func TestGitCheckoutNonexistentBranch(t *testing.T) {
	tmpDir := t.TempDir()
	if err := initTestRepo(tmpDir); err != nil {
		t.Fatalf("failed to init test repo: %v", err)
	}

	gc := GitCheckout{WorkingDir: tmpDir}

	// Try switching to a branch that doesn't exist (without create=true).
	input, _ := json.Marshal(map[string]any{
		"branch":      "nonexistent-branch",
		"description": "test",
	})
	result, err := gc.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for switching to nonexistent branch without create=true")
	}
}

// initTestRepo creates a minimal git repo with an initial commit in tmpDir.
func initTestRepo(tmpDir string) error {
	ctx := context.Background()

	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
	} {
		cmd := gitCommand(ctx, args...)
		cmd.Dir = tmpDir
		if out, err := cmd.CombinedOutput(); err != nil {
			return err
		} else {
			_ = out
		}
	}

	// Create initial commit.
	if err := os.WriteFile(filepath.Join(tmpDir, "README.md"), []byte("# test"), 0644); err != nil {
		return err
	}
	cmd := gitCommand(ctx, "add", "README.md")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		return err
	}
	cmd = gitCommand(ctx, "commit", "-m", "initial commit")
	cmd.Dir = tmpDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return err
	} else {
		_ = out
	}
	return nil
}

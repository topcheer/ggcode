package tool

import (
	"context"
	"encoding/json"
	"testing"
)

func TestWorktree_InvalidName(t *testing.T) {
	ew := EnterWorktree{WorkingDir: "."}
	input, _ := json.Marshal(map[string]interface{}{
		"name": "bad name with spaces!",
	})
	result, err := ew.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for invalid worktree name")
	}
}

func TestWorktree_NotGitRepo(t *testing.T) {
	ew := EnterWorktree{WorkingDir: t.TempDir()}
	input, _ := json.Marshal(map[string]interface{}{
		"name": "test-wt",
	})
	result, err := ew.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for non-git directory")
	}
}

func TestExitWorktree_NotInWorktree(t *testing.T) {
	dir := t.TempDir()
	// Init a git repo so it doesn't fail on "not a git repo"
	cmd := gitCommand(context.Background(), "init")
	cmd.Dir = dir
	cmd.Run()

	xw := ExitWorktree{WorkingDir: dir}
	input, _ := json.Marshal(map[string]interface{}{
		"action": "keep",
	})
	result, err := xw.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error when not in a worktree")
	}
}

func TestIsWorktreeNameChar(t *testing.T) {
	valid := []string{"abc", "ABC", "123", "my-work_tree.v2"}
	for _, name := range valid {
		for _, c := range name {
			if !isWorktreeNameChar(c) {
				t.Errorf("expected %q in %q to be valid", c, name)
			}
		}
	}
	invalid := []rune{' ', '/', '\\', ':', '*', '?', '"', '<', '>', '|'}
	for _, c := range invalid {
		if isWorktreeNameChar(c) {
			t.Errorf("expected %q to be invalid", c)
		}
	}
}

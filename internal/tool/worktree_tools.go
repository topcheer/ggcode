package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// EnterWorktree creates a new git worktree for isolated work.
type EnterWorktree struct {
	WorkingDir string
}

func (t EnterWorktree) Name() string { return "enter_worktree" }

func (t EnterWorktree) Description() string {
	return "Create an isolated git worktree. Returns the path to the new worktree directory. " +
		"Use this when the user explicitly asks to work in a worktree."
}

func (t EnterWorktree) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"name": {
				"type": "string",
				"description": "Name for the worktree (used as directory and branch name). Defaults to a random name."
			}
		}
	}`)
}

func (t EnterWorktree) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	name := args.Name
	if name == "" {
		// Generate a random-ish name
		name = fmt.Sprintf("wt-%d", os.Getpid())
	}

	// Sanitize name: only allow safe characters
	for _, c := range name {
		if !isWorktreeNameChar(c) {
			return Result{IsError: true, Content: fmt.Sprintf("invalid worktree name %q: only letters, digits, dots, underscores, and dashes allowed", name)}, nil
		}
	}

	// Find git root
	gitRoot, err := findGitRoot(ctx, t.WorkingDir)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("not a git repository: %v", err)}, nil
	}

	worktreesDir := filepath.Join(gitRoot, ".ggcode", "worktrees")
	if err := os.MkdirAll(worktreesDir, 0755); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error creating worktrees dir: %v", err)}, nil
	}

	worktreePath := filepath.Join(worktreesDir, name)
	branchName := name

	// Create worktree with new branch from HEAD
	cmd := exec.CommandContext(ctx, "git", "worktree", "add", "-b", branchName, worktreePath, "HEAD")
	cmd.Dir = gitRoot
	cmd.Env = append(os.Environ(), "GIT_PAGER=cat")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error creating worktree: %s", strings.TrimSpace(string(out)))}, nil
	}

	return Result{Content: fmt.Sprintf("Created worktree at %s (branch: %s)", worktreePath, branchName)}, nil
}

// ExitWorktree exits and optionally removes a git worktree.
type ExitWorktree struct {
	WorkingDir string
}

func (t ExitWorktree) Name() string { return "exit_worktree" }

func (t ExitWorktree) Description() string {
	return "Exit and optionally remove a git worktree. " +
		"Use this when the user asks to exit a worktree session."
}

func (t ExitWorktree) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["keep", "remove"],
				"description": "'keep' leaves the worktree directory and branch intact. 'remove' deletes both."
			},
			"discard_changes": {
				"type": "boolean",
				"default": false,
				"description": "If true, discard uncommitted changes when removing. Required when there are uncommitted changes."
			}
		},
		"required": ["action"]
	}`)
}

func (t ExitWorktree) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Action         string `json:"action"`
		DiscardChanges bool   `json:"discard_changes"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	// Find git root
	gitRoot, err := findGitRoot(ctx, t.WorkingDir)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("not a git repository: %v", err)}, nil
	}

	// Check if we're actually in a worktree
	isWorktree, worktreePath, err := isInsideWorktree(gitRoot)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error checking worktree status: %v", err)}, nil
	}
	if !isWorktree {
		return Result{IsError: true, Content: "not currently inside a worktree created by enter_worktree"}, nil
	}

	if args.Action == "keep" {
		return Result{Content: fmt.Sprintf("Worktree at %s kept intact. Switch back to the main working directory to continue.", worktreePath)}, nil
	}

	// action == "remove"
	// Check for uncommitted changes
	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
	cmd.Dir = gitRoot
	cmd.Env = append(os.Environ(), "GIT_PAGER=cat")
	out, err := cmd.Output()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error checking git status: %v", err)}, nil
	}
	if len(strings.TrimSpace(string(out))) > 0 && !args.DiscardChanges {
		return Result{IsError: true, Content: "worktree has uncommitted changes. Set discard_changes=true to remove anyway, or commit/stash your changes first."}, nil
	}

	// Get branch name before removing
	branchCmd := exec.CommandContext(ctx, "git", "branch", "--show-current")
	branchCmd.Dir = gitRoot
	branchCmd.Env = append(os.Environ(), "GIT_PAGER=cat")
	branchOut, _ := branchCmd.Output()
	branchName := strings.TrimSpace(string(branchOut))

	// Remove the worktree
	rmCmd := exec.CommandContext(ctx, "git", "worktree", "remove", "--force", worktreePath)
	rmCmd.Dir = filepath.Dir(filepath.Dir(worktreePath)) // back to git root
	// We need the actual git root, not the worktree's git dir
	actualGitRoot, _ := findGitRootFromWorktree(worktreePath)
	if actualGitRoot != "" {
		rmCmd.Dir = actualGitRoot
	}
	rmCmd.Env = append(os.Environ(), "GIT_PAGER=cat")
	if rmOut, err := rmCmd.CombinedOutput(); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error removing worktree: %s", strings.TrimSpace(string(rmOut)))}, nil
	}

	// Optionally delete the branch
	if branchName != "" && branchName != "main" && branchName != "master" {
		delCmd := exec.CommandContext(ctx, "git", "branch", "-D", branchName)
		delCmd.Dir = actualGitRoot
		delCmd.Env = append(os.Environ(), "GIT_PAGER=cat")
		_ = delCmd.Run() // best effort
	}

	return Result{Content: fmt.Sprintf("Removed worktree %s", worktreePath)}, nil
}

// isWorktreeNameChar returns true for characters allowed in worktree names.
func isWorktreeNameChar(c rune) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.'
}

// findGitRoot finds the git repository root from a directory.
func findGitRoot(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_PAGER=cat")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("not a git repository")
	}
	return strings.TrimSpace(string(out)), nil
}

// isInsideWorktree checks if the current directory is inside a .ggcode/worktrees subdirectory.
func isInsideWorktree(gitDir string) (bool, string, error) {
	// Check if gitDir is inside a .ggcode/worktrees directory
	// by looking at the path components
	worktreesPrefix := filepath.Join(".ggcode", "worktrees") + string(filepath.Separator)
	// Simple check: does the path contain .ggcode/worktrees
	absPath, _ := filepath.Abs(gitDir)
	if strings.Contains(absPath, worktreesPrefix) {
		return true, absPath, nil
	}

	// Also check via git: if .git is a file (not directory), we're in a worktree
	gitFile := filepath.Join(gitDir, ".git")
	info, err := os.Lstat(gitFile)
	if err != nil {
		return false, "", nil
	}
	if info.Mode().IsRegular() {
		// Read the gitdir reference
		data, err := os.ReadFile(gitFile)
		if err == nil && strings.HasPrefix(string(data), "gitdir: ") {
			gitdir := strings.TrimSpace(strings.TrimPrefix(string(data), "gitdir: "))
			// Check if it's under .ggcode/worktrees
			if strings.Contains(gitdir, "worktrees") {
				return true, absPath, nil
			}
		}
		return true, absPath, nil
	}

	return false, "", nil
}

// findGitRootFromWorktree finds the main repo root from a worktree path.
func findGitRootFromWorktree(worktreePath string) (string, error) {
	gitFile := filepath.Join(worktreePath, ".git")
	data, err := os.ReadFile(gitFile)
	if err != nil {
		return "", err
	}
	// Content is like: gitdir: /path/to/main-repo/.git/worktrees/wt-name
	gitdir := strings.TrimSpace(strings.TrimPrefix(string(data), "gitdir: "))
	// The main repo root is 2 levels up from the gitdir
	gitDir := filepath.Dir(filepath.Dir(gitdir))
	return gitDir, nil
}

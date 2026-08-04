package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// EnterWorktree creates a new git worktree for isolated work.
type EnterWorktree struct {
	WorkingDir string
}

func (t EnterWorktree) Name() string { return "enter_worktree" }

func (t EnterWorktree) Description() string {
	return "Create an isolated git worktree under .ggcode/worktrees/ with a new branch from HEAD. " +
		"Use the returned path as working_dir for operations inside the worktree."
}

func (t EnterWorktree) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"name": {
			"type": "string",
			"description": "Name for the worktree (used as directory and branch name). Defaults to a random name."
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Searching for TODO patterns', '检查构建配置'). You MUST always provide this field."
		}
	},
	"required": [
		"description"
	]
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
		name = fmt.Sprintf("wt-%s-%04d", time.Now().Format("20060102"), rand.Intn(10000))
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

	return Result{
		Content:             fmt.Sprintf("Created worktree at %s (branch: %s). SuggestedWorkingDir is set to this path; use the returned path for subsequent file and command operations inside the worktree.", worktreePath, branchName),
		SuggestedWorkingDir: worktreePath,
	}, nil
}

// ExitWorktree exits and optionally removes a git worktree.
type ExitWorktree struct {
	WorkingDir string
}

func (t ExitWorktree) Name() string { return "exit_worktree" }

func (t ExitWorktree) Description() string {
	return "Exit and optionally remove a git worktree. " +
		"Use this when the user asks to exit a worktree session. " +
		"'keep' leaves the worktree directory and branch intact for later use. " +
		"'remove' deletes the worktree directory and its branch. " +
		"If there are uncommitted changes, you must set discard_changes=true or the removal will be rejected."
}

func (t ExitWorktree) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"action": {
			"type": "string",
			"enum": [
				"keep",
				"remove"
			],
			"description": "'keep' leaves the worktree directory and branch intact. 'remove' deletes both."
		},
		"discard_changes": {
			"type": "boolean",
			"default": false,
			"description": "If true, discard uncommitted changes when removing. Required when there are uncommitted changes."
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Searching for TODO patterns', '检查构建配置'). You MUST always provide this field."
		}
	},
	"required": [
		"action",
		"description"
	]
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

	// Find the main repo root so we can suggest switching back
	mainRepoRoot, _ := findGitRootFromWorktree(worktreePath)

	if args.Action == "keep" {
		return Result{
			Content:             fmt.Sprintf("Worktree at %s kept intact.", worktreePath),
			SuggestedWorkingDir: mainRepoRoot,
		}, nil
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
	if mainRepoRoot != "" {
		rmCmd.Dir = mainRepoRoot
	} else {
		rmCmd.Dir = filepath.Dir(filepath.Dir(worktreePath))
	}
	rmCmd.Env = append(os.Environ(), "GIT_PAGER=cat")
	if rmOut, err := rmCmd.CombinedOutput(); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error removing worktree: %s", strings.TrimSpace(string(rmOut)))}, nil
	}

	// Optionally delete the branch
	if branchName != "" && branchName != "main" && branchName != "master" {
		delCmd := exec.CommandContext(ctx, "git", "branch", "-D", branchName)
		delCmd.Dir = mainRepoRoot
		delCmd.Env = append(os.Environ(), "GIT_PAGER=cat")
		_ = delCmd.Run() // best effort
	}

	return Result{
		Content:             fmt.Sprintf("Removed worktree %s", worktreePath),
		SuggestedWorkingDir: mainRepoRoot,
	}, nil
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
	// gitdir points to .git/worktrees/<name>, so we need 3 levels up:
	// .git/worktrees/<name> → .git/worktrees → .git → main repo root
	gitDir := filepath.Dir(filepath.Dir(filepath.Dir(gitdir)))
	return gitDir, nil
}

// ListWorktree lists all git worktrees with their branches, dirty status, and paths.
type ListWorktree struct {
	WorkingDir string
}

func (t ListWorktree) Name() string { return "list_worktree" }

func (t ListWorktree) Description() string {
	return "List all git worktrees with branch, dirty status, and last commit. " +
		"Use before creating a new worktree to avoid name collisions, or to find an existing worktree to resume work in."
}

func (t ListWorktree) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"description": {
				"type": "string",
				"description": "REQUIRED. Brief activity label shown in the UI."
			}
		},
		"required": ["description"]
	}`)
}

type worktreeEntry struct {
	Path      string `json:"path"`
	Branch    string `json:"branch"`
	HEAD      string `json:"head"`
	Dirty     bool   `json:"dirty"`
	IsCurrent bool   `json:"is_current"`
}

func (t ListWorktree) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	gitRoot, err := findGitRoot(ctx, t.WorkingDir)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("not a git repository: %v", err)}, nil
	}

	// Get porcelain v2 output: worktree <path> <head> <upstream> <branch>
	cmd := exec.CommandContext(ctx, "git", "worktree", "list", "--porcelain")
	cmd.Dir = gitRoot
	cmd.Env = append(os.Environ(), "GIT_PAGER=cat")
	out, err := cmd.Output()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error listing worktrees: %v", err)}, nil
	}

	// Parse porcelain output
	var entries []worktreeEntry
	var cur *worktreeEntry
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "worktree ") {
			wtPath := strings.TrimPrefix(line, "worktree ")
			entries = append(entries, worktreeEntry{Path: wtPath})
			cur = &entries[len(entries)-1]
			if wtPath == gitRoot || t.WorkingDir != "" && wtPath == t.WorkingDir {
				cur.IsCurrent = true
			}
		} else if cur != nil && strings.HasPrefix(line, "HEAD ") {
			cur.HEAD = strings.TrimPrefix(line, "HEAD ")
		} else if cur != nil && strings.HasPrefix(line, "branch ") {
			cur.Branch = strings.TrimPrefix(line, "branch ")
			cur.Branch = strings.TrimPrefix(cur.Branch, "refs/heads/")
		}
	}

	// Check dirty status for each worktree
	for i := range entries {
		stCmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
		stCmd.Dir = entries[i].Path
		stCmd.Env = append(os.Environ(), "GIT_PAGER=cat")
		stOut, stErr := stCmd.Output()
		if stErr == nil && len(strings.TrimSpace(string(stOut))) > 0 {
			entries[i].Dirty = true
		}
	}

	if len(entries) == 0 {
		entries = []worktreeEntry{}
	}

	// Build human-readable summary
	var sb strings.Builder
	for _, e := range entries {
		marker := " "
		if e.IsCurrent {
			marker = "*"
		}
		dirtyStr := ""
		if e.Dirty {
			dirtyStr = " (uncommitted changes)"
		}
		branchStr := e.Branch
		if branchStr == "" {
			branchStr = "(detached HEAD " + e.HEAD[:min(7, len(e.HEAD))] + ")"
		}
		sb.WriteString(fmt.Sprintf("%s %-40s  %s%s\n", marker, e.Path, branchStr, dirtyStr))
	}

	return Result{
		Content: fmt.Sprintf("%d worktree(s):\n%s", len(entries), strings.TrimSpace(sb.String())),
	}, nil
}

// Clone returns an independent copy of ListWorktree for use by a different agent.
func (t ListWorktree) Clone() Tool {
	return &ListWorktree{WorkingDir: t.WorkingDir}
}

// Clone returns an independent copy of EnterWorktree for use by a different agent.
func (t EnterWorktree) Clone() Tool {
	return &EnterWorktree{WorkingDir: t.WorkingDir}
}

// Clone returns an independent copy of ExitWorktree for use by a different agent.
func (t ExitWorktree) Clone() Tool {
	return &ExitWorktree{WorkingDir: t.WorkingDir}
}

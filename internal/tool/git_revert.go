package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// GitRevert implements the git_revert tool.
// It creates a new commit that undoes the changes made by a specified commit,
// preserving history rather than rewriting it (unlike git reset).
type GitRevert struct{ WorkingDir string }

func (t GitRevert) Name() string { return "git_revert" }

func (t GitRevert) Description() string {
	return "Revert a commit by creating a new commit that undoes the changes. Safe for shared branches because it preserves history. Use git_log first to identify the commit hash. For undoing local uncommitted changes, use git_reset or git_stash instead."
}

func (t GitRevert) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Repository path (default: current directory)"
			},
			"commit": {
				"type": "string",
				"description": "Commit hash to revert. Use git_log to find the hash. Supports abbreviated hashes."
			},
			"no_commit": {
				"type": "boolean",
				"description": "If true, stage the revert changes without creating a commit (default: false). Useful for combining multiple reverts into one commit."
			},
			"description": {
				"type": "string",
				"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language."
			}
		},
		"required": ["commit", "description"]
	}`)
}

func (t GitRevert) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Path     string `json:"path"`
		Commit   string `json:"commit"`
		NoCommit bool   `json:"no_commit"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if args.Commit == "" {
		return Result{IsError: true, Content: "commit is required"}, nil
	}

	dir := resolveDir(args.Path, t.WorkingDir)

	gitArgs := []string{"revert"}
	if args.NoCommit {
		gitArgs = append(gitArgs, "--no-commit")
	}
	gitArgs = append(gitArgs, args.Commit)

	cmd := gitCommand(ctx, gitArgs...)
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	if err != nil {
		// Detect merge conflict
		errStr := string(out)
		if strings.Contains(errStr, "CONFLICT") || strings.Contains(errStr, "conflict") {
			return Result{IsError: true, Content: fmt.Sprintf(
				"git revert failed due to merge conflicts. Resolve them manually, then run 'git revert --continue' (or 'git revert --abort' to cancel).\n%s", errStr)}, nil
		}
		return Result{IsError: true, Content: fmt.Sprintf("git revert failed: %v\n%s", err, errStr)}, nil
	}

	trimmed := strings.TrimSpace(string(out))
	if args.NoCommit {
		msg := fmt.Sprintf("Reverted commit %s (changes staged, not committed). Run git_commit to complete the revert.", args.Commit)
		if trimmed != "" {
			msg += "\n" + trimmed
		}
		return Result{Content: msg}, nil
	}

	if trimmed == "" {
		return Result{Content: fmt.Sprintf("Reverted commit %s successfully.", args.Commit)}, nil
	}
	return Result{Content: trimmed}, nil
}

// Clone returns an independent copy of this tool for use by a different agent.
func (t GitRevert) Clone() Tool {
	return &GitRevert{WorkingDir: t.WorkingDir}
}

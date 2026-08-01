package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// GitReset implements the git_reset tool.
// It resets the staging area and/or working tree to a specified state.
// This is a write operation that can discard uncommitted changes.
type GitReset struct{ WorkingDir string }

func (t GitReset) Name() string { return "git_reset" }

func (t GitReset) Description() string {
	return "Reset the staging area and/or working tree. Modes: 'soft' (unstage only, keep working changes), 'mixed' (unstage + keep file changes, default), 'hard' (discard ALL changes permanently). Use 'soft' to unstage accidentally staged files. Use 'hard' only when certain — it permanently discards uncommitted work. Check git_status before resetting."
}

func (t GitReset) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Repository path (default: current directory)"
			},
			"mode": {
				"type": "string",
				"description": "Reset mode: 'soft' (unstage only), 'mixed' (unstage + keep changes, default), 'hard' (discard all changes permanently)",
				"enum": ["soft", "mixed", "hard"]
			},
			"target": {
				"type": "string",
				"description": "What to reset to: 'HEAD' (default), 'HEAD~1' (parent commit), or a specific commit hash. Use to undo the last commit with mode='soft'."
			},
			"files": {
				"type": "array",
				"items": {"type": "string"},
				"description": "Optional: specific files to unstage (mode is ignored when files are specified; always uses mixed reset for those files)."
			},
			"description": {
				"type": "string",
				"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language."
			}
		},
		"required": ["description"]
	}`)
}

func (t GitReset) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Path   string   `json:"path"`
		Mode   string   `json:"mode"`
		Target string   `json:"target"`
		Files  []string `json:"files"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	mode := args.Mode
	if mode == "" {
		mode = "mixed"
	}
	if mode != "soft" && mode != "mixed" && mode != "hard" {
		return Result{IsError: true, Content: fmt.Sprintf("unsupported mode %q: must be soft, mixed, or hard", mode)}, nil
	}

	target := args.Target
	if target == "" {
		target = "HEAD"
	}

	dir := resolveDir(args.Path, t.WorkingDir)

	// File-specific reset: unstage specific files (always mixed mode)
	if len(args.Files) > 0 {
		gitArgs := append([]string{"reset"}, args.Files...)
		cmd := gitCommand(ctx, gitArgs...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("git reset failed: %v\n%s", err, out)}, nil
		}
		trimmed := strings.TrimSpace(string(out))
		if trimmed == "" {
			return Result{Content: fmt.Sprintf("Unstaged %d file(s).", len(args.Files))}, nil
		}
		return Result{Content: trimmed}, nil
	}

	gitArgs := []string{"reset", "--" + mode, target}

	cmd := gitCommand(ctx, gitArgs...)
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("git reset failed: %v\n%s", err, out)}, nil
	}

	trimmed := strings.TrimSpace(string(out))

	// Add advisory for hard reset
	if mode == "hard" {
		msg := "Reset to " + target + " (hard). Uncommitted changes were permanently discarded."
		if trimmed != "" {
			msg += "\n" + trimmed
		}
		return Result{Content: msg}, nil
	}

	if trimmed == "" {
		action := "unstaged"
		if mode == "soft" {
			action = "uncommitted (kept in index)"
		}
		return Result{Content: fmt.Sprintf("Reset %s to %s. Changes are %s.", mode, target, action)}, nil
	}
	return Result{Content: trimmed}, nil
}

// Clone returns an independent copy of this tool for use by a different agent.
func (t GitReset) Clone() Tool {
	return &GitReset{WorkingDir: t.WorkingDir}
}

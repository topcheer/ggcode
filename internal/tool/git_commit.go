package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/vcs"
)

// GitCommit implements the git_commit tool.
type GitCommit struct{ WorkingDir string }

func (t GitCommit) Name() string { return "git_commit" }

func (t GitCommit) Description() string {
	return "Commit staged changes with a message. Commit only after inspecting git status/diff and staging exactly the intended files. A Co-Authored-By trailer is appended automatically."
}

func (t GitCommit) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"path": {
			"type": "string",
			"description": "Repository path (default: current directory)"
		},
		"message": {
			"type": "string",
			"description": "Commit message"
		},
		"all": {
			"type": "boolean",
			"description": "Automatically stage modified/deleted files before committing (git commit -a). Use only when the user explicitly wants all tracked modifications included; new untracked files are not added."
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Searching for TODO patterns', '检查构建配置'). You MUST always provide this field."
		}
	},
	"required": [
		"message",
		"description"
	]
}`)
}

func (t GitCommit) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Path    string `json:"path"`
		Message string `json:"message"`
		All     bool   `json:"all"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if args.Message == "" {
		return Result{IsError: true, Content: "message is required"}, nil
	}

	// Message quality check: warn about vague commit messages that don't
	// explain what changed or why. This is advisory (non-blocking) — the
	// commit still proceeds, but the warning helps the agent self-correct.
	msgWarning := checkCommitMessageQuality(args.Message)

	// Append Co-Authored-By trailer
	fullMessage := args.Message + "\n\n" + coAuthorTrailer

	dir := resolveDir(args.Path, t.WorkingDir)

	// Branch safety: warn when committing to main/master/trunk to prevent
	// accidental direct-to-trunk commits. We check the current branch name
	// and append a warning to the commit result if it's a protected branch.
	branchWarning := checkProtectedBranch(ctx, dir)

	// Non-git VCS path.
	if v := vcs.Detect(dir); v != nil && v.Name() != "git" {
		out, err := v.Commit(ctx, dir, fullMessage)
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("%s commit failed: %v", v.Name(), err)}, nil
		}
		trimmed := strings.TrimSpace(out)
		if trimmed == "" {
			return Result{Content: "Committed successfully."}, nil
		}
		return Result{Content: trimmed}, nil
	}

	// Pre-commit diff scan: check staged changes for common quality issues
	// (debug statements, merge conflict markers, secrets, TODOs, debugger
	// breakpoints). This is advisory (non-blocking) — the commit proceeds, but
	// the warnings help the agent self-correct before pushing.
	var diffScanWarning string
	if !args.All {
		// For non -a commits, staged diff is available before commit.
		diffOutput := getStagedDiff(ctx, dir)
		issues := ScanStagedDiffForIssues(diffOutput)
		diffScanWarning = FormatDiffIssues(issues)
	}

	gitArgs := []string{"commit", "-m", fullMessage}
	if args.All {
		gitArgs = []string{"commit", "-a", "-m", fullMessage}
	}

	cmd := gitCommand(ctx, gitArgs...)
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("git commit failed: %v\n%s", err, out)}, nil
	}

	trimmed := strings.TrimSpace(string(out))
	// Append branch warning, message quality warning, and diff scan warning
	// if present.
	if branchWarning != "" {
		trimmed += "\n\n" + branchWarning
	}
	if msgWarning != "" {
		trimmed += "\n\n" + msgWarning
	}
	if diffScanWarning != "" {
		trimmed += "\n\n" + diffScanWarning
	}
	if trimmed == "" {
		result := "Committed successfully."
		if branchWarning != "" {
			result += "\n\n" + branchWarning
		}
		if msgWarning != "" {
			result += "\n\n" + msgWarning
		}
		if diffScanWarning != "" {
			result += "\n\n" + diffScanWarning
		}
		return Result{Content: result}, nil
	}

	return Result{Content: trimmed}, nil
}

// checkCommitMessageQuality returns a warning string if the commit message
// is too short or matches common vague patterns. Non-blocking advisory.
func checkCommitMessageQuality(msg string) string {
	trimmed := strings.TrimSpace(msg)
	if len(trimmed) < 10 {
		return fmt.Sprintf(
			"Warning: commit message is very short (%d chars). "+
				"A good commit message describes what changed and why, e.g., "+
				"'fix: handle nil pointer in context manager on resume'.",
			len(trimmed),
		)
	}
	lower := strings.ToLower(trimmed)
	vague := []string{"wip", "fix", "update", "changes", "misc", "stuff", "todo", "temp", "hack"}
	for _, v := range vague {
		if lower == v || lower == v+"." {
			return fmt.Sprintf(
				"Warning: commit message %q is too vague. "+
					"Describe what was changed, e.g., 'fix: null pointer exception in session loader'.",
				trimmed,
			)
		}
	}
	return ""
}

// Clone returns an independent copy of this tool for use by a different agent.
func (t GitCommit) Clone() Tool {
	return &GitCommit{WorkingDir: t.WorkingDir}
}

// checkProtectedBranch returns a warning string if the current git branch
// is a protected branch (main, master, trunk, develop). Returns empty string
// if on a feature/working branch or not in a git repo.
func checkProtectedBranch(ctx context.Context, dir string) string {
	cmd := gitCommand(ctx, "symbolic-ref", "--short", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "" // not in git repo or detached HEAD — don't block
	}
	branch := strings.TrimSpace(string(out))

	protectedBranches := map[string]bool{
		"main":       true,
		"master":     true,
		"trunk":      true,
		"develop":    true,
		"production": true,
	}
	if !protectedBranches[branch] {
		return ""
	}
	return fmt.Sprintf(
		"Warning: committed to protected branch %q. "+
			"Consider creating a feature branch for changes (e.g., 'git checkout -b feature/your-feature'). "+
			"Direct commits to %s may bypass code review and CI checks.",
		branch, branch,
	)
}

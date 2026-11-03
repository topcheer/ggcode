package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/vcs"
)

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
			"description": "REQUIRED. Brief activity label shown in the UI, in the user's language."
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

	msgWarning := checkCommitMessageQuality(args.Message)

	fullMessage := args.Message
	if !coAuthorInjectionDisabled() {
		fullMessage += "\n\n" + coAuthorTrailer
	}

	dir := resolveDir(args.Path, t.WorkingDir)

	branchWarning := checkProtectedBranch(ctx, dir)

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

	buildWarning := precommitBuildCheck(ctx, dir)

	var diffScanWarning, scopeWarning, convTip string
	var diffOutput string
	if args.All {
		diffOutput = getWorkingTreeDiff(ctx, dir)
	} else {
		diffOutput = getStagedDiff(ctx, dir)
	}
	issues := ScanStagedDiffForIssues(diffOutput)
	diffScanWarning = FormatDiffIssues(issues)
	stagingWarning := AnalyzeStagingQuality(diffOutput)
	cohesion, size := AnalyzeCommitScope(diffOutput)
	scopeWarning = combineScopeWarnings(cohesion, size)

	if scopeWarning != "" {
		if partition := SuggestCommitPartition(diffOutput); len(partition) >= 2 {
			scopeWarning += "\n\n" + FormatCommitPartition(partition)
		}
	}
	convTip = AnalyzeCommitMessage(args.Message)

	consistencyWarning := AnalyzeMessageDiffConsistency(args.Message, diffOutput)

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

	for _, w := range []string{buildWarning, branchWarning, msgWarning, diffScanWarning, stagingWarning, scopeWarning, convTip, consistencyWarning} {
		if w != "" {
			trimmed += "\n\n" + w
		}
	}
	if trimmed == "" {
		var b strings.Builder
		b.WriteString("Committed successfully.")
		for _, w := range []string{buildWarning, branchWarning, msgWarning, diffScanWarning, stagingWarning, scopeWarning, convTip, consistencyWarning} {
			if w != "" {
				b.WriteString("\n\n")
				b.WriteString(w)
			}
		}
		return Result{Content: b.String()}, nil
	}

	return Result{Content: trimmed}, nil
}

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

func (t GitCommit) Clone() Tool {
	return &GitCommit{WorkingDir: t.WorkingDir}
}

func checkProtectedBranch(ctx context.Context, dir string) string {
	cmd := gitCommand(ctx, "symbolic-ref", "--short", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
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

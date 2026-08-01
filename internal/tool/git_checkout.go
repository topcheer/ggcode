package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/topcheer/ggcode/internal/vcs"
)

// GitCheckout implements the git_checkout tool for creating new branches and
// switching between existing branches. Includes safety checks for uncommitted
// changes, branch name validation, and protected branch warnings.
type GitCheckout struct{ WorkingDir string }

func (t GitCheckout) Name() string { return "git_checkout" }

func (t GitCheckout) Description() string {
	return "Switch to an existing Git branch or create a new one. Set create=true to create and switch to a new branch (git checkout -b). Check git_status first to ensure clean working tree before switching."
}

func (t GitCheckout) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"path": {
			"type": "string",
			"description": "Repository path (default: current directory)"
		},
		"branch": {
			"type": "string",
			"description": "Branch name to switch to or create"
		},
		"create": {
			"type": "boolean",
			"description": "If true, create a new branch (git checkout -b). Default: false (switch to existing branch)."
		},
		"start_point": {
			"type": "string",
			"description": "Starting point for new branch (commit hash, tag, or branch). Only used when create=true. Defaults to HEAD."
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Searching for TODO patterns', '检查构建配置'). You MUST always provide this field."
		}
	},
	"required": [
		"branch",
		"description"
	]
}`)
}

func (t GitCheckout) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Path       string `json:"path"`
		Branch     string `json:"branch"`
		Create     bool   `json:"create"`
		StartPoint string `json:"start_point"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if strings.TrimSpace(args.Branch) == "" {
		return Result{IsError: true, Content: "branch is required"}, nil
	}

	// Validate branch name to prevent shell injection and invalid names.
	if err := validateBranchName(args.Branch); err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	dir := resolveDir(args.Path, t.WorkingDir)

	// Detect VCS and use the appropriate checkout implementation.
	vcsImpl := vcs.Detect(dir)
	if vcsImpl == nil {
		return Result{IsError: true, Content: "no VCS detected in " + dir}, nil
	}

	// Safety check: warn about uncommitted changes before switching.
	clean, _ := vcsImpl.IsClean(ctx, dir)
	var dirtyWarning string
	if !clean {
		dirtyWarning = checkWorkingTreeDirty(ctx, dir, vcsImpl.Name())
	}

	// Track the branch we're leaving for context in the result.
	prevBranch, _ := vcsImpl.CurrentBranch(ctx, dir)

	// Validate start point if provided.
	if args.Create && args.StartPoint != "" {
		if err := validateRefName(args.StartPoint); err != nil {
			return Result{IsError: true, Content: err.Error()}, nil
		}
	}

	// Execute checkout via VCS abstraction.
	out, err := vcsImpl.Checkout(ctx, dir, args.Branch, args.Create, args.StartPoint)
	if err != nil {
		if errors.Is(err, vcs.ErrCheckoutNotSupported) {
			return Result{IsError: true, Content: fmt.Sprintf("%s does not support branch checkout", vcsImpl.DisplayName())}, nil
		}
		return Result{IsError: true, Content: fmt.Sprintf("%s checkout failed: %v\n%s", vcsImpl.Name(), err, out)}, nil
	}

	// Build result message.
	trimmed := strings.TrimSpace(string(out))
	nowBranch, _ := vcsImpl.CurrentBranch(ctx, dir)

	var b strings.Builder
	if trimmed != "" {
		b.WriteString(trimmed)
	} else if args.Create {
		b.WriteString(fmt.Sprintf("Created and switched to branch %q.", args.Branch))
	} else {
		b.WriteString(fmt.Sprintf("Switched to branch %q.", args.Branch))
	}

	// Confirm current branch after checkout.
	if nowBranch != "" && nowBranch != prevBranch {
		b.WriteString(fmt.Sprintf("\nCurrent branch: %s", nowBranch))
	}

	// Append dirty warning if there were uncommitted changes.
	if dirtyWarning != "" {
		b.WriteString("\n\n" + dirtyWarning)
	}

	return Result{Content: b.String()}, nil
}

// validateBranchName checks that a branch name is safe for use as a git
// command argument. It prevents shell injection and rejects names that git
// itself would reject.
func validateBranchName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("branch name cannot be empty")
	}
	if len(name) > 200 {
		return fmt.Errorf("branch name too long (%d chars)", len(name))
	}
	// Git branch names cannot contain: space, ~, ^, :, ?, *, [, \, control chars
	for _, ch := range name {
		switch ch {
		case ' ', '~', '^', ':', '?', '*', '[', '\\':
			return fmt.Errorf("branch name %q contains invalid character %q", name, ch)
		}
		if ch < 0x20 || ch == 0x7f {
			return fmt.Errorf("branch name %q contains control character", name)
		}
	}
	// Branch names cannot start with '-' (would be interpreted as a flag).
	if strings.HasPrefix(name, "-") {
		return fmt.Errorf("branch name %q starts with '-' — use a letter or '/' prefix", name)
	}
	// Branch names cannot contain '..' (range syntax) or '@{' (reflog syntax).
	if strings.Contains(name, "..") {
		return fmt.Errorf("branch name %q contains '..' (reserved by git)", name)
	}
	if strings.Contains(name, "@{") {
		return fmt.Errorf("branch name %q contains '@{' (reserved by git)", name)
	}
	// Cannot end with '/' or '.lock'.
	if strings.HasSuffix(name, "/") {
		return fmt.Errorf("branch name %q ends with '/'", name)
	}
	if strings.HasSuffix(name, ".lock") {
		return fmt.Errorf("branch name %q ends with '.lock' (reserved by git)", name)
	}
	return nil
}

// validateRefName validates a git reference (commit hash, tag, branch) for
// command-line safety. Similar to branch validation but more permissive for
// hex hashes.
func validateRefName(ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return fmt.Errorf("start_point cannot be empty")
	}
	if len(ref) > 200 {
		return fmt.Errorf("start_point too long")
	}
	// Block characters that could enable shell injection.
	for _, ch := range ref {
		switch ch {
		case ' ', ';', '|', '&', '$', '`', '(', ')', '<', '>', '\n', '\r', '\\', '"', '\'':
			return fmt.Errorf("start_point %q contains invalid character %q", ref, ch)
		}
		if ch < 0x20 || ch == 0x7f {
			return fmt.Errorf("start_point %q contains control character", ref)
		}
	}
	if strings.HasPrefix(ref, "-") {
		return fmt.Errorf("start_point %q starts with '-'", ref)
	}
	return nil
}

// checkWorkingTreeDirty checks if the working tree has uncommitted changes
// and returns a warning string if so. Non-blocking advisory.
func checkWorkingTreeDirty(ctx context.Context, dir string, vcsName string) string {
	var cmd *exec.Cmd
	if vcsName == "git" {
		cmd = exec.CommandContext(ctx, "git", "status", "--porcelain")
	} else {
		// For non-git VCS, just check if status output is non-empty.
		cmd = exec.CommandContext(ctx, vcsName, "status")
	}
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "" // can't check — don't block
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return ""
	}

	// Count by type for a concise summary.
	lines := strings.Split(trimmed, "\n")
	var staged, unstaged, untracked int
	for _, l := range lines {
		if len(l) < 2 {
			continue
		}
		x, y := l[0], l[1]
		switch {
		case x == '?' && y == '?':
			untracked++
		case x != ' ' && x != '?':
			staged++
		}
		if y != ' ' && y != '?' {
			unstaged++
		}
	}

	parts := []string{}
	if staged > 0 {
		parts = append(parts, fmt.Sprintf("%d staged", staged))
	}
	if unstaged > 0 {
		parts = append(parts, fmt.Sprintf("%d unstaged", unstaged))
	}
	if untracked > 0 {
		parts = append(parts, fmt.Sprintf("%d untracked", untracked))
	}

	return fmt.Sprintf(
		"Warning: working tree has uncommitted changes (%s). "+
			"These changes carried over to the new branch. "+
			"Use git_stash if you want a clean switch, or commit them first.",
		strings.Join(parts, ", "),
	)
}

// currentBranchName returns the current git branch name, or empty string if
// it cannot be determined.
func currentBranchName(ctx context.Context, dir string) string {
	cmd := gitCommand(ctx, "symbolic-ref", "--short", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Clone returns an independent copy of this tool for use by a different agent.
func (t GitCheckout) Clone() Tool {
	return &GitCheckout{WorkingDir: t.WorkingDir}
}

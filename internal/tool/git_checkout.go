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
			"description": "REQUIRED. Brief activity label shown in the UI, in the user's language."
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

	if err := validateBranchName(args.Branch); err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	args.Branch = strings.TrimSpace(args.Branch)

	dir := resolveDir(args.Path, t.WorkingDir)

	vcsImpl := vcs.Detect(dir)
	if vcsImpl == nil {
		return Result{IsError: true, Content: "no VCS detected in " + dir}, nil
	}

	clean, _ := vcsImpl.IsClean(ctx, dir)
	var dirtyWarning string
	if !clean {
		dirtyWarning = checkWorkingTreeDirty(ctx, dir, vcsImpl.Name())
	}

	prevBranch, _ := vcsImpl.CurrentBranch(ctx, dir)

	if args.Create && args.StartPoint != "" {
		if err := validateRevspec(args.StartPoint); err != nil {
			return Result{IsError: true, Content: err.Error()}, nil
		}

		args.StartPoint = strings.TrimSpace(args.StartPoint)
	}

	out, err := vcsImpl.Checkout(ctx, dir, strings.TrimSpace(args.Branch), args.Create, strings.TrimSpace(args.StartPoint))
	if err != nil {
		if errors.Is(err, vcs.ErrCheckoutNotSupported) {
			return Result{IsError: true, Content: fmt.Sprintf("%s does not support branch checkout", vcsImpl.DisplayName())}, nil
		}
		return Result{IsError: true, Content: fmt.Sprintf("%s checkout failed: %v\n%s", vcsImpl.Name(), err, out)}, nil
	}

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

	if nowBranch != "" && nowBranch != prevBranch {
		b.WriteString(fmt.Sprintf("\nCurrent branch: %s", nowBranch))
	}

	if dirtyWarning != "" {
		b.WriteString("\n\n" + dirtyWarning)
	}

	return Result{Content: b.String()}, nil
}

func validateBranchName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("branch name cannot be empty")
	}
	if len(name) > 200 {
		return fmt.Errorf("branch name too long (%d chars)", len(name))
	}

	for _, ch := range name {
		switch ch {
		case ' ', '~', '^', ':', '?', '*', '[', '\\':
			return fmt.Errorf("branch name %q contains invalid character %q", name, ch)
		}
		if ch < 0x20 || ch == 0x7f {
			return fmt.Errorf("branch name %q contains control character", name)
		}
	}

	if strings.HasPrefix(name, "-") {
		return fmt.Errorf("branch name %q starts with '-' — use a letter or '/' prefix", name)
	}

	if strings.Contains(name, "..") {
		return fmt.Errorf("branch name %q contains '..' (reserved by git)", name)
	}
	if strings.Contains(name, "@{") {
		return fmt.Errorf("branch name %q contains '@{' (reserved by git)", name)
	}

	if strings.HasSuffix(name, "/") {
		return fmt.Errorf("branch name %q ends with '/'", name)
	}
	if strings.HasSuffix(name, ".lock") {
		return fmt.Errorf("branch name %q ends with '.lock' (reserved by git)", name)
	}
	return nil
}

func validateRevspec(rev string) error {
	rev = strings.TrimSpace(rev)
	if rev == "" {
		return fmt.Errorf("revision cannot be empty")
	}
	if len(rev) > 200 {
		return fmt.Errorf("revision too long")
	}
	if strings.Contains(rev, "..") {
		return fmt.Errorf("revision %q must not contain '..' (single commit, not a range)", rev)
	}
	if strings.HasPrefix(rev, "-") {
		return fmt.Errorf("revision %q starts with '-' (refusing option injection)", rev)
	}
	for _, ch := range rev {
		if ch < 0x20 || ch == 0x7f {
			return fmt.Errorf("revision %q contains control character", rev)
		}
	}
	return nil
}

func validateRefName(ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return fmt.Errorf("start_point cannot be empty")
	}
	if len(ref) > 200 {
		return fmt.Errorf("start_point too long")
	}
	if strings.Contains(ref, "..") {
		return fmt.Errorf("start_point %q must not contain '..'", ref)
	}
	if strings.HasPrefix(ref, "@{") || strings.HasSuffix(ref, ".lock") {
		return fmt.Errorf("start_point %q uses a reserved form", ref)
	}

	for _, ch := range ref {
		switch ch {
		case ' ', '~', '^', ':', '?', '*', '[', '\n', '\r', '\\':
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

func checkWorkingTreeDirty(ctx context.Context, dir string, vcsName string) string {
	var cmd *exec.Cmd
	if vcsName == "git" {
		cmd = exec.CommandContext(ctx, "git", "status", "--porcelain")
	} else {

		cmd = exec.CommandContext(ctx, vcsName, "status")
	}
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return ""
	}

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

func currentBranchName(ctx context.Context, dir string) string {
	cmd := gitCommand(ctx, "symbolic-ref", "--short", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (t GitCheckout) Clone() Tool {
	return &GitCheckout{WorkingDir: t.WorkingDir}
}

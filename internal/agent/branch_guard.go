package agent

// branch_guard.go implements Protected Branch Edit Warning — a pre-edit guard
// that warns when the agent is about to modify files on a protected branch
// (main, master, develop, release/*).
//
// Research basis: In 2025-2026, all major coding agents added branch protection
// awareness as a safety feature:
//   - Claude Code: shows "on branch main" in the permission dialog and warns
//     when editing protected branches
//   - Cursor: warns before edits on main/master with a "create branch?" option
//   - Aider: automatically creates a new branch for each session unless
//     configured otherwise
//   - Cline/OpenHands: shows the current branch in the diff preview
//   - Git best practices (Trunk-Based Development, GitHub Flow): never commit
//     directly to protected branches
//
// Our approach:
//  1. On the first file edit of each run, check the current git branch.
//  2. If on a protected branch, inject a warning recommending the agent
//     create a feature branch first.
//  3. The warning fires once per run — subsequent edits don't repeat it.
//
// The guard is informational (does not block the edit) because:
//   - Many users intentionally work on main for quick fixes or experiments
//   - Blocking would frustrate workflows that intentionally commit to main
//   - The warning creates awareness so the agent can suggest a branch

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// branchGuardState tracks whether the protected branch warning has already fired
// this run, so it only shows once.
type branchGuardState struct {
	fired        bool
	cachedBranch string // cached branch name to avoid repeated git calls
}

func newBranchGuardState() *branchGuardState {
	return &branchGuardState{}
}

func (s *branchGuardState) reset() {
	s.fired = false
	s.cachedBranch = ""
}

// defaultProtectedBranches is the set of branch names (or prefixes) considered
// protected. These follow common Git/GitHub/GitLab conventions.
var defaultProtectedBranches = []string{
	"main",
	"master",
	"develop",
	"development",
	"production",
	"prod",
	"staging",
	"release/", // release/1.0, release/v2, etc.
	"hotfix/",  // hotfix/ critical fixes
}

// isProtectedBranch returns true if the branch name matches a protected pattern.
func isProtectedBranch(branch string) bool {
	if branch == "" {
		return false
	}
	branch = strings.TrimSpace(branch)
	for _, p := range defaultProtectedBranches {
		if strings.HasSuffix(p, "/") {
			// Prefix match (e.g., "release/" matches "release/1.0")
			if strings.HasPrefix(branch, p) {
				return true
			}
		} else {
			// Exact match (e.g., "main" matches "main")
			if branch == p {
				return true
			}
		}
	}
	return false
}

// getCurrentBranch returns the current git branch name, or empty string if it
// cannot be determined (not a git repo, detached HEAD, etc.).
func getCurrentBranch(workingDir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "-C", workingDir, "symbolic-ref", "--short", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		debug.Log("branch_guard", "could not determine current branch: %v", err)
		return ""
	}
	return strings.TrimSpace(string(out))
}

// checkBranchGuard checks if the agent is editing on a protected branch and
// returns a warning string if so. Fires only once per run.
func (a *Agent) checkBranchGuard() string {
	if a.branchGuard == nil {
		return ""
	}
	if a.branchGuard.fired {
		return ""
	}

	workingDir := a.WorkingDir()
	if workingDir == "" {
		return ""
	}

	branch := a.branchGuard.cachedBranch
	if branch == "" {
		branch = getCurrentBranch(workingDir)
		a.branchGuard.cachedBranch = branch
	}

	// #698 (adjacent): only latch "fired" once the branch was actually
	// determined — a transient getCurrentBranch failure used to permanently
	// silence the advisory for the rest of the run.
	a.branchGuard.fired = true

	if !isProtectedBranch(branch) {
		return ""
	}

	return fmt.Sprintf(
		"⚠ PROTECTED BRANCH WARNING: You are editing on branch `%s`, which is a "+
			"protected branch. Direct commits to %s are risky because:\n"+
			"1. Changes may trigger CI/CD pipelines or deployments automatically.\n"+
			"2. History rewrites or force-pushes can break collaborators' work.\n"+
			"3. Code review may be bypassed.\n\n"+
			"Consider creating a feature branch first: `git checkout -b <branch-name>`. "+
			"If this is an intentional quick fix on %s, proceed — but commit carefully.",
		branch, branch, branch,
	)
}

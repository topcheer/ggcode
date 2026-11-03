package agent

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

type branchGuardState struct {
	fired        bool
	cachedBranch string
}

func newBranchGuardState() *branchGuardState {
	return &branchGuardState{}
}

func (s *branchGuardState) reset() {
	s.fired = false
	s.cachedBranch = ""
}

var defaultProtectedBranches = []string{
	"main",
	"master",
	"develop",
	"development",
	"production",
	"prod",
	"staging",
	"release/",
	"hotfix/",
}

func isProtectedBranch(branch string) bool {
	if branch == "" {
		return false
	}
	branch = strings.TrimSpace(branch)
	for _, p := range defaultProtectedBranches {
		if strings.HasSuffix(p, "/") {

			if strings.HasPrefix(branch, p) {
				return true
			}
		} else {

			if branch == p {
				return true
			}
		}
	}
	return false
}

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

	if branch != "" {
		a.branchGuard.fired = true
	}

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

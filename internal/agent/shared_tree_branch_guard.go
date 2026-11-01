package agent

import (
	"encoding/json"
	"os/exec"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

// Shared-tree branch guard (#4803 / S-35).
//
// Incident: while the SHARED MAIN TREE sat on a fix branch for 80+ minutes,
// every seat running build/test/validation there computed against the wrong
// baseline - green builds on a feature branch were read as "main is green".
// The multi-seat fleet shares one main checkout, and checkout state is
// global: any seat can leave a fix branch behind.
//
// This guard intercepts validation-class commands (go build/test/vet,
// make test/lint/verify-ci, npm test - the same classifier build-idempotency
// uses, detectBuildTestCommand) that run in the SHARED MAIN TREE (cwd NOT
// under .ggcode/worktrees/) and appends an explicit warning when
// `git branch --show-current` reports a non-main branch: name the branch,
// say the baseline may be wrong, and recommend a worktree. Warn-once per
// run (the branch rarely changes mid-run), silent on any git failure -
// a missing git or a bare repo must never block validation itself.

var (
	sharedTreeBranchOnce sync.Once
	sharedTreeBranchCur  string // cached branch for the run
	sharedTreeBranchMu   sync.Mutex
)

// sharedTreeCurrentBranch resolves (and caches for the run) the current git
// branch of the tree at dir. Empty string on any failure.
func sharedTreeCurrentBranch(dir string) string {
	sharedTreeBranchMu.Lock()
	defer sharedTreeBranchMu.Unlock()
	if sharedTreeBranchCur == "" {
		out, err := exec.Command("git", "-C", dir, "branch", "--show-current").Output()
		if err != nil {
			return ""
		}
		sharedTreeBranchCur = strings.TrimSpace(string(out))
	}
	return sharedTreeBranchCur
}

// resetSharedTreeBranchGuard clears the run-scoped cache + once-state
// (wired in reset_turn.go with the other detectors).
func resetSharedTreeBranchGuard() {
	sharedTreeBranchMu.Lock()
	sharedTreeBranchCur = ""
	sharedTreeBranchMu.Unlock()
	sharedTreeBranchOnce = sync.Once{}
}

// isSharedMainTree reports whether dir is the shared main checkout rather
// than an isolated .ggcode/worktrees/<name> worktree (isolated trees are
// SUPPOSED to be on a fix branch - no warning wanted there).
func isSharedMainTree(dir string) bool {
	dir = strings.TrimSuffix(dir, "/")
	return !strings.Contains(dir, "/.ggcode/worktrees/")
}

// checkSharedTreeBranchGuard returns a warning line to append to a
// successful run_command result when a validation command just ran on a
// non-main branch of the SHARED main tree. Empty string otherwise.
func (a *Agent) checkSharedTreeBranchGuard(toolName string, args json.RawMessage, resultContent string) string {
	if toolName != "run_command" || strings.Contains(resultContent, "IsError") {
		return ""
	}
	var call struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(args, &call) != nil || call.Command == "" {
		return ""
	}
	if isBuild, _, _ := detectBuildTestCommand(call.Command); !isBuild {
		return ""
	}
	cwd := a.workingDir
	if cwd == "" {
		cwd = "."
	}
	if !isSharedMainTree(cwd) {
		return ""
	}
	warned := false
	sharedTreeBranchOnce.Do(func() { warned = true })
	if !warned {
		return "" // already warned this run
	}
	branch := sharedTreeCurrentBranch(cwd)
	if branch == "" || branch == "main" || branch == "master" {
		return ""
	}
	debug.Log("agent", "shared-tree branch guard: validation ran on %s (not main) in the shared tree (#4803)", branch)
	return "[#4803 warning] validation ran on branch \"" + branch + "\" in the SHARED main tree - this is NOT a main-baseline result. If main-baseline verification is needed, run it in a dedicated worktree (enter_worktree) or switch the shared tree back to main first."
}

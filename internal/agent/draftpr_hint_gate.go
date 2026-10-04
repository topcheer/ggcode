package agent

// Post-Completion Draft-PR Hint Gate
//
// sa-223 research: every major AI coding agent closes the version-control
// loop differently. Aider/Claude Code auto-commit at task end (covered by
// commit_hint_gate), but the 2025-2026 frontier pattern goes one step
// further (competitor analysis docs/research/rotation-competitor-analysis-
// 2026-07-10.md: "auto ... draft PR creation on completion"): work landed
// on a feature branch should surface as a reviewable PR, not sit on the
// contributor's disk.
//
// ggcode had commit nudging (commit_hint_gate.go) but nothing past the
// commit: a feature branch with finished commits ends the run silently and
// the user rediscovers the branch days later.
//
// This gate fires right after the commit hint gate (both are advisory,
// non-blocking). Conditions (ALL must hold):
//   1. The agent called git_commit this run (it finished version control)
//   2. HEAD is on a non-default branch (never suggest a PR for main)
//   3. The branch has commits not pushed to its upstream (no upstream, or
//      ahead > 0 per git status -sb)
//
// The hint suggests push + gh pr create --draft via the normal tool path -
// it never executes anything itself, so supervised approvals still apply.
// Zero LLM cost; two git calls (<100ms).

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// draftPRHintState tracks whether the draft-PR gate fired this run.
type draftPRHintState struct {
	fired bool
}

func newDraftPRHintState() *draftPRHintState {
	return &draftPRHintState{}
}

func (d *draftPRHintState) reset() {
	d.fired = false
}

// checkDraftPRHintGate returns a non-empty advisory message when the agent
// committed work on an unpushed feature branch. Empty string otherwise.
func (a *Agent) checkDraftPRHintGate(runStats *RunStats) string {
	if a.draftPRHint == nil || a.draftPRHint.fired {
		return ""
	}
	a.draftPRHint.fired = true

	branch, ahead, eligible := a.draftPRHintEligibility(runStats)
	if !eligible || ahead == 0 {
		// ahead == 0 while eligible: everything committed is already pushed -
		// the PR (if any) exists.
		return ""
	}

	debug.Log("draftpr-hint", "injected draft-PR reminder: branch %s ahead %d", branch, ahead)
	return fmt.Sprintf(
		"[Post-completion reminder: You committed %d change(s) on branch %q that are not pushed yet. "+
			"If the task is complete, push the branch (git_push or `git push -u origin %s`) and open a draft PR "+
			"(`gh pr create --draft`) with a summary of what changed and why, so the work is reviewable and cannot be lost. "+
			"Do NOT merge it yourself unless explicitly asked.]",
		ahead, branch, branch)
}

// draftPRHintEligibility evaluates the non-git gating conditions and, when
// they pass, delegates to branchAheadOfUpstream for the git-side state.
func (a *Agent) draftPRHintEligibility(runStats *RunStats) (branch string, ahead int, ok bool) {
	// Only when the agent actually committed in this run.
	committed := false
	for toolName := range runStats.ToolCalls {
		if toolName == "git_commit" {
			committed = true
			break
		}
	}
	if !committed {
		return "", 0, false
	}

	workingDir := a.WorkingDir()
	if workingDir == "" {
		return "", 0, false
	}
	branch, ahead, ok = branchAheadOfUpstream(workingDir)
	return branch, ahead, ok
}

// branchAheadOfUpstreams returns (branch, aheadCount, ok). ok=false when
// not a repo, on the default branch, or git fails. aheadCount is the
// number of commits the branch is ahead of its upstream; a branch with no
// upstream counts ALL its commits ahead of the default branch (nothing is
// pushed anywhere).
func branchAheadOfUpstream(workingDir string) (string, int, bool) {
	branchOut, err := runGitCommandWithTimeout(
		gitCommand(workingDir, "rev-parse", "--abbrev-ref", "HEAD"),
		gitDiffTimeout,
	)
	if err != nil {
		return "", 0, false
	}
	branch := strings.TrimSpace(branchOut)
	if branch == "" || branch == "HEAD" || branch == "main" || branch == "master" {
		return "", 0, false
	}

	// Prefer the upstream view: "## branch...origin/branch [ahead 2]".
	statusOut, err := runGitCommandWithTimeout(
		gitCommand(workingDir, "status", "-sb"),
		gitDiffTimeout,
	)
	if err == nil {
		first := strings.SplitN(statusOut, "\n", 2)[0]
		if strings.Contains(first, "[ahead ") {
			n := 0
			if _, err := fmt.Sscanf(first[strings.Index(first, "[ahead ")+7:], "%d", &n); err == nil {
				return branch, n, true
			}
		}
		if strings.Contains(first, "...") {
			// Has an upstream and no ahead marker - fully pushed.
			return branch, 0, true
		}
		// No "..." - no upstream set for this branch.
	}

	// No upstream (or status unavailable): count commits ahead of the
	// default branch so a long-lived local branch still gets an honest n.
	for _, base := range []string{"main", "master"} {
		countOut, err := runGitCommandWithTimeout(
			gitCommand(workingDir, "rev-list", "--count", base+"..HEAD"),
			gitDiffTimeout,
		)
		if err == nil {
			n := 0
			if _, err := fmt.Sscanf(strings.TrimSpace(countOut), "%d", &n); err == nil && n > 0 {
				return branch, n, true
			}
			return branch, 0, true
		}
	}
	return branch, 0, true
}

package agent

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

type draftPRHintState struct {
	fired bool
}

func newDraftPRHintState() *draftPRHintState {
	return &draftPRHintState{}
}

func (d *draftPRHintState) reset() {
	d.fired = false
}

func (a *Agent) checkDraftPRHintGate(runStats *RunStats) string {
	if a.draftPRHint == nil || a.draftPRHint.fired {
		return ""
	}
	a.draftPRHint.fired = true

	branch, ahead, eligible := a.draftPRHintEligibility(runStats)
	if !eligible || ahead == 0 {

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

func (a *Agent) draftPRHintEligibility(runStats *RunStats) (branch string, ahead int, ok bool) {

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

			return branch, 0, true
		}

	}

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

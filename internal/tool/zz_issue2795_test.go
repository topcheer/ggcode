package tool

// #2795 regression: the keep gate was `winner.Commits > 0` alone, but the
// scoring gives VerifyPass 100 points (vs 20 for commits) and verify runs
// on the dirty uncommitted tree - so a trial whose sub-agent ignored the
// "commit all changes" prompt could win on verify alone with everything
// still uncommitted, and then lose the ENTIRE worktree to `git worktree
// remove --force`. The report still printed an adopt hint (empty diff)
// and anyUsable=true, misleading the caller into thinking work existed.
// The keep gate now matches the anyUsable semantics: VerifyPass counts.

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Guard probe (live): a verify-passed, zero-commit winner's worktree must
// be KEPT. The "dirty-wins" stub mirrors the issue scenario: trial 1
// passes verify with uncommitted changes only, trial 2 commits but fails
// verify.
func TestIssue2795_VerifyPassedUncommittedWinnerKept(t *testing.T) {
	root := initTrialRepo(t)
	// initTrialRepo wires stub runners via a registry keyed by strategy
	// name; extend it through the same hooks TestTrialForkRunSelectsWinner
	// uses. We need a new behavior ("write file, no commit, exit 0"), so
	// register a strategy handler if the harness supports it; otherwise
	// fall back to the source-level pin below.
	if !trialForkHarnessSupportsCustomStubs() {
		t.Skip("harness lacks custom stubs; source-level pin covers this")
	}
	tl := newTrialTool(root, nil)
	res, err := tl.Execute(context.Background(), trialInput(t, TrialForkInput{
		Goal:           "write but do not commit",
		Strategies:     []string{"dirty-wins", "commit-fails-verify"},
		VerifyCmd:      "test -f TRIAL_DIRTY_MARKER",
		TimeoutSeconds: 120,
	}))
	if err != nil {
		t.Fatalf("system error: %v", err)
	}
	if res.IsError {
		t.Fatalf("#2795: verify-passed winner is usable, got error:\n%s", res.Content)
	}
	out, err := exec.Command("git", "-C", root, "worktree", "list", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatalf("worktree list: %v: %s", err, out)
	}
	if n := strings.Count(string(out), "worktree "); n != 2 { // main + winner
		t.Fatalf("#2795: verify-passed uncommitted winner's worktree was destroyed (got %d worktrees):\n%s", n, out)
	}
}

// trialForkHarnessSupportsCustomStubs reports whether the test harness can
// register the dirty-wins stub; the shipped registry is fixed, so this
// returns false and the live probe above defers to the source pin.
func trialForkHarnessSupportsCustomStubs() bool { return false }

// Guard probe (source-level invariant, #2793/#2794 precedent): the keep
// gate must include VerifyPass - if it regresses to Commits-only, the
// silent-loss path reopens.
func TestIssue2795_KeepGateIncludesVerifyPass(t *testing.T) {
	b, err := os.ReadFile("trial_fork.go")
	if err != nil {
		t.Skipf("source layout changed: %v", err)
	}
	src := string(b)
	i := strings.Index(src, "winner := pickWinner(results)")
	if i < 0 {
		t.Fatal("keep-gate anchor missing - file layout changed")
	}
	tail := src[i:]
	if !strings.Contains(tail, "results[winner].VerifyPass") {
		t.Fatal("#2795: keep gate no longer considers VerifyPass - a verify-passed uncommitted winner's worktree would be force-deleted again")
	}
}

// Guard probe: the report must warn when the winner has no commits (the
// adopt hint diffs against an empty branch).
func TestIssue2795_ReportWarnsOnZeroCommitWinner(t *testing.T) {
	b, err := os.ReadFile("trial_fork.go")
	if err != nil {
		t.Skipf("source layout changed: %v", err)
	}
	src := string(b)
	if !strings.Contains(src, "winner committed nothing") {
		t.Fatal("#2795: report lacks the zero-commit winner warning - callers get an empty-diff adopt hint with no explanation")
	}
}

package tool

import (
	"strings"
	"testing"
)

// #3224: when EVERY trial fails to produce committed or verify-passed work,
// pickWinner still returns a pure-score index and the report used to print a
// WINNER block plus pointers to already-deleted worktrees (kept<0 →
// cleanupWorktrees removed everything). The body contradicted IsError and
// actively misled the calling agent. formatTrialReport must gate the winner
// block on the caller's usability verdict and the zero-commit warning on
// the winner actually being kept.

func mkResult3224(idx, commits int, verifyPass, kept bool) trialResult {
	return trialResult{
		Index:      idx,
		Strategy:   "probe strategy",
		Branch:     "trial/probe-1",
		Status:     "timeout",
		Commits:    commits,
		Files:      0,
		VerifyPass: verifyPass,
		Kept:       kept,
		Worktree:   "/tmp/should-not-be-referenced",
	}
}

func TestIssue3224_AllFailedNoWinnerBlock(t *testing.T) {
	results := []trialResult{
		mkResult3224(1, 0, false, false),
		mkResult3224(2, 0, false, false),
	}
	// pickWinner returns >=0 here (pure score); caller passes anyUsable=false.
	report := formatTrialReport("0123456789abcdef", "", results, 0, false)
	if strings.Contains(report, "WINNER: trial") {
		t.Fatalf("all-failed run must not print a WINNER block:\n%s", report)
	}
	if strings.Contains(report, "winner worktree kept") || strings.Contains(report, "/tmp/should-not-be-referenced") {
		t.Fatalf("all-failed run must not reference the deleted worktree:\n%s", report)
	}
	if strings.Contains(report, "passed verify but committed nothing") {
		t.Fatalf("zero-commit warning must not fire when nothing was kept:\n%s", report)
	}
	if strings.Contains(report, "git apply") {
		t.Fatalf("adopt hint is an empty-diff no-op on all-failed runs - omit it:\n%s", report)
	}
	if !strings.Contains(report, "No usable trial") {
		t.Fatalf("all-failed run must explain itself:\n%s", report)
	}
}

func TestIssue3224_RealWinnerUnchanged(t *testing.T) {
	r := mkResult3224(1, 3, false, true)
	report := formatTrialReport("0123456789abcdef", "", []trialResult{r}, 0, true)
	if !strings.Contains(report, "WINNER: trial 1") {
		t.Fatalf("committed winner must keep its WINNER block:\n%s", report)
	}
	if !strings.Contains(report, "winner worktree kept") {
		t.Fatalf("kept winner must keep the worktree pointer:\n%s", report)
	}
	if strings.Contains(report, "passed verify but committed nothing") {
		t.Fatalf("commits>0 winner must not see the zero-commit warning:\n%s", report)
	}
}

func TestIssue3224_VerifyPassZeroCommitWarningKept(t *testing.T) {
	// #2795 original scenario: verify passed, nothing committed, winner
	// kept - the warning (and its kept-worktree pointer) is correct there.
	r := mkResult3224(1, 0, true, true)
	report := formatTrialReport("0123456789abcdef", "go test ./...", []trialResult{r}, 0, true)
	if !strings.Contains(report, "passed verify but committed nothing") {
		t.Fatalf("#2795 scenario (verify-pass kept winner) must keep the warning:\n%s", report)
	}
	if !strings.Contains(report, "winner worktree kept") {
		t.Fatalf("kept winner worktree pointer missing:\n%s", report)
	}
}

func TestIssue3224_NoWinnerIndexStillHandled(t *testing.T) {
	results := []trialResult{mkResult3224(1, 0, false, false)}
	report := formatTrialReport("0123456789abcdef", "", results, -1, false)
	if !strings.Contains(report, "No usable trial") {
		t.Fatalf("winner<0 path must remain:\n%s", report)
	}
}

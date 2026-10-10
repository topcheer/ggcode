package agent

import "testing"

// #3782 probes.

// A: cross-field aggregation - a mutating git command in ANY field must
// disqualify, independent of map iteration order.
func Test3782CrossFieldMutatingAlwaysWins(t *testing.T) {
	args := `{"field_a":"git log --oneline","field_b":"git reset --hard"}`
	if isReadOnlyGitInvocation("git_status", args) {
		t.Fatal("mutating field_b swallowed (order-dependent leak still present)")
	}
	// Reversed field names exercise the other iteration order.
	argsRev := `{"field_z":"git reset --hard","field_a":"git log --oneline"}`
	if isReadOnlyGitInvocation("git_status", argsRev) {
		t.Fatal("mutating first-field case swallowed")
	}
	// All read-only fields stay read-only.
	argsRO := `{"field_a":"git log --oneline","field_b":"git diff --stat"}`
	if !isReadOnlyGitInvocation("git_status", argsRO) {
		t.Fatal("all-read-only invocation misclassified as mutating")
	}
}

// B: global flags before the subcommand must not flip the verdict to
// mutating (and must not burn the warning budget).
func Test3782GlobalFlagsBeforeSubcommand(t *testing.T) {
	for _, line := range []string{
		"git -C /other/repo status",
		"git --no-pager diff",
		"git -c core.autocrlf=false log --oneline",
		"git --git-dir=/x/.git --work-tree=/x status",
	} {
		if ro, found := classifyGitCommandLine(line); !found || !ro {
			t.Errorf("%q must classify read-only, got ro=%v found=%v", line, ro, found)
		}
	}
	// Mutating behind global flags must STILL be caught.
	if ro, _ := classifyGitCommandLine("git -C /other/repo reset --hard"); ro {
		t.Error("git -C ... reset --hard must classify mutating")
	}
}

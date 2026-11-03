package agent

import "testing"

func Test3782CrossFieldMutatingAlwaysWins(t *testing.T) {
	args := `{"field_a":"git log --oneline","field_b":"git reset --hard"}`
	if isReadOnlyGitInvocation("git_status", args) {
		t.Fatal("mutating field_b swallowed (order-dependent leak still present)")
	}

	argsRev := `{"field_z":"git reset --hard","field_a":"git log --oneline"}`
	if isReadOnlyGitInvocation("git_status", argsRev) {
		t.Fatal("mutating first-field case swallowed")
	}

	argsRO := `{"field_a":"git log --oneline","field_b":"git diff --stat"}`
	if !isReadOnlyGitInvocation("git_status", argsRO) {
		t.Fatal("all-read-only invocation misclassified as mutating")
	}
}

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

	if ro, _ := classifyGitCommandLine("git -C /other/repo reset --hard"); ro {
		t.Error("git -C ... reset --hard must classify mutating")
	}
}

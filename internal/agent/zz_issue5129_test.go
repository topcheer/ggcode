package agent

import "testing"

// #5129: irreversibility-gate two faces - first-turn false trigger and
// read-only table gaps that gated pure patrol commands.

// TestIssue5129ReadOnlyCompoundPatrol pins the acceptance case: a compound
// read-only patrol chain (echo header + git ls-remote + git reflog) must
// classify irrevTierNone instead of gating as medium.
func TestIssue5129ReadOnlyCompoundPatrol(t *testing.T) {
	cmds := []string{
		`echo "-- header --" && git ls-remote --heads origin 'fix*' && git reflog -3`,
		`git ls-remote --heads origin`,
		`git name-rev HEAD`,
		`git for-each-ref refs/tags`,
		`git check-attr binary -- pom.xml`,
		`echo deploy start`,
		`printf "%s\n" done`,
	}
	for _, c := range cmds {
		if got := irrevClassifyTool("run_command", `{"command":`+quoteJSON(c)+`}`); got != irrevTierNone {
			t.Errorf("irrevClassifyTool(run_command, %q) = %d, want irrevTierNone", c, got)
		}
	}
}

// TestIssue5129EchoRedirectStillMedium pins conservatism: echo with a
// redirect is NOT read-only (the segment check rejects '>' after
// neutralization only covers /dev/null style sinks).
func TestIssue5129EchoRedirectStillMedium(t *testing.T) {
	c := `echo done >> /tmp/finish.log`
	if got := irrevClassifyTool("run_command", `{"command":`+quoteJSON(c)+`}`); got != irrevTierMedium {
		t.Errorf("redirected echo = %d, want irrevTierMedium", got)
	}
}

// TestIssue5129FirstTurnMediumExempt pins Face 1: the session's FIRST medium
// action must not fire the advisory on an empty grounding history, while a
// second ungrounded medium action still gates.
func TestIssue5129FirstTurnMediumExempt(t *testing.T) {
	s := newIrrevGateState()
	// git_commit is medium and NOT a grounding action.
	if warn := s.recordAction("git_commit", `{}`); warn != "" {
		t.Errorf("first-turn medium must be exempt, got advisory: %q", warn)
	}
	if s.warnings != 0 {
		t.Errorf("warnings = %d after exempt first turn, want 0", s.warnings)
	}
	// Second medium with still-zero grounding must warn.
	if warn := s.recordAction("git_commit", `{}`); warn == "" {
		t.Error("second ungrounded medium action must still trigger the advisory")
	}
}

// TestIssue5129FirstTurnHighStillGates pins that the first-action exemption
// applies to medium only: a HIGH-impact action gates even on turn 1.
func TestIssue5129FirstTurnHighStillGates(t *testing.T) {
	s := newIrrevGateState()
	if warn := s.recordAction("git_push", `{}`); warn == "" {
		t.Error("first-turn HIGH-impact action must still trigger the advisory")
	}
}

// quoteJSON is provided by zz_issue3089_test.go in this package.

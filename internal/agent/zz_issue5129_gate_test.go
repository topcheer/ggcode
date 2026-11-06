package agent

import "testing"

// #5129 (S-70/S-71): two faces of the irreversibility gate.
//
// S-71: read-only patrol commands (git ls-remote patrols, echo headers,
// compound probes) were misclassified as medium because the read-only
// tables lacked ls-remote/echo/printf - gating the very grounding the
// gate demands (self-defeating loop).
//
// S-70: a session's FIRST medium-tier command always fired the gate
// because the grounding window starts empty. The first action is now
// exempt from the MEDIUM threshold (HIGH still gates).

// Acceptance 1: git probe-class subcommands classify read-only.
func TestIssue5129ProbeClassGitSubcommands(t *testing.T) {
	for _, sub := range []string{
		"git ls-remote origin main",
		`git ls-remote https://github.com/topcheer/ggcode.git`,
		"git name-rev HEAD",
		"git for-each-ref refs/heads",
		"git check-attr text README.md",
		"git show-ref --heads",
		"git show-ref --verify refs/heads/main",
		"git var GIT_AUTHOR_IDENT",
	} {
		if !irrevIsReadOnlyCommand(sub) {
			t.Errorf("probe command misclassified as not-read-only: %q", sub)
		}
		if got := irrevClassifyTool("run_command", sub); got != irrevTierNone {
			t.Errorf("irrevClassifyTool(%q) = %v, want irrevTierNone", sub, got)
		}
	}
}

// Acceptance 2: echo/printf (no redirect) are read-only; a redirect still
// disqualifies the segment.
func TestIssue5129EchoPrintfReadOnly(t *testing.T) {
	for _, cmd := range []string{
		"echo hello",
		`printf '%s\n' done`,
		`echo "── header ──"`,
	} {
		if !irrevIsReadOnlyCommand(cmd) {
			t.Errorf("side-effect-free command misclassified: %q", cmd)
		}
	}
	if irrevIsReadOnlyCommand("echo hi > /tmp/x") {
		t.Error("echo with file redirect must NOT be read-only")
	}
	if irrevIsReadOnlyCommand("printf p >> /tmp/x") {
		t.Error("printf with append redirect must NOT be read-only")
	}
}

// Acceptance 3+4: compound read-only patrol (echo + ls-remote + reflog +
// merge-base) is tierNone, so it records grounding instead of gating.
func TestIssue5129CompoundReadonlyPatrol(t *testing.T) {
	compound := `cd /Volumes/new/ggai/ggcode && echo "── patrol ──" && git ls-remote --tags origin | head -5; git reflog -5 | tail -3; git merge-base --is-ancestor 2237ccedc HEAD && echo ok`
	if got := irrevClassifyTool("run_command", compound); got != irrevTierNone {
		t.Errorf("compound read-only patrol tier = %v, want irrevTierNone", got)
	}

	// Same patrol as the session's FIRST action must not gate (S-70), and
	// must count as grounding so later medium commands pass cleanly.
	s := newIrrevGateState()
	if warn := s.recordAction("run_command", compound); warn != "" {
		t.Errorf("first-action read-only patrol gated: %q", warn)
	}

	// First-action MEDIUM command (grounding window empty) must not gate.
	// #1798 guard: run_command is deliberately NOT first-action-exempt
	// (self-grounding hole), so the medium exemption is pinned via a
	// non-shell medium tool instead.
	s2 := newIrrevGateState()
	if warn := s2.recordAction("git_commit", `{"message":"x"}`); warn != "" {
		t.Errorf("first-action medium tool gated (S-70): %q", warn)
	}
	// #1798 pinned: first run_command (medium) still gates.
	s2b := newIrrevGateState()
	if warn := s2b.recordAction("run_command", `{"command":"make build"}`); warn == "" {
		t.Error("first-action medium run_command must still gate (#1798 self-grounding guard)")
	}

	// First-action HIGH-IMPACT command still gates - the exemption is
	// medium-only by design.
	s3 := newIrrevGateState()
	if warn := s3.recordAction("run_command", "git push --force origin main"); warn == "" {
		t.Error("first-action HIGH-IMPACT command must still gate")
	}
}

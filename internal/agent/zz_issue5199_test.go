package agent

import "testing"

// #5199: patrol-first-batch misfire. `gh api` (GET-by-default) was absent
// from the read-only table, so every round's first batched call
// (annotations / merge-state probes) classified Medium and tripped the
// zero-grounding flag - the gate demanding grounding gated the very reads
// that produce it (#5129's own words). Pins: gh api GET-form is read-only;
// mutating verbs/field flags stay gated; git worktree list is read-only
// while worktree add stays gated; #5129 regressions intact.

func TestIssue5199_GhApiGetFormIsReadOnly(t *testing.T) {
	ro := []string{
		`{"command":"gh api repos/topcheer/ggcode/check-runs/123/annotations --jq '.[].path'"}`,
		`{"command":"gh api repos/topcheer/ggcode/pulls/5181/merge --jq .merged"}`,
		`{"command":"gh api repos/x/y -X GET --jq .state"}`,
		`{"command":"gh api repos/x/y --method GET"}`,
	}
	for _, args := range ro {
		if got := irrevClassifyTool("run_command", args); got != irrevTierNone {
			t.Errorf("read-only gh api misclassified: %s -> tier %d", args, got)
		}
	}
}

func TestIssue5199_GhApiMutatingFormsStayGated(t *testing.T) {
	medium := []string{
		`{"command":"gh api repos/x/y/pulls/1/merge -X PUT -f merge_method=merge"}`,
		`{"command":"gh api -X POST repos/x/y/releases --input body.json"}`,
		`{"command":"gh api --method DELETE repos/x/y/labels/bug"}`,
		`{"command":"gh api repos/x/y -F base=main"}`,
	}
	for _, args := range medium {
		if got := irrevClassifyTool("run_command", args); got == irrevTierNone {
			t.Errorf("mutating gh api must stay gated: %s", args)
		}
	}
}

func TestIssue5199_GitWorktreeListReadOnly(t *testing.T) {
	if got := irrevClassifyTool("run_command", `{"command":"git worktree list"}`); got != irrevTierNone {
		t.Errorf("git worktree list -> tier %d, want None", got)
	}
	if got := irrevClassifyTool("run_command", `{"command":"git worktree add /tmp/x main"}`); got == irrevTierNone {
		t.Error("git worktree add is stateful and must stay gated")
	}
	if got := irrevClassifyTool("run_command", `{"command":"git worktree remove /tmp/x"}`); got == irrevTierNone {
		t.Error("git worktree remove is stateful and must stay gated")
	}
}

func TestIssue5199_FirstBatchPatrolNoFlag(t *testing.T) {
	// The structural reproduction: fresh gate state (round boundary), first
	// action is the batched patrol command. Read-only classification means
	// zero warnings and zero interceptions; identical retry passes.
	s := newIrrevGateState()
	for _, args := range []string{
		`{"command":"gh pr list --repo topcheer/ggcode --state open"}`,
		`{"command":"gh api repos/topcheer/ggcode/issues/5199/comments --jq '.[].body'"}`,
		`{"command":"git show origin/main --stat"}`,
		`{"command":"git worktree list"}`,
	} {
		if w := s.recordAction("run_command", args); w != "" {
			t.Fatalf("patrol first-batch must not flag: %s -> %s", args, w)
		}
	}

	// Regression: a genuinely mutating first command after the reads still
	// gates via the normal Medium path (grounding now present; force the
	// zero-grounding case with a fresh state).
	s2 := newIrrevGateState()
	if w := s2.recordAction("run_command", `{"command":"gh api repos/x/y -X PUT -f a=b"}`); w == "" {
		t.Fatal("mutating gh api on zero grounding must still flag")
	}
}

package agent

import (
	"encoding/json"
	"testing"
)

func ghArgs(t *testing.T, cmd string) string {
	t.Helper()
	b, err := json.Marshal(map[string]string{"command": cmd})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// #4794 (S-24): read-only gh issue/pr/run/release triage commands must
// classify as read-only so the irreversibility advisory stops interrupting
// pure-read patrol rounds.
func TestIssue4794_GhReadOnlyTriageCommands(t *testing.T) {
	for _, cmd := range []string{
		"gh issue list --repo topcheer/ggcode --state open --limit 100",
		"gh issue view 4794 --repo topcheer/ggcode --json body",
		"gh pr list --state open --limit 100 --json number",
		"gh pr view 4870 --json body",
		"gh pr diff 4870",
		"gh pr checks 4870",
		"gh run list --branch main --limit 2",
		"gh run view 41288527298 --log-failed",
		"gh release list --limit 5",
		"gh search issues 'timeout' --limit 5",
	} {
		if !irrevIsReadOnlyCommand(ghArgs(t, cmd)) {
			t.Errorf("expected read-only: %q", cmd)
		}
	}
}

// Mutating gh commands must stay OUT of the whitelist: merge/close/create/
// comment/delete/reopen mutate repository state, and `gh api` is
// method-ambiguous (-X POST) even though its default is GET.
func TestIssue4794_GhWriteCommandsStayGated(t *testing.T) {
	for _, cmd := range []string{
		"gh pr merge 4870 --merge --admin",
		"gh issue close 4794 --reason completed",
		"gh issue comment 4794 --body 'x'",
		"gh issue create --title x",
		"gh api repos/topcheer/ggcode/pages/builds",
		"gh api -X POST repos/topcheer/ggcode/pages/builds",
		"gh release create v1.2.3",
		"gh run rerun 41288527298",
		"gh issue 4794", // bare resource subcommand prompts interactively
		"gh repo delete topcheer/ggcode",
	} {
		if irrevIsReadOnlyCommand(ghArgs(t, cmd)) {
			t.Errorf("expected gated (not read-only): %q", cmd)
		}
	}
}

// Compound commands: read-only gh steps chained with && stay read-only;
// any write step disqualifies the whole command (per-segment rule).
func TestIssue4794_GhChainedSegments(t *testing.T) {
	if !irrevIsReadOnlyCommand(ghArgs(t, "gh issue list --json number && gh pr list --json number")) {
		t.Error("chain of two read-only gh steps should be read-only")
	}
	if irrevIsReadOnlyCommand(ghArgs(t, "gh issue list && gh issue close 4794")) {
		t.Error("a write step in the chain must disqualify the whole command")
	}
}

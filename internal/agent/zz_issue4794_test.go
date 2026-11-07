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

func TestIssue4794_GhWriteCommandsStayGated(t *testing.T) {
	for _, cmd := range []string{
		"gh pr merge 4870 --merge --admin",
		"gh issue close 4794 --reason completed",
		"gh issue comment 4794 --body 'x'",
		"gh issue create --title x",
		"gh api -X POST repos/topcheer/ggcode/pages/builds",
		// gh api without -X is a GET by gh's own default (#5199): verb-less
		// api calls are now read-only, so the verb-explicit write forms
		// carry this pin's gate contract instead.
		"gh release create v1.2.3",
		"gh run rerun 41288527298",
		"gh issue 4794",
		"gh repo delete topcheer/ggcode",
	} {
		if irrevIsReadOnlyCommand(ghArgs(t, cmd)) {
			t.Errorf("expected gated (not read-only): %q", cmd)
		}
	}
}

func TestIssue4794_GhChainedSegments(t *testing.T) {
	if !irrevIsReadOnlyCommand(ghArgs(t, "gh issue list --json number && gh pr list --json number")) {
		t.Error("chain of two read-only gh steps should be read-only")
	}
	if irrevIsReadOnlyCommand(ghArgs(t, "gh issue list && gh issue close 4794")) {
		t.Error("a write step in the chain must disqualify the whole command")
	}
}

package agentruntime

import (
	"os"
	"strings"
	"testing"
)

// TestIssue5401SelectorLessDryRun pins the describe/dry-run face for the
// selector-less merge-gate forms introduced with #5401 ("gh pr merge",
// "gh pr merge feature-branch"): the output must flag the missing PR
// selector AND the preflight guidance must not instruct the agent to run
// merge_preflight on PR number 0, which the tool would reject - it must
// ask to identify the PR first.
func TestIssue5401SelectorLessDryRun(t *testing.T) {
	access := newGatesTestAccess(t)
	adapter := gatesAccessAdapter{inner: access}

	for _, cmd := range []string{
		"gh pr merge",
		"gh pr merge feature-branch",
	} {
		out, err := adapter.Test(cmd)
		if err != nil {
			t.Fatalf("test %q: %v", cmd, err)
		}
		for _, want := range []string{
			"protected: yes",
			"pr selector: none in command",
			"dry-run only",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("dry-run for %q missing %q:\n%s", cmd, want, out)
			}
		}
		if strings.Contains(out, `"pr_number":0`) {
			t.Errorf("dry-run for %q must not direct preflight at PR 0:\n%s", cmd, out)
		}
		if !strings.Contains(out, "identify the PR") {
			t.Errorf("dry-run for %q must ask to identify the PR first:\n%s", cmd, out)
		}
	}
}

// TestIssue5401GateDocMatchesBehavior guards the DefaultMergeGateCommands
// doc comment (QA #5401 nit 1): it must describe the selector-aware
// grammar the matcher actually implements (flags/URL/branch/no-selector
// forms match), not the stale "next token is the PR number" wording that
// described the pre-#5401 bug.
func TestIssue5401GateDocMatchesBehavior(t *testing.T) {
	src, err := os.ReadFile("../../internal/config/gates.go")
	if err != nil {
		t.Fatalf("read gates.go: %v", err)
	}
	for _, stale := range []string{
		"next token is the PR number",
	} {
		if strings.Contains(string(src), stale) {
			t.Errorf("gates.go still carries stale grammar wording %q - describe the selector-aware matcher instead", stale)
		}
	}
	for _, want := range []string{"gh pr merge [<number>", "| <url>", "| <branch>]"} {
		if !strings.Contains(string(src), want) {
			t.Errorf("gates.go should document the gh grammar (missing %q)", want)
		}
	}
}

package agent

// #3864 probes: start_command reaches the invalidation gate, branch
// creation and soft/mixed resets do not burn the warning budget, and the
// extra global flags are skipped.

import (
	"os"
	"strings"
	"testing"
)

func TestIssue3864_ClassifyExemptions(t *testing.T) {
	cases := []struct {
		cmd   string
		ro    bool
		found bool
	}{
		{"git checkout -b fix-x", true, true},           // #3864 B
		{"git switch -c fix-x", true, true},             // #3864 B
		{"git checkout main", false, true},              // target checkout mutates
		{"git reset --soft HEAD~1", true, true},         // #3864 C
		{"git reset --hard HEAD~1", false, true},        // hard rewrites tree
		{"git reset -- path/to/file", false, true},      // pathspec form mutates
		{"git --no-replace-objects status", true, true}, // #3864 D
		{"git --bare log", true, true},                  // #3864 D
	}
	for _, c := range cases {
		ro, found := classifyGitCommandLine(c.cmd)
		if found != c.found || ro != c.ro {
			t.Fatalf("%q: readOnly=%v found=%v, want %v/%v", c.cmd, ro, found, c.ro, c.found)
		}
	}
}

func TestIssue3864_StructuredToolExemptions(t *testing.T) {
	if !isReadOnlyGitInvocation("git_checkout", `{"branch":"fix-x","create":true}`) {
		t.Fatal("git_checkout create=true is a pure ref op - must be read-only")
	}
	if isReadOnlyGitInvocation("git_checkout", `{"branch":"main"}`) {
		t.Fatal("git_checkout switching content must stay mutating")
	}
	if !isReadOnlyGitInvocation("git_reset", `{"mode":"soft"}`) {
		t.Fatal("git_reset mode=soft must be read-only")
	}
	if isReadOnlyGitInvocation("git_reset", `{"mode":"hard"}`) {
		t.Fatal("git_reset mode=hard must stay mutating")
	}
}

func TestIssue3864_StartCommandGateWired(t *testing.T) {
	raw, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	anchor := strings.Index(src, "runCommandMutatesTree(string(tc.Arguments))")
	if anchor < 0 {
		t.Fatal("gate anchor not found")
	}
	window := src[anchor-400 : anchor+100]
	if !strings.Contains(window, `tc.Name == "start_command"`) {
		t.Fatal("start_command must reach the runCommandMutatesTree gate (#3864 A)")
	}
}

package agent

import (
	"strings"
	"testing"
)

func TestIssue3864_ClassifyExemptions(t *testing.T) {
	cases := []struct {
		cmd   string
		ro    bool
		found bool
	}{
		{"git checkout -b fix-x", true, true},
		{"git switch -c fix-x", true, true},
		{"git checkout main", false, true},
		{"git reset --soft HEAD~1", true, true},
		{"git reset --hard HEAD~1", false, true},
		{"git reset -- path/to/file", false, true},
		{"git --no-replace-objects status", true, true},
		{"git --bare log", true, true},
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
	// #5543: the command-channel fallback moved out of agent.go; scan
	// the stream-loop source files (shape-agnostic file targeting).
	src, err := readStreamLoopSource("agent.go", "run_tool_call_idx.go")
	if err != nil {
		t.Fatal(err)
	}
	anchor := strings.Index(src, "runCommandMutatesTree(string(tc.Arguments))")
	if anchor < 0 {
		t.Fatal("gate anchor not found")
	}
	window := src[anchor-400 : anchor+100]
	if !strings.Contains(window, `tc.Name == "start_command"`) {
		t.Fatal("start_command must reach the runCommandMutatesTree gate (#3864 A)")
	}
}

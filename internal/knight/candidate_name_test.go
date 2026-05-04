package knight

import "testing"

func TestSanitizeCandidateName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"  Build-Then-Verify ", "build-then-verify"},
		{"LLM--Tool", "llm-tool"},
		{"-lsp-tool-", "lsp-tool"},
		{"tool / status", "tool-status"},
		{"foo___bar", "foo-bar"},
		{"  ", ""},
		{"with空格chars!", "with-chars"},
	}
	for _, c := range cases {
		got := sanitizeCandidateName(c.in)
		if got != c.want {
			t.Errorf("sanitizeCandidateName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCandidateNameAcceptable(t *testing.T) {
	good := []string{"build-then-verify", "lsp-tools-first", "read-code-first", "no-push-without-verify", "llm-200", "tool-status"}
	bad := []string{"", "tool", "build-convention", "lsp-tool", "llm-tool", "fix-x", "abc", "x-y"}
	for _, n := range good {
		if !candidateNameAcceptable(n) {
			t.Errorf("expected acceptable: %q", n)
		}
	}
	for _, n := range bad {
		if candidateNameAcceptable(n) {
			t.Errorf("expected REJECTED: %q", n)
		}
	}
}

func TestMergeSkillCandidatesDropsBadNames(t *testing.T) {
	in := []SkillCandidate{
		{Name: "Build-Then-Verify", Scope: "project", Description: "good"},
		{Name: "tool", Scope: "project", Description: "too generic"},
		{Name: "llm--tool", Scope: "project", Description: "malformed"},
		{Name: "-lsp-tool-", Scope: "project", Description: "malformed"},
		{Name: "read-code-first", Scope: "project", Description: "good"},
	}
	out := mergeSkillCandidates(nil, in)
	got := map[string]bool{}
	for _, c := range out {
		got[c.Name] = true
	}
	if !got["build-then-verify"] || !got["read-code-first"] {
		t.Fatalf("expected acceptable names retained: %#v", got)
	}
	for _, bad := range []string{"tool", "llm-tool", "lsp-tool", "llm--tool", "-lsp-tool-"} {
		if got[bad] {
			t.Errorf("expected %q to be dropped", bad)
		}
	}
}

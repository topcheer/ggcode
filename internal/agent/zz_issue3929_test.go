package agent

import "testing"

func TestIssue3929_StartPointFormIsMutating(t *testing.T) {
	cases := []struct {
		cmd string
		ro  bool
	}{
		{"git checkout -b fix origin/main", false},
		{"git checkout -B fix v1.2.3", false},
		{"git switch -c new HEAD~3", false},
		{"git checkout -b fix", true},
		{"git switch -c fix", true},
		{"git checkout -b fix --track origin/main", false},
	}
	for _, c := range cases {
		ro, found := classifyGitCommandLine(c.cmd)
		if !found {
			t.Fatalf("%q: not classified as git", c.cmd)
		}
		if ro != c.ro {
			t.Fatalf("%q: readOnly=%v, want %v", c.cmd, ro, c.ro)
		}
	}
}

func TestIssue3929_StructuredCheckoutStartPointMutating(t *testing.T) {
	if !isReadOnlyGitInvocation("git_checkout", `{"branch":"fix","create":true}`) {
		t.Fatal("create from HEAD stays read-only (#3864 B)")
	}
	if isReadOnlyGitInvocation("git_checkout", `{"branch":"fix","create":true,"start_point":"origin/main"}`) {
		t.Fatal("create from start_point rewrites the tree - must be mutating (#3929)")
	}
}

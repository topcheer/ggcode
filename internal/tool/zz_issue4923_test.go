package tool

import "testing"

// #4923 Bug A: the GIT_PAGER clean rule must only rewrite LEADING
// env-assignment positions - CleanedCmd is the command that executes.
func TestIssue4923_GITPAGERCleanPositionLocked(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"leading override cleaned", `GIT_PAGER=cat git log --oneline`, `git log --oneline`},
		{"leading with other env vars", `FOO=1 GIT_PAGER=cat git log`, `FOO=1 git log`},
		{"quoted leading value", `GIT_PAGER="less -R" git log`, `git log`},
		{"after env(1)", `env GIT_PAGER=cat git log`, `git log`},
		// Mid-argument positions are ARGUMENT TEXT - never rewritten:
		{"-c value untouched", `git -c GIT_PAGER=cat log --oneline`, `git -c GIT_PAGER=cat log --oneline`},
		{"grep pattern untouched", `grep GIT_PAGER=x Makefile`, `grep GIT_PAGER=x Makefile`},
		{"quoted echo text untouched", `echo "GIT_PAGER=cat is set"`, `echo "GIT_PAGER=cat is set"`},
	}
	for _, tc := range cases {
		g := NewCommandGate()
		res := g.Check(tc.in)
		if got := res.CleanedCmd; got != tc.want {
			t.Errorf("%s: CleanedCmd = %q, want %q", tc.name, got, tc.want)
		}
	}
}

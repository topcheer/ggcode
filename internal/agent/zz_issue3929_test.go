package agent

// #3929 probes: the checkout/switch branch-create exemption must hold ONLY
// for the no-start-point form - `checkout -b <new> <start-point>` replaces
// tracked content when start-point != HEAD.

import "testing"

func TestIssue3929_StartPointFormIsMutating(t *testing.T) {
	cases := []struct {
		cmd string
		ro  bool
	}{
		{"git checkout -b fix origin/main", false},         // catch-up flow: tree rewritten
		{"git checkout -B fix v1.2.3", false},              // tag start-point
		{"git switch -c new HEAD~3", false},                // detached start-point
		{"git checkout -b fix", true},                      // HEAD-anchored: unchanged (#3864 B)
		{"git switch -c fix", true},                        // unchanged
		{"git checkout -b fix --track origin/main", false}, // --track carries a start-point (positional)
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

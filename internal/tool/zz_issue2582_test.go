package tool

import (
	"strings"
	"testing"
)

func TestIssue2582_SemicolonPathMatchesSingleCommandVerdicts(t *testing.T) {
	g := NewCommandGate()

	cases := []struct {
		name string
		cmd  string
		want GateBehavior
	}{

		{"quoted rm phrase", `grep 'rm -rf /etc/hosts' README.md`, Allow},
		{"quoted rm phrase in compound", `echo hi; grep 'rm -rf /etc/hosts' README.md`, Allow},

		{"benign tmp path", "rm -rf /var/folders/ab/c/T/cache", Ask},
		{"benign tmp path in compound", "echo hi; rm -rf /var/folders/ab/c/T/cache", Ask},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := g.Check(tc.cmd).Behavior
			if got != tc.want {
				t.Fatalf("Check(%q) = %v, want %v (compound path diverges from single-command verdict)", tc.cmd, got, tc.want)
			}
		})
	}
}

func TestIssue2582_CompoundStillBlocksRealThreats(t *testing.T) {
	g := NewCommandGate()
	res := g.Check("echo hi; rm -rf /etc")
	if res.Behavior != Block {
		t.Fatalf("genuinely destructive compound = %v, want Block", res.Behavior)
	}
	if !strings.Contains(res.Reason, "compound") {
		t.Fatalf("reason = %q, want compound marker", res.Reason)
	}

	if res := g.Check("echo go; killall Little Snitch"); res.Behavior != Block {
		t.Fatalf("unquoted killall compound = %v, want Block", res.Behavior)
	}
}

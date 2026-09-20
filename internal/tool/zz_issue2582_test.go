package tool

// #2582 regression: the compound-command (semicolon) pre-check must apply
// the same transformations as the main-flow Layer 1 - quotedInert rules
// match the blankQuotedAndHeredocs view (#814) and benignPrefixes are
// rewritten (#813). Before the fix, a quoted destructive phrase or a
// benign temp-dir path was HARD-Blocked on the semicolon path while the
// identical single command got Allow/Ask.

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
		// #814 alignment: quoted destructive phrase is inert on the main
		// path (Ask via fallback, not Block); the compound part must not
		// Block either.
		{"quoted rm phrase", `grep 'rm -rf /etc/hosts' README.md`, Ask},
		{"quoted rm phrase in compound", `echo hi; grep 'rm -rf /etc/hosts' README.md`, Ask},
		// #813 alignment: benign temp-dir prefix is Ask on the main path.
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

// The compound branch must still Block genuinely destructive parts - the
// alignment fixes false positives, not the guard itself.
func TestIssue2582_CompoundStillBlocksRealThreats(t *testing.T) {
	g := NewCommandGate()
	res := g.Check("echo hi; rm -rf /etc")
	if res.Behavior != Block {
		t.Fatalf("genuinely destructive compound = %v, want Block", res.Behavior)
	}
	if !strings.Contains(res.Reason, "compound") {
		t.Fatalf("reason = %q, want compound marker", res.Reason)
	}
	// Unquoted killall must stay blocked in compounds too (the quoted
	// form is the issue's Allow matrix row - quote-inert is correct).
	if res := g.Check("echo go; killall Little Snitch"); res.Behavior != Block {
		t.Fatalf("unquoted killall compound = %v, want Block", res.Behavior)
	}
}

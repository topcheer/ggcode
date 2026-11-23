package agent

import (
	"strings"
	"testing"
)

func TestIssue2255GlobalFlagVariantsReachRegexLayer(t *testing.T) {
	cases := map[string]string{
		"git -C /repo reset --hard":              "reset_hard",
		"git --git-dir=/repo/.git2 reset --hard": "reset_hard",
		"git -C /repo branch -D feature":         "branch_force",
		"git -C /repo stash drop":                "stash",
		"git -C /repo filter-branch --all":       "filter",
		"git -C /repo checkout -f main":          "discard",
	}
	for cmd, want := range cases {
		pats := detectDestructiveInShellCommand(cmd)
		if len(pats) == 0 {
			t.Errorf("%q: no patterns detected (was silent before fix)", cmd)
			continue
		}
		if !strings.Contains(pats[0].name, want) {
			t.Errorf("%q: first pattern %q, want substring %q", cmd, pats[0].name, want)
		}
	}
}

func TestIssue2255ForcePushSeesThroughC(t *testing.T) {
	if !isForcePushCommand("git -C /repo push --force origin main") {
		t.Error("force push behind -C must be detected")
	}
	if !isForcePushCommand("git -C /repo push origin +main") {
		t.Error("+refspec force push behind -C must be detected")
	}
}

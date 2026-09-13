package agent

import (
	"strings"
	"testing"
)

// #2255: three detector-layer business fixes, pinned against the exact shapes
// the review probes demonstrated.
//
// H1 - git -C / --git-dir global-flag variants must reach BOTH detection
// layers (six op classes were silent before).
// M1 - flag scans are scoped to the git command's own segment; a second
// command's `--`/`-f` after && cannot fire the reversibility gate, and the
// phantom warnings no longer eat the warnCount budget ahead of real ones.
// M2 - safety signals are owner-anchored: a commit MESSAGE token ("build:")
// cannot forge buildRan.

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

func TestIssue2255ReversibilityLayerSeesThroughC(t *testing.T) {
	if !isDestructiveGit("git -C /repo reset --hard") {
		t.Error("reversibility layer: reset --hard behind -C must fire")
	}
	if !isDestructiveGit("git --git-dir=/repo/x reset --hard") {
		t.Error("reversibility layer: --git-dir variant must fire")
	}
}

func TestIssue2255CrossSegmentFlagNoFalsePositive(t *testing.T) {
	if isDestructiveGit("git checkout main && git log -- file") {
		t.Error("second command's -- after && must not fire the checkout branch")
	}
	if isDestructiveGit("git clean -n && rm -f /tmp/x") {
		t.Error("rm -f after && must not fire the clean branch (clean -n is a dry run)")
	}
	if !isDestructiveGit("git reset --hard") {
		t.Error("bare destructive form must still fire")
	}
}

func TestIssue2255CommitMessageCannotForgeSafetySignal(t *testing.T) {
	r := &reversibilityState{}
	r.recordSafetySignal("run_command", `git commit -m "build: bump version"`)
	if r.buildRan || r.testsRan {
		t.Error("commit-message token forged a safety signal")
	}
	r2 := &reversibilityState{}
	r2.recordSafetySignal("run_command", "go test ./internal/agent/")
	if !r2.testsRan {
		t.Error("real go test must set testsRan")
	}
	r3 := &reversibilityState{}
	r3.recordSafetySignal("run_command", "make build")
	if !r3.buildRan {
		t.Error("make build must set buildRan")
	}
}

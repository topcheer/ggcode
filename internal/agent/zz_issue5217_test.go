package agent

import (
	"strings"
	"testing"
)

// #5217: a `gh issue comment --body '...git branch -D...'` (pure API call,
// zero git operations) triggered a CRITICAL branch_force_delete because
// the detector matched the literal text anywhere in the command string.
// Both layers (detector + #5213 probe) are now argv-grounded: patterns
// only fire inside shell segments that actually invoke git.

func TestIssue5217_ProseInGhCommentBodyDoesNotFireDetector(t *testing.T) {
	// exact reproduction from the issue: prose quoting the command
	cmd := "gh issue comment 5212 --body 'discussion of raw `git branch -D` semantics'"
	if branchForceDeleteInGitSegment(cmd) {
		t.Fatal("prose mention inside a gh body must NOT fire branch_force_delete")
	}
}

func TestIssue5217_ProseDoesNotFeedProbe(t *testing.T) {
	cmd := "gh issue comment 5212 --body 'we deleted main via git branch -D main yesterday'"
	if got := extractForceDeletedBranches(cmd); len(got) != 0 {
		t.Errorf("prose branches must not be extracted for probing, got %v", got)
	}
}

func TestIssue5217_RealGitCommandStillFires(t *testing.T) {
	if !branchForceDeleteInGitSegment("git branch -D feature") {
		t.Fatal("real git branch -D must fire")
	}
	if !branchForceDeleteInGitSegment("cd /repo && git branch -D feature | cat") {
		t.Fatal("chained git segment must fire")
	}
	if !branchForceDeleteInGitSegment("GIT_TRACE=1 git branch --delete --force feature") {
		t.Fatal("env-prefixed git invocation must fire")
	}
	if got := extractForceDeletedBranches("cd /repo && git branch -D feature"); len(got) != 1 || got[0] != "feature" {
		t.Errorf("probe must extract from real segments, got %v", got)
	}
}

func TestIssue5217_GitSegmentScoping(t *testing.T) {
	// non-git segment mentioning the text must not leak into the git segment's verdict
	cmd := "echo 'git branch -D prose-only' && git status"
	if branchForceDeleteInGitSegment(cmd) {
		t.Error("echo segment is prose; git status segment is not a branch delete - must not fire")
	}
	// branch -f move family
	if !branchForceMoveInGitSegment("git branch -f main origin/main") {
		t.Error("branch -f in a git segment must fire the move check")
	}
	if branchForceMoveInGitSegment("cat notes.md # mentions git branch -f") {
		t.Error("prose branch -f must not fire")
	}
}

func TestIssue5217_SafeDeleteStillNotForce(t *testing.T) {
	if branchForceDeleteInGitSegment("git branch -d feature") {
		t.Error("safe delete (-d) must not fire branch_force_delete")
	}
}

func TestIssue5217_GitLedSegmentsHelper(t *testing.T) {
	segs := gitLedSegments("FOO=1 git -C /x status; echo hi && git commit -m 'msg with git branch -D inside'")
	if len(segs) != 2 {
		t.Fatalf("want 2 git-led segments (env prefix tolerated; commit message prose ignored as text of that segment), got %q", segs)
	}
	if !strings.HasPrefix(segs[0], "git -C /x status") {
		t.Errorf("env prefix must be stripped from segment verdict, got %q", segs[0])
	}
}

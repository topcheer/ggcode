package agent

import "testing"

// #3841: the git branch mutating-flag scan ran to end-of-line - the only
// classifyGitCommandLine branch that did - so in a compound line like
// `git branch && git log -m` the LATER command's `-m` hit the branch flag
// table and the read-only branch listing was misjudged as mutating,
// burning the invalidation warning budget on read-only flows.

func TestIssue3841_BranchScanStopsAtCompoundBoundary(t *testing.T) {
	// read-only branch list followed by a read-only command whose -m flag
	// collides with git branch --move's -m.
	ro, found := classifyGitCommandLine(`git branch && git log -m`)
	if !found || !ro {
		t.Fatalf("#3841: read-only compound line must classify read-only, got ro=%v found=%v", ro, found)
	}
	// Same shape with echo text containing the flag token.
	if ro, found := classifyGitCommandLine(`git branch -a && echo use -m to move`); !found || !ro {
		t.Fatalf("#3841: echo continuation must not turn branch read-only, got ro=%v found=%v", ro, found)
	}
}

func TestIssue3841_TrueBranchMutationStillDetected(t *testing.T) {
	// The boundary fix must not weaken real mutation detection: flags of
	// the branch invocation itself still mark it mutating.
	for _, cmd := range []string{
		`git branch -d foo`,
		`git branch -m old new`,
		`git branch --move old new && echo done`,
	} {
		if ro, found := classifyGitCommandLine(cmd); !found || ro {
			t.Fatalf("true branch mutation must stay mutating: %q -> ro=%v found=%v", cmd, ro, found)
		}
	}
	// A tree-mutating follow-up command still makes the line mutating
	// overall (git commit/add are deliberately tree-preserving post-#3823;
	// stash push and checkout change tracked working-tree content).
	if ro, found := classifyGitCommandLine(`git branch && git checkout main`); !found || ro {
		t.Fatalf("tree-mutating follow-up must keep the line mutating, got ro=%v found=%v", ro, found)
	}
	if ro, found := classifyGitCommandLine(`git branch && git stash push`); !found || ro {
		t.Fatalf("stash follow-up must keep the line mutating, got ro=%v found=%v", ro, found)
	}
}

func TestIssue3841_RedirectionEndsFlagScan(t *testing.T) {
	// Redirect tokens are invocation boundaries too: `git branch > out`
	// stays read-only (list redirected to a file).
	if ro, found := classifyGitCommandLine(`git branch > /tmp/out.txt`); !found || !ro {
		t.Fatalf("redirected branch list must stay read-only, got ro=%v found=%v", ro, found)
	}
}

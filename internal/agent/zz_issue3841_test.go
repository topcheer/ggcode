package agent

// #3841 companion: the git-branch flag scan must stop at compound-command
// boundaries - a later command's -m (git log -m, echo "use -m to move")
// must not classify a read-only branch listing as mutating.

import "testing"

func TestIssue3841_BranchScanStopsAtBoundary(t *testing.T) {
	readOnlyCases := []string{
		`git branch && git log -m`,               // log's merge-diff flag
		`git branch -a && echo "use -m to move"`, // prose in a later command
		`git branch --list || git tag -m v1 msg`, // tag -m after ||
		`git branch ; git status`,                // ; separator, all read-only
		`git branch | grep -m 1 main`,            // pipe + grep's -m
	}
	for _, cmd := range readOnlyCases {
		ro, isGit := classifyGitCommandLine(cmd)
		if !isGit {
			t.Fatalf("%q: expected git line", cmd)
		}
		if !ro {
			t.Fatalf("%q: read-only chain misclassified as mutating", cmd)
		}
	}

	mutatingCases := []string{
		`git branch -m old new`,               // genuine move before any boundary
		`git branch -D wip`,                   // genuine delete
		`git branch && git branch -m old new`, // mutation in the second command
	}
	for _, cmd := range mutatingCases {
		ro, isGit := classifyGitCommandLine(cmd)
		if !isGit {
			t.Fatalf("%q: expected git line", cmd)
		}
		if ro {
			t.Fatalf("%q: genuine mutation misclassified as read-only", cmd)
		}
	}
}

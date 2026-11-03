package agent

import "testing"

func TestIssue3841_BranchScanStopsAtBoundary(t *testing.T) {
	readOnlyCases := []string{
		`git branch && git log -m`,
		`git branch -a && echo "use -m to move"`,
		`git branch --list || git tag -m v1 msg`,
		`git branch ; git status`,
		`git branch | grep -m 1 main`,
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
		`git branch -m old new`,
		`git branch -D wip`,
		`git branch && git branch -m old new`,
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

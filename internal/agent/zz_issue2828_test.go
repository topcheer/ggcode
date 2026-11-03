package agent

import "testing"

func TestIssue2828GitPushGlobalFlags(t *testing.T) {
	positive := []string{
		"git push",
		"git push origin main",
		"git -C /Volumes/x/repo push",
		"git -C /repo push origin main",
		"git --git-dir=/repo/.git push",
		"git --git-dir=/repo/.git push origin",
		"git --work-tree=/repo -C /repo push",
		`{"command":"git -C /repo push"}`,
		`{"command":"git -c http.proxy=http://p:8080 push origin main"}`,
	}
	for _, s := range positive {
		if !isGitPush(s) {
			t.Errorf("#2828 isGitPush(%q) = false, want true (global-flag push escaped the gate)", s)
		}
	}

	negative := []string{
		"",
		"git status",
		"git -C /repo status",
		"git -C /repo log --oneline",
		"git --git-dir=/repo/.git diff HEAD",
	}
	for _, s := range negative {
		if isGitPush(s) {
			t.Errorf("#2828 isGitPush(%q) = true, want false", s)
		}
	}

	if !isGitPush("git push --force-with-lease") {
		t.Errorf("#2828 plain git push stopped firing")
	}
}

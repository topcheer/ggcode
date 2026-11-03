package agent

import "testing"

func TestIssue1490D_SubstringMentionsDoNotFire(t *testing.T) {
	cases := []string{
		"# never git push in CI\n cat Makefile",
		`grep -rn "reset --hard" docs/`,
		"echo doing clean -f later; ls",
		"cat releases/checkout -- notes.txt",
	}
	for _, c := range cases {
		if isGitPush(c) {
			t.Errorf("isGitPush(%q) must be false - mention, not command", c)
		}
		if isDestructiveGit(c) {
			t.Errorf("isDestructiveGit(%q) must be false - mention, not command", c)
		}
	}
}

func TestIssue1490D_RealCommandsStillFire(t *testing.T) {
	if !isGitPush("# deploy\ngit push origin main") {
		t.Error("real git push must still fire")
	}
	if !isDestructiveGit("git reset --hard HEAD~1") {
		t.Error("git reset --hard must still fire")
	}
	if !isDestructiveGit("git clean -fd") {
		t.Error("git clean -f must still fire")
	}

	if !isDestructiveGit("git clean --force") {
		t.Error("git clean --force must still fire")
	}
	if !isDestructiveGit("git checkout -- internal/tui/repl.go") {
		t.Error("git checkout -- must still fire")
	}
}

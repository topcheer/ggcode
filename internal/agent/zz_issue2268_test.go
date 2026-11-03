package agent

import "testing"

func TestIssue2268ShDashCQuotedGit(t *testing.T) {
	cases := []struct {
		name, args string
	}{
		{"single-quote reset", `{"command":"sh -c 'git reset --hard'"}`},
		{"single-quote clean", `{"command":"sh -c 'git clean -fd'"}`},
		{"double-quote reset tail", `{"command":"sh -c \"git reset --hard\""}`},
	}
	for _, c := range cases {
		if !isDestructiveGit(c.args) {
			t.Errorf("%s: isDestructiveGit must fire, args=%s", c.name, c.args)
		}
	}
	if !isGitPush(`{"command":"sh -c 'git push --force origin main'"}`) {
		t.Error("isGitPush must see quoted push")
	}

	if isDestructiveGit(`{"command":"sh -c 'go build ./...'"}`) {
		t.Error("non-destructive sh -c must stay quiet")
	}
}

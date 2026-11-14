package agent

import (
	"strings"
	"testing"
)

// #5268 item 1 residual: quoted prose mentioning destructive git commands
// must not trip the lexical destructive gate, while real invocations and
// $( ) substitution bodies must keep firing.

func issue5268FiredNames(t *testing.T, shellCmd string) map[string]bool {
	t.Helper()
	got := map[string]bool{}
	for _, p := range detectDestructiveInShellCommand(shellCmd) {
		got[p.name] = true
	}
	return got
}

func TestIssue5268_QuotedProseDoesNotTripGitGate(t *testing.T) {
	prose := []string{
		"echo 'never run git reset --hard on main'",
		`echo "note: git stash drop loses data"`,
		"echo 'avoid git filter-branch'",
		"echo 'git rebase -i is dangerous'",
		`printf '%s' "git push --force origin main would be bad here"`,
		"echo 'docs say: git push --force-with-lease is safer, still avoid'",
		"echo 'git checkout -- . discards everything'",
		"echo 'git clean -fdx wipes untracked files'",
	}
	for _, cmd := range prose {
		if got := issue5268FiredNames(t, cmd); len(got) > 0 {
			t.Errorf("quoted prose %q fired gate: %v", cmd, got)
		}
	}
}

func TestIssue5268_RealGitCommandsStillFire(t *testing.T) {
	cases := []struct {
		cmd  string
		want string
	}{
		{"git reset --hard HEAD~1", "reset_hard"},
		{"git push --force origin main", "force_push"},
		{"git push --force-with-lease origin main", "force_with_lease"},
		{"git stash drop", "stash_drop"},
		{"git rebase main", "rebase"},
		{"git filter-branch --env-filter x", "filter_branch"},
		{"git checkout -- .", "discard_all"},
		{"git clean -fdx", "clean_force"},
		{"rm -rf build && echo 'rm -rf build'", "rm_rf"},
		{"echo $(git reset --hard)", "reset_hard"},
	}
	for _, c := range cases {
		got := issue5268FiredNames(t, c.cmd)
		if !got[c.want] {
			t.Errorf("cmd %q: expected %q to fire, got %v", c.cmd, c.want, got)
		}
	}
}

func TestIssue5268_BlankQuotedSegments(t *testing.T) {
	// Single quotes blank fully, double quotes keep $() bodies visible.
	cmd := `echo 'git reset --hard' "$(git status)" end`
	got := blankQuotedSegments(cmd)
	// Same length, single-quoted span fully blanked, $() body kept.
	if len(got) != len(cmd) {
		t.Errorf("length changed: %d -> %d", len(cmd), len(got))
	}
	if strings.Contains(got, "git reset") {
		t.Errorf("single-quoted literal leaked through: %q", got)
	}
	if !strings.Contains(got, "$(git status)") {
		t.Errorf("double-quoted $() body was blanked: %q", got)
	}
	if strings.Contains(got, "git reset") {
		t.Error("single-quoted literal leaked through")
	}
}

func TestIssue5268_InterpreterPayloadQuotedSpansStillFire(t *testing.T) {
	cases := []struct {
		cmd  string
		want string
	}{
		{`sh -c "git reset --hard HEAD~1"`, "reset_hard"},
		{`sh -c "git push --force origin main"`, "force_push"},
		{`bash -c 'git stash drop'`, "stash_drop"},
		{`sudo sh -c "git checkout -- ."`, "discard_all"},
		{`/bin/sh -c "git clean -fdx"`, "clean_force"},
	}
	for _, c := range cases {
		got := issue5268FiredNames(t, c.cmd)
		if !got[c.want] {
			t.Errorf("payload cmd %q: expected %q to fire, got %v", c.cmd, c.want, got)
		}
	}
}

package agent

import (
	"strings"
	"testing"
)

// #4795 (S-26): a persisted refusal's flag anchors must only block commands
// in the action domain the user refused. `git push --force` names the
// git-push domain; a `git worktree remove --force` cleanup exercises the
// git-worktree domain and must pass even though both carry `--force`.
func TestIssue4795_ForceFlagDomainScope(t *testing.T) {
	l := newRefusalTestLedger(t)
	l.record("never run git push --force", "nl", refusalKindDirect)

	cases := []struct {
		name    string
		command string
		blocked bool
	}{
		{"same domain push", "git push --force origin main", true},
		{"cross domain worktree cleanup", "git worktree remove --force ../wt", false},
		{"cross domain checkout", "git checkout --force main", false},
		{"no flag plain push", "git push origin main", false},
		{"sudo same domain still blocks", "sudo git push --force origin main", true},
		{"compound other domain", "cd /tmp && git worktree remove --force wt2", false},
	}

	for _, tc := range cases {
		args := mustJSON3469(t, map[string]string{"command": tc.command})
		msg := l.checkBlocked("run_command", args)
		if tc.blocked && msg == "" {
			t.Errorf("%s: expected block, got none", tc.name)
		}
		if !tc.blocked && msg != "" {
			t.Errorf("%s: expected no block, got %q", tc.name, msg)
		}
	}
}

// A flag refusal without command context stays wildcard: conservative scope,
// preserving the #3460 poisoning protection for terse refusals.
func TestIssue4795_FlagWithoutContextStaysWildcard(t *testing.T) {
	l := newRefusalTestLedger(t)
	l.record("never use --force", "nl", refusalKindDirect)

	if msg := l.checkBlocked("run_command", mustJSON3469(t, map[string]string{"command": "git worktree remove --force ../wt"})); msg == "" {
		t.Error("context-free flag refusal must keep wildcard scope and block")
	}
}

// Path anchors never domain-scope: they name protected objects, not verbs.
func TestIssue4795_PathAnchorsUnaffected(t *testing.T) {
	l := newRefusalTestLedger(t)
	l.record("don't touch internal/auth/core.go", "nl", refusalKindDirect)

	if msg := l.checkBlocked("edit_file", `{"file_path":"/repo/internal/auth/core.go","old_text":"x"}`); msg == "" {
		t.Error("path refusal must still block file edit")
	}
	rm := mustJSON3469(t, map[string]string{"command": "rm internal/auth/core.go"})
	if msg := l.checkBlocked("run_command", rm); msg == "" {
		t.Error("path refusal must still block destructive command naming the path")
	}
}

// Natural-language shapes seen around the issue must land on the git-push
// domain, not on a filler word domain.
func TestIssue4795_DomainDerivation(t *testing.T) {
	cases := []struct {
		carriers string
		domain   string
	}{
		{"never run git push ", "git-push"},
		{"never use git push ", "git-push"},
		{"do not run git push ", "git-push"},
		{"never ", ""},
		{"git ", "git"},
		{"stop using ", ""},
		{"don't use rm ", "rm"},
	}
	for _, tc := range cases {
		if got := refusalAnchorDomain("--force", tc.carriers); got != tc.domain {
			t.Errorf("refusalAnchorDomain(%q) = %q, want %q", tc.carriers, got, tc.domain)
		}
	}
}

// Call-side domain derivation sanity for compound and wrapped commands.
func TestIssue4795_CommandDomains(t *testing.T) {
	domains := refusalCommandDomains("cd /tmp && sudo git worktree remove --force wt")
	for _, want := range []string{"cd", "git-worktree", "worktree", "git"} {
		if !domains[want] {
			t.Errorf("domains %v missing %q", domains, want)
		}
	}
	if refusalCommandDomains("git push --force origin main")["git-push"] != true {
		t.Error("git push must carry git-push domain")
	}
}

// Release semantics must be untouched by domain scoping.
func TestIssue4795_ReleaseStillWorks(t *testing.T) {
	l := newRefusalTestLedger(t)
	l.record("never run git push --force", "nl", refusalKindDirect)
	if n := l.release("go ahead with git push --force"); n != 1 {
		t.Errorf("strong lift must release, got %d", n)
	}
}

// #4632 wiring invariant: checkBlocked keeps refusalAnchorHit with
// whole-token flag matching; #4795 only adds the domain gate on top.
func TestIssue4795_AnchorHitSemanticsUnchanged(t *testing.T) {
	if !refusalAnchorHit("git push --force origin main", "--force") {
		t.Error("whole-token flag hit must still work")
	}
	if refusalAnchorHit("git push --force-with-lease origin main", "--force") {
		t.Error("force-with-lease must not hit --force anchor")
	}
	if !strings.Contains("git push --force", "--force") {
		t.Fatal("unreachable")
	}
}

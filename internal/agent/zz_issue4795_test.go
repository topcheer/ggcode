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

// Path anchors without a git-family verb carrier never domain-scope: they
// name protected objects, not verbs ("touch" is directive filler here).
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

// #4795 residual (2026-11-17): path anchors scoped by a git-family verb
// carrier. "never push to release/2.0" must block pushes carrying the
// branch path but not checkout/branch-delete cleanup - the cross-domain
// branch-family false positive the 11-16 reviewer re-observed during the
// #5702 review (pre-fix pin: both cleanup rows were hard-blocked at main
// e90ac7495 before this change).
func TestIssue4795_BranchPathAnchorDomainScope(t *testing.T) {
	l := newRefusalTestLedger(t)
	l.record("never push to release/2.0", "nl", refusalKindDirect)

	cases := []struct {
		name    string
		command string
		blocked bool
	}{
		{"cross-domain checkout", "git checkout release/2.0", false},
		{"cross-domain branch delete", "git branch -D release/2.0", false},
		{"cross-domain merge", "git merge release/2.0", false},
		{"same-domain push", "git push origin release/2.0", true},
		{"sudo same-domain push", "sudo git push origin release/2.0", true},
		{"compound same-domain push", "cd /tmp && git push origin release/2.0", true},
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

// Semantic-verb path refusals must stay wildcard: "delete" is not a
// command word, so scoping it to a literal "delete" domain would unblock
// rm/shred/git rm - the wrongly-opened real lock the 11-01 owner ruling
// warns about. Every write-class carrier of the path still blocks.
func TestIssue4795_SemanticVerbPathRefusalStaysWildcard(t *testing.T) {
	l := newRefusalTestLedger(t)
	l.record("don't delete internal/auth/core.go", "nl", refusalKindDirect)

	if msg := l.checkBlocked("run_command", mustJSON3469(t, map[string]string{"command": "rm internal/auth/core.go"})); msg == "" {
		t.Error("semantic-verb path refusal must still block rm")
	}
	if msg := l.checkBlocked("run_command", mustJSON3469(t, map[string]string{"command": "git checkout internal/auth/core.go"})); msg == "" {
		t.Error("semantic-verb path refusal must stay wildcard across domains")
	}
	if msg := l.checkBlocked("edit_file", "{\"file_path\":\"/repo/internal/auth/core.go\",\"old_text\":\"x\"}"); msg == "" {
		t.Error("semantic-verb path refusal must still block file edit")
	}
}

// Path domain derivation: git-family verbs (incl. inflections and
// explicit git <sub> contexts) scope; filler, semantic verbs, and
// non-command heads stay wildcard.
func TestIssue4795_PathDomainDerivation(t *testing.T) {
	cases := []struct {
		carriers string
		domain   string
	}{
		{"never push to ", "push"},
		{"never pushing to ", "push"},
		{"don't push ", "push"},
		{"stop pushing the ", "push"},
		{"never run git push origin ", "git-push"},
		{"do not git push ", "git-push"},
		{"never merge from ", "merge"},
		{"don't rebase onto ", "rebase"},
		{"never checkout ", "checkout"},
		{"don't touch ", ""},
		{"never delete ", ""},
		{"don't remove ", ""},
		{"avoid overwriting ", ""},
		{"never ", ""},
		{"stop using ", ""},
		{"git ", ""},
	}
	for _, tc := range cases {
		if got := refusalPathAnchorDomain(tc.carriers); got != tc.domain {
			t.Errorf("refusalPathAnchorDomain(%q) = %q, want %q", tc.carriers, got, tc.domain)
		}
	}
}

// Carriers for a path anchor are the words before the anchor's own
// occurrence, so a verb that only trails the path cannot scope it - an
// ambiguous trailing verb must fail closed to wildcard.
func TestIssue4795_PathCarriersBeforeAnchorOnly(t *testing.T) {
	if got := refusalPathCarriers("never push to release/2.0", "release/2.0"); got != "never push to " {
		t.Errorf("refusalPathCarriers = %q", got)
	}
	l := newRefusalTestLedger(t)
	l.record("never force anything onto release/2.0 while pushing", "nl", refusalKindDirect)
	if msg := l.checkBlocked("run_command", mustJSON3469(t, map[string]string{"command": "git checkout release/2.0"})); msg == "" {
		t.Error("trailing-verb path anchor must stay wildcard (fail closed)")
	}
}

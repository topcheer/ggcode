package tool

import "testing"

// #4430 Bug3/Bug4 regression tests (issue-fixer f46; Bug1/Bug2 live in
// zz_issue4430_test.go).

// Bug3: IsDestructive must mirror Check's layer semantics exactly --
// quotedInert rules evaluate ONLY the inert view and benignPrefixes
// rewriting applies. Previously it matched the raw view, so quoted text
// mentioning a dispersed-flag rm made IsDestructive=true while Check
// said Allow (verdict divergence for danger assessors).
func TestIssue4430Bug3IsDestructiveMirrorsCheck(t *testing.T) {
	g := NewCommandGate()

	// Quoted argument DATA with dispersed flags: Check=Allow, so
	// IsDestructive must be false (old code diverged: raw-view match of
	// the quotedInert permutation rule returned true).
	for _, cmd := range []string{
		"echo '" + "rm" + " --recursive --force /etc'",
		"grep '" + "rm" + " --force /etc --recursive' doc.md",
	} {
		if res := g.Check(cmd); res.Behavior != Allow {
			t.Fatalf("precondition: Check(%q) should Allow, got %v", cmd, res.Behavior)
		}
		if g.IsDestructive(cmd) {
			t.Fatalf("IsDestructive(%q)=true diverges from Check=Allow", cmd)
		}
	}

	// Adjacent-flag quoted text: Check=Ask (non-quotedInert ask rules
	// match the raw view by #3125 design), so IsDestructive must agree.
	cmd := "echo '" + "rm" + " -" + "rf" + " /etc'"
	if res := g.Check(cmd); res.Behavior != Ask {
		t.Fatalf("precondition: Check(%q) should Ask, got %v", cmd, res.Behavior)
	}
	if !g.IsDestructive(cmd) {
		t.Fatalf("IsDestructive(%q)=false diverges from Check=Ask", cmd)
	}

	// Real destructive forms must stay destructive.
	for _, c := range []string{
		"rm" + " -" + "rf" + " /etc",
		"rm" + " --recursive --force /etc",
		"git reset --hard",
		"terraform destroy",
	} {
		if !g.IsDestructive(c) {
			t.Fatalf("genuinely destructive command not flagged: %q", c)
		}
	}
	// Quote-wrapped FLAG form (argv-equivalent) must still be caught via
	// the unwrapped view (#3125 parity).
	if !g.IsDestructive(`rm "-rf" /etc`) {
		t.Fatalf("quote-wrapped flag bypassed IsDestructive")
	}
}

// Bug4: GIT_PAGER cleanRule must not fire inside quotes -- the raw
// ReplaceAllString produced `echo '` (unbalanced quote, corrupt shell).
func TestIssue4430Bug4CleanRuleQuoteAware(t *testing.T) {
	g := NewCommandGate()
	res := g.Check(`echo 'GIT_PAGER=cat'`)
	if res.CleanedCmd != `echo 'GIT_PAGER=cat'` {
		t.Fatalf("quoted GIT_PAGER text was cleaned: got %q", res.CleanedCmd)
	}
	// Unquoted override still cleaned (the rule's purpose).
	res = g.Check("GIT_PAGER=cat git log")
	if res.CleanedCmd != "git log" {
		t.Fatalf("unquoted GIT_PAGER override not cleaned: got %q", res.CleanedCmd)
	}
	// Double quotes equally protected.
	res = g.Check(`echo "GIT_PAGER=cat"`)
	if res.CleanedCmd != `echo "GIT_PAGER=cat"` {
		t.Fatalf("double-quoted GIT_PAGER text was cleaned: got %q", res.CleanedCmd)
	}
}

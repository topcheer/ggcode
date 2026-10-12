package agent

import "testing"

// #4065: RE2 \b treats '-' as a word boundary, so hyphenated branch/PR names
// ("fix-4012", "patch-4013", "resolve-4016" - this repo's branch naming
// convention) matched \bfix\b and disabled research mode for read-heavy
// review tasks. A verb only counts as fix intent when the following rune is
// not a hyphen.
func TestZZIssue4065HyphenatedBranchNamesKeepResearchMode(t *testing.T) {
	research := []string{
		"analyze branch fix-4012 divergence",
		"review the patch-4013 PR changes and assess impact",
		"investigate the resolve-4016 regression",
		// NOTE: pure-CJK research text ("研究 fix-4012 分支的改动并分析根因")
		// still returns false AFTER this fix because detectResearchMode's
		// keyword list is English-only - a separate gap from #4065's hyphen
		// boundary bug (tracked in the #4065 fix comment, not pinned here).
		"explore what repairs-4012 covered, assess gaps",   // plural verb + hyphen token
		"audit commits around implement-77 on main branch", // hyphen directly after verb
	}
	for _, task := range research {
		if !detectResearchMode(task) {
			t.Errorf("detectResearchMode(%q) = false, want true (research task naming a hyphenated branch/PR must stay research mode)", task)
		}
	}
}

func TestZZIssue4065RealFixIntentStillOverrides(t *testing.T) {
	fixIntent := []string{
		"investigate why tests fail and fix it", // #3990 original case must keep working
		"implement the stub",
		"review the code then patch the bug",
		"fix the bug in fix-4012", // a real verb coexists with a hyphen token: verb wins
		"repair the flaky test and resolve the failure",
		"...and then fix", // end-of-string counts as a verb
	}
	for _, task := range fixIntent {
		if detectResearchMode(task) {
			t.Errorf("detectResearchMode(%q) = true, want false (explicit fix/make intent must override research verbs)", task)
		}
	}
}

func TestZZIssue4065ResearchKeywordsUnaffected(t *testing.T) {
	if !detectResearchMode("Explore competitor implementations") {
		t.Error("inflected noun 'implementations' must not be fix intent (#3990 author intent preserved)")
	}
	if !detectResearchMode("research the trend of agent frameworks") {
		t.Error("plain research keyword case regressed")
	}
	if detectResearchMode("") {
		t.Error("empty task must not be research mode")
	}
}

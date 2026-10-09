package agent

// #3618 probe: regression introduced by the #3607 fix - marker-less
// matches ("eslint-disable") used an "either side suffices" shortcut, and
// at comment-start the prefix is always the comment marker, so a single
// lowercase word after the match decided everything: pure-lowercase rule
// names ("eslint-disable semi") read as prose and real suppressions were
// skipped. Function words never open a rule list - that is the
// disambiguator.

import "testing"

func TestIssue3618_RuleNameAfterDisableStillFlags(t *testing.T) {
	real := []string{
		"/* eslint-disable semi */\n",
		"// eslint-disable quotes\n",
		"/* eslint-disable semi, quotes */\n", // first token "semi" then comma-list
		"a.js",                                // placeholder, replaced below per-case
	}
	real = real[:3]
	for _, added := range real {
		if w := checkSuppressionDirectives("a.js", "", added); len(w) == 0 {
			t.Fatalf("real scoped disable skipped as prose: %q", added)
		}
	}
}

func TestIssue3618_CommentStartProseStillSkipped(t *testing.T) {
	// Prose whose only signal is a function-word continuation after a
	// comment-start match must stay skipped (no word before).
	prose := []string{
		"<!-- eslint-disable is banned here -->\n",
		"/* eslint-disable in this repo is forbidden */\n",
	}
	for _, added := range prose {
		if w := checkSuppressionDirectives("a.html", "", added); len(w) != 0 {
			t.Fatalf("comment-start prose flagged: %q -> %v", added, w)
		}
	}
	// Word-before prose (the #3607 canonicals) stays skipped too.
	for _, added := range []string{
		"/* do not use eslint-disable in this repo */\n",
		"<!-- we avoid eslint-disable here -->\n",
	} {
		if w := checkSuppressionDirectives("a.js", "", added); len(w) != 0 {
			t.Fatalf("word-before prose flagged: %q -> %v", added, w)
		}
	}
}

func TestIssue3618_UnitDisambiguator(t *testing.T) {
	// Comment-start: bare lowercase identifier = rule argument (directive).
	if proseContinuation("semi", true) || proseContinuation("quotes", true) {
		t.Fatal("bare rule identifier treated as prose at comment-start")
	}
	// Function word opens prose regardless of position requirement.
	if !proseContinuation("is banned here", true) || !proseContinuation("in this repo", true) {
		t.Fatal("function-word continuation not treated as prose")
	}
	// Word-before: single trailing adverb is accepted prose (residual,
	// documented) and 2+ lowercase words are prose.
	if !proseContinuation("sparingly", false) || !proseContinuation("is banned", false) {
		t.Fatal("word-before prose continuation misjudged")
	}
	// Punctuated rule lists are never prose.
	if proseContinuation("no-foo", false) || proseContinuation("no-console, no-alert", false) {
		t.Fatal("punctuated rule list treated as prose")
	}
}

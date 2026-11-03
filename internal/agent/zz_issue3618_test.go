package agent

import "testing"

func TestIssue3618_RuleNameAfterDisableStillFlags(t *testing.T) {
	real := []string{
		"/* eslint-disable semi */\n",
		"// eslint-disable quotes\n",
		"/* eslint-disable semi, quotes */\n",
		"a.js",
	}
	real = real[:3]
	for _, added := range real {
		if w := checkSuppressionDirectives("a.js", "", added); len(w) == 0 {
			t.Fatalf("real scoped disable skipped as prose: %q", added)
		}
	}
}

func TestIssue3618_CommentStartProseStillSkipped(t *testing.T) {

	prose := []string{
		"<!-- eslint-disable is banned here -->\n",
		"/* eslint-disable in this repo is forbidden */\n",
	}
	for _, added := range prose {
		if w := checkSuppressionDirectives("a.html", "", added); len(w) != 0 {
			t.Fatalf("comment-start prose flagged: %q -> %v", added, w)
		}
	}

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

	if proseContinuation("semi", true) || proseContinuation("quotes", true) {
		t.Fatal("bare rule identifier treated as prose at comment-start")
	}

	if !proseContinuation("is banned here", true) || !proseContinuation("in this repo", true) {
		t.Fatal("function-word continuation not treated as prose")
	}

	if !proseContinuation("sparingly", false) || !proseContinuation("is banned", false) {
		t.Fatal("word-before prose continuation misjudged")
	}

	if proseContinuation("no-foo", false) || proseContinuation("no-console, no-alert", false) {
		t.Fatal("punctuated rule list treated as prose")
	}
}

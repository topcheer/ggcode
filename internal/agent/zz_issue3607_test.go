package agent

// #3607 probe: prose riding INSIDE comments/docstrings (the most natural
// place to DISCUSS suppression policy) must not count as directives, while
// every real form keeps firing. The old matchRidesComment gate inverted
// exactly there: the comment marker's presence let policy prose through.

import "testing"

func TestIssue3607_ProseInCommentsNotFlagged(t *testing.T) {
	cases := []struct {
		name, fp, added string
	}{
		// A: policy statement in a .js comment.
		{"js policy comment", "a.js", "/* do not use eslint-disable in this repo */\n"},
		// B: docstring prose mentioning # noqa.
		{"py docstring prose", "a.py", "\"\"\"Use # noqa only as last resort.\"\"\"\n"},
		// C: markup comment.
		{"html comment", "a.html", "<!-- we avoid eslint-disable here -->\n"},
	}
	for _, c := range cases {
		if w := checkSuppressionDirectives(c.fp, "", c.added); len(w) != 0 {
			t.Fatalf("%s: prose flagged as suppression: %v", c.name, w)
		}
	}
}

func TestIssue3607_RealDirectivesStillFlagged(t *testing.T) {
	cases := []struct {
		name, fp, added string
	}{
		{"js real block disable", "a.js", "/* eslint-disable */\n"},
		{"js next-line with rules", "a.js", "// eslint-disable-next-line no-foo\n"},
		{"py bare comment start", "a.py", "# noqa only as last resort\n"}, // flake8 parses as bare noqa
		{"py trailing after code", "a.py", "value = compute()  # noqa\n"},
	}
	for _, c := range cases {
		if w := checkSuppressionDirectives(c.fp, "", c.added); len(w) == 0 {
			t.Fatalf("%s: real directive no longer flagged", c.name)
		}
	}
}

func TestIssue3607_ProseMentionUnit(t *testing.T) {
	// Marker-less match, prose both sides.
	if !proseMention("/* do not use eslint-disable in this repo */", 14, "eslint-disable") {
		t.Fatal("policy prose not detected")
	}
	// Marker-less match, real directive after code.
	if proseMention("x = 1; /* eslint-disable */", 10, "eslint-disable") {
		t.Fatal("real trailing directive misread as prose")
	}
	// Marker-in-match: docstring sentence (both sides) is prose...
	if !proseMention("Use # noqa only as last resort", 4, "# noqa") {
		t.Fatal("docstring prose not detected")
	}
	// ...but bare trailing after code is not.
	if proseMention("value  # noqa", 7, "# noqa") {
		t.Fatal("bare trailing directive misread as prose")
	}
	// Rule arguments carry punctuation and are not prose continuations.
	if proseContinuation("no-foo") || proseContinuation("no-console, no-alert") {
		t.Fatal("rule argument treated as prose continuation")
	}
	if !proseContinuation("in this repo") {
		t.Fatal("prose continuation not detected")
	}
}

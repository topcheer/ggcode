package agent

import (
	"strings"
	"testing"
)

func TestCheckSuppressionDirectives_GoNolint(t *testing.T) {
	old := "package main\n\nfunc foo() {}\n"
	new_ := "package main\n\n//nolint\nfunc foo() {}\n"

	warnings := checkSuppressionDirectives("main.go", old, new_)
	if len(warnings) == 0 {
		t.Fatal("expected nolint warning for .go")
	}
	if !strings.Contains(warnings[0], "//nolint") {
		t.Errorf("expected '//nolint' in warning, got: %s", warnings[0])
	}
}

func TestCheckSuppressionDirectives_PythonTypeIgnore(t *testing.T) {
	old := "def foo():\n    pass\n"
	new_ := "def foo():\n    x: int = get_val()  # type: ignore\n"

	warnings := checkSuppressionDirectives("main.py", old, new_)
	if len(warnings) == 0 {
		t.Fatal("expected type:ignore warning for .py")
	}
	if !strings.Contains(warnings[0], "type: ignore") {
		t.Errorf("expected 'type: ignore' in warning, got: %s", warnings[0])
	}
}

func TestCheckSuppressionDirectives_PythonNoQA(t *testing.T) {
	old := "import os\n"
	// Bare # noqa (no rule code) — scoped forms like "# noqa: F401" are
	// legitimate targeted suppressions and must NOT warn (#572 contract).
	new_ := "import os  # noqa\n"

	warnings := checkSuppressionDirectives("main.py", old, new_)
	if len(warnings) == 0 {
		t.Fatal("expected noqa warning")
	}
	if !strings.Contains(warnings[0], "noqa") {
		t.Errorf("expected 'noqa' in warning, got: %s", warnings[0])
	}
}

func TestCheckSuppressionDirectives_PythonPragmaNoCover(t *testing.T) {
	old := "def foo():\n    return 1\n"
	new_ := "def foo():\n    return 1  # pragma: no cover\n"

	warnings := checkSuppressionDirectives("main.py", old, new_)
	if len(warnings) == 0 {
		t.Fatal("expected pragma: no cover warning")
	}
}

func TestCheckSuppressionDirectives_PythonPylintDisable(t *testing.T) {
	old := "import os\n"
	new_ := "import os  # pylint: disable=unused-import\n"

	warnings := checkSuppressionDirectives("main.py", old, new_)
	if len(warnings) == 0 {
		t.Fatal("expected pylint: disable warning")
	}
}

func TestCheckSuppressionDirectives_JSEslintDisable(t *testing.T) {
	old := "const x = 1;\n"
	new_ := "/* eslint-disable no-unused-vars */\nconst x = 1;\n"

	warnings := checkSuppressionDirectives("index.js", old, new_)
	if len(warnings) == 0 {
		t.Fatal("expected eslint-disable warning for .js")
	}
	if !strings.Contains(warnings[0], "eslint-disable") {
		t.Errorf("expected 'eslint-disable' in warning, got: %s", warnings[0])
	}
}

func TestCheckSuppressionDirectives_JSEslintDisableNextLine(t *testing.T) {
	old := "const x = 1;\n"
	new_ := "// eslint-disable-next-line no-console\nconsole.log(x);\n"

	warnings := checkSuppressionDirectives("index.ts", old, new_)
	if len(warnings) == 0 {
		t.Fatal("expected eslint-disable-next-line warning for .ts")
	}
}

func TestCheckSuppressionDirectives_GoGosecDisable(t *testing.T) {
	old := "package main\n"
	new_ := "package main\n\n//gosec:disable G104\nfunc main() {}\n"

	warnings := checkSuppressionDirectives("main.go", old, new_)
	if len(warnings) == 0 {
		t.Fatal("expected gosec:disable warning")
	}
}

func TestCheckSuppressionDirectives_GoReviveDisable(t *testing.T) {
	old := "package main\n"
	new_ := "package main\n\n//revive:disable\nfunc main() {}\n"

	warnings := checkSuppressionDirectives("main.go", old, new_)
	if len(warnings) == 0 {
		t.Fatal("expected revive:disable warning")
	}
}

func TestCheckSuppressionDirectives_GoLintIgnore(t *testing.T) {
	old := "package main\n"
	new_ := "package main\n\n//lint:ignore SA1000 false positive\nfunc main() {}\n"

	warnings := checkSuppressionDirectives("main.go", old, new_)
	if len(warnings) == 0 {
		t.Fatal("expected lint:ignore warning")
	}
}

func TestCheckSuppressionDirectives_PreExistingNotFlagged(t *testing.T) {
	// The suppression was already there -- should NOT warn
	content := "package main\n\n//nolint\nfunc foo() {}\n"

	warnings := checkSuppressionDirectives("main.go", content, content)
	if len(warnings) != 0 {
		t.Errorf("expected no warning for pre-existing suppression, got: %v", warnings)
	}
}

func TestCheckSuppressionDirectives_NoSuppression(t *testing.T) {
	old := "package main\n\nfunc foo() {}\n"
	new_ := "package main\n\nfunc bar() {}\n"

	warnings := checkSuppressionDirectives("main.go", old, new_)
	if len(warnings) != 0 {
		t.Errorf("expected no warning for clean code, got: %v", warnings)
	}
}

func TestCheckSuppressionDirectives_EmptyContent(t *testing.T) {
	warnings := checkSuppressionDirectives("main.go", "", "")
	if len(warnings) != 0 {
		t.Errorf("expected no warning for empty content, got: %v", warnings)
	}
}

func TestCheckSuppressionDirectives_MultipleAdded(t *testing.T) {
	old := "package main\n"
	new_ := "package main\n\n//nolint\nfunc foo() {}\n\n//nolint\nfunc bar() {}\n"

	warnings := checkSuppressionDirectives("main.go", old, new_)
	if len(warnings) == 0 {
		t.Fatal("expected warnings for multiple nolint additions")
	}
	if !strings.Contains(warnings[0], "2 suppression") {
		t.Errorf("expected count of 2, got: %s", warnings[0])
	}
}

func TestCheckSuppressionDirectives_JavaSuppressWarnings(t *testing.T) {
	old := "public class Foo {\n}\n"
	new_ := "public class Foo {\n    @SuppressWarnings(\"unchecked\")\n    void bar() {}\n}\n"

	warnings := checkSuppressionDirectives("Foo.java", old, new_)
	if len(warnings) == 0 {
		t.Fatal("expected @SuppressWarnings warning for .java")
	}
	if !strings.Contains(warnings[0], "@SuppressWarnings") {
		t.Errorf("expected '@SuppressWarnings' in warning, got: %s", warnings[0])
	}
}

func TestCheckSuppressionDirectives_RubyRubocopDisable(t *testing.T) {
	old := "def foo\nend\n"
	new_ := "# rubocop:disable Style/StringLiterals\ndef foo\nend\n"

	warnings := checkSuppressionDirectives("foo.rb", old, new_)
	if len(warnings) == 0 {
		t.Fatal("expected rubocop:disable warning for .rb")
	}
	if !strings.Contains(warnings[0], "rubocop:disable") {
		t.Errorf("expected 'rubocop:disable' in warning, got: %s", warnings[0])
	}
}

func TestCheckSuppressionDirectives_LineNumberInWarning(t *testing.T) {
	old := "package main\n\nfunc foo() {}\n"
	new_ := "package main\n\n//nolint\nfunc foo() {}\n"

	warnings := checkSuppressionDirectives("main.go", old, new_)
	if len(warnings) == 0 {
		t.Fatal("expected warning")
	}
	if !strings.Contains(warnings[0], "line 3") {
		t.Errorf("expected line number in warning, got: %s", warnings[0])
	}
}

func TestLangInList(t *testing.T) {
	langs := []Language{LangGo, LangPython}
	if !langInList(langs, LangGo) {
		t.Error("expected LangGo to be in list")
	}
	if langInList(langs, LangJSTS) {
		t.Error("expected LangJSTS to NOT be in list")
	}
	if langInList(nil, LangGo) {
		t.Error("expected empty list to return false")
	}
}

// Regression for #1500: bare '# type: ignore' followed by explanatory text
// is mypy's documented recommended form and must be classified bare
// (reportable); only bracket error codes (# type: ignore[return-value])
// make it scoped.
func TestIsBareSuppressionTypeIgnoreWithExplanation(t *testing.T) {
	if !isBareSuppression("x = f()  # type: ignore old API returns Any", "# type: ignore", true) {
		t.Fatal("type: ignore with explanation text must be bare (reportable)")
	}
	if isBareSuppression("x = f()  # type: ignore[return-value]", "# type: ignore", true) {
		t.Fatal("type: ignore[code] is scoped and must not be reported")
	}
	if !isBareSuppression("x = f()  # type: ignore", "# type: ignore", true) {
		t.Fatal("bare type: ignore with nothing after must stay bare")
	}
}

// TestCheckSuppressionDirectives_StringLiteralNotCounted pins #1778 case 3:
// a directive keyword inside a string literal ("eslint-disable" as a banner
// value) must not count - the old counter produced a warning with no line
// to point at.
func TestCheckSuppressionDirectives_StringLiteralNotCounted(t *testing.T) {
	old := "const banner = 'x';\n"
	new_ := "const banner = 'eslint-disable';\n"
	warnings := checkSuppressionDirectives("app.js", old, new_)
	if len(warnings) != 0 {
		t.Fatalf("string-literal keyword must not warn, got: %v", warnings)
	}
}

// TestCheckSuppressionDirectives_VueSfcCovered pins #1778 case 4: .vue
// single-file components carry eslint-disable inside <script> - previously
// excluded entirely (LangMarkup).
func TestCheckSuppressionDirectives_VueSfcCovered(t *testing.T) {
	old := "<script>\nexport default {}\n</script>\n"
	new_ := "<script>\n// eslint-disable\nexport default {}\n</script>\n"
	warnings := checkSuppressionDirectives("App.vue", old, new_)
	if len(warnings) == 0 {
		t.Fatal(".vue <script> eslint-disable must be detected")
	}
}

// #3607: prose-in-comment false positives. A policy comment that FORBIDS the
// directive is the correct thing for an agent to write, yet the old
// matchRidesComment gate passed it (the comment marker is exactly what makes
// the gate pass) and isBareSuppression returned true unconditionally for
// requiresRule=false JS/markup patterns. The prose-tail check must skip these.
func TestCheckSuppressionDirectives_ProseInComment3607(t *testing.T) {
	tests := []struct {
		name    string
		fp      string
		newCont string
		want    bool // wantWarn: whether a suppression warning is expected
		reason  string
	}{
		{
			// #3607 scenario A: JS block comment policy statement.
			name:    "js policy block comment",
			fp:      "src/policy.js",
			newCont: "/* do not use eslint-disable in this repo */\nconst x = 1;\n",
			want:    false,
			reason:  "policy comment must not be flagged as adding a suppression",
		},
		{
			// #3607 scenario C: HTML comment prose mention.
			name:    "html comment prose",
			fp:      "index.html",
			newCont: "<!-- we avoid eslint-disable here -->\n<div>x</div>\n",
			want:    false,
			reason:  "prose mention in HTML comment must not be flagged",
		},
		{
			// Regression: real bare directive still fires.
			name:    "js real bare directive",
			fp:      "src/a.js",
			newCont: "/* eslint-disable */\nconst x = 1;\n",
			want:    true,
			reason:  "bare eslint-disable must still be flagged",
		},
		{
			// Regression: scoped rule list still fires (flag-all for requiresRule=false).
			name:    "js scoped rule list",
			fp:      "src/a.js",
			newCont: "/* eslint-disable no-console, no-alert */\nconsole.log(1);\n",
			want:    true,
			reason:  "scoped eslint-disable is still a block-level blanket suppression",
		},
		{
			// Regression: -next-line variant suffix still fires.
			name:    "js next-line variant",
			fp:      "src/a.ts",
			newCont: "// eslint-disable-next-line no-console\nconsole.log(1);\n",
			want:    true,
			reason:  "variant suffix must still be flagged",
		},
		{
			// Regression guard: Go/Python prose-tail unaffected (their comment
			// markers are part of the pattern itself; proseTailCheck is off).
			name:    "go real revive directive",
			fp:      "a.go",
			newCont: "package a\n\nfunc f() {} // revive:disable\n",
			want:    true,
			reason:  "real Go revive:disable must still be flagged",
		},
		{
			// Known residual (#3607 scenario B), pinned deliberately: the same
			// text in a REAL comment is a bare noqa for flake8, so this stays
			// flagged until lexical docstring/string detection exists.
			name:    "py docstring noqa prose (residual)",
			fp:      "m.py",
			newCont: "def f():\n    \"\"\"Use # noqa only as last resort.\"\"\"\n    pass\n",
			want:    true,
			reason:  "accepted residual per #3607: indistinguishable from a real bare noqa without lexical parsing",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			warnings := checkSuppressionDirectives(tc.fp, "", tc.newCont)
			if tc.want && len(warnings) == 0 {
				t.Fatalf("expected warning (%s), got none", tc.reason)
			}
			if !tc.want && len(warnings) > 0 {
				t.Fatalf("expected NO warning (%s), got: %v", tc.reason, warnings)
			}
		})
	}
}

// suppressionProseTail unit coverage for the #3607 tail classifier.
func TestSuppressionProseTail(t *testing.T) {
	cases := []struct {
		line    string
		matched string
		prose   bool
	}{
		{"/* eslint-disable */", "eslint-disable", false},
		{"/* eslint-disable no-console */", "eslint-disable", false},
		{"/* eslint-disable no-console, no-alert */", "eslint-disable", false},
		{"// eslint-disable-next-line no-console", "eslint-disable", false},
		{"// eslint-disable-line", "eslint-disable", false},
		{"/* do not use eslint-disable in this repo */", "eslint-disable", true},
		{"<!-- we avoid eslint-disable here -->", "eslint-disable", true},
		{"<!-- do not add stylelint-disable -->", "stylelint-disable", true},
		{"<!-- do not add eslint-disable -->", "eslint-disable", true}, // prose precedes keyword
		{"// above all, never eslint-disable", "eslint-disable", true}, // prose precedes, line-comment form
		{"/* eslint-disable In This Repo */", "eslint-disable", true},
		{"/* eslint-disable eqeqeq */", "eslint-disable", true}, // documented trade-off: hyphenless rule code reads as prose
	}
	for _, c := range cases {
		if got := suppressionProseTail(c.line, c.matched); got != c.prose {
			t.Errorf("suppressionProseTail(%q, %q) = %v, want %v", c.line, c.matched, got, c.prose)
		}
	}
}

package agent

import (
	"strings"
	"testing"
)

// #2904: checkJSTSAntiPatterns was dead code (never registered) and had no
// comment/string stripping. These tests pin both: the registry wiring (so it
// can never silently become dead code again) and the stripping behavior.

// TestJSTSAntiPatternRegisteredInWriteIntegrity pins the registry wiring.
func TestJSTSAntiPatternRegisteredInWriteIntegrity(t *testing.T) {
	for _, c := range allChecks {
		if c.Name == "jsts-antipattern" {
			found := false
			for _, lang := range c.Langs {
				if lang == LangJSTS {
					found = true
				}
			}
			if !found {
				t.Fatalf("jsts-antipattern registered without LangJSTS: %v", c.Langs)
			}
			if c.Run == nil {
				t.Fatal("jsts-antipattern registered with nil Run")
			}
			return
		}
	}
	t.Fatal("jsts-antipattern not found in allChecks - dead code regression (#2904)")
}

// TestJSTSAntiPatternCommentAndStringMentionsNotFlagged covers defect 2:
// mentions of anti-patterns inside comments or string literals must not be
// reported as newly introduced anti-patterns.
func TestJSTSAntiPatternCommentAndStringMentionsNotFlagged(t *testing.T) {
	cases := []struct {
		name string
		new  string
	}{
		{
			name: "comment mentions var",
			new:  "// migrate var to let across the module\nconst x = 1;\n",
		},
		{
			name: "string mentions : any",
			new:  "expect(msg).toContain(\": any\");\n",
		},
		{
			name: "string mentions loose equality",
			new:  "const desc = \"a == b is loose equality\";\n",
		},
		{
			name: "string mentions ts-ignore",
			new:  "const hint = \"@ts-ignore is discouraged\";\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if w := checkJSTSAntiPatterns("src/app.ts", "", tc.new); w != "" {
				t.Fatalf("expected no warning for comment/string mention, got:\n%s", w)
			}
		})
	}
}

// TestJSTSAntiPatternCommentDirectiveStillFlagged: @ts-* directives are
// comment-form by nature - they must still be detected in comments (strings-
// only view keeps comments) while string mentions stay silent.
func TestJSTSAntiPatternCommentDirectiveStillFlagged(t *testing.T) {
	if w := checkJSTSAntiPatterns("src/app.ts", "", "// @ts-ignore\nconst x = 1;\n"); w == "" {
		t.Fatal("expected @ts-ignore warning for comment directive, got none")
	}
}

// TestJSTSAntiPatternRealCodeStillFlagged ensures stripping did not neuter
// detection of actual code anti-patterns.
func TestJSTSAntiPatternRealCodeStillFlagged(t *testing.T) {
	new := "var counter = 1;\nif (a == b) {}\n"
	w := checkJSTSAntiPatterns("src/app.ts", "", new)
	if w == "" {
		t.Fatal("expected var + loose-equality warnings for real code, got none")
	}
	if !strings.Contains(w, "var declaration") {
		t.Errorf("warning missing 'var declaration':\n%s", w)
	}
	if !strings.Contains(w, "loose equality") {
		t.Errorf("warning missing 'loose equality':\n%s", w)
	}
}

// TestJSTSAntiPatternDeltaStillGated: fixing code (removing an anti-pattern)
// must not warn even though the string form still mentions it.
func TestJSTSAntiPatternDeltaStillGated(t *testing.T) {
	old := "var counter = 1;\n"
	new := "// removed var counter\nlet counter = 1;\n"
	if w := checkJSTSAntiPatterns("src/app.ts", old, new); w != "" {
		t.Fatalf("expected no warning when var count decreases, got:\n%s", w)
	}
}

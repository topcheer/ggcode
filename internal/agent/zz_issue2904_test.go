package agent

import (
	"strings"
	"testing"
)

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

func TestJSTSAntiPatternCommentDirectiveStillFlagged(t *testing.T) {
	if w := checkJSTSAntiPatterns("src/app.ts", "", "// @ts-ignore\nconst x = 1;\n"); w == "" {
		t.Fatal("expected @ts-ignore warning for comment directive, got none")
	}
}

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

func TestJSTSAntiPatternDeltaStillGated(t *testing.T) {
	old := "var counter = 1;\n"
	new := "// removed var counter\nlet counter = 1;\n"
	if w := checkJSTSAntiPatterns("src/app.ts", old, new); w != "" {
		t.Fatalf("expected no warning when var count decreases, got:\n%s", w)
	}
}

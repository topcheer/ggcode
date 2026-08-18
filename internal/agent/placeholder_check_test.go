package agent

import (
	"strings"
	"testing"
)

func TestCheckPlaceholderCode_GoPanic(t *testing.T) {
	old := "package main\n\nfunc process() {\n\treturn\n}\n"
	new := "package main\n\nfunc process() {\n\tpanic(\"not implemented\")\n}\n"

	warnings := checkPlaceholderCode("process.go", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected placeholder warning for panic(\"not implemented\")")
	}
	if !strings.Contains(warnings[0], "panic: not implemented") {
		t.Errorf("warning should mention panic: not implemented, got: %s", warnings[0])
	}
}

func TestCheckPlaceholderCode_PythonNotImplemented(t *testing.T) {
	old := "def process():\n    pass\n"
	new := "def process():\n    raise NotImplementedError\n"

	warnings := checkPlaceholderCode("process.py", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected placeholder warning for NotImplementedError")
	}
	if !strings.Contains(warnings[0], "NotImplementedError") {
		t.Errorf("warning should mention NotImplementedError, got: %s", warnings[0])
	}
}

func TestCheckPlaceholderCode_JSThrow(t *testing.T) {
	old := "function process() {\n  return null;\n}\n"
	new := "function process() {\n  throw new Error(\"not implemented\");\n}\n"

	warnings := checkPlaceholderCode("process.ts", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected placeholder warning for throw new Error")
	}
}

func TestCheckPlaceholderCode_PreExistingNotFlagged(t *testing.T) {
	// panic("not implemented") already in old content - should NOT be flagged
	old := "package main\n\nfunc process() {\n\tpanic(\"not implemented\")\n}\n"
	new := old // no change

	warnings := checkPlaceholderCode("process.go", old, new)
	// Same count - should not flag
	for _, w := range warnings {
		if strings.Contains(w, "panic") {
			t.Errorf("pre-existing placeholder should not be flagged: %s", w)
		}
	}
}

func TestCheckPlaceholderCode_RemovedPlaceholderNotFlagged(t *testing.T) {
	// Agent REMOVES a placeholder - should definitely not be flagged
	old := "package main\n\nfunc process() {\n\tpanic(\"not implemented\")\n}\n"
	new := "package main\n\nfunc process() {\n\tfmt.Println(\"done\")\n}\n"

	warnings := checkPlaceholderCode("process.go", old, new)
	if len(warnings) > 0 {
		t.Errorf("removing a placeholder should not produce warnings, got: %v", warnings)
	}
}

func TestCheckPlaceholderCode_VagueTODO(t *testing.T) {
	old := "package main\n\nfunc process() {\n\treturn\n}\n"
	new := "package main\n\n// TODO: implement this\nfunc process() {\n\treturn\n}\n"

	warnings := checkPlaceholderCode("process.go", old, new)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "TODO") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected vague TODO warning, got: %v", warnings)
	}
}

func TestCheckPlaceholderCode_SpecificTODONotFlagged(t *testing.T) {
	// A specific, actionable TODO should NOT be flagged
	old := "package main\n\nfunc process() {\n\treturn\n}\n"
	new := "package main\n\n// TODO: add error handling for nil pointer edge case\nfunc process() {\n\treturn\n}\n"

	warnings := checkPlaceholderCode("process.go", old, new)
	for _, w := range warnings {
		if strings.Contains(w, "TODO") {
			t.Errorf("specific TODO should not be flagged, got: %s", w)
		}
	}
}

func TestCheckPlaceholderCode_TestFileSkipped(t *testing.T) {
	old := "package main\n\nfunc TestProcess(t *testing.T) {\n\treturn\n}\n"
	new := "package main\n\nfunc TestProcess(t *testing.T) {\n\tpanic(\"not implemented\")\n}\n"

	warnings := checkPlaceholderCode("process_test.go", old, new)
	for _, w := range warnings {
		if strings.Contains(w, "panic") {
			t.Errorf("test files should be skipped for placeholder detection, got: %s", w)
		}
	}
}

func TestCheckPlaceholderCode_RustTodoMacro(t *testing.T) {
	old := "fn process() {\n    return;\n}\n"
	new := "fn process() {\n    todo!()\n}\n"

	warnings := checkPlaceholderCode("process.rs", old, new)
	if len(warnings) == 0 {
		t.Fatal("expected placeholder warning for todo!()")
	}
}

func TestCheckPlaceholderCode_EmptyFile(t *testing.T) {
	warnings := checkPlaceholderCode("process.go", "", "")
	if len(warnings) != 0 {
		t.Errorf("empty content should produce no warnings, got: %v", warnings)
	}
}

func TestCheckPlaceholderCode_MultipleNew(t *testing.T) {
	old := "package main\n\nfunc a() {}\nfunc b() {}\n"
	new := "package main\n\nfunc a() {\n\tpanic(\"not implemented\")\n}\nfunc b() {\n\tpanic(\"TODO\")\n}\n"

	warnings := checkPlaceholderCode("process.go", old, new)
	if len(warnings) < 2 {
		t.Fatalf("expected at least 2 placeholder warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckPlaceholderCode_WarningCap(t *testing.T) {
	// Introduce many different placeholders
	old := "package main\n"
	new := `package main

func a() { panic("not implemented") }
func b() { panic("TODO") }
func c() { panic("unimplemented") }
func d() { panic("placeholder") }
func e() { panic("stub") }
`

	warnings := checkPlaceholderCode("process.go", old, new)
	if len(warnings) > maxPlaceholderWarnings {
		t.Errorf("expected at most %d warnings, got %d", maxPlaceholderWarnings, len(warnings))
	}
}

// Integration: verify placeholder check works through checkWriteIntegrity

func TestCheckWriteIntegrity_NoPlaceholderWarningForValidCode(t *testing.T) {
	goodGo := "package main\n\nimport \"fmt\"\n\nfunc process() {\n\tfmt.Println(\"hello\")\n}\n"
	warning := checkWriteIntegrity("main.go", "", goodGo)
	if warning != "" {
		t.Errorf("expected no warning for valid code, got: %s", warning)
	}
}

// TestPlaceholder_MovedPatternDetected pins fix #175: removing panic("TODO")
// in one function while adding it in another (net 0) must be reported.
func TestPlaceholder_MovedPatternDetected(t *testing.T) {
	old := "package main\nfunc a() { panic(\"TODO\") }\nfunc b() {}\n"
	newC := "package main\nfunc a() { println(\"done\") }\nfunc b() { panic(\"TODO\") }\n"
	w := checkPlaceholderCode("a.go", old, newC)
	found := false
	for _, s := range w {
		if strings.Contains(s, "panic") || strings.Contains(s, "placeholder") || strings.Contains(s, "TODO") {
			found = true
		}
	}
	if !found {
		t.Fatalf("moved panic(TODO) must be flagged: %v", w)
	}
}

// TestPlaceholder_CommentAndDocstringMentionsNotFlagged pins fix #730 (same
// family as #723/#728): MENTIONS of placeholder patterns inside comments,
// block-comment bodies, trailing comments, and Python docstrings must NOT
// count as introduced placeholders.
func TestPlaceholder_CommentAndDocstringMentionsNotFlagged(t *testing.T) {
	cases := []struct {
		name string
		file string
		code string
	}{
		{"Go full-line comment", "main.go", "package main\n\n// legacy path used to panic(\"not implemented\") before v2\nfunc f() {}\n"},
		{"Go trailing comment", "main.go", "package main\n\nfunc f() { x := 1 } // no longer panic(\"not implemented\")\n"},
		{"Go block-comment body", "main.go", "package main\n\n/*\nlegacy path: panic(\"not implemented\")\nwas removed in v2\n*/\nfunc f() {}\n"},
		{"Python # comment", "app.py", "# for unsupported ops we raise NotImplementedError here\nvalue = 1\n"},
		{"Python single-line docstring", "app.py", "def f():\n    \"\"\"Will raise NotImplementedError if unsupported.\"\"\"\n    return 1\n"},
		{"Python multi-line docstring", "app.py", "def f():\n    \"\"\"\n    Raises NotImplementedError if unsupported.\n    See docs for details.\n    \"\"\"\n    return 1\n"},
		{"JS comment mentions throw", "app.ts", "// throws new Error(\"not implemented\") on purpose? no, removed\nexport const f = () => 1;\n"},
	}
	for _, c := range cases {
		for _, w := range checkPlaceholderCode(c.file, "", c.code) {
			t.Errorf("%s (issue #730 FP): expected no warnings, got: %s", c.name, w)
		}
	}
}

// TestPlaceholder_RealStubsStillFlaggedAfterStrip is the control side of fix
// #730: real (non-comment) placeholder code must still be detected after
// comment stripping - including the quote-containing Python pattern whose
// literal is preserved by pyStripCommentsKeepStrings.
func TestPlaceholder_RealStubsStillFlaggedAfterStrip(t *testing.T) {
	cases := []struct {
		name    string
		file    string
		code    string
		wantSub string
	}{
		{"Go panic stub", "main.go", "package main\n\nfunc f() {\n\tpanic(\"not implemented\")\n}\n", "panic"},
		{"Python raise stub", "app.py", "def f(x):\n    raise NotImplementedError\n", "NotImplementedError"},
		{"Python quote-containing stub", "app.py", "def f(x):\n    raise Exception(\"TODO\")\n", "TODO"},
		{"JS throw stub", "app.ts", "function f() {\n  throw new Error(\"not implemented\");\n}\n", "throw"},
		// Real placeholder AFTER a docstring: proves cross-line docstring state
		// does not swallow subsequent code lines.
		{"Stub after docstring", "app.py", "def f():\n    \"\"\"Doc.\"\"\"\n    raise NotImplementedError\n", "NotImplementedError"},
		// Real placeholder inside what looks like comment territory: code line
		// with trailing comment stripped, placeholder intact.
		{"Go code then trailing comment", "main.go", "package main\n\nfunc f() { panic(\"TODO\") } // hint\n", "TODO"},
	}
	for _, c := range cases {
		found := false
		for _, w := range checkPlaceholderCode(c.file, "", c.code) {
			if strings.Contains(w, c.wantSub) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: expected warning mentioning %q, got none", c.name, c.wantSub)
		}
	}
}

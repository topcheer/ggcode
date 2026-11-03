package agent

import (
	"strings"
	"testing"
)

func TestLooksLikeCode_ParenthesizedProseNotCode(t *testing.T) {
	prose := []string{
		"The parser handles both cases (a) and (b).",
		"Errors are wrapped (see above)...",
		"Use with care (especially inside hot loops).",
		"Note: this also applies to nested calls (see #152).",
	}
	for _, line := range prose {
		if looksLikeCode(line) {
			t.Errorf("looksLikeCode(%q) = true, want false (plain prose)", line)
		}
	}
}

func TestLooksLikeCode_CallShapeStillCode(t *testing.T) {
	code := []string{
		"fmt.Sprintf(\"%d\", x)",
		"foo(bar)",
		"process(x, y)",
	}
	for _, line := range code {
		if !looksLikeCode(line) {
			t.Errorf("looksLikeCode(%q) = false, want true (call shape)", line)
		}
	}
}

func TestCheckCommentedCodeBlocks_DocCommentParenProseNotFlagged(t *testing.T) {
	old := "package main\n\nfunc f() {}\n"
	newContent := `package main

// The parser handles both cases (a) and (b).
// Errors are wrapped (see above)...
// Use with care (especially inside hot loops).
func f() {}
`
	warnings := checkCommentedCodeBlocks("main.go", old, newContent)
	if len(warnings) != 0 {
		t.Errorf("doc-comment prose flagged as commented-out code: %v", warnings)
	}
}

func TestCheckCommentedCodeBlocks_RealCommentedCodeStillFlagged(t *testing.T) {
	old := "package main\n\nfunc f() {\n\t_ = foo()\n\treturn\n}\n"
	newContent := `package main

func f() {
	// x = foo()
	// y = bar(x)
	// z = baz(y)
	return
}
`
	warnings := checkCommentedCodeBlocks("main.go", old, newContent)
	if len(warnings) == 0 {
		t.Error("expected warning for real commented-out code block")
	}
}

func TestCheckCommentedCodeBlocks_DuplicateBlockCopyDetected(t *testing.T) {
	block := "// x = foo()\n// y = bar(x)\n// z = baz(y)"
	old := "package main\n\nfunc a() {\n\treturn\n}\n\n" + block + "\n\nfunc b() {\n\treturn\n}\n"

	newContent := "package main\n\nfunc a() {\n\treturn\n}\n\n" + block + "\n\nfunc b() {\n\treturn\n}\n\n" + block + "\n"
	warnings := checkCommentedCodeBlocks("main.go", old, newContent)
	if len(warnings) == 0 {
		t.Error("#526 D2: duplicated commented block copy was swallowed by set-semantics delta")
	}
}

func TestCheckCommentedCodeBlocks_MultisetNoFalsePositiveOnIdentical(t *testing.T) {
	content := "package main\n\n// x = foo()\n// y = bar(x)\n// z = baz(y)\nfunc f() {}\n"
	warnings := checkCommentedCodeBlocks("main.go", content, content)
	if len(warnings) != 0 {
		t.Errorf("identical content must produce no warnings, got: %v", warnings)
	}
}

func TestCheckDeprecatedAPI_CommentMentionDoesNotSwallowNewCall(t *testing.T) {
	old := `package main

import "strings"

// TODO: remove strings.Title usage from this file.
func normalize(s string) string { return s }
`
	newContent := `package main

import "strings"

// TODO: remove strings.Title usage from this file.
func normalize(s string) string { return strings.Title(s) }
`
	got := checkDeprecatedAPI("main.go", old, newContent)
	if !strings.Contains(got, "strings.Title") {
		t.Errorf("#527 Bug A: new strings.Title call swallowed by old comment mention; got: %q", got)
	}
}

func TestCheckDeprecatedAPI_MultisetDeltaCountsOccurrences(t *testing.T) {
	old := `package main

import "strings"

func a() { _ = strings.Title("x") }
func b() {}
`
	newContent := `package main

import "strings"

func a() { _ = strings.Title("x") }
func b() { _ = strings.Title("y") }
`
	got := checkDeprecatedAPI("main.go", old, newContent)
	if !strings.Contains(got, "strings.Title") {
		t.Errorf("second strings.Title occurrence must warn, got: %q", got)
	}
}

func TestCheckDeprecatedAPI_ImportAliasMatched(t *testing.T) {
	old := "package main\n\nfunc f() {}\n"
	newContent := `package main

import t "strings"

func f() { _ = t.Title("x") }
`
	got := checkDeprecatedAPI("main.go", old, newContent)
	if !strings.Contains(got, "strings.Title") {
		t.Errorf("#527 Bug B: aliased t.Title call not matched to canonical strings.Title rule; got: %q", got)
	}
}

func TestCheckDeprecatedAPI_AliasNoFalsePositiveOnUnrelatedPkg(t *testing.T) {
	old := "package main\n\nfunc f() {}\n"
	newContent := `package main

import t "strconv"

func f() { _ = t.Title("x") }
`
	got := checkDeprecatedAPI("main.go", old, newContent)
	if strings.Contains(got, "deprecated") {
		t.Errorf("strconv.Title (undefined but non-deprecated pkg) must not warn, got: %q", got)
	}
}

func TestConfigSyntaxCheck_BinaryPlistSkipped(t *testing.T) {
	content := "bplist00\x00\x00\x00\x08\x00\x00\x00\x00\x00\x00\x01\x01\x00\x00\x00\x00\x00\x00\x00\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x09"
	if got := configSyntaxCheck("com.example.app.plist", content); got != "" {
		t.Errorf("binary plist must be skipped, got warning: %q", got)
	}
}

func TestConfigSyntaxCheck_XMLPlistStillValidated(t *testing.T) {
	broken := "<?xml version=\"1.0\"?><plist version=\"1.0\"><dict><key>a</key"
	if got := configSyntaxCheck("com.example.app.plist", broken); got == "" {
		t.Error("truncated XML plist must still warn")
	}
	valid := "<?xml version=\"1.0\"?><plist version=\"1.0\"><dict><key>a</key><string>b</string></dict></plist>"
	if got := configSyntaxCheck("com.example.app.plist", valid); got != "" {
		t.Errorf("valid XML plist must not warn, got: %q", got)
	}
}

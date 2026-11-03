package agent

import (
	"strings"
	"testing"
)

func TestCheckUnicodeChars_SmartQuotes(t *testing.T) {

	old := "x := \"hello\"\n"
	new_ := "x := “hello”\n"

	result := checkUnicodeChars("main.go", old, new_)
	if result == "" {
		t.Fatal("expected Unicode warning for smart quotes")
	}
	if !strings.Contains(result, "left double quote") {
		t.Errorf("should mention left double quote, got: %s", result)
	}
	if !strings.Contains(result, "right double quote") {
		t.Errorf("should mention right double quote, got: %s", result)
	}
}

func TestCheckUnicodeChars_NonBreakingSpace(t *testing.T) {

	old := "func main() {\n    return\n}\n"
	new_ := "func main() {\n\u00a0   return\n}\n"

	result := checkUnicodeChars("main.go", old, new_)
	if result == "" {
		t.Fatal("expected Unicode warning for non-breaking space")
	}
	if !strings.Contains(result, "non-breaking space") {
		t.Errorf("should mention non-breaking space, got: %s", result)
	}
	if !strings.Contains(result, "U+00A0") {
		t.Errorf("should include codepoint U+00A0, got: %s", result)
	}
}

func TestCheckUnicodeChars_ZeroWidthSpace(t *testing.T) {

	old := "var myVar int\n"
	new_ := "var my\u200bVar int\n"

	result := checkUnicodeChars("main.go", old, new_)
	if result == "" {
		t.Fatal("expected Unicode warning for zero-width space")
	}
	if !strings.Contains(result, "zero-width space") {
		t.Errorf("should mention zero-width space, got: %s", result)
	}
	if !strings.Contains(result, "remove it") {
		t.Errorf("should say to remove zero-width space, got: %s", result)
	}
}

func TestCheckUnicodeChars_DeltaDetection(t *testing.T) {

	old := "// This is “clever” code\n\n"
	new_ := "// This is “clever” code with more\n\n"

	result := checkUnicodeChars("main.go", old, new_)
	if result != "" {
		t.Errorf("expected no warning when smart quotes are pre-existing, got: %s", result)
	}
}

func TestCheckUnicodeChars_PartialDeltaDetection(t *testing.T) {

	old := "// This is “clever”\n\n"
	new_ := "// This is “clever” and “even better”\n\n"

	result := checkUnicodeChars("main.go", old, new_)
	if result == "" {
		t.Fatal("expected warning for newly introduced smart quotes")
	}
	if !strings.Contains(result, "left double quote") {
		t.Errorf("should mention left double quote, got: %s", result)
	}
}

func TestCheckUnicodeChars_NoProblematicChars(t *testing.T) {

	old := ""
	new_ := "package main\n\nfunc main() {\n\tprintln(\"hello world\")\n}\n"

	result := checkUnicodeChars("main.go", old, new_)
	if result != "" {
		t.Errorf("expected no warning for clean ASCII content, got: %s", result)
	}
}

func TestCheckUnicodeChars_EmDashWarning(t *testing.T) {

	old := "// This is a comment\n"
	new_ := "// This is a comment \u2014 with an em-dash\n"

	result := checkUnicodeChars("main.go", old, new_)
	if result == "" {
		t.Fatal("expected warning for em-dash")
	}
	if !strings.Contains(result, "em-dash") {
		t.Errorf("should mention em-dash, got: %s", result)
	}
	if !strings.Contains(result, "U+2014") {
		t.Errorf("should include codepoint U+2014, got: %s", result)
	}
}

func TestCheckUnicodeChars_BOM(t *testing.T) {

	old := "var data = []byte{}\n"
	new_ := "var data = []byte{\ufeff}\n"

	result := checkUnicodeChars("main.go", old, new_)
	if result == "" {
		t.Fatal("expected warning for BOM character")
	}
	if !strings.Contains(result, "BOM") {
		t.Errorf("should mention BOM, got: %s", result)
	}
}

func TestCheckUnicodeChars_FullWidthParen(t *testing.T) {

	old := "fmt.Println(x)\n"
	new_ := "fmt.Println\uff08x\uff09\n"

	result := checkUnicodeChars("main.go", old, new_)
	if result == "" {
		t.Fatal("expected warning for fullwidth parens")
	}
	if !strings.Contains(result, "fullwidth") {
		t.Errorf("should mention fullwidth, got: %s", result)
	}
}

func TestCheckUnicodeChars_LineNumber(t *testing.T) {

	old := "line1\nline2\nline3\n"
	new_ := "line1\nline2\nline3 ‘quoted’\n"

	result := checkUnicodeChars("test.go", old, new_)
	if result == "" {
		t.Fatal("expected warning")
	}
	if !strings.Contains(result, "line 3") {
		t.Errorf("should mention line 3, got: %s", result)
	}
}

func TestCheckUnicodeChars_WarningCap(t *testing.T) {

	old := ""
	var newContent strings.Builder
	newContent.WriteString("\u2018\u201c\u00a0\u200b\u2014\u2013\uff01\uff08\uff09\uff1b\uff1d\uff5b\uff5d\u00b7\u2026")

	result := checkUnicodeChars("main.go", old, newContent.String())
	if result == "" {
		t.Fatal("expected warning")
	}

	lines := strings.Split(result, "\n")
	typeLines := 0
	for _, l := range lines {
		if strings.Contains(l, " - ") && strings.Contains(l, "U+") {
			typeLines++
		}
	}
	if typeLines > maxUnicodeWarnings {
		t.Errorf("expected at most %d character types listed, got %d", maxUnicodeWarnings, typeLines)
	}

	if typeLines == 15 {
		t.Error("expected cap to limit displayed types, but all 15 shown")
	}
}

func TestCheckUnicodeChars_EmptyNewContent(t *testing.T) {
	result := checkUnicodeChars("main.go", "old content", "")
	if result != "" {
		t.Errorf("expected no warning for empty content, got: %s", result)
	}
}

func TestCheckUnicodeChars_IntegrationWithWriteIntegrity(t *testing.T) {

	old := "var x = \"hello\"\n"
	new_ := "var x = “hello”\n"

	result := checkWriteIntegrity("main.go", old, new_)
	if result == "" {
		t.Fatal("expected write integrity to catch Unicode chars")
	}

	if !strings.Contains(result, "Post-write integrity check") {
		t.Errorf("should have integrity check header, got: %s", result)
	}
}

func TestCheckUnicodeChars_CJKContextDowngraded(t *testing.T) {
	old := ""
	new_ := "s := \"x\"\n// 注释里的“中文引号”（全角括号）——省略号…\n"

	result := checkUnicodeChars("main.go", old, new_)
	if result == "" {
		t.Fatal("expected advisory note for CJK-context punctuation")
	}
	if strings.Contains(result, "Replace these with their ASCII equivalents") {
		t.Errorf("must not demand ASCII replacement for CJK-context punctuation: %s", result)
	}
	if !strings.Contains(result, "CJK") {
		t.Errorf("expected CJK context note, got: %s", result)
	}
	if !strings.Contains(result, "keep them as-is") {
		t.Errorf("expected keep-as-is guidance, got: %s", result)
	}
}

func TestCheckUnicodeChars_CJKInvisibleStillFlagged(t *testing.T) {
	result := checkUnicodeChars("main.go", "", "注释\u200b内容\n")
	if result == "" {
		t.Fatal("expected warning for zero-width char on CJK line")
	}
	if !strings.Contains(result, "remove it") {
		t.Errorf("invisible chars must keep removal directive even in CJK context: %s", result)
	}
}

func TestCheckUnicodeChars_MixedContextKeepsError(t *testing.T) {
	new_ := "// 中文“引号”\nx := “broken”\n"
	result := checkUnicodeChars("main.go", "", new_)
	if result == "" {
		t.Fatal("expected warning")
	}
	if !strings.Contains(result, "Replace these with their ASCII equivalents") {
		t.Errorf("pure-ASCII-line instances must keep replacement directive: %s", result)
	}
}

func TestUnicodeCheck_ExcludesProseFiles(t *testing.T) {
	for _, c := range allChecks {
		if c.Name != "unicode-check" {
			continue
		}
		if c.appliesTo(LangAny) {
			t.Error("unicode-check must not apply to LangAny (prose files)")
		}
		for _, lang := range []Language{LangGo, LangPython, LangJSTS, LangMarkup, LangConfig, LangRuby, LangJava} {
			if !c.appliesTo(lang) {
				t.Errorf("unicode-check must still apply to code language %d", lang)
			}
		}
		return
	}
	t.Fatal("unicode-check not found in registry")
}

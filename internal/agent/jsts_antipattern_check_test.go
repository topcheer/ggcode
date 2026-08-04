package agent

import (
	"strings"
	"testing"
)

func TestCheckJSTSAntiPatterns_LooseEquality(t *testing.T) {
	// == introduced
	old := "const x = 1;\n"
	new_ := "const x = 1;\nif (x == 2) {}\n"
	result := checkJSTSAntiPatterns("test.js", old, new_)
	if result == "" {
		t.Fatal("expected loose equality warning for .js")
	}
	if !strings.Contains(result, "loose equality") {
		t.Errorf("expected 'loose equality' in result, got: %s", result)
	}
}

func TestCheckJSTSAntiPatterns_StrictEqualityNoWarn(t *testing.T) {
	// === should not trigger
	old := "const x = 1;\n"
	new_ := "const x = 1;\nif (x === 2) {}\n"
	result := checkJSTSAntiPatterns("test.js", old, new_)
	if result != "" {
		t.Errorf("expected no warning for strict equality, got: %s", result)
	}
}

func TestCheckJSTSAntiPatterns_VarDeclaration(t *testing.T) {
	old := ""
	new_ := "var x = 1;\n"
	result := checkJSTSAntiPatterns("test.js", old, new_)
	if result == "" {
		t.Fatal("expected var declaration warning")
	}
	if !strings.Contains(result, "var declaration") {
		t.Errorf("expected 'var declaration' in result, got: %s", result)
	}
}

func TestCheckJSTSAntiPatterns_AnyTypeTSOnly(t *testing.T) {
	// any type should only warn in TS files
	old := ""
	new_ := "function f(x: any): void {}\n"

	// TS file - should warn
	tsResult := checkJSTSAntiPatterns("test.ts", old, new_)
	if tsResult == "" {
		t.Fatal("expected any-type warning for .ts")
	}
	if !strings.Contains(tsResult, "explicit any") {
		t.Errorf("expected 'explicit any' in result, got: %s", tsResult)
	}

	// JS file - should NOT warn
	jsResult := checkJSTSAntiPatterns("test.js", old, new_)
	if jsResult != "" {
		t.Errorf("expected no any-type warning for .js, got: %s", jsResult)
	}
}

func TestCheckJSTSAntiPatterns_PreExistingNoWarn(t *testing.T) {
	// Pre-existing == should not trigger if count didn't increase
	old := "if (x == 1) {}\n"
	new_ := "if (x == 1) {}\nconsole.log(x);\n"
	result := checkJSTSAntiPatterns("test.js", old, new_)
	if strings.Contains(result, "loose equality") {
		t.Errorf("should not warn about pre-existing ==, got: %s", result)
	}
}

func TestCheckJSTSAntiPatterns_NonJSFile(t *testing.T) {
	result := checkJSTSAntiPatterns("test.go", "", "var x = 1\nif x == 2 {}\n")
	if result != "" {
		t.Errorf("expected no warning for .go file, got: %s", result)
	}
}

func TestCheckJSTSAntiPatterns_ExemptDirs(t *testing.T) {
	result := checkJSTSAntiPatterns("node_modules/lib/test.js", "", "var x = 1;\n")
	if result != "" {
		t.Errorf("expected no warning for node_modules file, got: %s", result)
	}
}

func TestCheckJSTSAntiPatterns_EmptyContent(t *testing.T) {
	result := checkJSTSAntiPatterns("test.js", "", "")
	if result != "" {
		t.Errorf("expected no warning for empty content, got: %s", result)
	}
}

func TestCheckJSTSAntiPatterns_MultiplePatterns(t *testing.T) {
	old := ""
	new_ := "var x: any = 1;\nif (x == 2) {}\n"
	result := checkJSTSAntiPatterns("test.ts", old, new_)
	if result == "" {
		t.Fatal("expected warnings for multiple anti-patterns")
	}
	// Should contain at least 2 of the 3 patterns
	count := 0
	for _, s := range []string{"loose equality", "var declaration", "explicit any"} {
		if strings.Contains(result, s) {
			count++
		}
	}
	if count < 2 {
		t.Errorf("expected at least 2 anti-patterns detected, got %d: %s", count, result)
	}
}

func TestCheckJSTSAntiPatterns_TSX(t *testing.T) {
	result := checkJSTSAntiPatterns("Component.tsx", "", "var x = 1;\n")
	if result == "" {
		t.Fatal("expected var warning for .tsx")
	}
}

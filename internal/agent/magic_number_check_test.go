package agent

import (
	"strings"
	"testing"
)

func TestMagicNumbers_Comparison(t *testing.T) {
	old := "package main\n\nfunc check(x int) bool {\n\treturn true\n}\n"
	new := "package main\n\nfunc check(x int) bool {\n\treturn x > 100\n}\n"
	w := checkMagicNumbers("main.go", old, new)
	if w == "" {
		t.Fatal("expected magic number warning for x > 100")
	}
	if !strings.Contains(w, "100") {
		t.Errorf("should mention 100, got: %s", w)
	}
	if !strings.Contains(w, "comparison") {
		t.Errorf("should mention comparison context")
	}
}

func TestMagicNumbers_Assignment(t *testing.T) {
	old := "package main\n\nfunc config() {\n}\n"
	new := "package main\n\nfunc config() {\n\ttimeout := 30\n}\n"
	w := checkMagicNumbers("main.go", old, new)
	if w == "" {
		t.Fatal("expected magic number warning for timeout := 30")
	}
	if !strings.Contains(w, "30") {
		t.Errorf("should mention 30")
	}
}

func TestMagicNumbers_FunctionArg(t *testing.T) {
	old := "package main\n\nfunc setup() {\n}\n"
	new := "package main\n\nfunc setup() {\n\tmake([]int, 64)\n}\n"
	w := checkMagicNumbers("main.go", old, new)
	if w == "" {
		t.Fatal("expected magic number warning for make([]int, 64)")
	}
}

func TestMagicNumbers_SmallNumbersNotFlagged(t *testing.T) {
	old := "package main\n\nfunc check(x int) bool {\n\treturn true\n}\n"
	new := "package main\n\nfunc check(x int) bool {\n\treturn x > 2\n}\n"
	w := checkMagicNumbers("main.go", old, new)
	if w != "" {
		t.Errorf("number 2 should not be flagged, got: %s", w)
	}
}

func TestMagicNumbers_ZeroOneNotFlagged(t *testing.T) {
	old := "package main\n\nfunc check(x int) bool {\n\treturn true\n}\n"
	new := "package main\n\nfunc check(x int) bool {\n\treturn x > 0 || x == 1\n}\n"
	w := checkMagicNumbers("main.go", old, new)
	if w != "" {
		t.Errorf("0 and 1 should not be flagged, got: %s", w)
	}
}

func TestMagicNumbers_ConstNotFlagged(t *testing.T) {
	old := "package main\n\n"
	new := "package main\n\nconst maxRetries = 10\n"
	w := checkMagicNumbers("main.go", old, new)
	if w != "" {
		t.Errorf("const declarations should not be flagged, got: %s", w)
	}
}

func TestMagicNumbers_TestFileSkipped(t *testing.T) {
	old := "package main\n\nfunc check(x int) bool {\n\treturn true\n}\n"
	new := "package main\n\nfunc check(x int) bool {\n\treturn x > 100\n}\n"
	w := checkMagicNumbers("main_test.go", old, new)
	if w != "" {
		t.Errorf("test files should be skipped")
	}
}

func TestMagicNumbers_NonGoSkipped(t *testing.T) {
	w := checkMagicNumbers("main.py", "x = 1", "x = 100")
	if w != "" {
		t.Errorf("non-Go files should be skipped")
	}
}

func TestMagicNumbers_DeltaAware(t *testing.T) {
	// Both old and new have x > 100 -- no new occurrences, should not flag.
	src := "package main\n\nfunc check(x int) bool {\n\treturn x > 100\n}\n"
	w := checkMagicNumbers("main.go", src, src)
	if w != "" {
		t.Errorf("identical content should not produce warning, got: %s", w)
	}
}

func TestMagicNumbers_DeltaAwareSameValue(t *testing.T) {
	// Old has one "100", new has one "100" (just moved) -- should not flag.
	old := "package main\n\nfunc check(x int) bool {\n\treturn x > 100\n}\n"
	new := "package main\n\nfunc validate(x int) bool {\n\treturn x > 100\n}\n"
	w := checkMagicNumbers("main.go", old, new)
	// Same count of 100 -- delta-aware should skip.
	if w != "" && strings.Contains(w, "100") {
		t.Errorf("same count of value 100 should not be flagged, got: %s", w)
	}
}

func TestMagicNumbers_TypeConversionNotFlagged(t *testing.T) {
	old := "package main\n\nfunc conv(x int) {\n}\n"
	new := "package main\n\nfunc conv(x int) {\n\t_ = int64(x)\n}\n"
	w := checkMagicNumbers("main.go", old, new)
	// int64(x) is a type conversion, not a function call with magic number.
	if w != "" {
		t.Errorf("type conversion should not be flagged, got: %s", w)
	}
}

func TestMagicNumbers_MultipleCapped(t *testing.T) {
	old := "package main\n\nfunc f() {\n}\n"
	new := "package main\n\nfunc f() {\n\ta := 10\n\tb := 20\n\tc := 30\n\td := 40\n\te := 50\n\tg := 60\n}\n"
	w := checkMagicNumbers("main.go", old, new)
	if w == "" {
		t.Fatal("expected magic number warnings")
	}
	// Should cap at maxMagicWarnings (5).
	count := strings.Count(w, "assignment")
	if count > maxMagicWarnings {
		t.Errorf("should cap at %d warnings, got %d", maxMagicWarnings, count)
	}
}

func TestParseIntLit_Hex(t *testing.T) {
	n, err := parseIntLit("0xFF")
	if err != nil || n != 255 {
		t.Errorf("0xFF should be 255, got %d, err %v", n, err)
	}
}

func TestParseIntLit_Binary(t *testing.T) {
	n, err := parseIntLit("0b1010")
	if err != nil || n != 10 {
		t.Errorf("0b1010 should be 10, got %d, err %v", n, err)
	}
}

func TestParseIntLit_Octal(t *testing.T) {
	n, err := parseIntLit("0o17")
	if err != nil || n != 15 {
		t.Errorf("0o17 should be 15, got %d, err %v", n, err)
	}
}

func TestParseIntLit_UnderscoreSeparator(t *testing.T) {
	n, err := parseIntLit("1_000_000")
	if err != nil || n != 1000000 {
		t.Errorf("1_000_000 should be 1000000, got %d, err %v", n, err)
	}
}

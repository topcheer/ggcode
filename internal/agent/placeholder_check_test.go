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
	// panic("not implemented") already in old content — should NOT be flagged
	old := "package main\n\nfunc process() {\n\tpanic(\"not implemented\")\n}\n"
	new := old // no change

	warnings := checkPlaceholderCode("process.go", old, new)
	// Same count — should not flag
	for _, w := range warnings {
		if strings.Contains(w, "panic") {
			t.Errorf("pre-existing placeholder should not be flagged: %s", w)
		}
	}
}

func TestCheckPlaceholderCode_RemovedPlaceholderNotFlagged(t *testing.T) {
	// Agent REMOVES a placeholder — should definitely not be flagged
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
func TestCheckWriteIntegrity_PlaceholderWarning(t *testing.T) {
	old := "package main\n\nfunc process() {\n\treturn nil\n}\n"
	new := "package main\n\nfunc process() {\n\tpanic(\"not implemented\")\n}\n"

	warning := checkWriteIntegrity("main.go", old, new)
	if warning == "" {
		t.Fatal("expected integrity warning for placeholder code")
	}
	if !strings.Contains(warning, "placeholder") && !strings.Contains(warning, "stub") {
		t.Errorf("warning should mention placeholder/stub, got: %s", warning)
	}
}

func TestCheckWriteIntegrity_NoPlaceholderWarningForValidCode(t *testing.T) {
	goodGo := "package main\n\nimport \"fmt\"\n\nfunc process() {\n\tfmt.Println(\"hello\")\n}\n"
	warning := checkWriteIntegrity("main.go", "", goodGo)
	if warning != "" {
		t.Errorf("expected no warning for valid code, got: %s", warning)
	}
}

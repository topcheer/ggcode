package agent

import (
	"strings"
	"testing"
)

func TestCheckDebugStatements_ConsoleLogIntroduced(t *testing.T) {
	old := `function calc(a, b) {
	return a + b;
}
`
	updated := `function calc(a, b) {
	console.log("debug:", a, b);
	return a + b;
}
`
	warnings := checkDebugStatements("src/calc.js", old, updated)
	if len(warnings) == 0 {
		t.Fatal("expected debug warning for newly introduced console.log")
	}
	if !strings.Contains(warnings[0], "console.log") {
		t.Errorf("warning should mention console.log, got: %s", warnings[0])
	}
	if !strings.Contains(warnings[0], "remove") {
		t.Errorf("warning should suggest removal, got: %s", warnings[0])
	}
}

func TestCheckDebugStatements_PreExistingNotFlagged(t *testing.T) {
	// oldContent already has console.log — editing the file shouldn't re-flag it.
	old := `function calc(a, b) {
	console.log("debug:", a, b);
	return a + b;
}
`
	updated := `function calc(a, b) {
	console.log("debug:", a, b);
	return a + b + 1;
}
`
	warnings := checkDebugStatements("src/calc.js", old, updated)
	if len(warnings) != 0 {
		t.Errorf("expected no warning for pre-existing console.log, got: %v", warnings)
	}
}

func TestCheckDebugStatements_DebuggerStatement(t *testing.T) {
	old := `export function process(data) {
	return data.map(x => x * 2);
}
`
	updated := `export function process(data) {
	debugger;
	return data.map(x => x * 2);
}
`
	warnings := checkDebugStatements("src/process.ts", old, updated)
	if len(warnings) == 0 {
		t.Fatal("expected debug warning for debugger statement")
	}
	if !strings.Contains(warnings[0], "debugger") {
		t.Errorf("warning should mention debugger, got: %s", warnings[0])
	}
}

func TestCheckDebugStatements_NewFile(t *testing.T) {
	// New file (old="") with debug statements should be flagged.
	newContent := `function main() {
	console.log("starting...");
	console.log("done");
}
`
	warnings := checkDebugStatements("main.js", "", newContent)
	if len(warnings) == 0 {
		t.Fatal("expected debug warning for new file with console.log")
	}
}

func TestCheckDebugStatements_TestFileSkipped(t *testing.T) {
	// Test files should not trigger debug warnings.
	newContent := `describe("calc", () => {
	it("adds", () => {
		console.log("test debug");
		expect(1+1).toBe(2);
	});
});
`
	warnings := checkDebugStatements("calc.test.js", "", newContent)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for test file, got: %v", warnings)
	}
}

func TestCheckDebugStatements_PythonBreakpoint(t *testing.T) {
	old := `def process(data):
    return [x * 2 for x in data]
`
	updated := `def process(data):
    breakpoint()
    return [x * 2 for x in data]
`
	warnings := checkDebugStatements("process.py", old, updated)
	if len(warnings) == 0 {
		t.Fatal("expected debug warning for breakpoint()")
	}
}

func TestCheckDebugStatements_PHPDump(t *testing.T) {
	old := `<?php
function calc($a, $b) {
    return $a + $b;
}
`
	updated := `<?php
function calc($a, $b) {
    dd($a, $b);
    return $a + $b;
}
`
	warnings := checkDebugStatements("calc.php", old, updated)
	if len(warnings) == 0 {
		t.Fatal("expected debug warning for dd()")
	}
}

func TestCheckDebugStatements_RustDbg(t *testing.T) {
	old := `fn calc(a: i32, b: i32) -> i32 {
    a + b
}
`
	updated := `fn calc(a: i32, b: i32) -> i32 {
    dbg!(&a);
    a + b
}
`
	warnings := checkDebugStatements("calc.rs", old, updated)
	if len(warnings) == 0 {
		t.Fatal("expected debug warning for dbg!()")
	}
}

func TestCheckDebugStatements_MultiplePatterns(t *testing.T) {
	// Multiple different debug patterns introduced at once.
	old := `function init() {
	return true;
}
`
	updated := `function init() {
	console.log("start");
	console.debug("debug info");
	return true;
}
`
	warnings := checkDebugStatements("init.js", old, updated)
	if len(warnings) < 2 {
		t.Fatalf("expected at least 2 debug warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestCheckDebugStatements_UnsupportedExt(t *testing.T) {
	// Unsupported file types should return nil.
	warnings := checkDebugStatements("readme.md", "", "# Hello\n\nSome content\n")
	if warnings != nil {
		t.Errorf("expected nil for .md file, got: %v", warnings)
	}
}

func TestCheckDebugStatements_NoFalsePositiveForRemove(t *testing.T) {
	// If the edit REMOVES a debug statement, no warning should fire.
	old := `function calc(a, b) {
	console.log("debug:", a, b);
	return a + b;
}
`
	updated := `function calc(a, b) {
	return a + b;
}
`
	warnings := checkDebugStatements("calc.js", old, updated)
	if len(warnings) != 0 {
		t.Errorf("expected no warning when debug statement is removed, got: %v", warnings)
	}
}

func TestCheckDebugStatements_JavaPrintStackTrace(t *testing.T) {
	old := `public class Foo {
    public void bar() {
        doSomething();
    }
}
`
	updated := `public class Foo {
    public void bar() {
        try {
            doSomething();
        } catch (Exception e) {
            e.printStackTrace();
        }
    }
}
`
	warnings := checkDebugStatements("Foo.java", old, updated)
	if len(warnings) == 0 {
		t.Fatal("expected debug warning for printStackTrace()")
	}
}

func TestCheckDebugStatements_EmptyNewContent(t *testing.T) {
	warnings := checkDebugStatements("test.js", "old content", "")
	if warnings != nil {
		t.Errorf("expected nil for empty new content, got: %v", warnings)
	}
}

func TestDebugSniffer_IsTestFile(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{"foo_test.go", true},
		{"internal/agent/agent_test.go", true},
		{"calc.test.js", true},
		{"calc.spec.ts", true},
		{"calc.test.tsx", true},
		{"test_utils.py", true},
		{"utils_test.py", true},
		{"model_spec.rb", true},
		{"FooTest.java", true},
		{"FooTests.kt", true},
		{"App.test.jsx", true},
		{"foo_test.rs", true},
		{"src/test/helper.rs", true},
		{"internal/tests/setup.go", true},
		{"__tests__/index.test.js", true},
		// Non-test files
		{"foo.go", false},
		{"main.js", false},
		{"utils.py", false},
		{"README.md", false},
		{"handler.rs", false},
	}
	for _, tt := range tests {
		got := isTestFile(tt.path)
		if got != tt.expected {
			t.Errorf("isTestFile(%q) = %v, want %v", tt.path, got, tt.expected)
		}
	}
}

func TestCheckWriteIntegrity_DebugWarningIntegration(t *testing.T) {
	// Verify that debug detection integrates with the main integrity check.
	old := `function calc(a, b) {
	return a + b;
}
`
	updated := `function calc(a, b) {
	console.log("debug");
	return a + b;
}
`
	warning := checkWriteIntegrity("src/calc.js", old, updated)
	if warning == "" {
		t.Fatal("expected integrity warning for debug statement")
	}
	if !strings.Contains(warning, "console.log") {
		t.Errorf("warning should mention console.log, got: %s", warning)
	}
	if !strings.Contains(warning, "Post-write integrity check") {
		t.Errorf("warning should have header, got: %s", warning)
	}
}

func TestCheckDebugStatements_MultipleCount(t *testing.T) {
	// Multiple occurrences of same pattern should be counted.
	old := `function f() { return 1; }
`
	updated := `function f() {
	console.log("a");
	console.log("b");
	console.log("c");
	return 1;
}
`
	warnings := checkDebugStatements("f.js", old, updated)
	if len(warnings) == 0 {
		t.Fatal("expected warning for multiple console.log")
	}
	// Should mention the count (3 statements)
	if !strings.Contains(warnings[0], "3") {
		t.Errorf("warning should mention count 3, got: %s", warnings[0])
	}
}

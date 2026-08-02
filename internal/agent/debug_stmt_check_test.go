package agent

import (
	"strings"
	"testing"
)

func TestCheckDebugStmts_NoDebugStmts(t *testing.T) {
	old := "package main\n\nfunc main() {}\n"
	new := "package main\n\nfunc main() {\n\tx := 1\n\t_ = x\n}\n"
	warning := checkDebugStmts("main.go", old, new)
	if warning != "" {
		t.Errorf("expected no warning, got: %s", warning)
	}
}

func TestCheckDebugStmts_GoFmtPrintlnIntroduced(t *testing.T) {
	old := "package main\n\nfunc main() {}\n"
	new := "package main\n\nfunc main() {\n\tfmt.Println(\"debug\")\n}\n"
	warning := checkDebugStmts("main.go", old, new)
	if warning == "" {
		t.Fatal("expected debug statement warning for fmt.Println")
	}
	if !strings.Contains(warning, "fmt.Print") {
		t.Errorf("expected warning to mention fmt.Print, got: %s", warning)
	}
}

func TestCheckDebugStmts_GoBuiltinPrintln(t *testing.T) {
	old := ""
	new := "package main\n\nfunc main() {\n\tprintln(\"debug\")\n}\n"
	warning := checkDebugStmts("main.go", old, new)
	if warning == "" {
		t.Fatal("expected debug statement warning for builtin println")
	}
}

func TestCheckDebugStmts_ConsoleLogJS(t *testing.T) {
	old := "function add(a, b) {\n  return a + b;\n}\n"
	new := "function add(a, b) {\n  console.log(a, b);\n  return a + b;\n}\n"
	warning := checkDebugStmts("utils.js", old, new)
	if warning == "" {
		t.Fatal("expected debug statement warning for console.log")
	}
	if !strings.Contains(warning, "console.log") {
		t.Errorf("expected warning to mention console.log, got: %s", warning)
	}
}

func TestCheckDebugStmts_ConsoleLogTS(t *testing.T) {
	old := ""
	new := "export function foo() {\n  console.log('hi');\n}\n"
	warning := checkDebugStmts("foo.ts", old, new)
	if warning == "" {
		t.Fatal("expected debug statement warning for console.log in .ts")
	}
}

func TestCheckDebugStmts_DebuggerJS(t *testing.T) {
	old := "function f() { return 1; }\n"
	new := "function f() {\n  debugger;\n  return 1;\n}\n"
	warning := checkDebugStmts("f.js", old, new)
	if warning == "" {
		t.Fatal("expected debug statement warning for debugger")
	}
	if !strings.Contains(warning, "debugger") {
		t.Errorf("expected warning to mention debugger, got: %s", warning)
	}
}

func TestCheckDebugStmts_PythonPrint(t *testing.T) {
	old := "def foo():\n    pass\n"
	new := "def foo():\n    print('debugging')\n"
	warning := checkDebugStmts("foo.py", old, new)
	if warning == "" {
		t.Fatal("expected debug statement warning for print()")
	}
}

func TestCheckDebugStmts_PythonBreakpoint(t *testing.T) {
	old := ""
	new := "def foo():\n    breakpoint()\n"
	warning := checkDebugStmts("foo.py", old, new)
	if warning == "" {
		t.Fatal("expected debug statement warning for breakpoint()")
	}
}

func TestCheckDebugStmts_RustPrintln(t *testing.T) {
	old := "fn main() {}\n"
	new := "fn main() {\n    println!(\"debug\");\n}\n"
	warning := checkDebugStmts("main.rs", old, new)
	if warning == "" {
		t.Fatal("expected debug statement warning for println!")
	}
}

func TestCheckDebugStmts_RustDbgMacro(t *testing.T) {
	old := "fn compute() -> i32 {\n    42\n}\n"
	new := "fn compute() -> i32 {\n    let x = 42;\n    dbg!(x);\n    x\n}\n"
	warning := checkDebugStmts("lib.rs", old, new)
	if warning == "" {
		t.Fatal("expected debug statement warning for dbg!")
	}
}

func TestCheckDebugStmts_PreExistingNotFlagged(t *testing.T) {
	// If the debug statement was already in oldContent, it should NOT be flagged
	old := "package main\n\nfunc main() {\n\tfmt.Println(\"existing\")\n}\n"
	new := old + "\n// added comment\n"
	warning := checkDebugStmts("main.go", old, new)
	if warning != "" {
		t.Errorf("expected no warning for pre-existing debug stmt, got: %s", warning)
	}
}

func TestCheckDebugStmts_TestFileExempt(t *testing.T) {
	old := ""
	new := "package main\n\nfunc TestFoo(t *testing.T) {\n\tfmt.Println(\"debug\")\n}\n"
	warning := checkDebugStmts("main_test.go", old, new)
	if warning != "" {
		t.Errorf("expected test file to be exempt, got: %s", warning)
	}
}

func TestCheckDebugStmts_JSTestFileExempt(t *testing.T) {
	old := ""
	new := "test('works', () => {\n  console.log('setup');\n});\n"
	warning := checkDebugStmts("foo.test.js", old, new)
	if warning != "" {
		t.Errorf("expected .test.js file to be exempt, got: %s", warning)
	}
}

func TestCheckDebugStmts_SpecFileExempt(t *testing.T) {
	old := ""
	new := "it('works', () => {\n  console.log('setup');\n});\n"
	warning := checkDebugStmts("foo.spec.ts", old, new)
	if warning != "" {
		t.Errorf("expected .spec.ts file to be exempt, got: %s", warning)
	}
}

func TestCheckDebugStmts_NonSourceFileIgnored(t *testing.T) {
	old := ""
	new := "# Debug\n\nSome text with console.log in it\n"
	warning := checkDebugStmts("README.md", old, new)
	if warning != "" {
		t.Errorf("expected markdown file to be ignored, got: %s", warning)
	}
}

func TestCheckDebugStmts_VendorDirExempt(t *testing.T) {
	old := ""
	new := "package main\n\nfunc main() {\n\tfmt.Println(\"x\")\n}\n"
	warning := checkDebugStmts("vendor/foo/main.go", old, new)
	if warning != "" {
		t.Errorf("expected vendor/ dir to be exempt, got: %s", warning)
	}
}

func TestCheckDebugStmts_EmptyNewContent(t *testing.T) {
	warning := checkDebugStmts("main.go", "old", "")
	if warning != "" {
		t.Errorf("expected no warning for empty content, got: %s", warning)
	}
}

func TestCheckDebugStmts_MultipleTypes(t *testing.T) {
	old := ""
	new := "package main\n\nfunc main() {\n\tfmt.Println(\"a\")\n\tfmt.Printf(\"b\")\n\tprintln(\"c\")\n}\n"
	warning := checkDebugStmts("main.go", old, new)
	if warning == "" {
		t.Fatal("expected debug statement warning")
	}
	if !strings.Contains(warning, "fmt.Print") {
		t.Errorf("expected fmt.Print in warning: %s", warning)
	}
}

func TestCheckDebugStmts_JavaSystemOut(t *testing.T) {
	old := "public class App {\n}\n"
	new := "public class App {\n    public void run() {\n        System.out.println(\"debug\");\n    }\n}\n"
	warning := checkDebugStmts("App.java", old, new)
	if warning == "" {
		t.Fatal("expected debug statement warning for System.out.println")
	}
}

func TestCheckDebugStmts_CPP(t *testing.T) {
	old := "int main() {\n    return 0;\n}\n"
	new := "int main() {\n    printf(\"debug\\n\");\n    return 0;\n}\n"
	warning := checkDebugStmts("main.cpp", old, new)
	if warning == "" {
		t.Fatal("expected debug statement warning for printf")
	}
}

func TestCheckDebugStmts_LanguageIsolation(t *testing.T) {
	// fmt.Println should NOT trigger in a .js file
	old := ""
	new := "// using fmt-like syntax\nconst x = fmtPrintln();\n"
	warning := checkDebugStmts("foo.js", old, new)
	// fmt.Print pattern should only match .go files, not .js
	if warning != "" {
		t.Errorf("expected language isolation: fmt.Print should not trigger in .js, got: %s", warning)
	}
}

func TestIsTestFile_ForDebugStmts(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{"main_test.go", true},
		{"foo_test.py", true},
		{"bar.test.js", true},
		{"bar.spec.ts", true},
		{"main.go", false},
		{"utils.js", false},
		{"/home/user/project/test/helper.go", true},
		{"/home/user/project/__tests__/foo.js", true},
		{"/home/user/project/spec/bar.ts", true},
		{"package.json", false},
	}
	for _, tt := range tests {
		got := isTestFile(tt.path)
		if got != tt.expected {
			t.Errorf("isTestFile(%q) = %v, want %v", tt.path, got, tt.expected)
		}
	}
}

package agent

import (
	"strings"
	"testing"
)

func TestCheckSuppressionDirectives_GoNolint(t *testing.T) {
	old := "package main\n\nfunc foo() {}\n"
	new_ := "package main\n\n//nolint\nfunc foo() {}\n"

	warnings := checkSuppressionDirectives("main.go", old, new_)
	if len(warnings) == 0 {
		t.Fatal("expected nolint warning for .go")
	}
	if !strings.Contains(warnings[0], "//nolint") {
		t.Errorf("expected '//nolint' in warning, got: %s", warnings[0])
	}
}

func TestCheckSuppressionDirectives_PythonTypeIgnore(t *testing.T) {
	old := "def foo():\n    pass\n"
	new_ := "def foo():\n    x: int = get_val()  # type: ignore\n"

	warnings := checkSuppressionDirectives("main.py", old, new_)
	if len(warnings) == 0 {
		t.Fatal("expected type:ignore warning for .py")
	}
	if !strings.Contains(warnings[0], "type: ignore") {
		t.Errorf("expected 'type: ignore' in warning, got: %s", warnings[0])
	}
}

func TestCheckSuppressionDirectives_PythonNoQA(t *testing.T) {
	old := "import os\n"
	new_ := "import os  # noqa: F401\n"

	warnings := checkSuppressionDirectives("main.py", old, new_)
	if len(warnings) == 0 {
		t.Fatal("expected noqa warning")
	}
	if !strings.Contains(warnings[0], "noqa") {
		t.Errorf("expected 'noqa' in warning, got: %s", warnings[0])
	}
}

func TestCheckSuppressionDirectives_PythonPragmaNoCover(t *testing.T) {
	old := "def foo():\n    return 1\n"
	new_ := "def foo():\n    return 1  # pragma: no cover\n"

	warnings := checkSuppressionDirectives("main.py", old, new_)
	if len(warnings) == 0 {
		t.Fatal("expected pragma: no cover warning")
	}
}

func TestCheckSuppressionDirectives_PythonPylintDisable(t *testing.T) {
	old := "import os\n"
	new_ := "import os  # pylint: disable=unused-import\n"

	warnings := checkSuppressionDirectives("main.py", old, new_)
	if len(warnings) == 0 {
		t.Fatal("expected pylint: disable warning")
	}
}

func TestCheckSuppressionDirectives_JSEslintDisable(t *testing.T) {
	old := "const x = 1;\n"
	new_ := "/* eslint-disable no-unused-vars */\nconst x = 1;\n"

	warnings := checkSuppressionDirectives("index.js", old, new_)
	if len(warnings) == 0 {
		t.Fatal("expected eslint-disable warning for .js")
	}
	if !strings.Contains(warnings[0], "eslint-disable") {
		t.Errorf("expected 'eslint-disable' in warning, got: %s", warnings[0])
	}
}

func TestCheckSuppressionDirectives_JSEslintDisableNextLine(t *testing.T) {
	old := "const x = 1;\n"
	new_ := "// eslint-disable-next-line no-console\nconsole.log(x);\n"

	warnings := checkSuppressionDirectives("index.ts", old, new_)
	if len(warnings) == 0 {
		t.Fatal("expected eslint-disable-next-line warning for .ts")
	}
}

func TestCheckSuppressionDirectives_GoGosecDisable(t *testing.T) {
	old := "package main\n"
	new_ := "package main\n\n//gosec:disable G104\nfunc main() {}\n"

	warnings := checkSuppressionDirectives("main.go", old, new_)
	if len(warnings) == 0 {
		t.Fatal("expected gosec:disable warning")
	}
}

func TestCheckSuppressionDirectives_GoReviveDisable(t *testing.T) {
	old := "package main\n"
	new_ := "package main\n\n//revive:disable\nfunc main() {}\n"

	warnings := checkSuppressionDirectives("main.go", old, new_)
	if len(warnings) == 0 {
		t.Fatal("expected revive:disable warning")
	}
}

func TestCheckSuppressionDirectives_GoLintIgnore(t *testing.T) {
	old := "package main\n"
	new_ := "package main\n\n//lint:ignore SA1000 false positive\nfunc main() {}\n"

	warnings := checkSuppressionDirectives("main.go", old, new_)
	if len(warnings) == 0 {
		t.Fatal("expected lint:ignore warning")
	}
}

func TestCheckSuppressionDirectives_PreExistingNotFlagged(t *testing.T) {
	// The suppression was already there -- should NOT warn
	content := "package main\n\n//nolint\nfunc foo() {}\n"

	warnings := checkSuppressionDirectives("main.go", content, content)
	if len(warnings) != 0 {
		t.Errorf("expected no warning for pre-existing suppression, got: %v", warnings)
	}
}

func TestCheckSuppressionDirectives_NoSuppression(t *testing.T) {
	old := "package main\n\nfunc foo() {}\n"
	new_ := "package main\n\nfunc bar() {}\n"

	warnings := checkSuppressionDirectives("main.go", old, new_)
	if len(warnings) != 0 {
		t.Errorf("expected no warning for clean code, got: %v", warnings)
	}
}

func TestCheckSuppressionDirectives_EmptyContent(t *testing.T) {
	warnings := checkSuppressionDirectives("main.go", "", "")
	if len(warnings) != 0 {
		t.Errorf("expected no warning for empty content, got: %v", warnings)
	}
}

func TestCheckSuppressionDirectives_MultipleAdded(t *testing.T) {
	old := "package main\n"
	new_ := "package main\n\n//nolint\nfunc foo() {}\n\n//nolint\nfunc bar() {}\n"

	warnings := checkSuppressionDirectives("main.go", old, new_)
	if len(warnings) == 0 {
		t.Fatal("expected warnings for multiple nolint additions")
	}
	if !strings.Contains(warnings[0], "2 suppression") {
		t.Errorf("expected count of 2, got: %s", warnings[0])
	}
}

func TestCheckSuppressionDirectives_JavaSuppressWarnings(t *testing.T) {
	old := "public class Foo {\n}\n"
	new_ := "public class Foo {\n    @SuppressWarnings(\"unchecked\")\n    void bar() {}\n}\n"

	warnings := checkSuppressionDirectives("Foo.java", old, new_)
	if len(warnings) == 0 {
		t.Fatal("expected @SuppressWarnings warning for .java")
	}
	if !strings.Contains(warnings[0], "@SuppressWarnings") {
		t.Errorf("expected '@SuppressWarnings' in warning, got: %s", warnings[0])
	}
}

func TestCheckSuppressionDirectives_RubyRubocopDisable(t *testing.T) {
	old := "def foo\nend\n"
	new_ := "# rubocop:disable Style/StringLiterals\ndef foo\nend\n"

	warnings := checkSuppressionDirectives("foo.rb", old, new_)
	if len(warnings) == 0 {
		t.Fatal("expected rubocop:disable warning for .rb")
	}
	if !strings.Contains(warnings[0], "rubocop:disable") {
		t.Errorf("expected 'rubocop:disable' in warning, got: %s", warnings[0])
	}
}

func TestCheckSuppressionDirectives_LineNumberInWarning(t *testing.T) {
	old := "package main\n\nfunc foo() {}\n"
	new_ := "package main\n\n//nolint\nfunc foo() {}\n"

	warnings := checkSuppressionDirectives("main.go", old, new_)
	if len(warnings) == 0 {
		t.Fatal("expected warning")
	}
	if !strings.Contains(warnings[0], "line 3") {
		t.Errorf("expected line number in warning, got: %s", warnings[0])
	}
}

func TestLangInList(t *testing.T) {
	langs := []Language{LangGo, LangPython}
	if !langInList(langs, LangGo) {
		t.Error("expected LangGo to be in list")
	}
	if langInList(langs, LangJSTS) {
		t.Error("expected LangJSTS to NOT be in list")
	}
	if langInList(nil, LangGo) {
		t.Error("expected empty list to return false")
	}
}

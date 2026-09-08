package agent

import (
	"strings"
	"testing"
)

func TestCheckPythonIndentation_NotPython(t *testing.T) {
	result := checkPythonIndentation("test.js", "const x = 1;")
	if result != "" {
		t.Errorf("expected empty for non-Python file, got: %s", result)
	}
}

func TestCheckPythonIndentation_CleanSpaces(t *testing.T) {
	content := `def hello():
    print("hello")
    if True:
        print("world")
`
	result := checkPythonIndentation("test.py", content)
	if result != "" {
		t.Errorf("expected no warning for clean indentation, got: %s", result)
	}
}

func TestCheckPythonIndentation_CleanTabs(t *testing.T) {
	content := "def hello():\n\tprint('hello')\n\tif True:\n\t\tprint('world')\n"
	result := checkPythonIndentation("test.py", content)
	if result != "" {
		t.Errorf("expected no warning for consistent tab indentation, got: %s", result)
	}
}

func TestCheckPythonIndentation_MixedTabsSpaces(t *testing.T) {
	content := "def hello():\n    print('hello')\n\t    print('world')\n"
	result := checkPythonIndentation("test.py", content)
	if result == "" {
		t.Fatal("expected warning for mixed tabs and spaces")
	}
	if !strings.Contains(result, "mixed tabs and spaces") {
		t.Errorf("warning should mention 'mixed tabs and spaces', got: %s", result)
	}
	if !strings.Contains(result, "TabError") {
		t.Errorf("warning should mention TabError, got: %s", result)
	}
}

func TestCheckPythonIndentation_MultipleMixedLines(t *testing.T) {
	content := "def hello():\n    \tprint('a')\n\t    print('b')\n  \tx = 1\n"
	result := checkPythonIndentation("test.py", content)
	if result == "" {
		t.Fatal("expected warning for multiple mixed indentation lines")
	}
	if !strings.Contains(result, "occurrences") {
		t.Errorf("warning should mention occurrences for multiple lines, got: %s", result)
	}
}

func TestCheckPythonIndentation_SkipsBlankAndComments(t *testing.T) {
	content := "# comment with\t tab\n\n    x = 1\n"
	result := checkPythonIndentation("test.py", content)
	if result != "" {
		t.Errorf("expected no warning for blank lines and comments, got: %s", result)
	}
}

func TestCheckPythonIndentation_EmptyContent(t *testing.T) {
	result := checkPythonIndentation("test.py", "")
	if result != "" {
		t.Errorf("expected empty for empty content, got: %s", result)
	}
}

func TestFilepathExtSafe(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"test.py", ".py"},
		{"foo/bar.go", ".go"},
		{"noext", ""},
		{"/abs/path/file.tsx", ".tsx"},
		{"trailing/", ""},
	}
	for _, tt := range tests {
		got := filepathExtSafe(tt.path)
		if got != tt.want {
			t.Errorf("filepathExtSafe(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

// TestPythonIndentationRegistered pins #1481 case C: fc5c4aad's style-check
// purge orphaned checkPythonIndentation (zero production callers, registry
// entry deleted) even though the module's own docs call mixed indentation
// "guaranteed TabError in Python 3" - crash-class, squarely under the
// purge's stated retention standard. The registry must carry it again.
func TestPythonIndentationRegistered(t *testing.T) {
	found := false
	for _, c := range allChecks {
		if c.Name == "python-indentation" {
			found = true
			if c.Severity != SeverityCritical {
				t.Errorf("python-indentation must be Critical (TabError crashes the run), got %v", c.Severity)
			}
			break
		}
	}
	if !found {
		t.Fatal("python-indentation missing from the write-integrity registry (#1481 case C: dead wiring again)")
	}
	// And the registered check actually fires on a mixed-indent Python file.
	for _, c := range allChecks {
		if c.Name != "python-indentation" {
			continue
		}
		msgs := c.Run(CheckContext{FilePath: "test.py", NewContent: "def f():\n\t    pass\n"})
		if len(msgs) == 0 {
			t.Error("registered python-indentation check did not fire on mixed tabs/spaces")
		}
		return
	}
}

// Regression for #1865 case 2: bracket-continuation lines and
// triple-quoted string content are NOT code indentation - flagging them
// as "mixed tabs and spaces" was a Critical-severity false positive on
// perfectly legal Python.
func TestCheckPythonIndentation_NoFalsePositiveContinuation(t *testing.T) {
	// Indent inside open brackets has no syntactic meaning.
	bracket := "foo(a,\n\t    b)\nif True:\n    x = [1,\n\t 2]\n"
	if w := checkPythonIndentation("t.py", bracket); w != "" {
		t.Fatalf("bracket continuation indent must not be flagged: %q", w)
	}
	// String data inside triple quotes is not indentation.
	triple := "s = '''\n\t  data line\n\tx = [1]\n'''\n"
	if w := checkPythonIndentation("t.py", triple); w != "" {
		t.Fatalf("triple-quoted string content must not be flagged: %q", w)
	}
	// Backslash continuation lines are also not new logical lines.
	cont := "x = 1 + \\\n\t 2\n"
	if w := checkPythonIndentation("t.py", cont); w != "" {
		t.Fatalf("backslash continuation indent must not be flagged: %q", w)
	}
	// Genuine mixed indent AFTER a closed bracket still fires.
	real := "y = (1, 2)\n\tif True:\n\t    pass\n"
	if w := checkPythonIndentation("t.py", real); w == "" {
		t.Fatal("genuine mixed indent on a code line must still be flagged")
	}
}

// Regression for #1865 case 1: the check is delta-gated (#601 W4) - an
// unrelated edit to a file whose PRE-EXISTING indentation is mixed must
// not re-report the same warning, and a zero-delta write must not fire.
func TestPythonIndentationDeltaGated(t *testing.T) {
	old := "def f():\n\t    return 1\n" // pre-existing mixed indent

	// Zero-delta write: never fires.
	if w := checkWriteIntegrity("t.py", old, old); w != "" {
		t.Fatalf("zero-delta write must not fire: %v", w)
	}
	// Unrelated edit elsewhere in the same file: same warning on both
	// sides -> suppressed (pre-existing problem untouched by this write).
	edited := old + "# trailing comment\n"
	if w := checkWriteIntegrity("t.py", old, edited); w != "" {
		t.Fatalf("unrelated edit must not re-report pre-existing mixed indent: %v", w)
	}
	// This write INTRODUCES mixed indentation -> must fire.
	clean := "def f():\n    return 1\n"
	if w := checkWriteIntegrity("t.py", clean, old); w == "" {
		t.Fatal("write introducing mixed indent must be flagged")
	}
}

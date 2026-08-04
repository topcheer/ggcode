package agent

import (
	"strings"
	"testing"
)

func TestCheckTrailingWhitespace_DetectedOnNewLine(t *testing.T) {
	old := "line one\nline two\nline three"
	// "line two  " has trailing spaces (newly introduced)
	newContent := "line one\nline two  \nline three"
	result := checkTrailingWhitespace("test.py", old, newContent)
	if result == "" {
		t.Fatal("expected trailing whitespace warning, got empty")
	}
	if !strings.Contains(result, "line: 2") {
		t.Errorf("expected line number 2 in warning, got: %s", result)
	}
}

func TestCheckTrailingWhitespace_NoTrailingWhitespace(t *testing.T) {
	old := "line one\nline two\nline three"
	newContent := "line one\nline two modified\nline three"
	result := checkTrailingWhitespace("test.py", old, newContent)
	if result != "" {
		t.Errorf("expected no warning, got: %s", result)
	}
}

func TestCheckTrailingWhitespace_PreservesExistingNotFlagged(t *testing.T) {
	// Old content already has trailing whitespace on line 2
	old := "line one\nline two  \nline three"
	// New content keeps line 2 as-is but adds trailing ws on line 3
	newContent := "line one\nline two  \nline three   "
	result := checkTrailingWhitespace("test.py", old, newContent)
	if result == "" {
		t.Fatal("expected warning for newly introduced trailing whitespace on line 3")
	}
	if strings.Contains(result, "line 2") {
		t.Errorf("should not flag pre-existing trailing whitespace on line 2: %s", result)
	}
	if !strings.Contains(result, "line: 3") {
		t.Errorf("expected line 3 in warning, got: %s", result)
	}
}

func TestCheckTrailingWhitespace_GoFilesChecked(t *testing.T) {
	old := "package main\n"
	newContent := "package main  \nfunc foo() {}  \n"
	result := checkTrailingWhitespace("main.go", old, newContent)
	if result == "" {
		t.Error("Go files should be checked for trailing whitespace (auto-format removed)")
	}
	if !strings.Contains(result, "line") {
		t.Errorf("expected trailing whitespace warning, got: %s", result)
	}
}

func TestCheckTrailingWhitespace_UnsupportedExtSkipped(t *testing.T) {
	old := "some content\n"
	newContent := "some content  \n"
	result := checkTrailingWhitespace("file.bin", old, newContent)
	if result != "" {
		t.Errorf("unsupported file types should be skipped, got: %s", result)
	}
}

func TestCheckTrailingWhitespace_HighRatioSkipped(t *testing.T) {
	// Old content has trailing ws on >40% of lines
	old := "a  \nb  \nc  \nd  \ne  \nf  \ng  \nh\ni\nj\n"
	// New content adds more
	newContent := "a  \nb  \nc  \nd  \ne  \nf  \ng  \nh\ni\nj  \n"
	result := checkTrailingWhitespace("test.py", old, newContent)
	if result != "" {
		t.Errorf("files with high pre-existing trailing whitespace ratio should be skipped, got: %s", result)
	}
}

func TestCheckTrailingWhitespace_TabTrailingWhitespace(t *testing.T) {
	old := "line one\nline two\n"
	newContent := "line one\nline two\t\n" // trailing tab
	result := checkTrailingWhitespace("test.js", old, newContent)
	if result == "" {
		t.Fatal("expected trailing tab warning")
	}
	if !strings.Contains(result, "line: 2") {
		t.Errorf("expected line 2 in warning, got: %s", result)
	}
}

func TestCheckTrailingWhitespace_MultipleLinesCapped(t *testing.T) {
	old := strings.Repeat("clean line\n", 20)
	// Introduce trailing ws on many lines
	var newLines []string
	for i := 0; i < 20; i++ {
		if i%2 == 0 {
			newLines = append(newLines, "clean line  ")
		} else {
			newLines = append(newLines, "clean line")
		}
	}
	newContent := strings.Join(newLines, "\n")
	result := checkTrailingWhitespace("test.py", old, newContent)
	if result == "" {
		t.Fatal("expected warning for multiple trailing whitespace lines")
	}
	// Should cap at maxTrailingWhitespaceWarns (5)
	if !strings.Contains(result, "more)") {
		t.Errorf("expected 'more' suffix when exceeding cap, got: %s", result)
	}
}

func TestCheckTrailingWhitespace_Makefile(t *testing.T) {
	old := "target1:\n\techo hi\n"
	newContent := "target1:  \n\techo hi\n"
	result := checkTrailingWhitespace("Makefile", old, newContent)
	if result == "" {
		t.Fatal("expected trailing whitespace warning for Makefile")
	}
}

func TestHasTrailingWhitespace(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"hello", false},
		{"hello ", true},
		{"hello\t", true},
		{"  hello", false},
		{"", false},
		{"hello   world", false},
		{"hello world  ", true},
		{"\t\t", true},
	}
	for _, tt := range tests {
		got := hasTrailingWhitespace(tt.line)
		if got != tt.want {
			t.Errorf("hasTrailingWhitespace(%q) = %v, want %v", tt.line, got, tt.want)
		}
	}
}

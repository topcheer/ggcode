package agent

import (
	"strings"
	"testing"
)

func TestCheckWriteIntegrity_GoSyntaxError(t *testing.T) {
	// Go file with a syntax error (missing closing brace)
	badGo := "package main\n\nfunc broken( {\n\treturn nil\n"
	old := "package main\n\nfunc broken() {\n\treturn nil\n}\n"

	warning := checkWriteIntegrity("main.go", old, badGo)
	if warning == "" {
		t.Fatal("expected integrity warning for Go syntax error, got empty string")
	}
	if !strings.Contains(warning, "Post-write integrity check") {
		t.Errorf("warning should contain header, got: %s", warning)
	}
	if !strings.Contains(warning, "main.go") {
		t.Errorf("warning should reference file path, got: %s", warning)
	}
}

func TestCheckWriteIntegrity_ValidGoFile(t *testing.T) {
	goodGo := "package main\n\nfunc main() {}\n"
	warning := checkWriteIntegrity("main.go", "", goodGo)
	if warning != "" {
		t.Errorf("expected no warning for valid Go file, got: %s", warning)
	}
}

func TestCheckWriteIntegrity_NullBytes(t *testing.T) {
	content := "package main\x00\nfunc main() {}\n"
	warning := checkWriteIntegrity("main.go", "", content)
	if warning == "" {
		t.Fatal("expected warning for null bytes")
	}
	if !strings.Contains(warning, "null byte") {
		t.Errorf("warning should mention null bytes, got: %s", warning)
	}
}

func TestCheckWriteIntegrity_ContentLoss(t *testing.T) {
	// Non-empty file becomes empty after edit
	warning := checkWriteIntegrity("main.go", "package main\nfunc main() {}\n", "")
	if warning == "" {
		t.Fatal("expected content loss warning")
	}
	if !strings.Contains(warning, "EMPTY file") {
		t.Errorf("warning should mention empty file, got: %s", warning)
	}
}

func TestCheckWriteIntegrity_NoWarningForNewFile(t *testing.T) {
	// New file (old="") with valid content should produce no warning
	warning := checkWriteIntegrity("main.go", "", "package main\nfunc main() {}\n")
	if warning != "" {
		t.Errorf("expected no warning for new valid file, got: %s", warning)
	}
}

func TestCheckWriteIntegrity_NonGoFileSkipsSyntaxCheck(t *testing.T) {
	// Non-Go file with unbalanced brackets should not trigger syntax check
	content := "{ this is not valid code but not Go either"
	warning := checkWriteIntegrity("readme.md", "old content", content)
	// Should be no warning — no null bytes, no content loss, not a Go file
	if warning != "" {
		t.Errorf("expected no warning for non-Go file, got: %s", warning)
	}
}

func TestCheckWriteIntegrity_WhitespaceOnlyContentLoss(t *testing.T) {
	// File with content becomes whitespace-only
	warning := checkWriteIntegrity("main.go", "package main\n", "   \n  \t  ")
	if warning == "" {
		t.Fatal("expected content loss warning for whitespace-only result")
	}
	if !strings.Contains(warning, "EMPTY") {
		t.Errorf("warning should mention empty, got: %s", warning)
	}
}

func TestCheckGoSyntax_MultipleErrors(t *testing.T) {
	// File with multiple syntax errors
	badGo := "package main\n\nfunc a( {\nfunc b( {\nfunc c( {\n"
	warnings := checkGoSyntax("multi.go", badGo)
	if len(warnings) == 0 {
		t.Fatal("expected syntax errors")
	}
	// Should cap at maxGoSyntaxErrors + 1 "more" message
	if len(warnings) > maxGoSyntaxErrors+1 {
		t.Errorf("too many warnings: %d (max should be %d+1)", len(warnings), maxGoSyntaxErrors)
	}
}

func TestCheckGoSyntax_ValidFile(t *testing.T) {
	goodGo := `package test

import "fmt"

func main() {
	fmt.Println("hello")
}
`
	warnings := checkGoSyntax("valid.go", goodGo)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for valid Go, got: %v", warnings)
	}
}

func TestCheckWriteIntegrity_WarningCap(t *testing.T) {
	// Create content that triggers multiple warnings:
	// null bytes + content loss + Go syntax errors
	bad := "\x00\x00\x00"
	warning := checkWriteIntegrity("main.go", "package main\nfunc main(){}\n", bad)
	if warning == "" {
		t.Fatal("expected warning")
	}
	// Count warning lines (each separated by newline in the header block)
	lines := strings.Split(warning, "\n")
	// First line is header, rest are warnings (capped at maxIntegrityWarnings)
	warningLines := lines[1:] // skip header
	if len(warningLines) > maxIntegrityWarnings {
		t.Errorf("expected at most %d warnings, got %d: %v", maxIntegrityWarnings, len(warningLines), warningLines)
	}
}
